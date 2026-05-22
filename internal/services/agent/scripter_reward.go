// scripter_reward.go — Reward agent: generates a completion reward (通关奖励).
//
// Runs in a completely isolated context (fresh message history).
// Queries the rulebook to produce accurate mechanical data, then returns a
// models.ScenarioReward that the pipeline injects into the final ScenarioDraft.
package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"

	"github.com/llmcoc/server/internal/models"
	"github.com/llmcoc/server/internal/services/llm"
	"github.com/llmcoc/server/internal/services/rulebook"
)

const rewardAgentSystemPrompt = `<role>COC7通关奖励设计专家</role>
<task>收到本剧本的通关奖励概念（Stage2 Architect提供的叙事描述）和已确认的mythos_anchor。通过ask_lawyer向规则书专家查询确认机械数据（tome的阅读SAN代价和学习收益，或artifact的激活条件和代价），然后通过respond返回一个完整的ScenarioReward。通关奖励在调查员满足win_condition后自动给予，无需技能检定。</task>
<response_format>json_array</response_format>
<output>每轮只输出合法JSON数组，不要Markdown、标题、解释或代码围栏。</output>
<tools>
- think：内部推理（可选，无副作用）
  {"action":"think","think":"推理内容"}
- ask_lawyer：向COC7规则书专家提出一个具体规则书问题；确认候选物品是否在规则书中存在、出处、阅读SAN代价、学习收益或激活条件；可多次调用
  {"action":"ask_lawyer","question":"具体规则书问题"}
- respond：返回完整通关奖励并退出；必须在至少一次ask_lawyer之后调用；必须单独一轮输出
  {"action":"respond","reward":{"name":"COC7正式名称或场景专属名称","type":"tome|artifact","description":"外观特征及与mythos_anchor和剧本主题的叙事关联","mechanics_note":"tome: 阅读代价≥1d4 SAN（来自规则书裁定）+ 具体学习收益（克苏鲁神话技能+N 或 可学法术名称）；artifact: 激活条件 + 代价/副作用"}}
</tools>
<batch_rules>
- 每轮只能是以下两种批次之一：
  A. 查询批次：可包含 think 和一个或多个 ask_lawyer；不得包含 respond。
  B. 最终批次：只能包含一个 respond；不得包含 think、ask_lawyer 或任何其他action。
- 绝对禁止把 respond 和 think/ask_lawyer 放在同一个JSON数组中。错误示例：[think, ask_lawyer, respond]。
- 如果还需要向规则书专家提问，本轮只输出查询批次，等待工具结果后下一轮再单独输出 respond。
</batch_rules>
<design_rules>
- 第一轮必须至少调用一次ask_lawyer；不得凭常识或记忆直接respond。
- type=tome：mechanics_note必须包含具体阅读SAN代价（≥1d4，来自规则书裁定，非猜测）和学习收益（克苏鲁神话技能+N 或 可学法术名称）。
- type=artifact：mechanics_note必须包含激活条件和副作用/代价；不得提供无代价的纯数值提升。
- 优先使用COC7规则书中记载的正式名称；若使用场景专属名称，需在description中说明叙事根据。
- ask_lawyer返回must_avoid中的禁令不得绕过。
- description必须说明物品与mythos_anchor的叙事关联。
</design_rules>`

const rewardAgentToolCallExample = `[{"action":"ask_lawyer","question":"COC7规则书中与食尸鬼相关的典籍有哪些？阅读SAN代价和学习收益各是什么？"}]`

// rewardAgentCall is a tool call in the reward agent's dispatch loop.
type rewardAgentCall struct {
	Action   ToolCallType       `json:"action"`
	Think    string             `json:"think,omitempty"`
	Question string             `json:"question,omitempty"` // ask_lawyer
	Reward   *rewardAgentReward `json:"reward,omitempty"`   // respond
}

// rewardAgentReward is the structured reward returned by respond.
// No find_condition — completion rewards are given when win_condition is met.
type rewardAgentReward struct {
	Name          string `json:"name"`
	Type          string `json:"type"`
	Description   string `json:"description"`
	MechanicsNote string `json:"mechanics_note"`
}

// runRewardAgent runs the reward agent in an isolated context.
// It queries the rulebook to produce a rule-accurate ScenarioReward,
// or returns nil if the concept is empty or no provider is available.
func runRewardAgent(ctx context.Context, room *scripterRoom, concept, mythosAnchor string) (*models.ScenarioReward, error) {
	concept = strings.TrimSpace(concept)
	if concept == "" {
		return nil, nil
	}
	provider := room.architect
	if provider.provider == nil {
		provider = room.lawyer
	}
	if provider.provider == nil {
		return nil, fmt.Errorf("reward agent: no LLM provider available")
	}

	requestJSON, _ := json.Marshal(struct {
		Concept      string `json:"concept"`
		MythosAnchor string `json:"mythos_anchor,omitempty"`
	}{Concept: concept, MythosAnchor: mythosAnchor})

	// Isolated context: fresh message history, no shared state with main pipeline.
	msgs := []llm.ChatMessage{
		{Role: "system", Content: provider.systemPrompt(rewardAgentSystemPrompt)},
		{Role: "user", Content: fmt.Sprintf(`<reward_request>%s</reward_request>`, string(requestJSON))},
	}

	const maxRounds = 16
	askedLawyer := false
	for round := 1; round <= maxRounds; round++ {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		logStagePrompt(fmt.Sprintf("reward_agent_round_%d", round), msgs)
		raw, err := provider.provider.Chat(ctx, msgs)
		if err != nil {
			return nil, err
		}
		log.Printf("[scripter:reward_agent] round=%d raw_len=%d raw=%s", round, len(raw), truncateRunes(raw, scripterRawLogLimit))
		msgs = append(msgs, llm.ChatMessage{Role: "assistant", Content: raw})

		calls, parseErr := parseRewardAgentToolCalls(ctx, room.parser, raw)
		if parseErr != nil {
			msgs = append(msgs, llm.ChatMessage{Role: "user", Content: "SYSTEM REJECT: JSON解析失败，必须重新输出合法JSON数组。"})
			continue
		}
		if len(calls) == 0 {
			msgs = append(msgs, llm.ChatMessage{Role: "user", Content: "SYSTEM REJECT: 必须输出至少一个工具调用。"})
			continue
		}

		if rewardRespondMixed(calls) {
			msgs = append(msgs, llm.ChatMessage{Role: "user", Content: "SYSTEM REJECT: respond必须单独一轮输出，不能和think、ask_lawyer或任何其他action混在同一个JSON数组中。若还需查询，本轮只输出ask_lawyer；若已有足够信息，下一轮只输出一个respond。"})
			continue
		}

		invalid := false
		var result *rewardAgentReward
		var toolResults []string
		for _, call := range calls {
			switch call.Action {
			case ToolThink:
				// silent
			case toolTranslatorAskLawyer: // "ask_lawyer"
				askedLawyer = true
				toolResults = append(toolResults, rewardAgentAskLawyer(ctx, room, call.Question))
			case toolTranslatorRespond: // "respond"
				if !askedLawyer {
					msgs = append(msgs, llm.ChatMessage{Role: "user", Content: "SYSTEM REJECT: respond前必须至少调用一次ask_lawyer。"})
					invalid = true
				} else if call.Reward == nil {
					msgs = append(msgs, llm.ChatMessage{Role: "user", Content: "SYSTEM REJECT: respond的reward字段不能为空。"})
					invalid = true
				} else if strings.TrimSpace(call.Reward.Name) == "" || strings.TrimSpace(call.Reward.MechanicsNote) == "" {
					msgs = append(msgs, llm.ChatMessage{Role: "user", Content: "SYSTEM REJECT: respond.reward的name和mechanics_note不能为空。"})
					invalid = true
				} else {
					result = call.Reward
				}
			default:
				msgs = append(msgs, llm.ChatMessage{Role: "user", Content: fmt.Sprintf(
					"SYSTEM REJECT: reward_agent只允许think/ask_lawyer/respond，不允许%s。", call.Action)})
				invalid = true
			}
		}
		if invalid {
			continue
		}
		if len(toolResults) > 0 {
			msgs = append(msgs, llm.ChatMessage{Role: "user", Content: strings.Join(toolResults, "\n")})
			continue
		}
		if result != nil {
			return &models.ScenarioReward{
				Name:          result.Name,
				Type:          result.Type,
				Description:   result.Description,
				MechanicsNote: result.MechanicsNote,
			}, nil
		}
		msgs = append(msgs, llm.ChatMessage{Role: "user", Content: "SYSTEM REJECT: 必须调用ask_lawyer获取规则书裁定，或在已有裁定基础上调用respond返回通关奖励。"})
	}
	return nil, fmt.Errorf("reward agent未在%d轮内返回respond", maxRounds)
}

func rewardRespondMixed(calls []rewardAgentCall) bool {
	respondCount := 0
	for _, call := range calls {
		if call.Action == toolTranslatorRespond {
			respondCount++
		}
	}
	return respondCount > 0 && len(calls) != 1
}

func parseRewardAgentToolCalls(ctx context.Context, parser agentHandle, raw string) ([]rewardAgentCall, error) {
	stripped := strings.TrimSpace(llm.StripCodeFence(llm.JsonArryProtect(raw)))
	var calls []rewardAgentCall
	if err := json.Unmarshal([]byte(stripped), &calls); err == nil {
		return calls, nil
	} else if parser.provider != nil {
		fixed, repairErr := repairJSONWith(ctx, parser, stripped, err, rewardAgentToolCallExample)
		if repairErr != nil {
			return nil, repairErr
		}
		fixed = strings.TrimSpace(llm.JsonArryProtect(fixed))
		if err2 := json.Unmarshal([]byte(fixed), &calls); err2 != nil {
			return nil, err2
		}
		return calls, nil
	} else {
		return nil, err
	}
}

func rewardAgentAskLawyer(ctx context.Context, room *scripterRoom, question string) string {
	question = strings.TrimSpace(question)
	if question == "" {
		return `<ask_lawyer_result error="question字段为空"/>`
	}
	log.Printf("[scripter:reward_agent] ask_lawyer question=%q", truncateRunes(question, 300))
	if room.lawyer.provider == nil {
		return fmt.Sprintf(`<ask_lawyer_result question=%q status="lawyer_unavailable">规则书专家不可用；不得声称已核验具体规则书数据。</ask_lawyer_result>`, question)
	}
	results := runLawyer(ctx, room.lawyer, question, rulebook.GlobalIndex)
	if len(results) == 0 {
		return fmt.Sprintf(`<ask_lawyer_result question=%q status="no_result">规则书中未找到相关裁定；可换用更具体的候选重新提问，或在结论中标记uncertain。</ask_lawyer_result>`, question)
	}
	return fmt.Sprintf(`<ask_lawyer_result question=%q status="found">%s</ask_lawyer_result>`, question, formatLawyerResults(results))
}

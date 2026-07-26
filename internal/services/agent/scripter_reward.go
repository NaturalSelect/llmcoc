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
)

const rewardAgentSystemPrompt = `<role>COC7通关奖励设计专家</role>
<task>收到本剧本的通关奖励概念（Stage2 Architect提供的叙事描述）和已确认的mythos_anchor。通过ask_lawyer工具向规则书专家查询确认机械数据（tome的阅读SAN代价和学习收益，或artifact的激活条件和代价），然后通过respond工具返回一个完整的ScenarioReward。通关奖励在调查员达成非失败结局后自动给予，无需技能检定。</task>
<design_rules>
- 第一轮必须至少调用一次ask_lawyer；不得凭常识或记忆直接respond。
- type=tome：mechanics_note必须包含具体阅读SAN代价（≥1d4，来自规则书裁定，非猜测）和学习收益（克苏鲁神话技能+N 或 可学法术名称）。
- type=artifact：mechanics_note必须包含激活条件和副作用/代价；不得提供无代价的纯数值提升。
- 优先使用COC7规则书中记载的正式名称；若使用场景专属名称，需在description中说明叙事根据。
- ask_lawyer返回must_avoid中的禁令不得绕过。
- description必须说明物品与mythos_anchor的叙事关联。
- 仔细思考，考虑设计一个有趣的奖励，避免过于平庸或过于强力的奖励；如果概念本身很弱，考虑在respond中设计一个更有趣的奖励来替代概念，但必须有规则书裁定作为支持。奖励设计要兼顾叙事和机制，避免纯叙事或纯数值提升。
</design_rules>`

// rewardAgentReward is the structured reward returned by respond.
// No find_condition — completion rewards are given when a non-failure ending is reached.
type rewardAgentReward struct {
	Name          string `json:"name"`
	Type          string `json:"type"`
	Description   string `json:"description"`
	MechanicsNote string `json:"mechanics_note"`
}

// rewardAgentRespondTool 是 reward_agent 的 respond 工具定义（solo，终止本轮循环）。
func rewardAgentRespondTool() scripterTool {
	return scripterTool{
		solo: true,
		def: llm.ToolDefinition{
			Name: toolNameRespond,
			Description: "返回完整通关奖励并退出；必须在至少一次ask_lawyer之后调用；必须单独一轮调用。" +
				"奖励必须有规则书的证据支持。奖励可以是一个神话物品（artifact）或者一个神话典籍（tome），" +
				"以及来自其他法师的笔记(tome)，其中记载的法术必须直接来自规则书，不能自定义或基于规则书数据改编。",
			Parameters: jsonSchemaObject(`{
				"type": "object",
				"properties": {
					"reward": {
						"type": "object",
						"properties": {
							"name": {"type": "string", "description": "COC7正式名称或场景专属名称"},
							"type": {"type": "string", "enum": ["tome", "artifact"]},
							"description": {"type": "string", "description": "外观特征及与mythos_anchor和剧本主题的叙事关联"},
							"mechanics_note": {"type": "string", "description": "tome: 阅读代价≥1d4 SAN（来自规则书裁定）+ 具体学习收益（克苏鲁神话技能+N 或 可学法术名称）；artifact: 激活条件 + 代价/副作用"}
						},
						"required": ["name", "type", "description", "mechanics_note"]
					}
				},
				"required": ["reward"]
			}`),
		},
	}
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
		MythosAnchor string `json:"mythos_anchor"`
	}{Concept: concept, MythosAnchor: mythosAnchor})

	// Isolated context: fresh message history, no shared state with main pipeline.
	msgs := []llm.ChatMessage{
		{Role: "system", Content: provider.systemPrompt(rewardAgentSystemPrompt)},
		{Role: "user", Content: fmt.Sprintf(`<reward_request>%s</reward_request>`, string(requestJSON))},
	}

	tools := []scripterTool{
		askLawyerTool("向COC7规则书专家提出一个具体规则书问题；确认候选物品是否在规则书中存在、出处、阅读SAN代价、学习收益或激活条件；可多次调用"),
		rewardAgentRespondTool(),
	}

	askedLawyer := false
	var result *rewardAgentReward
	dispatch := func(ctx context.Context, call llm.ToolCall) toolOutcome {
		switch call.Name {
		case toolNameAskLawyer:
			var args askLawyerArgs
			if err := json.Unmarshal([]byte(call.Arguments), &args); err != nil {
				return toolOutcome{reject: "SYSTEM REJECT: ask_lawyer参数不是合法JSON，请重新调用。"}
			}
			askedLawyer = true
			return toolOutcome{result: rewardAgentAskLawyer(ctx, room, args.Question)}
		case toolNameRespond:
			if !askedLawyer {
				return toolOutcome{reject: "SYSTEM REJECT: respond前必须至少调用一次ask_lawyer。"}
			}
			var args struct {
				Reward *rewardAgentReward `json:"reward"`
			}
			if err := json.Unmarshal([]byte(call.Arguments), &args); err != nil {
				return toolOutcome{reject: "SYSTEM REJECT: respond参数不是合法JSON，请重新调用。"}
			}
			if args.Reward == nil {
				return toolOutcome{reject: "SYSTEM REJECT: respond的reward字段不能为空。"}
			}
			if strings.TrimSpace(args.Reward.Name) == "" || strings.TrimSpace(args.Reward.MechanicsNote) == "" {
				return toolOutcome{reject: "SYSTEM REJECT: respond.reward的name和mechanics_note不能为空。"}
			}
			result = args.Reward
			return toolOutcome{result: "已收到，奖励已提交。", done: true}
		default:
			return toolOutcome{reject: fmt.Sprintf("SYSTEM REJECT: reward_agent只允许ask_lawyer/respond，不允许%s。", call.Name)}
		}
	}

	const maxRounds = 16
	if err := runScripterToolLoop(ctx, room, provider, "reward_agent", msgs, tools, maxRounds, dispatch); err != nil {
		return nil, err
	}
	return &models.ScenarioReward{
		Name:          result.Name,
		Type:          result.Type,
		Description:   result.Description,
		MechanicsNote: result.MechanicsNote,
	}, nil
}

func rewardAgentAskLawyer(ctx context.Context, room *scripterRoom, question string) string {
	sessionID := scripterSessionID(ctx, room)
	question = strings.TrimSpace(question)
	if question == "" {
		return `<ask_lawyer_result error="question字段为空"/>`
	}
	log.Printf("[scripter:reward_agent] session=%s ask_lawyer question=%q", sessionID, truncateRunes(question, 300))
	if room.lawyer.provider == nil {
		return fmt.Sprintf(`<ask_lawyer_result question=%q status="lawyer_unavailable">规则书专家不可用；不得声称已核验具体规则书数据。</ask_lawyer_result>`, question)
	}
	results := runLawyer(ctx, room.lawyer, question)
	if len(results) == 0 {
		return fmt.Sprintf(`<ask_lawyer_result question=%q status="no_result">规则书中未找到相关裁定；可换用更具体的候选重新提问，或在结论中标记uncertain。</ask_lawyer_result>`, question)
	}
	return fmt.Sprintf(`<ask_lawyer_result question=%q status="found">%s</ask_lawyer_result>`, question, formatLawyerResults(results))
}

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

const scripterSystemPrompt = `你是 COC TRPG 模组创作的编导者（scripter）。
你的职责是协调整个创作流程，最终生成通过质检的完整模组 JSON。

【工作流程】
1. 调用 generate_architect：让 architect 生成模组框架和大纲（包括结构、场景顺序、NPC关系、线索逻辑）
2. 填充内容：基于 architect 的框架，自行填充具体的场景描述、对白、规则细节等，形成完整可玩的模组
3. 调用 check_qa：让 qa_guard 审查完整模组
4. 迭代修正：根据 QA 反馈，针对性地改进模组内容直到通过审查

【可用工具】
1) generate_architect
{"action":"generate_architect","input":"基于创作需求生成模组框架和大纲"}

2) check_qa
{"action":"check_qa","input":"对完整模组进行可玩性和规则一致性审查"}

【输出规则】
- 仅输出 JSON 数组，不要输出额外文字
- 每轮可调用多个工具
- 任务完成条件：hasDraft=true ∧ hasQA=true ∧ qaResult.pass=true

【创作指导】
- architect 生成框架时会自行咨询 lawyer 保证规则合规
- 你在填充内容时要确保：场景描述生动、NPC 有个性、线索清晰、难度与人数匹配
- 重视 must_fix 反馈，每轮迭代要针对性地解决指出的问题，不要重复提交相同内容`

const architectSystemPrompt = `你是 COC 模组的框架设计者（architect）。
你的职责是设计模组的整体框架和大纲结构，为编导者提供创意方向。

【设计输出结构】
你的 answer 应包含：
- 模组基本信息（名称、难度、人数、主题）
- 核心设定和背景（历史、地理、势力关系）
- 三幕结构（引入→冲突→高潮）
- 主要 NPC 及其动机
- 关键线索与证据链条
- 胜负条件及可能结局

不需要生成完整的 JSON，只需提供清晰的框架大纲和指导方向。

【可用工具】
1) call_lawyer
{"action":"call_lawyer","question":"规则相关的设计问题"}

2) answer
{"action":"answer","result":{
  "name":"模组名",
  "theme":"主题",
  "difficulty":"normal",
  "min_players":1,
  "max_players":4,
  "framework":{
    "setting":"背景设定与历史背景",
    "three_acts":["第一幕：引入事件与冲突起源","第二幕：调查与冲突升级","第三幕：高潮与解决"],
    "key_npcs":["NPC1：身份与动机","NPC2：身份与动机"],
    "clue_chain":["线索1→线索2→线索3..."],
    "win_condition":"胜利条件",
    "failure_condition":"失败条件"
  }
}}

【执行规则】
- 每次输出必须是 JSON 数组
- 可先多轮 call_lawyer，再输出 answer
- 涉及规则内容时必须先咨询 lawyer
- 禁止杜撰不存在的神话元素`

const qaGuardSystemPrompt = `你是质量把控员（qa_guard），负责确保模组的可玩性、一致性和规则合规。

【审查维度】
1. 结构完整性（20分）：框架清晰、场景逻辑顺畅、转折自然
2. 线索设计（25分）：线索完整闭环、证据链清晰、难度适中
3. 规则合规（20分）：涉及的怪物/法术/神话元素都来自 COC 规则，无原创杜撰
4. 可玩性（20分）：NPC 有个性、选项多样、结局开放，适合目标玩家数
5. 文本质量（15分）：描述生动、对白自然、易于理解

【评分标准】
- score >= 85 且 must_fix 为空才通过（pass=true）
- must_fix 中列出的是"必须修正"的阻断项，不是建议
- issues 中可列出"可改进"的地方

【可用工具】
1) call_lawyer
{"action":"call_lawyer","question":"规则相关的审查问题"}

2) answer
{"action":"answer","result":{
	"score":85,
	"pass":true,
	"strengths":["优点1","优点2"],
	"issues":["可改进项1","可改进项2"],
	"must_fix":[]
}}

【执行规则】
- 每次输出必须是 JSON 数组
- 可先多轮 call_lawyer，再输出 answer
- 仅输出 JSON 数组，不要输出额外说明`

type ScenarioCreationRequest struct {
	Name         string `json:"name"`
	Theme        string `json:"theme"`
	Era          string `json:"era"`
	Difficulty   string `json:"difficulty"`
	MinPlayers   int    `json:"min_players"`
	MaxPlayers   int    `json:"max_players"`
	TargetLength string `json:"target_length"`
	Brief        string `json:"brief"`
}

type ScenarioCreationOutput struct {
	Draft      ScenarioDraft `json:"draft"`
	QA         qaGuardResult `json:"qa"`
	Iterations int           `json:"iterations"`
}

type scripterToolCall struct {
	Action string `json:"action"`
	Input  string `json:"input,omitempty"`
}

type qaGuardResult struct {
	Score     int      `json:"score"`
	Pass      bool     `json:"pass"`
	Strengths []string `json:"strengths"`
	Issues    []string `json:"issues"`
	MustFix   []string `json:"must_fix"`
}

type ScenarioDraft struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description"`
	Author      string                 `json:"author"`
	Tags        string                 `json:"tags"`
	MinPlayers  int                    `json:"min_players"`
	MaxPlayers  int                    `json:"max_players"`
	Difficulty  string                 `json:"difficulty"`
	Content     models.ScenarioContent `json:"content"`
}

type qaToolCall struct {
	Action   string         `json:"action"`
	Question string         `json:"question,omitempty"`
	Result   *qaGuardResult `json:"result,omitempty"`
}

type architectToolCall struct {
	Action   string         `json:"action"`
	Question string         `json:"question,omitempty"`
	Result   *ScenarioDraft `json:"result,omitempty"`
}

func RunScripterScenarioTeam(ctx context.Context, req ScenarioCreationRequest) (ScenarioCreationOutput, error) {
	scripter, err := loadSingleAgent(models.AgentRoleScripter)
	if err != nil {
		return ScenarioCreationOutput{}, err
	}
	architect, err := loadSingleAgent(models.AgentRoleArchitect)
	if err != nil {
		return ScenarioCreationOutput{}, err
	}
	lawyer, err := loadSingleAgent(models.AgentRoleLawyer)
	if err != nil {
		return ScenarioCreationOutput{}, err
	}
	qa, err := loadSingleAgent(models.AgentRoleQAGuard)
	if err != nil {
		return ScenarioCreationOutput{}, err
	}

	if req.MinPlayers <= 0 {
		req.MinPlayers = 1
	}
	if req.MaxPlayers <= 0 {
		req.MaxPlayers = 4
	}
	if req.Difficulty == "" {
		req.Difficulty = "normal"
	}

	reqJSON, _ := json.Marshal(req)
	log.Printf("[scripter] 开始生成模组 req=%s", reqJSON)
	msgs := []llm.ChatMessage{
		{Role: "system", Content: scripter.systemPrompt(scripterSystemPrompt)},
		{Role: "user", Content: "创作需求如下（JSON）：\n" + string(reqJSON)},
	}
	// Keep per-agent memory only in-process for this run; no DB persistence.
	architectHistory := []llm.ChatMessage{{Role: "system", Content: architect.systemPrompt(architectSystemPrompt)}}
	qaHistory := []llm.ChatMessage{{Role: "system", Content: qa.systemPrompt(qaGuardSystemPrompt)}}

	var (
		draft     ScenarioDraft
		qaResult  qaGuardResult
		hasDraft  bool
		hasQA     bool
		iterCount int
	)

	for iter := 0; iter < 8; iter++ {
		if ctx.Err() != nil {
			return ScenarioCreationOutput{}, ctx.Err()
		}
		iterCount = iter + 1
		log.Printf("[scripter] 主循环 iter=%d", iterCount)

		raw, err := scripter.provider.Chat(ctx, msgs)
		if err != nil {
			log.Printf("[scripter] iter=%d scripter LLM 调用失败: %v", iterCount, err)
			return ScenarioCreationOutput{}, fmt.Errorf("scripter 调用失败: %w", err)
		}
		calls := parseScripterCalls(raw)
		if len(calls) == 0 {
			log.Printf("[scripter] iter=%d 未解析到 tool call，raw=%s", iterCount, raw)
			return ScenarioCreationOutput{}, fmt.Errorf("scripter 未返回可执行 tool call")
		}
		log.Printf("[scripter] iter=%d tool_calls=%d: %s", iterCount, len(calls), formatScripterCallNames(calls))
		msgs = append(msgs, llm.ChatMessage{Role: "assistant", Content: raw})

		var toolFeedback []string

		for _, c := range calls {
			action := strings.TrimSpace(c.Action)
			switch action {
			case "generate_architect":
				log.Printf("[scripter] iter=%d → generate_architect input=%q", iterCount, c.Input)
				prompt := "根据以下创作指令生成完整模组JSON：\n" + c.Input
				if err := runArchitectWithTools(ctx, architect, lawyer, &architectHistory, prompt, &draft); err != nil {
					log.Printf("[scripter] iter=%d architect 失败: %v", iterCount, err)
					toolFeedback = append(toolFeedback, "generate_architect: 调用失败 - "+err.Error())
					continue
				}
				// Apply deterministic guardrails from request.
				draft.Name = firstNonEmpty(req.Name, draft.Name)
				draft.MinPlayers = req.MinPlayers
				draft.MaxPlayers = req.MaxPlayers
				draft.Difficulty = firstNonEmpty(req.Difficulty, draft.Difficulty)
				if draft.Author == "" {
					draft.Author = "agent-team"
				}
				hasDraft = true
				draftJSON, _ := json.Marshal(draft)
				log.Printf("[scripter] iter=%d generate_architect 完成 name=%q scenes=%d", iterCount, draft.Name, len(draft.Content.Scenes))
				toolFeedback = append(toolFeedback, "generate_architect: "+string(draftJSON))

			case "ask_lawyer":
				log.Printf("[scripter] iter=%d → ask_lawyer input=%q", iterCount, c.Input)
				// Lawyer is queried with search_rule tool in architect's loop
				// Here we just log that the request was made
				toolFeedback = append(toolFeedback, "ask_lawyer: 规则咨询将在 architect 的工具调用中处理（search_rule/call_lawyer）")

			case "check_qa":
				if !hasDraft {
					log.Printf("[scripter] iter=%d check_qa 跳过: 尚无模组", iterCount)
					toolFeedback = append(toolFeedback, "check_qa: 当前没有可审查的模组")
					continue
				}
				log.Printf("[scripter] iter=%d → check_qa input=%q", iterCount, c.Input)
				prompt := "请对以下模组进行可玩性和一致性审查：\n" + c.Input
				if err := runQAGuardWithTools(ctx, qa, lawyer, &qaHistory, prompt, &qaResult); err != nil {
					log.Printf("[scripter] iter=%d qa_guard 失败: %v", iterCount, err)
					toolFeedback = append(toolFeedback, "check_qa: 调用失败 - "+err.Error())
					continue
				}
				hasQA = true
				qaJSON, _ := json.Marshal(qaResult)
				log.Printf("[scripter] iter=%d check_qa 完成 score=%d pass=%v must_fix=%d", iterCount, qaResult.Score, qaResult.Pass, len(qaResult.MustFix))
				toolFeedback = append(toolFeedback, "check_qa: "+string(qaJSON))

				// If QA passes, we can finalize immediately
				if qaResult.Pass {
					log.Printf("[scripter] QA通过，生成完成 iter=%d name=%q qa_score=%d", iterCount, draft.Name, qaResult.Score)
					return ScenarioCreationOutput{Draft: draft, QA: qaResult, Iterations: iterCount}, nil
				}

			default:
				log.Printf("[scripter] iter=%d 未知 tool: %s", iterCount, action)
				toolFeedback = append(toolFeedback, "unknown_tool: "+action)
			}
		}

		if len(toolFeedback) == 0 {
			toolFeedback = append(toolFeedback, "本轮无有效工具输出")
		}
		msgs = append(msgs, llm.ChatMessage{Role: "user", Content: "工具执行结果：\n" + strings.Join(toolFeedback, "\n")})
	}

	if hasDraft && hasQA && qaResult.Pass {
		log.Printf("[scripter] 达到最大迭代但QA已通过，返回结果 name=%q qa_score=%d", draft.Name, qaResult.Score)
		return ScenarioCreationOutput{Draft: draft, QA: qaResult, Iterations: iterCount}, nil
	}

	if hasDraft {
		if hasQA && !qaResult.Pass {
			log.Printf("[scripter] 达到最大迭代，qa 未通过 score=%d must_fix=%v", qaResult.Score, qaResult.MustFix)
			return ScenarioCreationOutput{}, fmt.Errorf("scripter 达到最大迭代，qa 未通过：%s", strings.Join(qaResult.MustFix, "；"))
		}
		log.Printf("[scripter] 达到最大迭代，返回已有草案 name=%q（QA未执行）", draft.Name)
		return ScenarioCreationOutput{Draft: draft, QA: qaResult, Iterations: iterCount}, nil
	}
	log.Printf("[scripter] 达到最大迭代仍未产出模组")
	return ScenarioCreationOutput{}, fmt.Errorf("scripter 达到最大迭代仍未产出模组")
}

func runJSONAgentWithHistory[T any](ctx context.Context, h agentHandle, history *[]llm.ChatMessage, input string, out *T) error {
	if history == nil || len(*history) == 0 {
		return fmt.Errorf("agent history 未初始化")
	}

	var roleTag string
	if h.config != nil {
		roleTag = string(h.config.Role)
	}
	*history = append(*history, llm.ChatMessage{Role: "user", Content: input})
	log.Printf("[agent:%s] Chat history_len=%d", roleTag, len(*history))

	const maxRetries = 5
	var lastErr error
	var raw string

	for attempt := 1; attempt <= maxRetries; attempt++ {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		resp, err := h.provider.Chat(ctx, *history)
		if err != nil {
			log.Printf("[agent:%s] Chat 失败: %v", roleTag, err)
			return err
		}
		raw = resp
		log.Printf("[agent:%s] attempt=%d resp_len=%d", roleTag, attempt, len([]rune(raw)))

		if err := parseJSONObject(raw, out); err == nil {
			// Success: commit assistant message and return.
			*history = append(*history, llm.ChatMessage{Role: "assistant", Content: raw})
			log.Printf("[agent:%s] 完成 attempt=%d", roleTag, attempt)
			return nil
		} else {
			lastErr = err
			log.Printf("[agent:%s] JSON 解析失败 attempt=%d err=%v raw=%s", roleTag, attempt, err, raw)
			if attempt < maxRetries {
				// Ask model to fix output without polluting history.
				retryMsgs := make([]llm.ChatMessage, len(*history))
				copy(retryMsgs, *history)
				retryMsgs = append(retryMsgs,
					llm.ChatMessage{Role: "assistant", Content: raw},
					llm.ChatMessage{Role: "user", Content: "你的上一条输出不是合法的JSON对象，请只输出合法JSON，不要包含其他内容。"},
				)
				if resp2, err2 := h.provider.Chat(ctx, retryMsgs); err2 == nil {
					if err3 := parseJSONObject(resp2, out); err3 == nil {
						*history = append(*history, llm.ChatMessage{Role: "assistant", Content: resp2})
						log.Printf("[agent:%s] JSON 修复成功 attempt=%d", roleTag, attempt)
						return nil
					}
					raw = resp2
				}
			}
		}
	}

	return fmt.Errorf("[agent:%s] JSON 解析失败（%d次重试）: %w", roleTag, maxRetries, lastErr)
}

func runArchitectWithTools(ctx context.Context, architect agentHandle, lawyer agentHandle, history *[]llm.ChatMessage, input string, out *ScenarioDraft) error {
	if history == nil || len(*history) == 0 {
		return fmt.Errorf("architect history 未初始化")
	}

	*history = append(*history, llm.ChatMessage{Role: "user", Content: input})
	log.Printf("[architect] 开始 history_len=%d", len(*history))

	const maxIter = 6
	for iter := 0; iter < maxIter; iter++ {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		log.Printf("[architect] iter=%d", iter+1)

		raw, err := architect.provider.Chat(ctx, *history)
		if err != nil {
			log.Printf("[architect] iter=%d Chat 失败: %v", iter+1, err)
			return err
		}
		*history = append(*history, llm.ChatMessage{Role: "assistant", Content: raw})

		calls := parseArchitectCalls(raw)
		if len(calls) == 0 {
			if err := parseJSONObject(raw, out); err == nil {
				log.Printf("[architect] iter=%d 直接解析 JSON 成功", iter+1)
				return nil
			}
			return fmt.Errorf("architect 未返回可执行 tool call")
		}

		var feedback []string
		for _, c := range calls {
			action := strings.TrimSpace(c.Action)
			switch action {
			case "call_lawyer":
				question := strings.TrimSpace(c.Question)
				if question == "" {
					feedback = append(feedback, "call_lawyer: question 为空")
					continue
				}
				results := runLawyer(ctx, lawyer, question, rulebook.GlobalIndex)
				text := formatLawyerResults(results)
				if text == "" {
					text = "（lawyer 未给出可用规则结论）"
				}
				feedback = append(feedback, "call_lawyer["+question+"]: "+text)

			case "answer":
				if c.Result == nil {
					return fmt.Errorf("architect answer 缺少 result")
				}
				*out = *c.Result
				return nil

			default:
				feedback = append(feedback, "unknown_tool: "+action)
			}
		}

		if len(feedback) == 0 {
			feedback = append(feedback, "本轮无有效工具输出")
		}
		*history = append(*history, llm.ChatMessage{Role: "user", Content: "工具执行结果：\n" + strings.Join(feedback, "\n")})
	}

	return fmt.Errorf("architect 达到最大迭代仍未返回 answer")
}

func runQAGuardWithTools(ctx context.Context, qa agentHandle, lawyer agentHandle, history *[]llm.ChatMessage, input string, out *qaGuardResult) error {
	if history == nil || len(*history) == 0 {
		return fmt.Errorf("qa history 未初始化")
	}

	*history = append(*history, llm.ChatMessage{Role: "user", Content: input})
	log.Printf("[qa_guard] 开始 history_len=%d", len(*history))

	const maxIter = 5
	for iter := 0; iter < maxIter; iter++ {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		log.Printf("[qa_guard] iter=%d", iter+1)

		raw, err := qa.provider.Chat(ctx, *history)
		if err != nil {
			log.Printf("[qa_guard] iter=%d Chat 失败: %v", iter+1, err)
			return err
		}
		*history = append(*history, llm.ChatMessage{Role: "assistant", Content: raw})

		calls := parseQACalls(raw)
		if len(calls) == 0 {
			if err := parseJSONObject(raw, out); err == nil {
				log.Printf("[qa_guard] iter=%d 直接解析 JSON 成功", iter+1)
				return nil
			}
			log.Printf("[qa_guard] iter=%d 未解析到 tool call，raw=%s", iter+1, raw)
			return fmt.Errorf("qa_guard 未返回可执行 tool call")
		}

		var feedback []string
		for _, c := range calls {
			action := strings.TrimSpace(c.Action)
			switch action {
			case "call_lawyer":
				question := strings.TrimSpace(c.Question)
				if question == "" {
					feedback = append(feedback, "call_lawyer: question 为空")
					continue
				}
				log.Printf("[qa_guard] call_lawyer question=%q", question)
				results := runLawyer(ctx, lawyer, question, rulebook.GlobalIndex)
				text := formatLawyerResults(results)
				if text == "" {
					text = "（lawyer 未给出可用规则结论）"
				}
				log.Printf("[qa_guard] call_lawyer 结果 len=%d", len(text))
				feedback = append(feedback, "call_lawyer["+question+"]: "+text)

			case "answer":
				if c.Result == nil {
					return fmt.Errorf("qa_guard answer 缺少 result")
				}
				log.Printf("[qa_guard] answer 完成 score=%d pass=%v must_fix=%d", c.Result.Score, c.Result.Pass, len(c.Result.MustFix))
				*out = *c.Result
				return nil

			default:
				log.Printf("[qa_guard] 未知 tool: %s", action)
				feedback = append(feedback, "unknown_tool: "+action)
			}
		}

		if len(feedback) == 0 {
			feedback = append(feedback, "本轮无有效工具输出")
		}
		*history = append(*history, llm.ChatMessage{Role: "user", Content: "工具执行结果：\n" + strings.Join(feedback, "\n")})
	}

	log.Printf("[qa_guard] 达到最大迭代未返回 answer")
	return fmt.Errorf("qa_guard 达到最大迭代仍未返回 answer")
}

func parseScripterCalls(raw string) []scripterToolCall {
	stripped := llm.StripCodeFence(raw)
	var calls []scripterToolCall
	if err := json.Unmarshal([]byte(stripped), &calls); err == nil && len(calls) > 0 {
		return calls
	}
	if s := strings.Index(stripped, "["); s >= 0 {
		if e := strings.LastIndex(stripped, "]"); e > s {
			_ = json.Unmarshal([]byte(stripped[s:e+1]), &calls)
		}
	}
	return calls
}

func parseQACalls(raw string) []qaToolCall {
	stripped := llm.StripCodeFence(raw)
	var calls []qaToolCall
	if err := json.Unmarshal([]byte(stripped), &calls); err == nil && len(calls) > 0 {
		return calls
	}
	if s := strings.Index(stripped, "["); s >= 0 {
		if e := strings.LastIndex(stripped, "]"); e > s {
			_ = json.Unmarshal([]byte(stripped[s:e+1]), &calls)
		}
	}
	return calls
}

func parseArchitectCalls(raw string) []architectToolCall {
	stripped := llm.StripCodeFence(raw)
	var calls []architectToolCall
	if err := json.Unmarshal([]byte(stripped), &calls); err == nil && len(calls) > 0 {
		return calls
	}
	if s := strings.Index(stripped, "["); s >= 0 {
		if e := strings.LastIndex(stripped, "]"); e > s {
			_ = json.Unmarshal([]byte(stripped[s:e+1]), &calls)
		}
	}
	return calls
}

func parseJSONObject[T any](raw string, out *T) error {
	var err error
	stripped := llm.StripCodeFence(strings.TrimSpace(raw))
	if err = json.Unmarshal([]byte(stripped), out); err == nil {
		return nil
	}
	s := strings.Index(stripped, "{")
	e := strings.LastIndex(stripped, "}")
	if s >= 0 && e > s {
		if err = json.Unmarshal([]byte(stripped[s:e+1]), out); err == nil {
			return nil
		}
	}
	return fmt.Errorf("JSON 解析失败: %w", err)
}

func buildQAInput(req ScenarioCreationRequest, draft ScenarioDraft, extra string) string {
	reqJSON, _ := json.Marshal(req)
	draftJSON, _ := json.Marshal(draft)
	base := "请对以下模组草案做质量审查。\n需求：" + string(reqJSON) + "\n草案：" + string(draftJSON)
	if strings.TrimSpace(extra) != "" {
		base += "\n额外审查维度：" + extra
	}
	return base
}

func formatScripterCallNames(calls []scripterToolCall) string {
	names := make([]string, len(calls))
	for i, c := range calls {
		names[i] = c.Action
	}
	return strings.Join(names, ", ")
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

func requirePersistedSystemPrompt(h agentHandle, role models.AgentRole) (string, error) {
	if h.config == nil {
		return "", fmt.Errorf("agent %q 缺少配置", role)
	}
	prompt := strings.TrimSpace(h.config.SystemPrompt)
	if prompt == "" {
		return "", fmt.Errorf("agent %q 未配置 system_prompt，请在管理面板保存后重试", role)
	}
	return prompt, nil
}

// scripter_toolloop.go — 原生 tool calling 共享驱动器。
//
// 把 Scripter 内所有多轮工具循环（translator/reward_agent/story architect/
// oneshot architect repair）以及单次工具输出（qa_humanize/logic_review/compile）
// 共同的协议机制抽成一个驱动器：发起请求、把助手消息（含 ToolCalls）写入历史、
// 未知工具名拒绝、"独占一轮"工具的混批拒绝、为每个 tool_call_id 生成对应的
// tool 消息、连续空 tool_calls 快速失败、轮数耗尽报错。业务判定（参数解码、
// 字段校验、前置状态如"respond前必须先ask_lawyer"）由各调用方通过 dispatch
// 闭包提供，终止工具成功后的结构化结果由调用方通过闭包捕获的变量取回。
package agent

import (
	"context"
	"fmt"
	"log"
	"strings"

	"github.com/llmcoc/server/internal/services/llm"
)

// scripterTool 是 Scripter 侧一个原生工具的定义；solo=true 表示该工具必须独占一轮
// （不能与其他工具调用出现在同一轮响应中）。
type scripterTool struct {
	def  llm.ToolDefinition
	solo bool
}

// toolOutcome 是 dispatch 闭包处理一次工具调用后的结果。
//
//   - reject 非空：本次调用被拒绝，reject 的内容作为对应 tool 消息回传，
//     驱动器继续下一轮，不认为循环已终止（即使 done=true 也会被忽略）。
//   - reject 为空且 done=true：终止工具已成功执行并被调用方闭包捕获，
//     驱动器在本轮所有 tool_call 都得到响应后返回成功。
//   - reject 为空且 done=false：普通非终止工具调用（如 ask_lawyer/translate_anchor）
//     成功执行，result 作为对应 tool 消息回传，循环继续。
type toolOutcome struct {
	result string
	reject string
	done   bool
}

// scripterToolDispatch 处理一次工具调用；call.Name 保证已在 tools 列表中。
type scripterToolDispatch func(ctx context.Context, call llm.ToolCall) toolOutcome

// maxConsecutiveEmptyRounds 是连续多少轮模型未返回任何工具调用后判定端点不支持/
// 不配合 function calling 并快速失败；避免在不兼容端点上跑满 maxRounds 才报错。
const maxConsecutiveEmptyRounds = 3

// runScripterToolLoop 驱动一次原生工具调用的多轮循环。
//
// room 允许为 nil：nil 时 recordScripterLLMExchange 不会经由 room.generationLog/
// room.progressFn 记录，只落到 ctx 携带的生成日志（与迁移前 chatAndParseJSON 对
// compile/qa_humanize/logic_review 恒传 nil room 的行为一致）。
func runScripterToolLoop(
	ctx context.Context,
	room *scripterRoom,
	handle agentHandle,
	stage string,
	msgs []llm.ChatMessage,
	tools []scripterTool,
	maxRounds int,
	dispatch scripterToolDispatch,
) error {
	if handle.provider == nil {
		return fmt.Errorf("%s provider unavailable", stage)
	}
	sessionID := scripterSessionID(ctx, room)

	toolDefs := make([]llm.ToolDefinition, len(tools))
	validNames := make(map[string]bool, len(tools))
	soloNames := make(map[string]bool)
	for i, t := range tools {
		toolDefs[i] = t.def
		validNames[t.def.Name] = true
		if t.solo {
			soloNames[t.def.Name] = true
		}
	}

	emptyRounds := 0
	for round := 1; round <= maxRounds; round++ {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		roundStage := fmt.Sprintf("%s_round_%d", stage, round)
		logStagePrompt(roundStage, sessionID, msgs)
		callMessages := append([]llm.ChatMessage(nil), msgs...)

		result, err := handle.provider.ChatWithTools(ctx, handle.cacheKey(sessionID), msgs, toolDefs)
		if err != nil {
			return err
		}
		recordScripterLLMExchange(ctx, room, roundStage, callMessages, renderToolChatResultForLog(result))
		log.Printf("[scripter:%s] session=%s round=%d tool_calls=%d content_len=%d",
			stage, sessionID, round, len(result.ToolCalls), len([]rune(result.Content)))
		msgs = append(msgs, llm.ChatMessage{Role: "assistant", Content: result.Content, ToolCalls: result.ToolCalls})

		if len(result.ToolCalls) == 0 {
			emptyRounds++
			if emptyRounds >= maxConsecutiveEmptyRounds {
				modelName := "?"
				if handle.config != nil && strings.TrimSpace(handle.config.ModelName) != "" {
					modelName = handle.config.ModelName
				}
				return fmt.Errorf("%s 连续 %d 轮未返回任何工具调用，端点可能不支持或未正确配置 function calling（agent=%s model=%s）",
					stage, emptyRounds, handle.roleName(), modelName)
			}
			msgs = append(msgs, llm.ChatMessage{Role: "user", Content: "SYSTEM REJECT: 必须输出至少一个工具调用。"})
			continue
		}
		emptyRounds = 0

		// 独占一轮的工具与其他任意调用混批：整批拒绝，每个 tool_call_id 都要有回执。
		if soloMixed(result.ToolCalls, soloNames) {
			names := soloNamesIn(result.ToolCalls, soloNames)
			rejectMsg := fmt.Sprintf(
				"SYSTEM REJECT: %s 必须单独一轮调用，不能与其他工具调用混在同一轮响应中。若还需调用其他工具，本轮先不要包含%s；确认无需再调用其他工具后，下一轮再单独调用%s。",
				strings.Join(names, "/"), strings.Join(names, "/"), strings.Join(names, "/"))
			for _, call := range result.ToolCalls {
				msgs = append(msgs, llm.ChatMessage{Role: "tool", ToolCallID: call.ID, Content: rejectMsg})
			}
			continue
		}

		done := false
		for _, call := range result.ToolCalls {
			if !validNames[call.Name] {
				msgs = append(msgs, llm.ChatMessage{
					Role: "tool", ToolCallID: call.ID,
					Content: fmt.Sprintf("SYSTEM REJECT: 未知工具 %q，此阶段不允许调用。", call.Name),
				})
				continue
			}
			outcome := dispatch(ctx, call)
			content := outcome.result
			if outcome.reject != "" {
				content = outcome.reject
			}
			msgs = append(msgs, llm.ChatMessage{Role: "tool", ToolCallID: call.ID, Content: content})
			if outcome.reject == "" && outcome.done {
				done = true
			}
		}
		if done {
			return nil
		}
	}
	return fmt.Errorf("%s 未在%d轮内完成", stage, maxRounds)
}

// soloMixed 判断本轮响应中是否有 solo 工具与其他调用（含另一个 solo 工具）混批。
func soloMixed(calls []llm.ToolCall, soloNames map[string]bool) bool {
	hasSolo := false
	for _, c := range calls {
		if soloNames[c.Name] {
			hasSolo = true
			break
		}
	}
	return hasSolo && len(calls) != 1
}

// soloNamesIn 返回本轮响应中出现的 solo 工具名（去重，保持首次出现顺序），用于拒绝提示文案。
func soloNamesIn(calls []llm.ToolCall, soloNames map[string]bool) []string {
	seen := make(map[string]bool)
	var names []string
	for _, c := range calls {
		if soloNames[c.Name] && !seen[c.Name] {
			seen[c.Name] = true
			names = append(names, c.Name)
		}
	}
	return names
}

// jsonSchemaObject 是最简 JSON Schema 构造辅助：避免每个工具定义手写转义字符串。
func jsonSchemaObject(schema string) []byte {
	return []byte(schema)
}

// ---------------------------------------------------------------------------
// 共享工具名与参数 schema
// ---------------------------------------------------------------------------

const (
	toolNameAskLawyer       = "ask_lawyer"
	toolNameRespond         = "respond"
	toolNameTranslateAnchor = "translate_anchor"
	toolNameSubmitStory     = "submit_story"
	toolNameSubmit          = "submit"
	toolNameReportIssues    = "report_issues"
	toolNameSubmitCompiled  = "submit_compiled_scenario"
)

// askLawyerTool 是 translator / reward_agent 共用的 ask_lawyer 工具定义。
func askLawyerTool(description string) scripterTool {
	return scripterTool{
		solo: false,
		def: llm.ToolDefinition{
			Name:        toolNameAskLawyer,
			Description: description,
			Parameters: jsonSchemaObject(`{
				"type": "object",
				"properties": {
					"question": {"type": "string", "description": "向COC7规则书专家提出的具体问题"}
				},
				"required": ["question"]
			}`),
		},
	}
}

// translateAnchorTool 是 story architect / oneshot architect repair 共用的
// translate_anchor 工具定义。
func translateAnchorTool(description string) scripterTool {
	return scripterTool{
		solo: false,
		def: llm.ToolDefinition{
			Name:        toolNameTranslateAnchor,
			Description: description,
			Parameters: jsonSchemaObject(`{
				"type": "object",
				"properties": {
					"concept": {"type": "string", "description": "概念描述"},
					"reason": {"type": "string", "description": "该概念在剧本中承担什么角色"}
				},
				"required": ["concept", "reason"]
			}`),
		},
	}
}

// askLawyerArgs 是 ask_lawyer 工具调用参数。
type askLawyerArgs struct {
	Question string `json:"question"`
}

// translateAnchorArgs 是 translate_anchor 工具调用参数。
type translateAnchorArgs struct {
	Concept string `json:"concept"`
	Reason  string `json:"reason"`
}

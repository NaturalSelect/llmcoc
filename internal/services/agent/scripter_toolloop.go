// scripter_toolloop.go — 原生 tool calling 共享驱动器。
//
// 把 Scripter 内所有多轮工具循环（translator/reward_agent/story architect/
// oneshot architect repair）以及单次工具输出（qa_humanize/logic_review/compile）
// 共同的协议机制抽成一个驱动器：发起请求、把助手消息（含 ToolCalls）写入历史、
// 未知工具名拒绝、"独占一轮"工具的混批拒绝、为每个 tool_call_id 生成对应的
// tool 消息、连续空 tool_calls 快速失败、轮数耗尽报错。业务判定（参数解码、
// 字段校验、前置状态如"respond前必须先ask_lawyer"）由各调用方通过 dispatch
// 闭包提供，终止工具成功后的结构化结果由调用方通过闭包捕获的变量取回。
//
// runToolLoop 核心已扩展支持按轮次变化的工具集（firstRoundTools）和自定义分组
// 互斥策略（batchPolicy），供 Lawyer（lawyer.go）复用；runScripterToolLoop 是
// 面向 Scripter 现有调用点的兼容包装，参数与行为保持逐字不变。
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

// toolBatchPolicy 校验一轮工具调用是否违反独占/分组约束；返回非空字符串表示整批
// 拒绝，内容作为本轮每个 tool_call_id 的统一回执；返回空字符串表示本轮合法，继续
// 正常分发。
type toolBatchPolicy func(calls []llm.ToolCall) string

// toolBatchDispatch 批量处理一轮全部合法工具调用（已剔除未知工具名），下标与入参
// calls 一一对应；用于需要保留批内并发（如同轮多个独立查询）的调用方。返回切片
// 长度必须等于入参长度，否则驱动器会为缺失位置补一条统一拒绝回执。
type toolBatchDispatch func(ctx context.Context, calls []llm.ToolCall) []toolOutcome

// maxConsecutiveEmptyRounds 是连续多少轮模型未返回任何工具调用后判定端点不支持/
// 不配合 function calling 并快速失败；避免在不兼容端点上跑满 maxRounds 才报错。
const maxConsecutiveEmptyRounds = 3

// toolLoopOptions 是 runToolLoop 的完整参数集合。runScripterToolLoop 是它面向
// Scripter 现有调用点的兼容包装，只暴露下面几个必填字段；后四个可选字段零值时
// 沿用与迁移前 Scripter 逐字一致的行为，供 Lawyer 等其他 agent 定制。
type toolLoopOptions struct {
	room      *scripterRoom
	handle    agentHandle
	stage     string
	msgs      []llm.ChatMessage
	tools     []scripterTool
	maxRounds int
	dispatch  scripterToolDispatch

	// firstRoundTools 非空时，仅第1轮把可用工具集限制为这个列表（强制模型第一轮
	// 只能调用其中的工具），第2轮起恢复使用 tools。为空则每轮都使用 tools。
	firstRoundTools []scripterTool
	// batchPolicy 为 nil 时使用默认策略：由 tools 中 solo=true 的工具构成
	// soloNames，调用 soloMixed/soloNamesIn 判定"独占工具与任意其他调用混批"。
	batchPolicy toolBatchPolicy
	// cacheKeyOverride 非空时替代 handle.cacheKey(sessionID) 作为 prompt cache key。
	cacheKeyOverride string
	// afterRound 非 nil 时，在每轮工具分发完成后（跳过空 tool_calls 轮与整批拒绝轮）
	// 调用一次，供调用方在闭包中记录逐轮统计。
	afterRound func()

	// batchDispatch 非 nil 时，本轮全部合法工具调用整批交给它处理（保留调用方自定义
	// 的批内并发），驱动器按原始 tool_call 顺序把结果散回；为 nil 时使用现有的
	// dispatch 串行分支，逐字不变。
	batchDispatch toolBatchDispatch
	// beforeRound 非 nil 时，在每轮 ctx.Err() 检查之后、ChatWithTools 调用之前触发，
	// round 从 1 开始计数。
	beforeRound func(round int)
	// onToolCalls 非 nil 时，在拿到本轮非空 tool_calls（assistant 消息已写入 msgs）、
	// batchPolicy 判定之前触发，供调用方发出"计划执行哪些工具"一类的进度提示。
	onToolCalls func(calls []llm.ToolCall)
}

// runScripterToolLoop 驱动一次原生工具调用的多轮循环。
//
// room 允许为 nil：nil 时 recordScripterLLMExchange 不会经由 room.generationLog/
// room.progressFn 记录，只落到 ctx 携带的生成日志（与迁移前 chatAndParseJSON 对
// compile/qa_humanize/logic_review 恒传 nil room 的行为一致）。
//
// 本函数是 runToolLoop 的兼容包装，参数与行为保持逐字不变；按轮次变化的工具集、
// 非二元的分组互斥策略等能力见 runToolLoop 与 toolLoopOptions。
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
	return runToolLoop(ctx, toolLoopOptions{
		room:      room,
		handle:    handle,
		stage:     stage,
		msgs:      msgs,
		tools:     tools,
		maxRounds: maxRounds,
		dispatch:  dispatch,
	})
}

// buildToolState 把工具定义列表展开为 ChatWithTools 需要的 []ToolDefinition，
// 以及供本轮校验使用的"合法工具名"和"独占工具名"集合。
func buildToolState(tools []scripterTool) (defs []llm.ToolDefinition, validNames, soloNames map[string]bool) {
	defs = make([]llm.ToolDefinition, len(tools))
	validNames = make(map[string]bool, len(tools))
	soloNames = make(map[string]bool)
	for i, t := range tools {
		defs[i] = t.def
		validNames[t.def.Name] = true
		if t.solo {
			soloNames[t.def.Name] = true
		}
	}
	return defs, validNames, soloNames
}

// defaultBatchPolicy 用 soloNames 构造默认的独占互斥策略：某个 solo 工具与本轮
// 任意其他调用（含另一个 solo 工具）同时出现即整批拒绝。
func defaultBatchPolicy(soloNames map[string]bool) toolBatchPolicy {
	return func(calls []llm.ToolCall) string {
		if !soloMixed(calls, soloNames) {
			return ""
		}
		names := soloNamesIn(calls, soloNames)
		return fmt.Sprintf(
			"SYSTEM REJECT: %s 必须单独一轮调用，不能与其他工具调用混在同一轮响应中。若还需调用其他工具，本轮先不要包含%s；确认无需再调用其他工具后，下一轮再单独调用%s。",
			strings.Join(names, "/"), strings.Join(names, "/"), strings.Join(names, "/"))
	}
}

// runToolLoop 驱动一次原生工具调用的多轮循环，是 Scripter（经 runScripterToolLoop
// 兼容包装）与 Lawyer 等 agent 共用的核心实现。
func runToolLoop(ctx context.Context, opts toolLoopOptions) error {
	handle := opts.handle
	stage := opts.stage
	msgs := opts.msgs
	if handle.provider == nil {
		return fmt.Errorf("%s provider unavailable", stage)
	}
	sessionID := scripterSessionID(ctx, opts.room)

	defToolDefs, defValidNames, defSoloNames := buildToolState(opts.tools)
	firstToolDefs, firstValidNames, firstSoloNames := defToolDefs, defValidNames, defSoloNames
	if len(opts.firstRoundTools) > 0 {
		firstToolDefs, firstValidNames, firstSoloNames = buildToolState(opts.firstRoundTools)
	}

	cacheKey := opts.cacheKeyOverride
	if cacheKey == "" {
		cacheKey = handle.cacheKey(sessionID)
	}

	emptyRounds := 0
	for round := 1; round <= opts.maxRounds; round++ {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if opts.beforeRound != nil {
			opts.beforeRound(round)
		}
		toolDefs, validNames, soloNames := defToolDefs, defValidNames, defSoloNames
		if round == 1 && len(opts.firstRoundTools) > 0 {
			toolDefs, validNames, soloNames = firstToolDefs, firstValidNames, firstSoloNames
		}
		policy := opts.batchPolicy
		if policy == nil {
			policy = defaultBatchPolicy(soloNames)
		}

		roundStage := fmt.Sprintf("%s_round_%d", stage, round)
		logStagePrompt(roundStage, sessionID, msgs)
		callMessages := append([]llm.ChatMessage(nil), msgs...)

		result, err := handle.provider.ChatWithTools(ctx, cacheKey, msgs, toolDefs)
		if err != nil {
			return err
		}
		recordScripterLLMExchange(ctx, opts.room, roundStage, callMessages, renderToolChatResultForLog(result))
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

		if opts.onToolCalls != nil {
			opts.onToolCalls(result.ToolCalls)
		}

		// 独占一轮的工具（或自定义分组策略判定违规的调用）与其他调用混批：
		// 整批拒绝，每个 tool_call_id 都要有回执。
		if rejectMsg := policy(result.ToolCalls); rejectMsg != "" {
			for _, call := range result.ToolCalls {
				msgs = append(msgs, llm.ChatMessage{Role: "tool", ToolCallID: call.ID, Content: rejectMsg})
			}
			continue
		}

		done := false
		if opts.batchDispatch != nil {
			// 按索引分流：未知工具名直接产出 reject outcome（文案与串行分支逐字相同），
			// 合法调用收集为子切片整批交给 batchDispatch，再按原索引把结果散回。
			outcomes := make([]toolOutcome, len(result.ToolCalls))
			var validIdx []int
			var validCalls []llm.ToolCall
			for i, call := range result.ToolCalls {
				if !validNames[call.Name] {
					outcomes[i] = toolOutcome{reject: fmt.Sprintf("SYSTEM REJECT: 未知工具 %q，此阶段不允许调用。", call.Name)}
					continue
				}
				validIdx = append(validIdx, i)
				validCalls = append(validCalls, call)
			}
			if len(validCalls) > 0 {
				batchOutcomes := opts.batchDispatch(ctx, validCalls)
				for j, idx := range validIdx {
					if j < len(batchOutcomes) {
						outcomes[idx] = batchOutcomes[j]
					} else {
						outcomes[idx] = toolOutcome{reject: "SYSTEM REJECT: 工具执行未返回结果，请重试。"}
					}
				}
			}
			for i, call := range result.ToolCalls {
				outcome := outcomes[i]
				content := outcome.result
				if outcome.reject != "" {
					content = outcome.reject
				}
				msgs = append(msgs, llm.ChatMessage{Role: "tool", ToolCallID: call.ID, Content: content})
				if outcome.reject == "" && outcome.done {
					done = true
				}
			}
		} else {
			for _, call := range result.ToolCalls {
				if !validNames[call.Name] {
					msgs = append(msgs, llm.ChatMessage{
						Role: "tool", ToolCallID: call.ID,
						Content: fmt.Sprintf("SYSTEM REJECT: 未知工具 %q，此阶段不允许调用。", call.Name),
					})
					continue
				}
				outcome := opts.dispatch(ctx, call)
				content := outcome.result
				if outcome.reject != "" {
					content = outcome.reject
				}
				msgs = append(msgs, llm.ChatMessage{Role: "tool", ToolCallID: call.ID, Content: content})
				if outcome.reject == "" && outcome.done {
					done = true
				}
			}
		}
		if opts.afterRound != nil {
			opts.afterRound()
		}
		if done {
			return nil
		}
	}
	return fmt.Errorf("%s 未在%d轮内完成", stage, opts.maxRounds)
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
	toolNameGenerateNPCName = "generate_npc_name"
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

// generateNPCNameTool 是 story architect / oneshot architect repair 共用的
// generate_npc_name 工具定义：从确定性姓名池中随机抽取姓名，避免 AI 自行编造。
// 具体的参数解析与执行逻辑见 scripter_names.go 的 dispatchGenerateNPCName。
func generateNPCNameTool() scripterTool {
	return scripterTool{
		solo: false,
		def: llm.ToolDefinition{
			Name: toolNameGenerateNPCName,
			Description: `从预置姓名池中随机生成符合指定文化背景与性别的NPC姓名，避免自行编造。可一次生成多个候选（count，默认1，最多5）供挑选。
调用示例：{"culture":"western","gender":"male"}`,
			Parameters: jsonSchemaObject(`{
				"type": "object",
				"properties": {
					"culture": {
						"type": "string",
						"enum": ["western", "chinese", "japanese"],
						"description": "姓名的文化背景：western=英美/欧洲，chinese=中文，japanese=日本"
					},
					"gender": {
						"type": "string",
						"enum": ["male", "female"],
						"description": "性别"
					},
					"count": {
						"type": "integer",
						"description": "一次生成几个候选姓名，默认1，最多5"
					}
				},
				"required": ["culture", "gender"]
			}`),
		},
	}
}

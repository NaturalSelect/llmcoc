package agent

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/llmcoc/server/internal/models"
	"github.com/llmcoc/server/internal/services/llm"
)

// NOTE: 本文件是 Director 原生 tool calling 的批次校验(batchPolicy)与批量执行
// (batchDispatch)桥接，取代旧 orchestrator.go run() 里手写的 hasResponse/
// hasNonCompatible/emptyResponse/generateImage 系列检查，以及 executeParallelBatch
// 的并发调度。行为对齐旧代码，唯一实质变化是：act_npc 与副作用工具混批从旧协议里
// "act_npc执行后*actx.Interrupt=true静默丢弃本批剩余调用"，改成在 batchPolicy 阶段
// 整批显式拒绝并给出可读错误，逼模型下一轮再发——不再需要 Interrupt。

// directorActNPCCompatible 判断某个动作是否可以与 act_npc 同批调用：无副作用查询类
// 工具(含 act_npc 自身)以及零副作用的 report 自由工具。此外的任何工具都必须等
// act_npc 结果被读取后，放到下一轮再调用。
func directorActNPCCompatible(action ToolCallType) bool {
	if noSideEffectActions[action] {
		return true
	}
	return action == ToolReport
}

// directorBatchPolicy 构造 Director 原生工具循环的整批校验策略。
// imageGeneratedThisTurn 由 run() 持有并跨轮次共享，用于保留"每回合最多生成一张
// 图片"的限制(这条限制跨越多轮 ChatWithTools 调用，必须用闭包捕获的指针维持)。
// emitProgress 用于在拒绝分支保留原有的 SSE 提示文案。
func directorBatchPolicy(imageGeneratedThisTurn *bool, emitProgress func(string)) toolBatchPolicy {
	return func(calls []llm.ToolCall) string {
		hasResponse := false
		hasNonCompatible := false
		respStr := ""
		for _, call := range calls {
			action := ToolCallType(call.Name)
			if action == ToolResponse || action == ToolEndGame {
				hasResponse = true
				if tc, err := decodeDirectorToolCall(call); err == nil {
					if tc.Reply != "" {
						respStr = tc.Reply
					} else {
						respStr = tc.EndSummary
					}
				}
			}
			if !responseCompatibleActions[action] {
				hasNonCompatible = true
			}
		}
		if hasResponse && hasNonCompatible {
			emitProgress("KP正在修正工具调用顺序")
			return "SYSTEM REJECT: your entire batch was rejected. response/end_game must not share a batch with check_rule/roll_dice/query_clues/query_character/query_npc_card/describe_characters/act_npc — their results have to be read in an earlier round first. Split into two rounds: call the result-producing tools now, then call response/end_game after reading the results. Write and state-update tools may stay in the same batch as response."
		}
		if hasResponse && respStr == "" {
			emitProgress("KP正在补全主流程回复")
			return "SYSTEM REJECT: empty response"
		}

		actNPCPresent := false
		var incompatible []string
		seenIncompatible := map[string]bool{}
		for _, call := range calls {
			action := ToolCallType(call.Name)
			if action == ToolActNPC {
				actNPCPresent = true
				continue
			}
			if !directorActNPCCompatible(action) && !seenIncompatible[call.Name] {
				seenIncompatible[call.Name] = true
				incompatible = append(incompatible, call.Name)
			}
		}
		if actNPCPresent && len(incompatible) > 0 {
			emitProgress("KP正在修正工具调用顺序")
			return fmt.Sprintf(
				"SYSTEM REJECT: act_npc 返回结果必须先读到才能执行状态更新或叙事，本轮不能把 act_npc 与 %s 混在一起。请先单独调用 act_npc，读取结果后再在下一轮调用这些工具。",
				strings.Join(incompatible, "、"))
		}

		// SKILL-ROLL SEQUENCING：查询结果要到下一轮才存在，同批的技能检定只能靠
		// 猜测填技能值。按角色名匹配，避免误伤"查A的卡 + 掷B的骰"这类合法组合；
		// what 为空的伤害/资源类纯数值骰不需要技能值，不参与判定。
		queriedAll := false
		queried := map[string]bool{}
		for _, call := range calls {
			tc, err := decodeDirectorToolCall(call)
			if err != nil {
				continue
			}
			var name string
			switch ToolCallType(call.Name) {
			case ToolQueryCharacter:
				name = strings.TrimSpace(tc.CharacterName)
			case ToolQueryNPCCard:
				name = strings.TrimSpace(tc.NPCName)
			default:
				continue
			}
			if name == "" {
				queriedAll = true
			} else {
				queried[name] = true
			}
		}
		if queriedAll || len(queried) > 0 {
			for _, call := range calls {
				if ToolCallType(call.Name) != ToolRollDice {
					continue
				}
				tc, err := decodeDirectorToolCall(call)
				if err != nil || tc.Dice == nil || strings.TrimSpace(tc.Dice.What) == "" {
					continue
				}
				if queriedAll || queried[strings.TrimSpace(tc.Dice.Character)] {
					emitProgress("KP正在修正工具调用顺序")
					return "SYSTEM REJECT: query_character/query_npc_card 与技能检定 roll_dice 不能同批——提交时查询结果还不存在，骰子里的技能值只能是猜测。请本轮只发查询，读到真实技能值后再在下一轮掷骰。"
				}
			}
		}

		// IMAGE-CHARACTER SEQUENCING：同理，外貌描写要先读到才能写进 image_prompt。
		hasDescribe := false
		hasImage := false
		for _, call := range calls {
			switch ToolCallType(call.Name) {
			case ToolDescribeCharacters:
				hasDescribe = true
			case ToolGenerateImage:
				hasImage = true
			}
		}
		if hasDescribe && hasImage {
			emitProgress("KP正在修正工具调用顺序")
			return "SYSTEM REJECT: describe_characters 与 generate_image 不能同批——提交时外貌描写还没返回，image_prompt 里的角色外貌只能是编造。请本轮只发 describe_characters，读到结果后再在下一轮画图。"
		}

		// EMBEDDED-SKILL-VALUE：roll_dice.what 只是纯文本标签，不能编码猜测的技能值。
		// 只检测数字——COC技能名本身常见圆括号(格斗(斗殴)/母语(英语)等)，不能用圆括号判定。
		for _, call := range calls {
			if ToolCallType(call.Name) != ToolRollDice {
				continue
			}
			tc, err := decodeDirectorToolCall(call)
			if err != nil || tc.Dice == nil {
				continue
			}
			if strings.ContainsAny(tc.Dice.What, "0123456789０１２３４５６７８９") {
				emitProgress("KP正在修正工具调用顺序")
				return fmt.Sprintf("SYSTEM REJECT: roll_dice.what=%q 疑似嵌入了猜测的技能值(例如\"投掷(50)\")。what只是纯文本技能名标签，技能值必须来自query_character/query_npc_card的真实结果，不得写进what。", tc.Dice.What)
			}
		}

		// ITEM-NAME-FORMAT：item_name 是物品基础名，状态/数量等附加信息应放入 item_desc/item_count。
		for _, call := range calls {
			if ToolCallType(call.Name) != ToolManageInventory {
				continue
			}
			tc, err := decodeDirectorToolCall(call)
			if err != nil {
				continue
			}
			if strings.ContainsAny(tc.ItemName, "()（）") {
				emitProgress("KP正在修正工具调用顺序")
				return fmt.Sprintf("SYSTEM REJECT: manage_inventory.item_name=%q 不能包含圆括号。item_name只写物品基础名，状态描述放item_desc，数量放item_count。", tc.ItemName)
			}
		}

		// OPTIONS-CAP：response.options 固定 0-2 条，宁可给0条也不要泄露。
		for _, call := range calls {
			if ToolCallType(call.Name) != ToolResponse {
				continue
			}
			tc, err := decodeDirectorToolCall(call)
			if err != nil {
				continue
			}
			if len(tc.Options) > 2 {
				emitProgress("KP正在修正工具调用顺序")
				return fmt.Sprintf("SYSTEM REJECT: response.options 有 %d 条，超过上限2条。宁可给0条也不要泄露，删减到最多2条最合适的行动入口。", len(tc.Options))
			}
		}

		// DUP-SETTLEMENT-IN-BATCH：同一批次内对同一角色的同一物品重复调用 manage_inventory，
		// 通常是同一个叙事事件被记了两次结算。跨轮的重复结算由提示词的DUP CHECK覆盖，这里只管同批。
		type invSettlement struct{ character, operate, item string }
		seenInventoryOps := map[invSettlement]bool{}
		for _, call := range calls {
			if ToolCallType(call.Name) != ToolManageInventory {
				continue
			}
			tc, err := decodeDirectorToolCall(call)
			if err != nil {
				continue
			}
			key := invSettlement{
				character: strings.TrimSpace(tc.CharacterName),
				operate:   strings.TrimSpace(tc.Operate),
				item:      strings.TrimSpace(tc.ItemName),
			}
			if seenInventoryOps[key] {
				emitProgress("KP正在修正工具调用顺序")
				return fmt.Sprintf("SYSTEM REJECT: 本批次对角色%q的物品%q重复调用了manage_inventory(重复结算)。确认这不是同一个事件被记了两次；如需一次性增减多个，合并成单次调用。", key.character, key.item)
			}
			seenInventoryOps[key] = true
		}

		generateImageCalls := 0
		for _, call := range calls {
			if call.Name == string(ToolGenerateImage) {
				generateImageCalls++
			}
		}
		if generateImageCalls > 1 || (generateImageCalls > 0 && *imageGeneratedThisTurn) {
			emitProgress("KP正在减少重复画图调用")
			return "SYSTEM REJECT: generate_image may be called at most once per turn. Keep only the single most necessary visual moment."
		}
		if generateImageCalls > 0 {
			*imageGeneratedThisTurn = true
		}

		return ""
	}
}

// directorDispatchState 汇总 run() 里跨轮次共享、由 Director 工具执行读写的可变
// 状态；每轮 ActionContext 都指向同一份状态(HasEnd/switchInThisBatch
// 除外，这两个是每轮独立重置的局部状态，见 directorBatchDispatch)。
type directorDispatchState struct {
	sid                 uint
	gctx                *GameContext
	handles             map[models.AgentRole]agentHandle
	tempNPCs            *[]models.SessionNPC
	timeAdvancedInTurn  *bool
	switchRole          *bool
	kpNarration         *string
	pendingWrite        *string
	pendingImages       *[]ImagePromptRequest
	wroteNarrative      *bool
	diceMsg             *string
	needsWriterFallback *bool
	emitProgress        func(string)
}

// joinToolResults 把一次 Action.Execute 的 []ToolResult 折成原生协议里一个 tool_call
// 唯一对应的 tool 消息内容。旧协议里多个 ToolResult 会被聚合进同一条组合消息按
// Action 类型分流(如 query_character/query_npc_card 单独放进 XML 块)；原生协议下
// 每个 tool_call 都有自己独立的 tool 消息，不再需要那层聚合/分流包装，直接把结果
// 文本原样返回即可。没有返回值的工具(write/response/hint/end_game等)必须给出非空
// 占位内容，避免产出空 tool 消息。
func joinToolResults(results []ToolResult) string {
	if len(results) == 0 {
		return "已执行。"
	}
	parts := make([]string, len(results))
	for i, r := range results {
		parts[i] = r.Result
	}
	return strings.Join(parts, "\n")
}

// directorBatchDispatch 构造 Director 原生工具循环的批量分发闭包：解码每个原生
// tool_call 到共享 ToolCall 结构体、执行 actionRegistry 对应的 Action.Execute，
// 并把结果写回 st 持有的可变状态。
//
// 并发策略与旧 executeParallelBatch 一致：check_rule 各自并发；act_npc 按 NPC 名
// 分组，不同 NPC 并发、同一 NPC 顺序执行以保留其记忆顺序；其余工具按原始顺序依次
// 执行。switchRole 语义照旧保留(目前唯一置位来源是战斗/追逐死代码，实际不会触发)。
// SSE 进度文案的粒度与旧代码一致：批内出现 >1 个 check_rule 或 >1 个不同 NPC 时，
// 发一条合并提示；否则按调用顺序逐条发。
func directorBatchDispatch(st directorDispatchState) toolBatchDispatch {
	return func(ctx context.Context, calls []llm.ToolCall) []toolOutcome {
		hasEnd := false
		switchInThisBatch := false

		actx := ActionContext{
			Ctx:                ctx,
			GCtx:               st.gctx,
			Sid:                st.sid,
			Handles:            st.handles,
			TempNPCs:           st.tempNPCs,
			HasEnd:             &hasEnd,
			TimeAdvancedInTurn: st.timeAdvancedInTurn,
			SwitchRole:         st.switchRole,
			KPNarration:        st.kpNarration,
			PendingWrite:       st.pendingWrite,
			PendingImages:      st.pendingImages,
			WroteNarrative:     st.wroteNarrative,
			DiceMsg:            st.diceMsg,
		}

		decoded := make([]ToolCall, len(calls))
		decodeErr := make([]error, len(calls))
		for i, call := range calls {
			tc, err := decodeDirectorToolCall(call)
			decoded[i] = tc
			decodeErr[i] = err
		}

		// 统计 check_rule 数量与不同 NPC 数量，只用于决定 SSE 进度文案的粒度
		// (合并一条 vs 逐条)，不影响下面实际的并发/顺序执行分组。
		nCheckRule := 0
		npcNames := map[string]bool{}
		for i, tc := range decoded {
			if decodeErr[i] != nil {
				continue
			}
			if tc.Action == ToolCheckRule {
				nCheckRule++
			}
			if tc.Action == ToolActNPC {
				npcNames[tc.NPCName] = true
			}
		}
		if st.emitProgress != nil {
			if nCheckRule > 1 || len(npcNames) > 1 {
				st.emitProgress(progressExecutingCalls(calls))
			} else {
				for i, call := range calls {
					if decodeErr[i] != nil {
						continue
					}
					st.emitProgress(progressExecutingCall(call))
				}
			}
		}

		// 按 NPC 名分组 act_npc 调用下标(排除解码失败的)。
		npcCallOrder := map[string][]int{}
		for i, tc := range decoded {
			if decodeErr[i] != nil {
				continue
			}
			if tc.Action == ToolActNPC {
				npcCallOrder[tc.NPCName] = append(npcCallOrder[tc.NPCName], i)
			}
		}

		type slotResult struct {
			idx     int
			results []ToolResult
		}
		resultSlots := make([][]ToolResult, len(calls))
		ch := make(chan slotResult, len(calls))
		var wg sync.WaitGroup
		asyncIdx := map[int]bool{}

		for _, indices := range npcCallOrder {
			wg.Add(1)
			go func(idxList []int) {
				defer wg.Done()
				for _, idx := range idxList {
					var results []ToolResult
					if handler, ok := actionRegistry[decoded[idx].Action]; ok {
						results = handler.Execute(decoded[idx], actx)
					}
					ch <- slotResult{idx: idx, results: results}
				}
			}(indices)
			for _, idx := range indices {
				asyncIdx[idx] = true
			}
		}

		for i, tc := range decoded {
			if decodeErr[i] != nil || tc.Action != ToolCheckRule {
				continue
			}
			asyncIdx[i] = true
			wg.Add(1)
			go func(idx int, c ToolCall) {
				defer wg.Done()
				if handler, ok := actionRegistry[c.Action]; ok {
					ch <- slotResult{idx: idx, results: handler.Execute(c, actx)}
				}
			}(i, tc)
		}

		// 顺序执行剩余调用；switchRole 语义与旧代码一致(目前恒为false，仅保留结构)。
		for i, tc := range decoded {
			if decodeErr[i] != nil || asyncIdx[i] {
				continue
			}
			if ctx.Err() != nil {
				break
			}
			if *st.switchRole {
				if switchInThisBatch || (tc.Action != ToolWrite && tc.Action != ToolResponse && tc.Action != ToolEndGame && tc.Action != ToolHint) {
					resultSlots[i] = []ToolResult{{
						Action: tc.Action,
						Result: "Interrupted: KP has switched control to Player, skipping this tool call. Please use write or response in next message to proceed.",
					}}
					continue
				}
			}
			prevSwitch := *st.switchRole
			if handler, ok := actionRegistry[tc.Action]; ok {
				resultSlots[i] = handler.Execute(tc, actx)
			}
			if !switchInThisBatch && *st.switchRole && !prevSwitch {
				switchInThisBatch = true
			}
		}

		wg.Wait()
		close(ch)
		for r := range ch {
			resultSlots[r.idx] = r.results
		}

		outcomes := make([]toolOutcome, len(calls))
		for i := range calls {
			if decodeErr[i] != nil {
				outcomes[i] = toolOutcome{reject: fmt.Sprintf("SYSTEM REJECT: 参数解析失败: %v", decodeErr[i])}
				continue
			}
			if visibleActionNeedsWriter(decoded[i].Action) {
				*st.needsWriterFallback = true
			}
			outcomes[i] = toolOutcome{result: joinToolResults(resultSlots[i]), done: hasEnd}
		}
		return outcomes
	}
}

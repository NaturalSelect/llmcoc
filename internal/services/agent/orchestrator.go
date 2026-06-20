// NOTE: Defines AI agent roles and their interactions.
package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/llmcoc/server/internal/models"
	"github.com/llmcoc/server/internal/services/game"
	"github.com/llmcoc/server/internal/services/llm"
	"github.com/llmcoc/server/internal/services/rulebook"
)

const MaxKpRound = 30

var internalTagPattern = regexp.MustCompile(`(?s)<(?:ack|direction|response_options)\b[^>]*>.*?</(?:ack|direction|response_options)>`)

// activeSessions prevents concurrent agent runs for the same game session.
var activeSessions sync.Map

// agentHandle pairs a Provider with its DB config and enable state.
type agentHandle struct {
	provider llm.Provider
	config   *models.AgentConfig
	enabled  bool
}

func (h agentHandle) isEnabled() bool {
	if !h.enabled || h.provider == nil {
		return false
	}
	if h.config == nil {
		return true
	}
	return h.config.IsActive
}

// systemPrompt always returns the built-in prompt. Runtime prompt overrides are disabled.
func (h agentHandle) systemPrompt(defaultPrompt string) string {
	return defaultPrompt
}

func newAgentHandleFromConfig(cfg *models.AgentConfig, temperatureOverride *float32) (agentHandle, error) {
	if cfg == nil {
		return agentHandle{}, fmt.Errorf("agent config is nil")
	}
	if !cfg.IsActive {
		return agentHandle{config: cfg, enabled: false}, nil
	}
	if cfg.ProviderConfigID == nil || cfg.ProviderConfig == nil || !cfg.ProviderConfig.IsActive {
		return agentHandle{}, fmt.Errorf("agent %q 未绑定可用的 LLM provider", cfg.Role)
	}
	maxTok := cfg.MaxTokens
	if maxTok == 0 {
		maxTok = 1024
	}
	temperature := cfg.Temperature
	if temperatureOverride != nil {
		temperature = *temperatureOverride
	}
	p := llm.NewProviderFromConfig(cfg.ProviderConfig, cfg.ModelName, maxTok, temperature, cfg.ThinkingLevel)
	return agentHandle{provider: p, config: cfg, enabled: true}, nil
}

// batchLoadAgents fetches all AgentConfigs in a single DB query.
// Disabled optional agents are returned as disabled handles; disabled required
// agents still fail fast so the core pipeline does not run with nil providers.
func batchLoadAgents() (map[models.AgentRole]agentHandle, error) {
	var configs []models.AgentConfig
	models.DB.Preload("ProviderConfig").Find(&configs)

	index := make(map[models.AgentRole]*models.AgentConfig, len(configs))
	for i := range configs {
		index[configs[i].Role] = &configs[i]
	}

	roles := []models.AgentRole{
		models.AgentRoleDirector,
		models.AgentRoleAntiCheat,
		models.AgentRoleWriter,
		models.AgentRoleLawyer,
		models.AgentRoleNPC,
	}
	requiredRoles := map[models.AgentRole]bool{
		models.AgentRoleDirector: true,
		models.AgentRoleLawyer:   true,
		models.AgentRoleNPC:      true,
	}
	result := make(map[models.AgentRole]agentHandle, len(roles))
	for _, role := range roles {
		cfg, ok := index[role]
		if !ok {
			if requiredRoles[role] {
				return nil, fmt.Errorf("agent %q 未配置,请在管理面板配置 LLM provider", role)
			}
			result[role] = agentHandle{enabled: false}
			continue
		}
		h, err := newAgentHandleFromConfig(cfg, nil)
		if err != nil {
			if requiredRoles[role] {
				return nil, err
			}
			result[role] = agentHandle{config: cfg, enabled: false}
			continue
		}
		if requiredRoles[role] && !h.isEnabled() {
			return nil, fmt.Errorf("agent %q 已禁用,请在管理面板启用", role)
		}
		result[role] = h
	}
	return result, nil
}

// Run executes the agent pipeline synchronously and returns the structured output.
// Only one pipeline may run per session at a time; concurrent calls receive an error.
func Run(ctx context.Context, gctx GameContext) (RunOutput, error) {
	if _, loaded := activeSessions.LoadOrStore(gctx.Session.ID, struct{}{}); loaded {
		return RunOutput{}, fmt.Errorf("当前房间正在处理上一条消息,请稍候")
	}
	defer activeSessions.Delete(gctx.Session.ID)

	ctx = context.WithValue(ctx, "session", fmt.Sprintf("%v", gctx.Session.ID))
	return run(ctx, gctx)
}

var sessionAgents = sync.Map{}

func getCachedAgents(sessionID uint) (agent map[models.AgentRole]agentHandle, err error) {
	tmp, ok := sessionAgents.Load(sessionID)
	if !ok {
		agent, err = batchLoadAgents()
		if err != nil {
			return nil, err
		}
		sessionAgents.Store(sessionID, agent)
	} else {
		agent = tmp.(map[models.AgentRole]agentHandle)
	}
	return agent, err
}

func deleteCachedAgents(sessionID uint) {
	sessionAgents.Delete(sessionID)
}

func ClearAllCachedAgents() {
	agents := make([]uint, 0)
	sessionAgents.Range(func(key, value any) bool {
		agents = append(agents, key.(uint))
		return true
	})
	for _, id := range agents {
		sessionAgents.Delete(id)
	}
}

// run 执行KP主流程工具循环。
// KP负责游戏状态推进和短回复; write动作只收集白字导演指令,不在主流程里调用Writer。
// 玩家行动记录由ChatStream在调用run前完成。
func run(ctx context.Context, gctx GameContext) (RunOutput, error) {
	handles, err := getCachedAgents(gctx.Session.ID)
	if err != nil {
		return RunOutput{}, err
	}
	emitProgress := func(text string) {
		if gctx.Progress != nil {
			gctx.Progress(text)
		}
	}

	sid := gctx.Session.ID
	debugf("run", "session=%d user=%q input=%s",
		sid, gctx.UserName, gctx.UserInput)
	emitProgress("KP正在整理本轮行动")
	if len(gctx.PendingActions) > 1 {
		debugf("run", "session=%d multi-player round: %d actions", sid, len(gctx.PendingActions))
	}
	turnPlayerIDs := activeTurnPlayerIDs(gctx.Session.Players)

	// Load temp NPCs for this session.
	var tempNPCs []models.SessionNPC
	models.DB.Where("session_id = ?", gctx.Session.ID).Find(&tempNPCs)

	rbIdx := rulebook.GlobalIndex

	// timeAdvancedInTurn tracks whether advance_time was called so we can skip the
	// normal per-turn +1 advancement at the end (the KP already pushed the clock).
	timeAdvancedInTurn := false
	wroteNarrative := false
	needsWriterFallback := false
	var kpNarration string

	// Seed KP with read-only transcript context. Do not pass previous user turns
	// as active LLM user messages, otherwise the KP may process old requests again.
	kpMsgs := []llm.ChatMessage{{Role: "user", Content: formatHistoryTranscript(gctx.History)}}

	kpMsgs = buildKPMessages(gctx, handles[models.AgentRoleDirector].systemPrompt(kpSystemPrompt), kpMsgs, tempNPCs)

	switchRole := false

	pendingWrite := ""

	diceMsg := ""

	// warnning := "YOU DONOT FOLLOW THE RULES, THIS ABUSE IS RECORDED BY MONITOR SYSTEM.\n"
	for iter := 0; iter < MaxKpRound; iter++ {
		if ctx.Err() != nil {
			return RunOutput{}, ctx.Err()
		}

		debugf("KP", "session=%d iter=%d/%d — calling LLM", sid, iter+1, MaxKpRound)
		emitProgress(progressKPIteration(iter, MaxKpRound))

		doneKP := timedDebug("KP", "session=%d iter=%d Chat", sid, iter+1)
		// 请求一次JSON
		calls, rawResp, _, err := runKP(ctx, handles[models.AgentRoleDirector], kpMsgs)
		doneKP()
		if err != nil {
			log.Printf("[agent] KP iter %d error: %v", iter+1, err)
			if iter == 0 {
				return RunOutput{}, fmt.Errorf("KP agent failed: %w", err)
			}
			break
		}

		debugf("KP", "session=%d iter=%d → %d tool call(s): %s",
			sid, iter+1, len(calls), formatCallNames(calls))
		debugf("KP", "session=%d iter=%d raw_resp=%s",
			sid, iter+1, rawResp)
		if len(calls) == 0 {
			debugf("KP", "session=%d iter=%d no calls, skipping rest of loop", sid, iter+1)
			continue
		}
		emitProgress(progressPlannedCalls(calls))

		// LLM 的结果加回去
		kpMsgs = append(kpMsgs, llm.ChatMessage{Role: "assistant", Content: rawResp})

		var toolResults []ToolResult
		hasEnd := false
		interrupt := false

		actx := ActionContext{
			Ctx:                ctx,
			GCtx:               &gctx,
			Sid:                sid,
			Handles:            handles,
			TempNPCs:           &tempNPCs,
			RbIdx:              rbIdx,
			HasEnd:             &hasEnd,
			TimeAdvancedInTurn: &timeAdvancedInTurn,
			SwitchRole:         &switchRole,
			KPNarration:        &kpNarration,
			Interrupt:          &interrupt,
			PendingWrite:       &pendingWrite,
			WroteNarrative:     &wroteNarrative,
			DiceMsg:            &diceMsg,
		}

		switchInThisBatch := false

		// Guard against the KP including a response/end_game in the same batch as
		// any tool that returns results the KP needs to see first.
		// Only responseCompatibleActions may share a batch with response.
		// If violated, reject the ENTIRE batch and force the KP to retry.
		hasResponse := false
		respStr := ""
		hasNonCompatible := false
		hasContract := false
		for _, call := range calls {
			if call.Action == ToolResponse || call.Action == ToolEndGame {
				hasResponse = true
				respStr = call.Reply
				if respStr == "" {
					respStr = call.EndSummary
				}
			}
			if call.Action == ToolContract {
				hasContract = true
			}
			if !responseCompatibleActions[call.Action] {
				hasNonCompatible = true
			}
		}
		if !hasContract {
			debugf("KP", "session=%d iter=%d rejecting entire batch: missing contract", sid, iter+1)
			emitProgress("KP正在补全裁定合约")
			kpMsgs = append(kpMsgs, llm.ChatMessage{
				Role: "user",
				Content: `
<error>
	1. your entire batch was rejected. 
	2. missing contract call. 
	3. every batch must include a contract call describing the ANTI_CHEAT_CONTRACT.
	4. **LOOK THIS ERROR MESSAGE CAREFULLY, FOLLOW THE INSTRUCTIONS TO FIX THE ISSUE.**
</error>`,
			})
			continue
		}
		if hasResponse && hasNonCompatible {
			debugf("KP", "session=%d iter=%d rejecting entire batch: response mixed with result-producing tools", sid, iter+1)
			emitProgress("KP正在修正工具调用顺序")
			kpMsgs = append(kpMsgs, llm.ChatMessage{
				Role:    "user",
				Content: "<error>SYSTEM REJECT: your entire batch was rejected. response/end_game must be the ONLY action in a batch (except write/hint/introspection/contract/update_llm_note). Split into two batches: first call the result-producing tools, then after reading the results call response separately.</error>",
			})
			continue
		}
		if hasResponse && respStr == "" {
			debugf("KP", "session=%d iter=%d rejecting entire batch: empty response", sid, iter+1)
			emitProgress("KP正在补全主流程回复")
			kpMsgs = append(kpMsgs, llm.ChatMessage{
				Role:    "user",
				Content: "<error>SYSTEM REJECT: empty response</error>",
			})
			continue
		}

		emitProgress("系统正在审查裁定一致性")
		verdict, allowed, rejectMsg := checkAntiCheat(ctx, handles[models.AgentRoleAntiCheat], gctx, calls, tempNPCs)
		if !allowed {
			debugf("anti_cheat", "session=%d iter=%d reject: %s, calls=%+v", sid, iter+1, rejectMsg, calls)
			emitProgress("KP正在修正不一致的裁定")
			kpMsgs = append(kpMsgs, llm.ChatMessage{Role: "user", Content: fmt.Sprintf("<error>%s</error>", rejectMsg)})
			continue
		}
		debugf("anti_cheat", "session=%d iter=%d allow: %s", sid, iter+1, verdict.Reason)

		// When a batch has multiple check_rule calls, run them concurrently since
		// each is an independent LLM query with no shared mutable state.
		// act_npc calls targeting different NPCs also run concurrently (each NPC
		// has its own independent memory; same-NPC calls remain sequential).
		// All other tools remain sequential.
		nCheckRule := 0
		npcNames := map[string]int{}
		for _, call := range calls {
			if call.Action == ToolCheckRule {
				nCheckRule++
			}
			if call.Action == ToolActNPC {
				npcNames[call.NPCName]++
			}
		}
		useParallel := nCheckRule > 1 || len(npcNames) > 1
		if useParallel {
			debugf("KP", "session=%d iter=%d parallel batch: %d check_rule, %d distinct npcs", sid, iter+1, nCheckRule, len(npcNames))
			emitProgress(progressExecutingCalls(calls))
			for _, call := range calls {
				if visibleActionNeedsWriter(call.Action) {
					needsWriterFallback = true
					break
				}
			}
			toolResults = executeParallelBatch(calls, actx)
		} else {
			for _, call := range calls {
				if ctx.Err() != nil {
					return RunOutput{}, ctx.Err()
				}
				if interrupt {
					continue
				}

				if switchRole {
					// 如果发生了切换跳过本批次其他调用,期望KP在下一轮使用 write/response 工具交出控制权。
					if switchInThisBatch || (call.Action != ToolWrite && call.Action != ToolResponse && call.Action != ToolEndGame && call.Action != ToolHint) {
						debugf("tool", "session=%d iter=%d switching KP role to Player for next calls", sid, iter+1)
						toolResults = append(toolResults, ToolResult{
							Action: call.Action,
							Result: "Interrupted: KP has switched control to Player, skipping this tool call. Please use write or response in next message to proceed.",
						})
						continue
					}
				}

				prevSwitch := switchRole
				if handler, ok := actionRegistry[call.Action]; ok {
					emitProgress(progressExecutingCall(call))
					results := handler.Execute(call, actx)
					if visibleActionNeedsWriter(call.Action) {
						needsWriterFallback = true
					}
					if len(results) > 0 {
						toolResults = append(toolResults, results...)
					}
				}
				if !switchInThisBatch && switchRole && !prevSwitch {
					switchInThisBatch = true
				}
			}
		}

		if hasEnd {
			if !timeAdvancedInTurn {
				for i := range gctx.Session.Players {
					card := &gctx.Session.Players[i].CharacterCard
					if card.MadnessState == "none" || card.MadnessState == "" {
						continue
					}
					card.MadnessDuration -= 1
					if card.MadnessDuration <= 0 {
						card.MadnessState = "none"
						card.MadnessSymptom = ""
						card.MadnessDuration = 0
						debugf("madness", "session=%d char=%s madness ended", sid, card.Name)
					}
					models.DB.Save(card)
					break
				}
				if checkTurnReadyForPlayers(gctx, turnPlayerIDs) {
					// if wroteNarrative {
					// 	// Real game turn: narrative was written, advance the clock.
					// 	advanceTurnRound(&gctx)
					// } else {
					// 	// Pure OOC consultation (KP-QUERY): no in-game action happened,
					// 	// so TurnRound stays the same. But we must still clear
					// 	// SessionTurnAction records so the next submission doesn't
					// 	// immediately look like "all players already submitted".
					// 	clearTurnActions(gctx)
					// }
					clearTurnActions(gctx)
				}
			}
			writerDirection := strings.TrimSpace(pendingWrite)
			if writerDirection == "" && needsWriterFallback {
				writerDirection = fallbackWriterDirection(kpNarration)
			}
			debugf("run", "session=%d completed iter=%d writer_direction_len=%d narration_len=%d",
				sid, iter+1, len([]rune(writerDirection)), len([]rune(kpNarration)))
			emitProgress("KP主流程裁定完成")
			// 将骰子结果和当前时间注入到玩家可见的回复中
			if diceMsg != "" {
				kpNarration += "\n<dice>" + strings.TrimSuffix(diceMsg, "; ") + "</dice>"
			}
			kpNarration += "\n<time_point>" + formatGameTime(gctx.Session.TurnRound, scenarioStartSlot(gctx.Session)) + "</time_point>"
			return RunOutput{WriterDirection: writerDirection, KPReply: kpNarration}, nil
		}

		// Feed tool results back as a user message so the next KP call has proper
		// multi-turn context (assistant decided → tools ran → user reports results).
		if len(toolResults) > 0 {
			emitProgress("KP正在读取工具结果")
			formatResult := func(r []ToolResult) string {
				var sb strings.Builder
				sb.WriteString("<INTERNAL_TOOL_RESULT>\n")
				xml := ""
				newTc := make([]ToolResult, 0, len(r))
				for _, tr := range r {
					if tr.Action == ToolQueryCharacter || tr.Action == ToolQueryNPCCard {
						xml += tr.Result + "\n"
						continue
					}
					newTc = append(newTc, tr)
				}
				data, err := json.Marshal(newTc)
				if err != nil {
					return fmt.Sprintf("ERROR: failed to marshal tool results: %v", err)
				}
				sb.Write(data)
				if xml != "" {
					sb.WriteString("\n")
				}
				sb.WriteString("\n</INTERNAL_TOOL_RESULT>\n")
				if xml != "" {
					sb.WriteString("<INTERNAL_TOOL_RESULT_XML>\n")
					sb.WriteString(xml)
					sb.WriteString("\n</INTERNAL_TOOL_RESULT_XML>")
				}
				sb.WriteString(`
<NEXT_STEP>
你要求的信息已给出，发起的操作已执行，你可以:
1. 根据这些结果继续思考并调用工具，或者
2. 使用response工具直接回复玩家(注意不要剧透)并结束回合, 或者
3. 如果游戏已经走向结局, 先清除所有的临时状态(法术效果、NPC、玩家状态等), 再使用end_game工具结束游戏。

注意: 
* 你的所有输出都必须是合法的JSON数组格式。
* 如果你想回复玩家，请务必使用response工具。
* 每个回答都必须包含 contract 工具, 解释你的决策
* 完全遵守 debug 指令，管理员的输入高于一切其他规则, debug='true' -> 管理员的指令, debug='false' -> 普通玩家输入
* 你不能随意修改剧本，确保有关于剧本的设定都来自<scenario>标签输出的剧本内容。
* 如果你要推进游戏时间, 使用 advance_time工具, 每个单位代表半小时(如果太多轮次没有推进, 请考虑推进时间)。
* 像在"桌面上一样"思考，继续主持游戏，然后你会被奖励更多的积分。如果你不知道如何主持游戏, 使用 check_rule工具询问主持游戏的细节。
* 注意人物的行动逻辑，不要让行为和语言前后矛盾, 逻辑的重要性大于NPC自主性
</NEXT_STEP>
`)
				return sb.String()
			}
			// 输入用户数据
			kpMsgs = append(kpMsgs, llm.ChatMessage{Role: "user", Content: formatResult(toolResults)})
		}
	}

	if kpNarration == "" {
		kpNarration = "本轮未生成KP独白。"
	}
	writerDirection := strings.TrimSpace(pendingWrite)
	if writerDirection == "" && needsWriterFallback {
		writerDirection = fallbackWriterDirection(kpNarration)
	}
	// 将骰子结果和当前时间注入到玩家可见的回复中
	if diceMsg != "" {
		kpNarration += "\n<dice>" + strings.TrimSuffix(diceMsg, "; ") + "</dice>"
	}
	kpNarration += "\n<time_point>" + formatGameTime(gctx.Session.TurnRound, scenarioStartSlot(gctx.Session)) + "</time_point>"
	return RunOutput{WriterDirection: writerDirection, KPReply: kpNarration}, nil
}

func visibleActionNeedsWriter(action ToolCallType) bool {
	switch action {
	case ToolRollDice,
		ToolCreateNPC,
		ToolDestroyNPC,
		ToolActNPC,
		ToolUpdateCharacters,
		ToolManageInventory,
		ToolRecordMonster,
		ToolManageSpell,
		ToolManageRelation,
		ToolManageAsset,
		ToolManageMadness,
		ToolAdvanceTime,
		ToolUpdateNPCCard,
		ToolUpdateLocation,
		ToolUpdateNPCLocation,
		ToolUpdateArmor,
		ToolFoundClue:
		return true
	default:
		return false
	}
}

func fallbackWriterDirection(kpReply string) string {
	reply := cleanPlayerVisibleText(kpReply)
	if reply == "" {
		return ""
	}
	return "根据以下KP主流程回复生成白字描述,只展开已经发生的可见事实和环境/NPC反应,停在玩家选择点,不要替玩家决定后续行动:\n" + reply
}

func cleanPlayerVisibleText(text string) string {
	return strings.TrimSpace(internalTagPattern.ReplaceAllString(text, ""))
}

func progressKPIteration(iter, _ int) string {
	if iter == 0 {
		return "KP正在判断本轮需要哪些裁定"
	}
	return "KP正在继续处理工具结果"
}

func progressPlannedCalls(calls []ToolCall) string {
	labels := compactProgressLabels(calls)
	if len(labels) == 0 {
		return "KP正在整理下一步"
	}
	return "KP计划：" + strings.Join(labels, "、")
}

func progressExecutingCalls(calls []ToolCall) string {
	labels := compactProgressLabels(calls)
	if len(labels) == 0 {
		return "系统正在执行工具"
	}
	return "系统正在并行处理：" + strings.Join(labels, "、")
}

func progressExecutingCall(call ToolCall) string {
	return "系统正在" + progressToolLabel(call.Action)
}

func compactProgressLabels(calls []ToolCall) []string {
	seen := map[string]bool{}
	labels := make([]string, 0, len(calls))
	for _, call := range calls {
		label := progressToolLabel(call.Action)
		if label == "" || seen[label] {
			continue
		}
		seen[label] = true
		labels = append(labels, label)
		if len(labels) >= 4 {
			break
		}
	}
	return labels
}

func progressToolLabel(action ToolCallType) string {
	switch action {
	case ToolContract:
		return "规划回合步骤"
	case ToolCheckRule, ToolReadRulebookConst:
		return "查询规则"
	case ToolRollDice:
		return "掷骰检定"
	case ToolCreateNPC, ToolDestroyNPC:
		return "处理NPC登场"
	case ToolActNPC:
		return "处理NPC行动"
	case ToolQueryCharacter, ToolQueryNPCCard:
		return "读取角色状态"
	case ToolUpdateCharacters, ToolUpdateNPCCard, ToolUpdateLocation, ToolUpdateNPCLocation, ToolUpdateArmor:
		return "更新角色和场景状态"
	case ToolManageInventory, ToolManageSpell, ToolManageRelation, ToolManageAsset, ToolManageMadness:
		return "更新角色记录"
	case ToolRecordMonster, ToolFoundClue, ToolQueryClues:
		return "处理线索"
	case ToolAdvanceTime:
		return "推进时间"
	case ToolWrite:
		return "整理白字素材"
	case ToolResponse:
		return "生成KP主回复"
	case ToolEndGame:
		return "结算结局"
	case ToolUpdateLLMNote, ToolUpdateNPCLLMNote, ToolHint:
		return "记录局势备注"
	case ToolYield:
		return "等待下一步输入"
	case ToolReport:
		return "记录异常报告"
	default:
		return "处理工具调用"
	}
}

// executeParallelBatch runs check_rule calls and act_npc calls targeting distinct
// NPCs concurrently; act_npc calls for the same NPC run sequentially to preserve
// that NPC's memory order. All other tools run sequentially in original order.
// Result order matches the original call order.
func executeParallelBatch(calls []ToolCall, actx ActionContext) []ToolResult {
	type slotResult struct {
		idx     int
		results []ToolResult
	}
	resultSlots := make([][]ToolResult, len(calls))
	ch := make(chan slotResult, len(calls))
	var wg sync.WaitGroup

	// Group act_npc indices by NPC name to detect which names have >1 call.
	npcCallOrder := map[string][]int{} // npc_name → ordered list of call indices
	for i, call := range calls {
		if call.Action == ToolActNPC {
			npcCallOrder[call.NPCName] = append(npcCallOrder[call.NPCName], i)
		}
	}

	// Track which indices will be handled asynchronously.
	asyncIdx := map[int]bool{}

	// Launch one goroutine per distinct NPC name; calls for the same NPC run
	// sequentially inside that goroutine to preserve memory order.
	for npcName, indices := range npcCallOrder {
		_ = npcName
		wg.Add(1)
		go func(idxList []int) {
			defer wg.Done()
			for _, idx := range idxList {
				var results []ToolResult
				if handler, ok := actionRegistry[calls[idx].Action]; ok {
					results = handler.Execute(calls[idx], actx)
				}
				ch <- slotResult{idx: idx, results: results}
			}
		}(indices)
		for _, idx := range indices {
			asyncIdx[idx] = true
		}
	}

	// check_rule calls run concurrently (independent reads).
	for i, call := range calls {
		if call.Action == ToolCheckRule {
			asyncIdx[i] = true
			wg.Add(1)
			go func(idx int, c ToolCall) {
				defer wg.Done()
				if handler, ok := actionRegistry[c.Action]; ok {
					ch <- slotResult{idx: idx, results: handler.Execute(c, actx)}
				}
			}(i, call)
		}
	}

	// Sequential pass for everything else.
	for i, call := range calls {
		if asyncIdx[i] {
			continue
		}
		if handler, ok := actionRegistry[call.Action]; ok {
			resultSlots[i] = handler.Execute(call, actx)
		}
	}

	wg.Wait()
	close(ch)
	for r := range ch {
		resultSlots[r.idx] = r.results
	}

	var out []ToolResult
	for _, r := range resultSlots {
		out = append(out, r...)
	}
	return out
}

// executeParallelCheckRule runs all check_rule calls in the batch concurrently
// while executing every other call sequentially in original order.
// Result order matches the original call order.
func executeParallelCheckRule(calls []ToolCall, actx ActionContext) []ToolResult {
	type slotResult struct {
		idx     int
		results []ToolResult
	}
	resultSlots := make([][]ToolResult, len(calls))
	ch := make(chan slotResult, len(calls))
	var wg sync.WaitGroup

	for i, call := range calls {
		if call.Action == ToolCheckRule {
			wg.Add(1)
			go func(idx int, c ToolCall) {
				defer wg.Done()
				if handler, ok := actionRegistry[c.Action]; ok {
					ch <- slotResult{idx: idx, results: handler.Execute(c, actx)}
				}
			}(i, call)
		} else {
			// Non-check_rule tools run sequentially.
			if handler, ok := actionRegistry[call.Action]; ok {
				resultSlots[i] = handler.Execute(call, actx)
			}
		}
	}

	wg.Wait()
	close(ch)
	for r := range ch {
		resultSlots[r.idx] = r.results
	}

	var out []ToolResult
	for _, r := range resultSlots {
		out = append(out, r...)
	}
	return out
}

// saveWriterHistory persists the Writer's conversation history to the session
// so it carries over across rounds for narrative continuity.
func saveWriterHistory(sessionID uint, state *WriterState) {
	models.DB.Model(&models.GameSession{}).
		Where("id = ?", sessionID).
		Update("writer_history", models.JSONField[[]models.ChatMsg]{
			Data: llmToChatMsgs(state.History),
		})
}

// ── Combat state helpers ──────────────────────────────────────────────────────

// saveCombatState persists the CombatState JSON column on GameSession.
// Pass nil to clear an ended combat.
func saveCombatState(sessionID uint, cs *models.CombatState) {
	models.DB.Model(&models.GameSession{}).
		Where("id = ?", sessionID).
		Update("combat_state", models.JSONField[*models.CombatState]{Data: cs})
}

// buildCombatState initialises a new CombatState from KP-provided participant inputs.
// Participants are sorted by DEX descending (ties keep input order).
func buildCombatState(inputs []CombatParticipantInput) models.CombatState {
	parts := make([]models.CombatParticipant, len(inputs))
	for i, inp := range inputs {
		parts[i] = models.CombatParticipant{
			Name:       inp.Name,
			DEX:        inp.DEX,
			HP:         inp.HP,
			IsNPC:      inp.IsNPC,
			WoundState: "none",
		}
	}
	// Stable sort: higher DEX acts first.
	sort.SliceStable(parts, func(i, j int) bool {
		return parts[i].DEX > parts[j].DEX
	})
	return models.CombatState{
		Active:       true,
		Round:        1,
		Participants: parts,
		ActorIndex:   0,
	}
}

// applyCombatAct applies one combatant's action to the CombatState and advances
// the actor pointer. Returns a human-readable result string for the KP.
func applyCombatAct(cs *models.CombatState, call ToolCall) (result string, switchRole bool) {
	actorName := call.CombatActorName
	act := call.CombatAction

	// Find the actor in the participant list.
	actorIdx := -1
	for i, p := range cs.Participants {
		if p.Name == actorName {
			actorIdx = i
			break
		}
	}
	if actorIdx < 0 {
		return fmt.Sprintf("错误:找不到战斗参与者 %q", actorName), false
	}

	actor := &cs.Participants[actorIdx]
	actor.HasActed = true

	switchRole = false
	if next := (actorIdx + 1) % len(cs.Participants); next != actorIdx {
		// NOTE: 如果下一个行动者是NPC,则保持KP角色不变让它继续决策；如果是玩家,则切换到玩家角色让KP决策玩家行动。
		nextActor := cs.Participants[next]
		switchRole = !nextActor.IsNPC && actor.IsNPC
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("【%s 行动】", actorName))

	if act != nil {
		switch act.Type {
		case "aim":
			actor.IsAiming = true
			sb.WriteString("正在瞄准,下轮攻击获得奖励骰。")
		case "take_cover":
			debt := act.APDebtNext
			if debt <= 0 {
				debt = 1
			}
			actor.APDebt += debt
			sb.WriteString(fmt.Sprintf("寻找掩体,下轮行动点-1。"))
		case "dodge":
			actor.HasDodgedOrFB = true
			sb.WriteString(fmt.Sprintf("闪避 %s。", act.TargetName))
		case "fight_back":
			actor.HasDodgedOrFB = true
			sb.WriteString(fmt.Sprintf("反击 %s。", act.TargetName))
		case "attack":
			// Clear aiming bonus after use.
			if actor.IsAiming {
				actor.IsAiming = false
				sb.WriteString("(使用瞄准奖励骰)")
			}
			sb.WriteString(fmt.Sprintf("攻击 %s(武器:%s)。", act.TargetName, act.WeaponName))
		default:
			sb.WriteString(fmt.Sprintf("执行动作:%s。", act.Type))
		}
	}

	// Advance actor index; if all have acted, start a new round.
	allActed := true
	for _, p := range cs.Participants {
		if !p.HasActed && p.WoundState != "dead" {
			allActed = false
			break
		}
	}
	if allActed {
		cs.Round++
		// Reset per-round flags and apply AP debts.
		for i := range cs.Participants {
			cs.Participants[i].HasActed = false
			cs.Participants[i].HasDodgedOrFB = false
			if cs.Participants[i].APDebt > 0 {
				cs.Participants[i].APDebt-- // consume one point of debt
			}
		}
		cs.ActorIndex = 0
		sb.WriteString(fmt.Sprintf(" 本轮结束,进入第%d轮。", cs.Round))
	} else {
		// Advance to next living actor.
		next := (actorIdx + 1) % len(cs.Participants)
		for cs.Participants[next].HasActed || cs.Participants[next].WoundState == "dead" {
			next = (next + 1) % len(cs.Participants)
		}
		cs.ActorIndex = next
		sb.WriteString(fmt.Sprintf(" 下一行动者:%s(DEX %d)。", cs.Participants[next].Name, cs.Participants[next].DEX))
	}

	if switchRole {
		sb.WriteString(fmt.Sprintf(" 控制权从KP,移交到玩家,请使用 write/response 移交控制权"))
	}

	return sb.String(), switchRole
}

// combatOrderSummary returns a compact DEX-order string for the KP result message.
func combatOrderSummary(parts []models.CombatParticipant) string {
	names := make([]string, len(parts))
	for i, p := range parts {
		names[i] = fmt.Sprintf("%s(DEX%d)", p.Name, p.DEX)
	}
	return strings.Join(names, " → ")
}

// ── Chase state helpers ───────────────────────────────────────────────────────

// saveChaseState persists the ChaseState JSON column on GameSession.
// Pass nil to clear an ended chase.
func saveChaseState(sessionID uint, chs *models.ChaseState) {
	models.DB.Model(&models.GameSession{}).
		Where("id = ?", sessionID).
		Update("chase_state", models.JSONField[*models.ChaseState]{Data: chs})
}

// buildChaseState initialises a new ChaseState from KP-provided participant inputs.
// MinMOV is computed from the participant list.
func buildChaseState(inputs []ChaseParticipantInput) models.ChaseState {
	parts := make([]models.ChaseParticipant, len(inputs))
	minMOV := -1
	for i, inp := range inputs {
		parts[i] = models.ChaseParticipant{
			Name:      inp.Name,
			IsNPC:     inp.IsNPC,
			MOV:       inp.MOV,
			Location:  inp.Location,
			IsPursuer: inp.IsPursuer,
		}
		if minMOV < 0 || inp.MOV < minMOV {
			minMOV = inp.MOV
		}
	}
	if minMOV < 0 {
		minMOV = 0
	}
	return models.ChaseState{
		Active:       true,
		Round:        1,
		MinMOV:       minMOV,
		Participants: parts,
		Obstacles:    nil,
	}
}

// applyChaseAct applies one participant's chase action and returns a result string.
func applyChaseAct(chs *models.ChaseState, call ToolCall) (result string, switchRole bool) {
	actorName := call.ChaseActorName
	act := call.ChaseAction

	actorIdx := -1
	for i, p := range chs.Participants {
		if p.Name == actorName {
			actorIdx = i
			break
		}
	}
	if actorIdx < 0 {
		return fmt.Sprintf("错误:找不到追逐参与者 %q", actorName), false
	}

	actor := &chs.Participants[actorIdx]
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("【%s 追逐行动】", actorName))

	if act == nil {
		return sb.String() + "(无行动详情)", false
	}

	if next := (actorIdx + 1) % len(chs.Participants); next != actorIdx {
		switchRole = !chs.Participants[next].IsNPC && actor.IsNPC
	}

	switch act.Type {
	case "move":
		actor.Location += act.MoveDelta
		sb.WriteString(fmt.Sprintf("移动%+d格,当前位置:%d。", act.MoveDelta, actor.Location))
	case "hazard":
		if act.APDebtNext > 0 {
			actor.APDebt += act.APDebtNext
			sb.WriteString(fmt.Sprintf("险境检定失败,下轮行动点-%d。", act.APDebtNext))
		} else {
			sb.WriteString("险境检定成功,正常通过。")
		}
	case "obstacle":
		// Create or update an obstacle.
		if act.ObstacleName != "" && act.ObstacleMaxHP > 0 {
			found := false
			for i, ob := range chs.Obstacles {
				if ob.Name == act.ObstacleName {
					chs.Obstacles[i].HP = act.ObstacleHP
					found = true
					sb.WriteString(fmt.Sprintf("障碍【%s】HP更新为%d/%d。", act.ObstacleName, act.ObstacleHP, ob.MaxHP))
					break
				}
			}
			if !found {
				chs.Obstacles = append(chs.Obstacles, models.ChaseObstacle{
					Name:  act.ObstacleName,
					HP:    act.ObstacleHP,
					MaxHP: act.ObstacleMaxHP,
				})
				sb.WriteString(fmt.Sprintf("新增障碍【%s】HP=%d/%d。", act.ObstacleName, act.ObstacleHP, act.ObstacleMaxHP))
			}
		}
	case "conflict":
		sb.WriteString(fmt.Sprintf("与%s发生冲突。", act.TargetName))
	default:
		sb.WriteString(fmt.Sprintf("执行追逐动作:%s。", act.Type))
	}

	// Check for chase-end conditions: pursuer reaches same location as prey.
	for _, p := range chs.Participants {
		if p.IsPursuer {
			for _, q := range chs.Participants {
				if !q.IsPursuer && p.Location >= q.Location {
					sb.WriteString(fmt.Sprintf(" ⚠ 追逐者%s已追上%s(位置%d≥%d),KP可宣告追逐结束。",
						p.Name, q.Name, p.Location, q.Location))
				}
			}
		}
	}

	if switchRole {
		sb.WriteString(fmt.Sprintf(" 控制权从KP,移交到玩家,请使用 write/response 移交控制权"))
	}

	return sb.String(), switchRole
}

// chaseParticipantSummary returns a compact participant list string.
func chaseParticipantSummary(parts []models.ChaseParticipant) string {
	names := make([]string, len(parts))
	for i, p := range parts {
		role := "猎物"
		if p.IsPursuer {
			role = "追逐者"
		}
		names[i] = fmt.Sprintf("%s(%s,MOV%d,位置%d)", p.Name, role, p.MOV, p.Location)
	}
	return strings.Join(names, "、")
}

// chaseAPSummary returns each participant's action points for the round.
func chaseAPSummary(parts []models.ChaseParticipant, minMOV int) string {
	items := make([]string, len(parts))
	for i, p := range parts {
		ap := 1 + (p.MOV - minMOV)
		if ap < 1 {
			ap = 1
		}
		items[i] = fmt.Sprintf("%s=%d", p.Name, ap)
	}
	return strings.Join(items, "、")
}

func formatSingleDiceResult(r DiceCheckResult) string {
	hidden := ""
	if r.Hidden {
		hidden = " (暗骰)"
	}
	level := ""
	if r.Level != "" {
		level = fmt.Sprintf("难度:%s ", r.Level)
	}
	return fmt.Sprintf("%v鉴定: %v %v%v", r.What, r.Roll, level, hidden)
}

// formatNPCAction formats an NPCAction as an unverified roleplay result for the KP.
func formatNPCAction(a NPCAction) string {
	result := "【未验证NPC反应】" + a.NPCName + ":" + a.Action
	if a.Dialogue != "" {
		result += fmt.Sprintf("(对话:\"%s\")", a.Dialogue)
	}
	return result
}

// executeSingleDiceCheck wraps executeDiceChecks for a single check.
func executeSingleDiceCheck(dc DiceCheck, players []models.SessionPlayer) DiceCheckResult {
	results := executeDiceChecks([]DiceCheck{dc}, players)
	if len(results) == 0 {
		return DiceCheckResult{DiceCheck: dc}
	}
	return results[0]
}

// ── Turn tracking ─────────────────────────────────────────────────────────────

// checkTurnReady returns true when every non-dead investigator in the session
// has a SessionTurnAction record for the current round. Dead investigators do
// not block multiplayer turns; if revived later, WoundState is cleared and they
// are counted again in subsequent rounds.
func checkTurnReady(gctx GameContext) bool {
	if len(gctx.Session.Players) == 0 {
		return false
	}
	return checkTurnReadyForPlayers(gctx, activeTurnPlayerIDs(gctx.Session.Players))
}

func checkTurnReadyForPlayers(gctx GameContext, playerIDs []uint) bool {
	if len(playerIDs) == 0 {
		return true
	}
	var count int64
	models.DB.Model(&models.SessionTurnAction{}).
		Where("session_id = ? AND round = ? AND user_id IN ?", gctx.Session.ID, gctx.Session.TurnRound, playerIDs).
		Count(&count)
	return count >= int64(len(playerIDs))
}

func activeTurnPlayerIDs(players []models.SessionPlayer) []uint {
	ids := make([]uint, 0, len(players))
	for _, p := range players {
		if p.CharacterCard.WoundState == "dead" {
			continue
		}
		ids = append(ids, p.UserID)
	}
	return ids
}

// advanceTurnRound increments TurnRound and deletes turn action records for the
// completed round.
func advanceTurnRound(gctx *GameContext) {
	nextRound := gctx.Session.TurnRound + 1
	models.DB.Model(&models.GameSession{}).Where("id = ?", gctx.Session.ID).Update("turn_round", nextRound)
	models.DB.Where("session_id = ? AND round <= ?", gctx.Session.ID, gctx.Session.TurnRound).Delete(&models.SessionTurnAction{})
	gctx.Session.TurnRound = nextRound
	log.Printf("[agent] session %d advanced to round %d", gctx.Session.ID, nextRound)
}

// clearTurnActions removes SessionTurnAction records for the current round
// without incrementing TurnRound. Used for out-of-character KP consultations
// so that players can resubmit in-game actions afterwards.
func clearTurnActions(gctx GameContext) {
	models.DB.Where("session_id = ? AND round = ?", gctx.Session.ID, gctx.Session.TurnRound).Delete(&models.SessionTurnAction{})
	log.Printf("[agent] session %d cleared turn actions (round %d, no time advance)", gctx.Session.ID, gctx.Session.TurnRound)
}

// formatCallNames returns a comma-joined list of action names for debug logging.
func formatCallNames(calls []ToolCall) string {
	names := make([]string, 0, len(calls))
	for _, c := range calls {
		names = append(names, string(c.Action))
	}
	return strings.Join(names, ", ")
}

// formatGameTime converts an absolute round number to a human-readable game time string.
// Each round = 30 minutes; 48 rounds = 1 day.
// startSlot is the scenario start slot in [0,47], where each slot is 30 minutes.
// Default start slot is 16 (08:00) when input is out of range.
// Also includes elapsed time since game start so the KP can track time-sensitive deadlines.
func formatGameTime(round int, startSlot int) string {
	if round <= 0 {
		round = 1
	}
	if startSlot < 0 || startSlot > 47 {
		startSlot = 16 // 08:00
	}
	zi := round - 1 // zero-indexed
	day := (zi+startSlot)/48 + 1
	slot := (zi + startSlot) % 48
	hour := slot / 2
	min := (slot % 2) * 30
	// Elapsed time since game start (round 1 = 0 elapsed).
	elapsedMins := zi * 30
	elapsedH := elapsedMins / 60
	elapsedM := elapsedMins % 60
	var elapsed string
	if elapsedH > 0 {
		elapsed = fmt.Sprintf("距开局已过%dh%02dm", elapsedH, elapsedM)
	} else {
		elapsed = fmt.Sprintf("距开局已过%dm", elapsedM)
	}
	return fmt.Sprintf("第%d天 %02d:%02d(%s)", day, hour, min, elapsed)
}

func scenarioStartSlot(session models.GameSession) int {
	return session.Scenario.Content.Data.GameStartSlot
}

// ResetMadnessAfterSession clears non-permanent madness state on session end.
// Returns true when any field was changed.
func ResetMadnessAfterSession(card *models.CharacterCard) bool {
	if card == nil {
		return false
	}
	if card.MadnessState == "permanent" {
		return false
	}
	changed := card.MadnessState != "none" || card.MadnessSymptom != "" || card.MadnessDuration != 0 || card.DailySanLoss != 0
	card.MadnessState = "none"
	card.MadnessSymptom = ""
	card.MadnessDuration = 0
	card.DailySanLoss = 0
	return changed
}

func mapSkillToStat(card *models.CharacterCard, skill string) (val int, ok bool) {
	if card == nil {
		return 0, false
	}
	switch skill {
	case "力量", "STR":
		return card.Stats.Data.STR, true
	case "体质", "CON":
		return card.Stats.Data.CON, true
	case "体型", "SIZ":
		return card.Stats.Data.SIZ, true
	case "敏捷", "DEX":
		return card.Stats.Data.DEX, true
	case "外貌", "APP":
		return card.Stats.Data.APP, true
	case "灵感", "智力", "INT":
		return card.Stats.Data.INT, true
	case "意志", "POW":
		return card.Stats.Data.POW, true
	case "教育", "EDU":
		return card.Stats.Data.EDU, true
	case "幸运", "LUCK":
		return card.Stats.Data.Luck, true
	default:
		return 0, false
	}
}

// executeDiceChecks auto-rolls all checks the Director requested.
// Actual skill values are looked up from character cards when available.
// For check_type="sanity", the current SAN value is used and SAN loss is
// calculated automatically from the san_success_loss / san_fail_loss fields.
func executeDiceChecks(checks []DiceCheck, players []models.SessionPlayer) []DiceCheckResult {
	if len(checks) == 0 {
		return nil
	}
	results := make([]DiceCheckResult, 0, len(checks))
	for _, dc := range checks {
		diceVal := game.RollDiceExpr(dc.DiceExpr)
		results = append(results, DiceCheckResult{
			DiceCheck: dc,
			Roll:      diceVal,
		})
	}
	return results
}

// ── Helpers ──────────────────────────────────────────────────────────────────

func buildPlayerStatus(players []models.SessionPlayer) string {
	if len(players) == 0 {
		return ""
	}
	s := "调查员状态:"
	for _, p := range players {
		card := p.CharacterCard
		line := fmt.Sprintf("\n• %s(%s)HP:%d/%d SAN:%d/%d",
			card.Name, card.Occupation,
			card.Stats.Data.HP, card.Stats.Data.MaxHP,
			card.Stats.Data.SAN, card.Stats.Data.MaxSAN)

		// 疯狂状态标注
		switch card.MadnessState {
		case "temporary":
			line += "【临时性疯狂:" + card.MadnessSymptom + "】"
		case "indefinite":
			line += "【不定性疯狂(任何SAN损失将再次触发发作):" + card.MadnessSymptom + "】"
		case "permanent":
			line += "【永久性疯狂:" + card.MadnessSymptom + "】"
		}

		// 伤亡状态标注
		switch card.WoundState {
		case "major":
			line += "【重伤】"
		case "dying":
			line += "【濒死(需急救)】"
		case "dead":
			line += "【已死亡】"
		}
		if card.IsUnconscious && card.WoundState != "dead" {
			line += "【昏迷】"
		}

		// 克苏鲁神话技能
		if card.CthulhuMythosSkill > 0 {
			line += fmt.Sprintf("(克苏鲁神话技能:%d,最大SAN上限:%d)", card.CthulhuMythosSkill, 99-card.CthulhuMythosSkill)
		}
		if len(card.SeenMonsters.Data) > 0 {
			line += fmt.Sprintf("(已见神话存在:%s)", strings.Join(card.SeenMonsters.Data, "、"))
		}
		s += line
	}
	return s
}

// buildCharacterDetail returns a compact XML character card dump for the given character name.
// If characterName is empty, all players' cards are returned.
func buildCharacterDetail(characterName string, players []models.SessionPlayer) string {
	if len(players) == 0 {
		return `<err>当前无调查员</err>`
	}

	var sb strings.Builder
	sb.WriteString("<chars>")
	matched := 0
	for _, p := range players {
		card := p.CharacterCard
		if characterName != "" && card.Name != characterName {
			continue
		}
		matched++
		st := card.Stats.Data
		sb.WriteString(fmt.Sprintf(`<c n=%q race=%q job=%q age=%q gender=%q loc=%q armor=%q>`, card.Name, card.Race, card.Occupation, compactInt(card.Age), card.Gender, p.Location, compactInt(p.Armor)))
		sb.WriteString(fmt.Sprintf(`<stat base="STR=%d CON=%d SIZ=%d DEX=%d APP=%d INT=%d POW=%d EDU=%d" hp="%d/%d" mp="%d/%d" san="%d/%d" luck="%d" mov="%d" build="%d" db=%q/>`,
			st.STR, st.CON, st.SIZ, st.DEX, st.APP, st.INT, st.POW, st.EDU,
			st.HP, st.MaxHP, st.MP, st.MaxMP, st.SAN, st.MaxSAN, st.Luck, st.MOV, st.Build, st.DB))
		if card.CthulhuMythosSkill > 0 {
			sb.WriteString(fmt.Sprintf(`<mythos skill="%d" max_san="%d"/>`, card.CthulhuMythosSkill, 99-card.CthulhuMythosSkill))
		}
		writeCompactMap(&sb, "skills", card.Skills.Data)
		writeCompactList(&sb, "inv", card.Inventory.Data)
		writeCompactList(&sb, "spells", card.Spells.Data)
		writeCompactList(&sb, "seen", card.SeenMonsters.Data)
		if len(card.SocialRelations.Data) > 0 {
			sb.WriteString("<rels>")
			for _, r := range card.SocialRelations.Data {
				sb.WriteString(fmt.Sprintf(`<rel n=%q type=%q note=%q/>`, r.Name, r.Relationship, r.Note))
			}
			sb.WriteString("</rels>")
		}
		if len(card.Assets.Data) > 0 {
			sb.WriteString("<assets>")
			for _, a := range card.Assets.Data {
				sb.WriteString(fmt.Sprintf(`<asset n=%q cat=%q note=%q/>`, a.Name, a.Category, a.Note))
			}
			sb.WriteString("</assets>")
		}
		if p.LLMNote != "" {
			sb.WriteString(fmt.Sprintf("<note>%s</note>", xmlEscape(p.LLMNote)))
		}
		if card.MadnessState != "" && card.MadnessState != "none" {
			sb.WriteString(fmt.Sprintf(`<mad state=%q symptom=%q/>`, card.MadnessState, card.MadnessSymptom))
		}
		sb.WriteString("</c>")
	}
	if matched == 0 {
		return `<err>未找到角色：` + xmlEscape(characterName) + `</err>`
	}
	sb.WriteString("</chars>")
	return sb.String()
}

// buildNPCDetail returns a detailed NPC card dump for temporary/session NPCs.
// If npcName is empty, returns all known NPC details in this session context.
// npcNameMatch returns true when query is empty, is an exact match, or is a
// substring of name. For ASCII names, comparison is case-insensitive;
// for Chinese (and other non-ASCII) names, direct substring matching is used
// since they have no concept of case.
func npcNameMatch(name, query string) bool {
	if query == "" {
		return true
	}
	if name == query {
		return true
	}
	// Case-insensitive ASCII fallback.
	if strings.Contains(strings.ToLower(name), strings.ToLower(query)) {
		return true
	}
	// Direct substring match for Chinese / non-ASCII names.
	return strings.Contains(name, query)
}

func xmlEscape(s string) string {
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	s = strings.ReplaceAll(s, ">", "&gt;")
	return s
}

func compactInt(v int) string {
	if v == 0 {
		return ""
	}
	return strconv.Itoa(v)
}

func writeCompactText(sb *strings.Builder, tag, text string) {
	text = strings.TrimSpace(text)
	if text == "" {
		return
	}
	sb.WriteString("<")
	sb.WriteString(tag)
	sb.WriteString(">")
	sb.WriteString(xmlEscape(text))
	sb.WriteString("</")
	sb.WriteString(tag)
	sb.WriteString(">")
}

func writeCompactList(sb *strings.Builder, tag string, items []string) {
	clean := make([]string, 0, len(items))
	for _, item := range items {
		item = strings.TrimSpace(item)
		if item != "" {
			clean = append(clean, item)
		}
	}
	if len(clean) == 0 {
		return
	}
	writeCompactText(sb, tag, strings.Join(clean, "; "))
}

func writeCompactMap(sb *strings.Builder, tag string, values map[string]int) {
	if len(values) == 0 {
		return
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		parts = append(parts, fmt.Sprintf("%s=%d", key, values[key]))
	}
	writeCompactText(sb, tag, strings.Join(parts, " "))
}

func buildNPCDetail(npcName string, tempNPCs []models.SessionNPC, scenarioNPCs []models.NPCData) string {
	var sb strings.Builder
	sb.WriteString("<npcs>")
	matched := 0

	// 1. 优先搜索临时 NPC（动态状态）
	for _, npc := range tempNPCs {
		if !npcNameMatch(npc.Name, npcName) {
			continue
		}
		matched++
		status := npcCompactState(npc)
		sb.WriteString(fmt.Sprintf(`<npc n=%q race=%q job=%q mythos=%q src="tmp" st=%q wound_state=%q loc=%q>`, npc.Name, npc.Race, npc.Occupation, compactInt(npc.CthulhuMythosSkill), status, npc.WoundState, npc.Location))
		writeCompactText(&sb, "desc", npc.Description)
		writeCompactText(&sb, "att", strings.TrimSpace(npc.Attitude))
		writeCompactText(&sb, "goal", strings.TrimSpace(npc.Goal))
		writeCompactText(&sb, "secret", strings.TrimSpace(npc.Secret))
		writeCompactText(&sb, "risk", strings.TrimSpace(npc.RiskPref))
		writeCompactText(&sb, "note", npc.LLMNote)
		writeCompactMap(&sb, "stats", npc.Stats.Data)
		writeCompactMap(&sb, "skills", npc.Skills.Data)
		writeCompactList(&sb, "spells", npc.Spells.Data)
		sb.WriteString("</npc>")
	}

	// 如果临时 NPC 中没有匹配，则回退到剧本静态 NPC（只读基准信息）
	if matched == 0 {
		for _, npc := range scenarioNPCs {
			if !npcNameMatch(npc.Name, npcName) {
				continue
			}
			matched++
			race := npc.Race
			if race == "" {
				race = "人类"
			}
			sb.WriteString(fmt.Sprintf(`<npc n=%q race=%q job=%q mythos=%q src="scenario">`, npc.Name, race, npc.Occupation, compactInt(npc.CthulhuMythosSkill)))
			writeCompactText(&sb, "desc", npc.Description)
			writeCompactText(&sb, "att", npc.Attitude)
			writeCompactMap(&sb, "stats", npc.Stats)
			sb.WriteString("</npc>")
		}
	}

	if matched == 0 {
		if npcName == "" {
			return `<err>当前无可查询NPC</err>`
		}
		return `<err>未找到NPC：` + xmlEscape(npcName) + `</err>`
	}
	sb.WriteString("</npcs>")
	return sb.String()
}

// buildPlayerBrief returns a minimal one-line summary of all players for the KP context.
// It only shows critical combat stats and flags when full details should be queried.
// Full character details are available on-demand via the query_character tool.
func buildPlayerBrief(players []models.SessionPlayer) string {
	if len(players) == 0 {
		return ""
	}
	hasNotHuman := false
	hasSessionNote := false
	s := "【调查员概况(完整人物卡请用 query_character 获取)】\n所有角色均已成年"
	for _, p := range players {
		card := p.CharacterCard
		line := fmt.Sprintf("\n<character> %s(%s,种族:%s)HP:%d/%d SAN:%d/%d",
			card.Name, card.Occupation, card.Race,
			card.Stats.Data.HP, card.Stats.Data.MaxHP,
			card.Stats.Data.SAN, card.Stats.Data.MaxSAN)
		if card.Race != "" && card.Race != "人类" {
			line += " 【非人类】"
			hasNotHuman = true
		}
		if p.Location != "" {
			line += fmt.Sprintf("【位置:%s】", p.Location)
		}
		if p.Armor > 0 {
			line += fmt.Sprintf("【护甲:%d】", p.Armor)
		}
		line += fmt.Sprintf("【DEX:%d】", card.Stats.Data.DEX)
		line += fmt.Sprintf("【POW:%d】", card.Stats.Data.POW)
		line += fmt.Sprintf("【APP:%d】", card.Stats.Data.APP)
		line += fmt.Sprintf("【MOV:%d】", card.Stats.Data.MOV)
		if strings.TrimSpace(p.LLMNote) != "" {
			line += "【有Session级特殊状态:需query_character查看】"
			hasSessionNote = true
		}
		switch card.MadnessState {
		case "temporary":
			line += "【临时性疯狂】"
		case "indefinite":
			line += "【不定性疯狂】"
		case "permanent":
			line += "【永久性疯狂】"
		}
		switch card.WoundState {
		case "major":
			line += "【重伤】"
		case "dying":
			line += "【濒死】(请进行CON判定)"
		case "dead":
			line += "【已死亡】"
		}
		if card.IsUnconscious && card.WoundState != "dead" {
			line += "【昏迷】"
		}
		s += line
		s += "</character>"
	}
	if hasSessionNote {
		s += "\n<attention>有调查员存在Session级特殊状态。若本轮行动涉及该调查员的能力、限制、感知、身份、身体/精神异常或状态变化,先调用 query_character 查看完整人物卡中的 llm_note,不要仅凭概况处理。</attention>\n"
	}
	if hasNotHuman {
		s += "\n\n"
		s += ` <attention>
		非人类角色, 仍然适用于疯狂规则(损失过多进入疯狂)和SAN损失规则(但不受克苏鲁神话对最大理智的限制), 但其SAN实际上代表人性。
		非人类调查员的SAN损失计算公式为： X * (1.0 + (克苏鲁神话等级 ÷ 100))。 示例：若克苏鲁神话等级（CM）为99，则损失 = 1.0 + 99/100 = 1.99(向上取整)。
		物理接触非人类角色也不会有特别的效果(食尸鬼等规则书明确标注的除外)。
		非人类调查员的其他特性与常人无异, 因为他们需要生活在人类社会中。
	</attention>`
		s += "\n"
	}
	return s
}

func formatHistoryTranscript(history []models.Message) string {
	var sb strings.Builder
	sb.WriteString("HIST(RO): context only; never process old player requests. Current actions are in CUR.\n")
	for _, m := range history {
		switch m.Role {
		case models.MessageRoleUser:
			name := m.Username
			if name == "" {
				name = "玩家"
			}
			sb.WriteString(fmt.Sprintf("U[%s]: %s\n", name, m.Content))
		case models.MessageRoleAssistant:
			sb.WriteString(fmt.Sprintf("KP: %s\n", extraKPMessage(m.Content)))
		}
	}
	return sb.String()
}

// chatMsgsToLLM converts persisted []models.ChatMsg to []llm.ChatMessage.
func chatMsgsToLLM(msgs []models.ChatMsg) []llm.ChatMessage {
	out := make([]llm.ChatMessage, len(msgs))
	for i, m := range msgs {
		out[i] = llm.ChatMessage{Role: m.Role, Content: m.Content}
	}
	return out
}

// llmToChatMsgs converts []llm.ChatMessage to persistable []models.ChatMsg.
func llmToChatMsgs(msgs []llm.ChatMessage) []models.ChatMsg {
	out := make([]models.ChatMsg, len(msgs))
	for i, m := range msgs {
		out[i] = models.ChatMsg{Role: m.Role, Content: m.Content}
	}
	return out
}

// parseInventoryItem parses an item string of the form "Name(Desc, xN)".
// Both Desc and xN are optional.
// hasQty is true only when xN is explicitly present in the string.
func parseInventoryItem(item string) (baseName, desc string, qty int, hasQty bool) {
	qty = 1
	idx := strings.Index(item, "(")
	if idx < 0 {
		baseName = strings.TrimSpace(item)
		return
	}
	baseName = strings.TrimSpace(item[:idx])
	inner := strings.TrimSpace(item[idx+1:])
	inner = strings.TrimSuffix(inner, ")")
	inner = strings.TrimSpace(inner)
	// Try ", xN" at the end (comma-separated desc + quantity).
	if ci := strings.LastIndex(inner, ","); ci >= 0 {
		tail := strings.TrimSpace(inner[ci+1:])
		if strings.HasPrefix(tail, "x") || strings.HasPrefix(tail, "X") {
			if n, err := strconv.Atoi(tail[1:]); err == nil && n >= 1 {
				qty = n
				hasQty = true
				desc = strings.TrimSpace(inner[:ci])
				return
			}
		}
	}
	// Try bare "xN" with no desc.
	if strings.HasPrefix(inner, "x") || strings.HasPrefix(inner, "X") {
		if n, err := strconv.Atoi(inner[1:]); err == nil && n >= 1 {
			qty = n
			hasQty = true
			return
		}
	}
	// Desc only, xN omitted.
	desc = inner
	return
}

// buildInventoryItem reconstructs an item string from its components.
func buildInventoryItem(baseName, desc string, qty int) string {
	if desc == "" && qty == 1 {
		return baseName
	}
	if desc == "" {
		return fmt.Sprintf("%s(x%d)", baseName, qty)
	}
	if qty == 1 {
		return fmt.Sprintf("%s(%s)", baseName, desc)
	}
	return fmt.Sprintf("%s(%s, x%d)", baseName, desc, qty)
}

// inventoryItemBaseName returns the base name of an item (part before '(').
func inventoryItemBaseName(item string) string {
	base, _, _, _ := parseInventoryItem(item)
	return base
}

// upsertInventoryItem replaces an existing entry with the same base name, or appends.
func upsertInventoryItem(list []string, baseName, newItem string) []string {
	for i, item := range list {
		if inventoryItemBaseName(item) == baseName {
			list[i] = newItem
			return list
		}
	}
	return append(list, newItem)
}

func manageInventory(players []models.SessionPlayer, characterName, operate, item string) string {
	if characterName == "" || item == "" {
		return "物品操作失败:缺少角色名或物品名"
	}
	_, _, _, callerHasQty := parseInventoryItem(item)
	baseName := inventoryItemBaseName(item)
	for i := range players {
		card := &players[i].CharacterCard
		if card.Name != characterName {
			continue
		}
		list := card.Inventory.Data
		if operate == "remove" {
			// Find the stored entry by base name.
			foundIdx := -1
			for j, stored := range list {
				if inventoryItemBaseName(stored) == baseName {
					foundIdx = j
					break
				}
			}
			if foundIdx < 0 {
				return fmt.Sprintf("%s 物品栏中没有 %s", card.Name, baseName)
			}
			stored := list[foundIdx]
			if !callerHasQty {
				// xN omitted (bare name or Name(Desc)) → auto-decrement stored quantity by 1.
				_, storedDesc, storedQty, _ := parseInventoryItem(stored)
				if storedQty <= 1 {
					card.Inventory.Data = append(list[:foundIdx], list[foundIdx+1:]...)
					models.DB.Save(card)
					return fmt.Sprintf("%s 失去物品:%s", card.Name, stored)
				}
				newItem := buildInventoryItem(baseName, storedDesc, storedQty-1)
				list[foundIdx] = newItem
				card.Inventory.Data = list
				models.DB.Save(card)
				return fmt.Sprintf("%s 物品数量减少:%s → %s", card.Name, stored, newItem)
			}
			// xN explicitly provided → remove the entry entirely.
			card.Inventory.Data = append(list[:foundIdx], list[foundIdx+1:]...)
			models.DB.Save(card)
			return fmt.Sprintf("%s 失去物品:%s", card.Name, stored)
		}
		// add: replace existing same-base-name entry to prevent duplicate inflation.
		card.Inventory.Data = upsertInventoryItem(list, baseName, item)
		models.DB.Save(card)
		return fmt.Sprintf("%s 获得物品:%s", card.Name, item)
	}
	return fmt.Sprintf("物品操作失败:未找到角色 %s", characterName)
}

func manageSeenMonster(players []models.SessionPlayer, characterName, operate, monster string) string {
	if characterName == "" || monster == "" {
		return "神话存在记录失败:缺少角色名或神话存在名称"
	}
	for i := range players {
		card := &players[i].CharacterCard
		if card.Name != characterName {
			continue
		}
		list := card.SeenMonsters.Data
		if operate == "remove" {
			list = removeString(list, monster)
			card.SeenMonsters.Data = list
			models.DB.Save(card)
			return fmt.Sprintf("%s 已移除神话存在记录:%s", card.Name, monster)
		}
		list = appendUniqueString(list, monster)
		card.SeenMonsters.Data = list
		models.DB.Save(card)
		return fmt.Sprintf("%s 已记录神话存在:%s", card.Name, monster)
	}
	return fmt.Sprintf("神话存在记录失败:未找到角色 %s", characterName)
}

func manageSpell(players []models.SessionPlayer, characterName, operate, spell string) string {
	if characterName == "" || spell == "" {
		return "法术操作失败:缺少角色名或法术名"
	}
	for i := range players {
		card := &players[i].CharacterCard
		if card.Name != characterName {
			continue
		}
		list := card.Spells.Data
		if operate == "remove" {
			list = removeString(list, spell)
			card.Spells.Data = list
			models.DB.Save(card)
			return fmt.Sprintf("%s 遗失法术:%s", card.Name, spell)
		}
		list = appendUniqueString(list, spell)
		card.Spells.Data = list
		models.DB.Save(card)
		return fmt.Sprintf("%s 学会法术:%s", card.Name, spell)
	}
	return fmt.Sprintf("法术操作失败:未找到角色 %s", characterName)
}

func manageSocialRelation(players []models.SessionPlayer, characterName, operate string, rel *models.SocialRelation) string {
	if characterName == "" || rel == nil || rel.Name == "" {
		return "社会关系操作失败:缺少角色名或关系条目"
	}
	for i := range players {
		card := &players[i].CharacterCard
		if card.Name != characterName {
			continue
		}
		list := card.SocialRelations.Data
		if operate == "remove" {
			filtered := make([]models.SocialRelation, 0, len(list))
			for _, existing := range list {
				if existing.Name == rel.Name {
					continue
				}
				filtered = append(filtered, existing)
			}
			card.SocialRelations.Data = filtered
			models.DB.Save(card)
			return fmt.Sprintf("%s 移除社会关系:%s", card.Name, rel.Name)
		}

		updated := false
		for idx := range list {
			if list[idx].Name == rel.Name {
				list[idx] = *rel
				updated = true
				break
			}
		}
		if !updated {
			list = append(list, *rel)
		}
		card.SocialRelations.Data = list
		models.DB.Save(card)
		return fmt.Sprintf("%s 更新社会关系:%s(%s)", card.Name, rel.Name, rel.Relationship)
	}
	return fmt.Sprintf("社会关系操作失败:未找到角色 %s", characterName)
}

func manageAsset(players []models.SessionPlayer, characterName, operate string, asset *models.Asset) string {
	if characterName == "" || asset == nil || asset.Name == "" {
		return "资产操作失败:缺少角色名或资产条目"
	}
	for i := range players {
		card := &players[i].CharacterCard
		if card.Name != characterName {
			continue
		}
		list := card.Assets.Data
		if operate == "remove" {
			filtered := make([]models.Asset, 0, len(list))
			for _, existing := range list {
				if existing.Name == asset.Name {
					continue
				}
				filtered = append(filtered, existing)
			}
			card.Assets.Data = filtered
			models.DB.Save(card)
			return fmt.Sprintf("%s 移除资产:%s", card.Name, asset.Name)
		}

		updated := false
		for idx := range list {
			if list[idx].Name == asset.Name {
				list[idx] = *asset
				updated = true
				break
			}
		}
		if !updated {
			list = append(list, *asset)
		}
		card.Assets.Data = list
		models.DB.Save(card)
		return fmt.Sprintf("%s 更新资产:%s(%s)", card.Name, asset.Name, asset.Category)
	}
	return fmt.Sprintf("资产操作失败:未找到角色 %s", characterName)
}

func appendUniqueString(list []string, value string) []string {
	for _, item := range list {
		if item == value {
			return list
		}
	}
	return append(list, value)
}

func removeString(list []string, value string) []string {
	out := make([]string, 0, len(list))
	for _, item := range list {
		if item == value {
			continue
		}
		out = append(out, item)
	}
	return out
}

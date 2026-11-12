// NOTE: floor_run.go 实现 Director 的"发言权周期(floor)"：一次 run() 就是一个周期，KP 在其中按 ReAct 方式
// 任意多轮调用工具；每一轮产生了玩家可见消息就同步 Publish 并 Checkpoint，只有 ask_players/end_game
// 才自然结束周期，其余结束方式(打断、防循环、停滞、上下文已满、出错)都由这里的钩子返回哨兵错误。
package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/llmcoc/server/internal/models"
	"github.com/llmcoc/server/internal/services/llm"
)

// ErrFloorStalled 表示自上次产生任何玩家可见消息以来，工具调用轮数超过了预算。
var ErrFloorStalled = errors.New("director produced no player-visible message for too many rounds")

// ErrHostStop 供 handler 用 context.WithCancelCause 取消周期：区别于进程关闭等其他取消原因。
var ErrHostStop = errors.New("floor stopped by host")

type floorRun struct {
	gctx      *GameContext
	cm        *ContextManager
	guard     *loopGuard
	floor     int
	ctx       context.Context
	summarize summarizeFunc
	lastUsage llm.Usage
	progress  func(string)
	// chain 是本轮 LLM 调用返回后的 Director 消息链，末尾是带 tool_calls 的 assistant；执行器读它给 NPC 组视图。
	chain []llm.ChatMessage

	// 以下状态由工具执行器/批次策略通过指针读写，生命周期是整个周期。
	outbox          []Output
	events          []models.GameEvent // 本轮尚未发布的工具事件
	kpStreak        int
	ask             *AskResult
	roundClosed     bool
	dramaturgDone   bool
	combat          *models.CombatState
	chase           *models.ChaseState
	published       int
	ended           bool
	contextFull     bool
	idleRounds      int // 自上次产生任何消息以来的轮数
	beatRounds      int // 自上一条 kp/ask 消息以来的轮数
	beatStart       time.Time
	beatNeedsWriter bool // 本拍内出现过需要白字叙事的可见动作
	beatHasWriter   bool // 本拍内已经发布过 writer 消息
	// writerWait 是本拍内等 Writer 写完花掉的时间，计入周期总等待；两者都要从 Director 决策耗时里扣掉。
	writerWait      time.Duration
	totalWriterWait time.Duration
}

func newFloorRun(ctx context.Context, gctx *GameContext, cm *ContextManager, floor int, summarize summarizeFunc, progress func(string)) *floorRun {
	return &floorRun{
		gctx:      gctx,
		cm:        cm,
		guard:     newLoopGuard(),
		floor:     floor,
		ctx:       ctx,
		summarize: summarize,
		progress:  progress,
		beatStart: time.Now(),
	}
}

// stallLimit 返回停滞预算：遭遇战中每个存活参战者各要一次 combat_act/chase_act，预算更大。
func (f *floorRun) stallLimit() int {
	if f.combat != nil || f.chase != nil {
		return MaxKpRoundEncounter
	}
	return MaxKpRound
}

// afterCall 接到 toolLoopOptions.afterCall：记下本次用量和上下文是否已满，供 afterDispatch 决定是否就地总结。
func (f *floorRun) afterCall(usage llm.Usage, msgs []llm.ChatMessage) []llm.ChatMessage {
	out, full := f.cm.observe(usage, msgs)
	f.contextFull = full
	f.lastUsage = usage
	f.chain = out
	return out
}

// afterDispatch 接到 toolLoopOptions.afterDispatch：每一轮工具结果追加完之后调用。
// 顺序：发布本轮产生的消息并落库 → 防循环判定 → 周期是否需要强制结束。
func (f *floorRun) afterDispatch(round int, calls []llm.ToolCall, msgs []llm.ChatMessage, rejected bool) ([]llm.ChatMessage, error) {
	f.beatRounds++
	if !rejected {
		f.noteVisibleActions(calls, msgs)
	}
	// NOTE: flush 会把 Writer 正文回填进 msgs 里的 tool 结果，后面的防循环和总结都要基于回填后的消息链。
	var patched []llm.ChatMessage
	if !rejected && len(f.outbox) > 0 {
		var err error
		if patched, err = f.flush(round, msgs); err != nil {
			return nil, err
		}
		msgs = patched
	} else {
		if err := f.publishEvents(round); err != nil {
			return nil, err
		}
		f.idleRounds++
	}

	out := patched
	next, err := f.guard.afterDispatch(round, calls, msgs, rejected)
	if err != nil {
		return nil, err
	}
	if next != nil {
		out = next
	}

	// NOTE: 本轮已经以 ask_players/end_game 自然结束时，不能再被强制结束覆盖，否则发言权会被误判为 KP 持有。
	if f.ask == nil && !f.ended {
		if f.contextFull {
			cur := msgs
			if out != nil {
				cur = out
			}
			f.progress("KP 正在整理记忆…")
			rebuilt, stillFull := f.cm.CompactInFloor(f.ctx, f.summarize, cur, f.lastUsage)
			f.contextFull = false
			if stillFull {
				return rebuilt, ErrContextFull
			}
			out = rebuilt
		}
		if f.idleRounds > f.stallLimit() {
			return out, ErrFloorStalled
		}
	}
	return out, nil
}

// noteVisibleActions 记录本拍内是否出现过需要白字叙事的可见动作(被拒绝的调用不算)。
func (f *floorRun) noteVisibleActions(calls []llm.ToolCall, msgs []llm.ChatMessage) {
	results := toolResultsByID(msgs, calls)
	for _, c := range calls {
		if isToolRejected(results[c.ID]) {
			continue
		}
		if visibleActionNeedsWriter(ToolCallType(c.Name)) {
			f.beatNeedsWriter = true
		}
	}
}

// flush 把本轮 outbox 整理成 writer → kp → ask 的固定顺序(同类保持调用顺序)，
// 必要时在 kp 前补一条自动 writer。每条 writer 单独 Publish 并等它写完，正文回填进对应的 tool 结果，
// 所以同一轮里后一条 writer 看得到前一条的正文；kp/ask 在所有 writer 之后一起发布，最后 Checkpoint。
// 返回回填后的消息链，调用方必须用它替换原来的 msgs。
func (f *floorRun) flush(round int, msgs []llm.ChatMessage) ([]llm.ChatMessage, error) {
	var writers, kps, asks []Output
	for _, it := range f.outbox {
		switch it.Kind {
		case OutputWriter:
			writers = append(writers, it)
		case OutputKP:
			kps = append(kps, it)
		case OutputAsk:
			asks = append(asks, it)
		}
	}
	f.outbox = nil

	if len(kps)+len(asks) > 0 && len(writers) == 0 && !f.beatHasWriter && f.beatNeedsWriter {
		source, sourceCallID := "", ""
		if len(kps) > 0 {
			source, sourceCallID = kps[0].Text, kps[0].CallID
		} else {
			source, sourceCallID = asks[0].Text, asks[0].CallID
		}
		if dir := fallbackWriterDirection(source); dir != "" {
			writers = append(writers, Output{Kind: OutputWriter, Text: dir, Auto: true, CallID: sourceCallID})
		}
	}

	beats := make([]Output, 0, len(kps)+len(asks))
	beats = append(beats, kps...)
	beats = append(beats, asks...)

	closesBeat := len(beats) > 0
	if closesBeat {
		timePoint := f.gctx.Session.GameTime
		for i := range beats {
			beats[i].TimePoint = timePoint
			if beats[i].Ended {
				f.ended = true
			}
		}
	}

	if f.gctx.Publish == nil {
		f.published += len(writers) + len(beats)
	} else {
		for i, w := range writers {
			ro := RoundOutputs{Floor: f.floor, Round: round, Items: []Output{w}, WriterCtx: snapshotGameContext(*f.gctx)}
			if i == 0 {
				ro.Events = f.events
				f.events = nil
			}
			// NOTE: Writer 在 Publish 期间就会入队开跑，此时 Checkpoint 还没落库，只能在这里把消息链浅拷贝交出去。
			ro.Director = append([]llm.ChatMessage(nil), msgs...)
			start := time.Now()
			results, err := f.gctx.Publish(ro)
			f.addWriterWait(time.Since(start))
			if err != nil {
				return msgs, fmt.Errorf("publish round outputs: %w", err)
			}
			f.published++
			msgs = backfillWriterResults(msgs, results)
		}

		ro := RoundOutputs{Floor: f.floor, Round: round, Items: beats, WriterCtx: snapshotGameContext(*f.gctx)}
		if len(writers) == 0 {
			ro.Events = f.events
			f.events = nil
		}
		if closesBeat {
			ro.ElapsedMs = max(0, (time.Since(f.beatStart) - f.writerWait).Milliseconds())
			ro.Steps = f.beatRounds
		}
		if len(ro.Items) > 0 || len(ro.Events) > 0 {
			if _, err := f.gctx.Publish(ro); err != nil {
				return msgs, fmt.Errorf("publish round outputs: %w", err)
			}
			f.published += len(beats)
		}
	}
	f.events = nil
	f.cm.Checkpoint(f.gctx.Session.TurnRound, msgs)
	f.guard.markMessagesProduced()
	f.idleRounds = 0

	if closesBeat {
		f.beatRounds = 0
		f.beatStart = time.Now()
		f.writerWait = 0
		f.beatNeedsWriter = false
		f.beatHasWriter = false
	} else if len(writers) > 0 {
		f.beatHasWriter = true
	}
	return msgs, nil
}

// addWriterWait 记下一次发布里等 Writer 花掉的时间。
func (f *floorRun) addWriterWait(d time.Duration) {
	f.writerWait += d
	f.totalWriterWait += d
}

// backfillWriterResults 按 CallID 把 Writer 的结果写回消息链里对应的 tool 消息。
// 返回新切片：原来的切片可能已经被交给异步的 Writer 当快照，不能原地修改。
func backfillWriterResults(msgs []llm.ChatMessage, results []WriterResult) []llm.ChatMessage {
	if len(results) == 0 {
		return msgs
	}
	out := append([]llm.ChatMessage(nil), msgs...)
	for _, r := range results {
		for i := len(out) - 1; i >= 0; i-- {
			if out[i].Role != "tool" || out[i].ToolCallID != r.CallID {
				continue
			}
			switch r.Status {
			case WriterDone:
				out[i].Content, _ = attachWriterOutput(out[i].Content, r.Text)
			case WriterFailed:
				out[i].Content, _ = attachWriterFailure(out[i].Content)
			}
			break
		}
	}
	return out
}

// publishEvents 发布只有工具事件、没有消息的一轮。
// NOTE: 这种轮次不算"产生了消息"：不写检查点、不重置停滞计数、不计入 published。
func (f *floorRun) publishEvents(round int) error {
	if len(f.events) == 0 {
		return nil
	}
	events := f.events
	f.events = nil
	if f.gctx.Publish == nil {
		return nil
	}
	ro := RoundOutputs{Floor: f.floor, Round: round, Events: events, WriterCtx: snapshotGameContext(*f.gctx)}
	if _, err := f.gctx.Publish(ro); err != nil {
		return fmt.Errorf("publish round events: %w", err)
	}
	return nil
}

// NextDirectorFloor 返回 Director 下一个发言权周期的编号，供 handler 在周期开始前给玩家消息和事件标 floor。
func NextDirectorFloor(sessionID uint) int {
	return LoadContext(sessionID, "director", nil, ContextOptions{}).NextFloor()
}

// snapshotGameContext 返回供 Writer/Painter 异步读取的 GameContext 拷贝：Players 切片单独拷贝，
// 回调置空——Director 继续原地修改原 gctx 时不会与异步读取产生数据竞争。
func snapshotGameContext(g GameContext) GameContext {
	g.Session.Players = append([]models.SessionPlayer(nil), g.Session.Players...)
	g.Progress = nil
	g.Publish = nil
	return g
}

// run 执行一个发言权周期(floor)：KP 在其中按 ReAct 方式任意多轮调用工具，每一轮产生了玩家可见消息就
// 通过 gctx.Publish 同步发布并落库；只有 ask_players/end_game 自然结束周期。
// 强制结束(打断、防循环、停滞、上下文已满、出错)由 RunOutput.Stop 说明原因，只有 StopError 时 err 非 nil。
// 玩家行动记录由 handler 在调用 run 前完成。
func run(ctx context.Context, gctx GameContext) (RunOutput, error) {
	sid := gctx.Session.ID
	handles, err := getCachedAgents(sid)
	if err != nil {
		return RunOutput{Stop: StopError}, err
	}
	emitProgress := func(text string) {
		if gctx.Progress != nil {
			gctx.Progress(text)
		}
	}
	debugf("run", "session=%d floor_reason=%q user=%q input=%s", sid, gctx.KPFloor, gctx.UserName, gctx.UserInput)
	emitProgress(progressThinking)

	var tempNPCs []models.SessionNPC
	models.DB.Where("session_id = ?", sid).Find(&tempNPCs)

	director := handles[models.AgentRoleDirector]
	directorWindow := int64(0)
	if director.config != nil {
		directorWindow = int64(director.config.ContextWindow)
	}
	kpSysPrompt := director.systemPrompt(renderNSFW(kpSystemPrompt, gctx.Session.EnableNSFW))
	cm := LoadContext(sid, "director", buildKPHead(gctx, kpSysPrompt), ContextOptions{Window: directorWindow, Strategy: TrimSummarize})
	// 老会话transcript为空但messages表有历史时，用旧的扁平transcript当作seq=0的一轮
	// 种子先提交进去；它没有任何工具调用，总结时按纯文本原样放进输入。
	if cm.IsEmpty() && len(gctx.History) > 0 {
		cm.Commit(0, []llm.ChatMessage{{Role: "user", Content: formatHistoryTranscript(gctx.History)}})
	}
	// NOTE: 打断时没等到的 Writer、以及正文进历史这个机制上线前的旧会话，tool 结果里缺正文，这里按调用 ID 从消息表补上，
	// 要在总结之前做，Build 和总结看到的都是补齐后的历史。
	cm.backfillWriterProse(loadWriterProse(sid))
	// NOTE: 新周期开始前先看历史是否逼近窗口，逼近就用 Director 自己的模型把较早的拍总结掉。
	summarize := directorSummarizer(director, sid)
	cm.MaybeCompact(ctx, summarize)

	balanceRules := strings.TrimSpace(models.GetSiteSetting("balance_rules", models.DefaultBalanceRules))
	floor := cm.NextFloor()
	fr := newFloorRun(ctx, &gctx, cm, floor, summarize, emitProgress)
	fr.combat = gctx.Session.CombatState.Data
	fr.chase = gctx.Session.ChaseState.Data
	kpMsgs := cm.Build(buildKPTurnOpening(gctx, floor, tempNPCs, balanceRules, fr.combat, fr.chase)...)

	st := directorDispatchState{
		sid:          sid,
		gctx:         &gctx,
		handles:      handles,
		tempNPCs:     &tempNPCs,
		combat:       &fr.combat,
		chase:        &fr.chase,
		roundClosed:  &fr.roundClosed,
		outbox:       &fr.outbox,
		events:       &fr.events,
		kpStreak:     &fr.kpStreak,
		ask:          &fr.ask,
		chain:        &fr.chain,
		emitProgress: emitProgress,
	}

	round := 0
	directorStart := time.Now()
	finalMsgs, loopErr := runToolLoop(ctx, toolLoopOptions{
		handle:        director,
		stage:         "director",
		msgs:          kpMsgs,
		tools:         directorTools(handles[models.AgentRoleDramaturg].isEnabled()),
		maxRounds:     0,
		batchPolicy:   directorBatchPolicy(&fr.dramaturgDone, &fr.combat, &fr.chase, &fr.roundClosed, gctx.PendingActions, emitProgress),
		batchDispatch: fr.guard.wrapDispatch(directorBatchDispatch(st)),
		afterCall:     fr.afterCall,
		afterDispatch: fr.afterDispatch,
		renderResult:  renderDirectorToolResult,
		beforeRound: func(r int) {
			round = r
			debugf("KP", "session=%d floor=%d round=%d — calling LLM", sid, floor, r)
			emitProgress(progressKPIteration(r-1, 0))
		},
		onToolCalls: func(calls []llm.ToolCall) {
			debugf("KP", "session=%d round=%d → %d tool call(s): %s", sid, round, len(calls), formatNativeCallNames(calls))
			emitProgress(progressPlannedCalls(calls))
		},
	})
	// NOTE: Director 决策耗时不含等 Writer 写完的时间。
	elapsedMs := max(0, (time.Since(directorStart) - fr.totalWriterWait).Milliseconds())

	stop, runErr := classifyFloorEnd(ctx, loopErr, round)
	// NOTE: 已经执行完的轮(哪怕没有产生消息)的副作用必须留在 KP 的记忆里，所以无论怎么结束都补落库一次；
	// 没有任何完整轮可落(如首轮 LLM 调用就失败)时 CommitTail 什么都不做。
	cm.CommitTail(gctx.Session.TurnRound, finalMsgs)

	natural := loopErr == nil
	if natural || fr.published > 0 {
		clearConsumedTurnActions(gctx, fr.combat, fr.chase)
	}
	if natural {
		emitProgress("KP主流程裁定完成")
	}
	debugf("run", "session=%d floor=%d done stop=%q published=%d rounds=%d", sid, floor, stop, fr.published, round)

	ask := fr.ask
	if stop != StopNone {
		ask = nil
	}
	return RunOutput{
		Messages:          fr.published,
		Ask:               ask,
		Ended:             fr.ended,
		Stop:              stop,
		DirectorElapsedMs: elapsedMs,
		DirectorSteps:     round,
	}, runErr
}

// classifyFloorEnd 把 runToolLoop 的结束错误归类为周期的 Stop 原因；只有 StopError 才返回非 nil 的 err。
func classifyFloorEnd(ctx context.Context, loopErr error, round int) (StopReason, error) {
	switch {
	case loopErr == nil:
		return StopNone, nil
	case ctx.Err() != nil && errors.Is(context.Cause(ctx), ErrHostStop):
		return StopHostStop, nil
	case errors.Is(loopErr, ErrLoopDetected):
		return StopLoop, nil
	case errors.Is(loopErr, ErrFloorStalled):
		return StopStall, nil
	case errors.Is(loopErr, ErrContextFull):
		return StopContext, nil
	}
	alog.Error("KP floor failed", "round", round, "err", loopErr)
	return StopError, fmt.Errorf("KP agent failed: %w", loopErr)
}

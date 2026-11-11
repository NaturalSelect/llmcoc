// NOTE: floor_run_test.go 用脚本化的假 Provider 端到端驱动 run()，验证发言权周期(floor)的行为：
// 多拍发布、消息顺序、兜底 writer、每条 write 独立 NSFW、每拍一张图、停滞、打断、防循环、
// 上下文已满，以及 transcript 与已发布消息一一对应。不涉及真实 LLM。
package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/llmcoc/server/internal/models"
	"github.com/llmcoc/server/internal/services/llm"
)

// scriptedDirectorProvider 按轮次调用 script 生成响应，并记录每次调用收到的 msgs。
// summaryReply 非空时 Chat（总结调用）返回它并记录输入；为空时返回空串，总结会因缺少 <summary> 块而失败。
type scriptedDirectorProvider struct {
	mu           sync.Mutex
	calls        [][]llm.ChatMessage
	summaryCalls [][]llm.ChatMessage
	summaryReply string
	script       func(n int, msgs []llm.ChatMessage) (llm.ToolChatResult, error)
}

func (p *scriptedDirectorProvider) Chat(_ context.Context, _ string, msgs []llm.ChatMessage) (string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.summaryCalls = append(p.summaryCalls, append([]llm.ChatMessage(nil), msgs...))
	return p.summaryReply, nil
}

func (p *scriptedDirectorProvider) ChatStream(context.Context, string, []llm.ChatMessage, []llm.ToolDefinition) (<-chan string, <-chan error, error) {
	return nil, nil, fmt.Errorf("scriptedDirectorProvider: ChatStream not supported")
}

func (p *scriptedDirectorProvider) JsonChat(context.Context, string, []llm.ChatMessage) (string, error) {
	return "", nil
}

func (p *scriptedDirectorProvider) ChatWithTools(_ context.Context, _ string, msgs []llm.ChatMessage, _ []llm.ToolDefinition) (llm.ToolChatResult, error) {
	p.mu.Lock()
	n := len(p.calls) + 1
	p.calls = append(p.calls, append([]llm.ChatMessage(nil), msgs...))
	p.mu.Unlock()
	return p.script(n, msgs)
}

func (p *scriptedDirectorProvider) callCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.calls)
}

var _ llm.Provider = (*scriptedDirectorProvider)(nil)

// floorCall 构造一个原生工具调用，参数用 map 序列化，避免手写 JSON 出错。
func floorCall(id, name string, args map[string]any) llm.ToolCall {
	raw, _ := json.Marshal(args)
	return llm.ToolCall{ID: id, Name: name, Arguments: string(raw)}
}

func toolRound(calls ...llm.ToolCall) llm.ToolChatResult {
	return llm.ToolChatResult{ToolCalls: calls}
}

// floorImageProvider 让 generate_image 通过 ImageGenerator 检查；测试里不会真的出图。
type floorImageProvider struct{ scriptedDirectorProvider }

func (p *floorImageProvider) GenerateImage(context.Context, string, llm.ImageOptions) (string, string, error) {
	return "", "", errors.New("not used")
}

func setFloorHandles(t *testing.T, sessionID uint, director llm.Provider, withPainter bool) {
	t.Helper()
	handles := map[models.AgentRole]agentHandle{
		models.AgentRoleDirector:  {provider: director, config: &models.AgentConfig{Role: models.AgentRoleDirector, IsActive: true}, enabled: true},
		models.AgentRoleDramaturg: {enabled: false},
	}
	if withPainter {
		handles[models.AgentRolePainter] = agentHandle{
			provider: &floorImageProvider{},
			config:   &models.AgentConfig{Role: models.AgentRolePainter, IsActive: true},
			enabled:  true,
		}
	}
	sessionAgents.Store(sessionID, handles)
	t.Cleanup(func() { deleteCachedAgents(sessionID) })
}

// floorGctx 构造一次 run() 的 GameContext，并把每次 Publish 的内容记录到 published。
func floorGctx(sessionID uint, published *[]RoundOutputs) GameContext {
	g := newDirectorRunGctx(sessionID, 1, "我环顾四周")
	g.Publish = func(ro RoundOutputs) error {
		*published = append(*published, ro)
		return nil
	}
	return g
}

func newFloorSession(t *testing.T, id uint) {
	t.Helper()
	initAgentTestDB(t)
	if err := models.DB.Create(&models.GameSession{ID: id}).Error; err != nil {
		t.Fatalf("create session: %v", err)
	}
}

func outputKinds(items []Output) []OutputKind {
	kinds := make([]OutputKind, len(items))
	for i, it := range items {
		kinds[i] = it.Kind
	}
	return kinds
}

func equalKinds(a, b []OutputKind) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// TestFloor_MultiBeatPublishesEachRoundAndEndsOnAsk 验证 write → narrate → write+generate_image → ask_players
// 的序列：每个产生消息的轮各 Publish 一次，周期只在 ask_players 后结束，transcript 按单元落库。
func TestFloor_MultiBeatPublishesEachRoundAndEndsOnAsk(t *testing.T) {
	const sid = 201
	newFloorSession(t, sid)
	prov := &scriptedDirectorProvider{script: func(n int, _ []llm.ChatMessage) (llm.ToolChatResult, error) {
		switch n {
		case 1:
			return toolRound(floorCall("c1", "write", map[string]any{"direction": "雾气从港口漫上来"})), nil
		case 2:
			return toolRound(floorCall("c2", "narrate", map[string]any{"reply": "你听见远处的钟声。"})), nil
		case 3:
			return toolRound(
				floorCall("c3a", "write", map[string]any{"direction": "灯塔的光扫过海面"}),
				floorCall("c3b", "generate_image", map[string]any{"image_prompt": "a foggy lighthouse at night"}),
			), nil
		default:
			return toolRound(floorCall("c4", "ask_players", map[string]any{"targets": []string{"甲"}, "question": "你要做什么？"})), nil
		}
	}}
	setFloorHandles(t, sid, prov, true)

	var published []RoundOutputs
	out, err := run(context.Background(), floorGctx(sid, &published))
	if err != nil {
		t.Fatalf("run(): %v", err)
	}

	if out.Stop != StopNone || out.Ended {
		t.Fatalf("应以ask_players自然结束, got stop=%q ended=%v", out.Stop, out.Ended)
	}
	if out.Ask == nil || len(out.Ask.Names) != 1 || out.Ask.Names[0] != "甲" || out.Ask.Question != "你要做什么？" {
		t.Fatalf("RunOutput.Ask 应点名甲, got %+v", out.Ask)
	}
	if got := prov.callCount(); got != 4 {
		t.Fatalf("provider 调用次数 = %d, want 4", got)
	}
	if len(published) != 4 {
		t.Fatalf("Publish 次数 = %d, want 4(每个产生消息的轮一次)", len(published))
	}

	wantKinds := [][]OutputKind{
		{OutputWriter},
		{OutputKP},
		{OutputWriter, OutputImage},
		{OutputAsk},
	}
	total := 0
	for i, ro := range published {
		if !equalKinds(outputKinds(ro.Items), wantKinds[i]) {
			t.Errorf("第%d次Publish的消息种类 = %v, want %v", i+1, outputKinds(ro.Items), wantKinds[i])
		}
		if ro.Floor != 1 {
			t.Errorf("第%d次Publish的Floor = %d, want 1", i+1, ro.Floor)
		}
		total += len(ro.Items)
	}
	if out.Messages != total || total != 5 {
		t.Errorf("RunOutput.Messages = %d, 实际发布 %d 条, want 5", out.Messages, total)
	}

	if got := published[1].Items[0].Text; got != "你听见远处的钟声。" {
		t.Errorf("kp 消息正文 = %q", got)
	}
	if published[1].Items[0].TimePoint == "" {
		t.Error("kp 消息应带上游戏内时间点")
	}
	if published[0].ElapsedMs != 0 || published[0].Steps != 0 {
		t.Errorf("只有write的轮不关闭一拍，不应携带耗时/轮数, got %+v", published[0])
	}
	if published[1].Steps != 2 {
		t.Errorf("kp 消息的 Steps = %d, want 2(write轮+narrate轮)", published[1].Steps)
	}
	if published[3].Steps != 2 {
		t.Errorf("ask 消息的 Steps = %d, want 2(write+image轮、ask轮)", published[3].Steps)
	}

	data := loadTranscript(t, sid, "director")
	if len(data.Turns) != 4 {
		t.Fatalf("transcript 应有4个发布单元, got %d", len(data.Turns))
	}
	for i, turn := range data.Turns {
		if turn.Floor != 1 || turn.Unit != i+1 {
			t.Errorf("单元%d: Floor=%d Unit=%d, want Floor=1 Unit=%d", i, turn.Floor, turn.Unit, i+1)
		}
	}
	if data.NextFloor != 2 {
		t.Errorf("NextFloor = %d, want 2", data.NextFloor)
	}
}

// TestFloor_RoundOutputsOrderedWriterImageKPAsk 验证同一轮 [ask_players, narrate, generate_image, write]
// 被整理成 writer → image → kp → ask。
func TestFloor_RoundOutputsOrderedWriterImageKPAsk(t *testing.T) {
	const sid = 202
	newFloorSession(t, sid)
	prov := &scriptedDirectorProvider{script: func(n int, _ []llm.ChatMessage) (llm.ToolChatResult, error) {
		return toolRound(
			floorCall("a", "ask_players", map[string]any{"targets": []string{"甲"}, "question": "你呢？"}),
			floorCall("b", "narrate", map[string]any{"reply": "门开了。"}),
			floorCall("c", "generate_image", map[string]any{"image_prompt": "an opening door"}),
			floorCall("d", "write", map[string]any{"direction": "门缓缓打开"}),
		), nil
	}}
	setFloorHandles(t, sid, prov, true)

	var published []RoundOutputs
	if _, err := run(context.Background(), floorGctx(sid, &published)); err != nil {
		t.Fatalf("run(): %v", err)
	}
	if len(published) != 1 {
		t.Fatalf("Publish 次数 = %d, want 1", len(published))
	}
	want := []OutputKind{OutputWriter, OutputImage, OutputKP, OutputAsk}
	if got := outputKinds(published[0].Items); !equalKinds(got, want) {
		t.Fatalf("消息种类顺序 = %v, want %v", got, want)
	}

	ro := published[0]
	if got := ro.Items[0].CallID; got != "d" {
		t.Errorf("writer Output.CallID = %q, want the write call id %q", got, "d")
	}
	if got := ro.Items[2].CallID; got != "b" {
		t.Errorf("kp Output.CallID = %q, want the narrate call id %q", got, "b")
	}
	if n := len(ro.Director); n == 0 {
		t.Fatal("含 writer 的轮应带上 Director 消息链快照")
	} else if last := ro.Director[n-1]; last.Role != "tool" || last.ToolCallID != "d" {
		t.Errorf("快照应以 write 的 tool 结果结尾(包含本轮所有工具回执), got %+v", last)
	}
}

// TestFloor_WriteNSFWIsIndependentPerCall 验证两条 write 的 NSFW 标记各自独立。
func TestFloor_WriteNSFWIsIndependentPerCall(t *testing.T) {
	const sid = 203
	newFloorSession(t, sid)
	if err := models.DB.Model(&models.GameSession{}).Where("id = ?", sid).Update("enable_nsfw", true).Error; err != nil {
		t.Fatalf("enable nsfw: %v", err)
	}
	prov := &scriptedDirectorProvider{script: func(n int, _ []llm.ChatMessage) (llm.ToolChatResult, error) {
		switch n {
		case 1:
			return toolRound(
				floorCall("w1", "write", map[string]any{"direction": "亲密场景", "nsfw": true}),
				floorCall("w2", "write", map[string]any{"direction": "清晨的街道", "nsfw": false}),
			), nil
		default:
			return toolRound(floorCall("ask", "ask_players", map[string]any{"targets": []string{"甲"}, "question": "继续吗？"})), nil
		}
	}}
	setFloorHandles(t, sid, prov, false)

	var published []RoundOutputs
	g := floorGctx(sid, &published)
	g.Session.EnableNSFW = true
	if _, err := run(context.Background(), g); err != nil {
		t.Fatalf("run(): %v", err)
	}
	if len(published) < 1 || len(published[0].Items) != 2 {
		t.Fatalf("第一次Publish应有两条write, got %+v", published)
	}
	if !published[0].Items[0].NSFW || published[0].Items[1].NSFW {
		t.Errorf("两条write的NSFW应各自独立: got [%v, %v], want [true, false]", published[0].Items[0].NSFW, published[0].Items[1].NSFW)
	}
}

// TestFloor_FallbackWriterWhenVisibleActionWithoutWrite 验证：一拍内有需要白字叙事的可见动作、
// 出了 kp 消息却没有 write 时，在 kp 之前自动补一条 Auto writer。
func TestFloor_FallbackWriterWhenVisibleActionWithoutWrite(t *testing.T) {
	const sid = 204
	newFloorSession(t, sid)
	prov := &scriptedDirectorProvider{script: func(n int, _ []llm.ChatMessage) (llm.ToolChatResult, error) {
		switch n {
		case 1:
			return toolRound(floorCall("d", "roll_dice", map[string]any{"character": "甲", "what": "侦查", "dice_expr": "1d100"})), nil
		case 2:
			return toolRound(floorCall("r", "narrate", map[string]any{"reply": "你发现了一道划痕。"})), nil
		default:
			return toolRound(floorCall("ask", "ask_players", map[string]any{"targets": []string{"甲"}, "question": "然后呢？"})), nil
		}
	}}
	setFloorHandles(t, sid, prov, false)

	var published []RoundOutputs
	if _, err := run(context.Background(), floorGctx(sid, &published)); err != nil {
		t.Fatalf("run(): %v", err)
	}
	var kpRound *RoundOutputs
	for i := range published {
		for _, it := range published[i].Items {
			if it.Kind == OutputKP {
				kpRound = &published[i]
			}
		}
	}
	if kpRound == nil {
		t.Fatalf("没有发布kp消息: %+v", published)
	}
	if got := outputKinds(kpRound.Items); !equalKinds(got, []OutputKind{OutputWriter, OutputKP}) {
		t.Fatalf("含可见动作的kp轮应补一条writer在kp前, got %v", got)
	}
	if !kpRound.Items[0].Auto || kpRound.Items[0].Text == "" {
		t.Errorf("兜底writer应标记Auto且带direction, got %+v", kpRound.Items[0])
	}
	if kpRound.Items[0].CallID != "r" || kpRound.Items[1].CallID != "r" {
		t.Errorf("自动补写的 CallID 应等于来源 narrate 调用的 ID, got writer=%q kp=%q", kpRound.Items[0].CallID, kpRound.Items[1].CallID)
	}
	if len(kpRound.Director) == 0 {
		t.Error("自动补写的轮也应带上 Director 消息链快照")
	}
}

// TestFloor_ImageQuotaResetsEachBeat 验证每拍各有一张配图额度：同一拍第二张被拒，kp 消息结束一拍后额度重置。
func TestFloor_ImageQuotaResetsEachBeat(t *testing.T) {
	const sid = 205
	newFloorSession(t, sid)
	var secondImageResult string
	prov := &scriptedDirectorProvider{script: func(n int, msgs []llm.ChatMessage) (llm.ToolChatResult, error) {
		switch n {
		case 1:
			return toolRound(floorCall("i1", "generate_image", map[string]any{"image_prompt": "a quiet harbor"})), nil
		case 2:
			return toolRound(floorCall("i2", "generate_image", map[string]any{"image_prompt": "the same harbor again"})), nil
		case 3:
			secondImageResult = toolResultFor(msgs, "i2")
			return toolRound(floorCall("r1", "narrate", map[string]any{"reply": "海风很冷。"})), nil
		case 4:
			return toolRound(floorCall("i3", "generate_image", map[string]any{"image_prompt": "a new beat, a new image"})), nil
		default:
			return toolRound(floorCall("ask", "ask_players", map[string]any{"targets": []string{"甲"}, "question": "你怎么办？"})), nil
		}
	}}
	setFloorHandles(t, sid, prov, true)

	var published []RoundOutputs
	if _, err := run(context.Background(), floorGctx(sid, &published)); err != nil {
		t.Fatalf("run(): %v", err)
	}
	if secondImageResult == "" || secondImageResult[:len("SYSTEM REJECT")] != "SYSTEM REJECT" {
		t.Errorf("同一拍的第二张配图应被拒绝, got %q", secondImageResult)
	}
	images := 0
	for _, ro := range published {
		for _, it := range ro.Items {
			if it.Kind == OutputImage {
				images++
			}
		}
	}
	if images != 2 {
		t.Errorf("发布的配图数 = %d, want 2(每拍各一张)", images)
	}
}

// toolResultFor 返回 msgs 里对应 tool_call_id 的 tool 结果文本，找不到返回空串。
func toolResultFor(msgs []llm.ChatMessage, id string) string {
	for _, m := range msgs {
		if m.Role == "tool" && m.ToolCallID == id {
			return m.Content
		}
	}
	return ""
}

// TestFloor_CancelKeepsPublishedMessagesAndCommitsTail 验证被房主打断：已发布的消息保留、
// 已完整的尾部落库、返回 StopHostStop 且没有点名结果。
func TestFloor_CancelKeepsPublishedMessagesAndCommitsTail(t *testing.T) {
	const sid = 206
	newFloorSession(t, sid)
	ctx, cancel := context.WithCancelCause(context.Background())
	defer cancel(nil)

	prov := &scriptedDirectorProvider{script: func(n int, _ []llm.ChatMessage) (llm.ToolChatResult, error) {
		switch n {
		case 1:
			return toolRound(floorCall("r1", "narrate", map[string]any{"reply": "你站在门前。"})), nil
		case 2:
			return toolRound(floorCall("q1", "query_clues", map[string]any{})), nil
		default:
			cancel(ErrHostStop)
			return toolRound(floorCall("q2", "query_clues", map[string]any{})), nil
		}
	}}
	setFloorHandles(t, sid, prov, false)

	var published []RoundOutputs
	out, err := run(ctx, floorGctx(sid, &published))
	if err != nil {
		t.Fatalf("host_stop 不应返回err, got %v", err)
	}
	if out.Stop != StopHostStop {
		t.Fatalf("Stop = %q, want %q", out.Stop, StopHostStop)
	}
	if out.Ask != nil || out.Ended {
		t.Errorf("被打断时不应有点名/结束: %+v", out)
	}
	if out.Messages != 1 || len(published) != 1 {
		t.Errorf("已发布的消息应保留: Messages=%d published=%d, want 1", out.Messages, len(published))
	}

	data := loadTranscript(t, sid, "director")
	if len(data.Turns) < 2 {
		t.Fatalf("应至少有 narrate 单元和尾部单元, got %d", len(data.Turns))
	}
	if data.Turns[len(data.Turns)-1].Kind != "tail" {
		t.Errorf("最后一个单元应是尾部(tail), got %q", data.Turns[len(data.Turns)-1].Kind)
	}
}

// TestFloor_StallStopsAfterBudgetWithoutMessages 验证连续多轮没有产生消息时以 StopStall 结束。
func TestFloor_StallStopsAfterBudgetWithoutMessages(t *testing.T) {
	const sid = 207
	newFloorSession(t, sid)
	prov := &scriptedDirectorProvider{script: func(n int, _ []llm.ChatMessage) (llm.ToolChatResult, error) {
		// NOTE: 每轮用不同的 hint 内容，避开防循环的签名重复，只让停滞预算起作用。
		return toolRound(floorCall(fmt.Sprintf("h%d", n), "hint", map[string]any{"hint": fmt.Sprintf("备注%d", n)})), nil
	}}
	setFloorHandles(t, sid, prov, false)

	var published []RoundOutputs
	out, err := run(context.Background(), floorGctx(sid, &published))
	if err != nil {
		t.Fatalf("stall 不应返回err, got %v", err)
	}
	if out.Stop != StopStall {
		t.Fatalf("Stop = %q, want %q", out.Stop, StopStall)
	}
	if out.Messages != 0 || len(published) != 0 {
		t.Errorf("停滞的周期不应发布消息, got Messages=%d", out.Messages)
	}
	if got := prov.callCount(); got != MaxKpRound+1 {
		t.Errorf("provider 调用次数 = %d, want %d(预算 %d 轮后的下一轮结束)", got, MaxKpRound+1, MaxKpRound)
	}
}

// TestFloor_LoopDetectedStopsRepeatedIdenticalCalls 验证重复发出相同的工具调用时以 StopLoop 结束。
func TestFloor_LoopDetectedStopsRepeatedIdenticalCalls(t *testing.T) {
	const sid = 208
	newFloorSession(t, sid)
	prov := &scriptedDirectorProvider{script: func(n int, _ []llm.ChatMessage) (llm.ToolChatResult, error) {
		return toolRound(floorCall(fmt.Sprintf("q%d", n), "query_clues", map[string]any{})), nil
	}}
	setFloorHandles(t, sid, prov, false)

	var published []RoundOutputs
	out, err := run(context.Background(), floorGctx(sid, &published))
	if err != nil {
		t.Fatalf("loop 不应返回err, got %v", err)
	}
	if out.Stop != StopLoop {
		t.Fatalf("Stop = %q, want %q", out.Stop, StopLoop)
	}
	if got := prov.callCount(); got != loopGuardAbortAt {
		t.Errorf("provider 调用次数 = %d, want %d(第%d次相同调用中止)", got, loopGuardAbortAt, loopGuardAbortAt)
	}
}

// TestFloor_ContextFullStopsWithStopContext 验证兜底路径：上下文已满且总结没有把它降下来（这里本周期之前没有任何可总结的历史）时以 StopContext 结束。
func TestFloor_ContextFullStopsWithStopContext(t *testing.T) {
	const sid = 209
	newFloorSession(t, sid)
	prov := &scriptedDirectorProvider{script: func(n int, _ []llm.ChatMessage) (llm.ToolChatResult, error) {
		return llm.ToolChatResult{
			ToolCalls: []llm.ToolCall{floorCall(fmt.Sprintf("h%d", n), "hint", map[string]any{"hint": fmt.Sprintf("备注%d", n)})},
			Usage:     llm.Usage{PromptTokens: 950},
		}, nil
	}}
	sessionAgents.Store(uint(sid), map[models.AgentRole]agentHandle{
		models.AgentRoleDirector:  {provider: prov, config: &models.AgentConfig{Role: models.AgentRoleDirector, IsActive: true, ContextWindow: 1000}, enabled: true},
		models.AgentRoleDramaturg: {enabled: false},
	})
	t.Cleanup(func() { deleteCachedAgents(sid) })

	var published []RoundOutputs
	out, err := run(context.Background(), floorGctx(sid, &published))
	if err != nil {
		t.Fatalf("context 不应返回err, got %v", err)
	}
	if out.Stop != StopContext {
		t.Fatalf("Stop = %q, want %q", out.Stop, StopContext)
	}
	if out.Ask != nil {
		t.Errorf("上下文已满时不应有点名结果, got %+v", out.Ask)
	}
}

// TestFloor_PublishErrorStopsWithStopError 验证 Publish 返回错误时周期以 StopError 结束并带上 err。
func TestFloor_PublishErrorStopsWithStopError(t *testing.T) {
	const sid = 210
	newFloorSession(t, sid)
	prov := &scriptedDirectorProvider{script: func(n int, _ []llm.ChatMessage) (llm.ToolChatResult, error) {
		return toolRound(floorCall("r", "narrate", map[string]any{"reply": "你好。"})), nil
	}}
	setFloorHandles(t, sid, prov, false)

	g := newDirectorRunGctx(sid, 1, "输入")
	g.Publish = func(RoundOutputs) error { return errors.New("db down") }
	out, err := run(context.Background(), g)
	if err == nil {
		t.Fatal("Publish 失败应让 run() 返回err")
	}
	if out.Stop != StopError {
		t.Errorf("Stop = %q, want %q", out.Stop, StopError)
	}
	if out.Messages != 0 {
		t.Errorf("Publish 失败的轮不应计入 Messages, got %d", out.Messages)
	}
}

// TestFloor_ResponseKeepsFloorUntilAsk 验证 narrate 不结束周期：KP 在 narrate 之后继续被调用。
func TestFloor_ResponseKeepsFloorUntilAsk(t *testing.T) {
	const sid = 211
	newFloorSession(t, sid)
	prov := &scriptedDirectorProvider{script: func(n int, _ []llm.ChatMessage) (llm.ToolChatResult, error) {
		if n <= 3 {
			return toolRound(floorCall(fmt.Sprintf("r%d", n), "narrate", map[string]any{"reply": fmt.Sprintf("第%d段叙述", n)})), nil
		}
		return toolRound(floorCall("ask", "ask_players", map[string]any{"targets": []string{"甲"}, "question": "你们呢？"})), nil
	}}
	setFloorHandles(t, sid, prov, false)

	var published []RoundOutputs
	out, err := run(context.Background(), floorGctx(sid, &published))
	if err != nil {
		t.Fatalf("run(): %v", err)
	}
	if got := prov.callCount(); got != 4 {
		t.Fatalf("provider 调用次数 = %d, want 4(3次narrate之后仍继续)", got)
	}
	if out.Messages != 4 || out.Ask == nil {
		t.Errorf("want 4条消息且有点名, got Messages=%d Ask=%+v", out.Messages, out.Ask)
	}
}

// TestFloor_EndGameMarksEnded 验证 end_game 以 kp 消息(Ended=true)发布并让 RunOutput.Ended 为真。
func TestFloor_EndGameMarksEnded(t *testing.T) {
	const sid = 212
	newFloorSession(t, sid)
	prov := &scriptedDirectorProvider{script: func(n int, _ []llm.ChatMessage) (llm.ToolChatResult, error) {
		return toolRound(floorCall("e", "end_game", map[string]any{
			"reply": "故事落幕。", "win": true, "end_summary": "触发了结局",
			"settlement": []map[string]any{{"character_name": "甲", "score": 80, "skills": []string{"侦查"}}},
		})), nil
	}}
	setFloorHandles(t, sid, prov, false)

	var published []RoundOutputs
	out, err := run(context.Background(), floorGctx(sid, &published))
	if err != nil {
		t.Skipf("end_game 结算依赖完整的游戏库，最小测试库下返回 %v，由 endsession_test 覆盖", err)
	}
	if !out.Ended {
		t.Errorf("RunOutput.Ended = false, want true; out=%+v", out)
	}
	if len(published) == 0 {
		t.Fatal("end_game 应发布一条kp消息")
	}
	last := published[len(published)-1].Items
	if len(last) == 0 || last[len(last)-1].Kind != OutputKP || !last[len(last)-1].Ended {
		t.Errorf("最后一条应是 Ended 的kp消息, got %+v", last)
	}
}

// seedFloorHistory 往 session 的 director transcript 里预置 n 个已完成的周期，每个周期一拍：write + narrate。
func seedFloorHistory(sid uint, n int) {
	cm := LoadContext(sid, "director", nil, ContextOptions{})
	for f := 1; f <= n; f++ {
		msgs := cm.Build(llm.ChatMessage{Role: "user", Content: fmt.Sprintf("<player_turn floor=%d>旧行动%d</player_turn>", f, f)})
		msgs = append(msgs,
			ckAssistant(ckCall(fmt.Sprintf("hw%d", f), "write", fmt.Sprintf(`{"direction":"旧叙事%d"}`, f))), ckTool(fmt.Sprintf("hw%d", f), "已记录"),
			ckAssistant(ckCall(fmt.Sprintf("hr%d", f), "narrate", fmt.Sprintf(`{"reply":"旧回复%d"}`, f))), ckTool(fmt.Sprintf("hr%d", f), "已发给玩家"))
		cm.Checkpoint(1, msgs)
	}
}

// TestFloor_MidFloorCompactContinuesSameFloor 验证周期中途上下文满时就地总结并继续同一个周期：
// 周期以 ask 自然结束(Stop 为空)，总结单元排在最前面，总结之后的调用以 head+总结 开头并仍含本周期的 opening，
// 期间发布的消息数不受影响。
func TestFloor_MidFloorCompactContinuesSameFloor(t *testing.T) {
	const sid = 213
	newFloorSession(t, sid)
	seedFloorHistory(sid, 3)

	prov := &scriptedDirectorProvider{summaryReply: "<summary>旧周期的摘要</summary>"}
	prov.script = func(n int, _ []llm.ChatMessage) (llm.ToolChatResult, error) {
		switch n {
		case 1: // 第一次调用就报告上下文已满，且这一轮没有玩家可见消息
			return llm.ToolChatResult{
				ToolCalls: []llm.ToolCall{floorCall("h1", "hint", map[string]any{"hint": "备注1"})},
				Usage:     llm.Usage{PromptTokens: 5_000},
			}, nil
		case 2:
			return llm.ToolChatResult{
				ToolCalls: []llm.ToolCall{floorCall("r1", "narrate", map[string]any{"reply": "总结之后的回复。"})},
				Usage:     llm.Usage{PromptTokens: 100},
			}, nil
		default:
			return llm.ToolChatResult{
				ToolCalls: []llm.ToolCall{floorCall("a1", "ask_players", map[string]any{"targets": []string{"甲"}, "question": "你怎么做？"})},
				Usage:     llm.Usage{PromptTokens: 100},
			}, nil
		}
	}
	sessionAgents.Store(uint(sid), map[models.AgentRole]agentHandle{
		models.AgentRoleDirector:  {provider: prov, config: &models.AgentConfig{Role: models.AgentRoleDirector, IsActive: true, ContextWindow: 20_000}, enabled: true},
		models.AgentRoleDramaturg: {enabled: false},
	})
	t.Cleanup(func() { deleteCachedAgents(sid) })

	var published []RoundOutputs
	out, err := run(context.Background(), floorGctx(sid, &published))
	if err != nil {
		t.Fatalf("run(): %v", err)
	}
	if out.Stop != StopNone || out.Ask == nil {
		t.Fatalf("周期应以 ask 自然结束, got Stop=%q Ask=%+v", out.Stop, out.Ask)
	}
	if got := prov.callCount(); got != 3 {
		t.Fatalf("provider 调用次数 = %d, want 3(总结之后继续同一个周期)", got)
	}
	if len(prov.summaryCalls) != 1 {
		t.Fatalf("总结应只调用一次, got %d", len(prov.summaryCalls))
	}
	if in := prov.summaryCalls[0][1].Content; !strings.Contains(in, "旧回复1") || strings.Contains(in, "备注1") {
		t.Errorf("总结输入应含旧周期、不含本周期:\n%s", in)
	}
	if out.Messages != 2 {
		t.Errorf("Messages = %d, want 2(narrate + ask)", out.Messages)
	}

	// 总结发生在第 1 轮之后：第 2、3 次调用收到的 msgs 以 head + 总结开头，且仍含本周期的 opening 和备注轮。
	for _, idx := range []int{1, 2} {
		sent := prov.calls[idx]
		const headLen = 2
		if len(sent) <= headLen || !strings.HasPrefix(sent[headLen].Content, "<story_so_far") {
			t.Fatalf("第 %d 次调用 head 之后应紧跟总结: %+v", idx+1, sent[:min(len(sent), headLen+1)])
		}
		var opening, hint bool
		for _, m := range sent {
			if strings.Contains(m.Content, "<player_turn floor=4>") {
				opening = true
			}
			for _, tc := range m.ToolCalls {
				if tc.ID == "h1" {
					hint = true
				}
			}
		}
		if !opening || !hint {
			t.Errorf("第 %d 次调用应仍含本周期的 opening(%v) 和已执行的备注轮(%v)", idx+1, opening, hint)
		}
		for _, m := range sent[headLen+1:] {
			if strings.Contains(m.Content, "旧回复1") {
				t.Errorf("被总结掉的旧周期不应再原样出现")
			}
		}
	}

	saved := loadTranscript(t, sid, "director")
	if saved.Turns[0].Kind != "summary" {
		t.Fatalf("transcript 最前面应是总结: %+v", saved.Turns[0])
	}
	if last := saved.Turns[len(saved.Turns)-1]; last.Floor != 4 {
		t.Errorf("本周期的单元应仍用 floor 4 落库: %+v", last)
	}
}

// TestFloor_ToolEventsPublishImmediatelyWithoutCountingAsMessages 验证：只有工具事件的一轮会立刻单独发布，
// 但不算产生消息；事件按调用顺序排列，暗骰只带 hidden。
func TestFloor_ToolEventsPublishImmediatelyWithoutCountingAsMessages(t *testing.T) {
	const sid = 230
	newFloorSession(t, sid)
	prov := &scriptedDirectorProvider{script: func(n int, _ []llm.ChatMessage) (llm.ToolChatResult, error) {
		switch n {
		case 1:
			return toolRound(
				floorCall("d1", "roll_dice", map[string]any{"dice": map[string]any{"character": "甲", "what": "侦查", "dice_expr": "1d100", "level": "困难"}}),
				floorCall("d2", "roll_dice", map[string]any{"dice": map[string]any{"character": "甲", "what": "聆听", "dice_expr": "1d100", "hidden": true}}),
				floorCall("u1", "update_characters", map[string]any{"changes": []string{"HP -3(甲)"}}),
			), nil
		case 2:
			return toolRound(floorCall("r", "narrate", map[string]any{"reply": "你被划伤了。"})), nil
		default:
			return toolRound(floorCall("ask", "ask_players", map[string]any{"targets": []string{"甲"}, "question": "然后呢？"})), nil
		}
	}}
	setFloorHandles(t, sid, prov, false)

	var published []RoundOutputs
	gctx := floorGctx(sid, &published)
	gctx.Session.Players[0].CharacterCard.Stats.Data = models.CharacterStats{HP: 10, MaxHP: 10}
	out, err := run(context.Background(), gctx)
	if err != nil {
		t.Fatalf("run(): %v", err)
	}
	if len(published) < 2 {
		t.Fatalf("published = %+v, want an events-only round first", published)
	}
	first := published[0]
	if len(first.Items) != 0 || len(first.Events) != 3 {
		t.Fatalf("first publish items=%v events=%+v", outputKinds(first.Items), first.Events)
	}
	if ev := first.Events[0]; ev.Type != "dice" || ev.Character != "甲" || ev.Skill != "侦查" || ev.Level != "困难" || ev.Roll == 0 || ev.Hidden {
		t.Errorf("public dice event = %+v", ev)
	}
	if ev := first.Events[1]; ev != (models.GameEvent{Type: "dice", Hidden: true}) {
		t.Errorf("hidden dice event = %+v, want only type+hidden", ev)
	}
	if ev := first.Events[2]; ev.Type != "stat" || ev.Field != "hp" || *ev.From != 10 || *ev.To != 7 {
		t.Errorf("hp event = %+v", ev)
	}
	if second := published[1]; len(second.Events) != 0 || !equalKinds(outputKinds(second.Items), []OutputKind{OutputWriter, OutputKP}) {
		t.Errorf("second publish items=%v events=%+v", outputKinds(second.Items), second.Events)
	}
	if out.Messages != 3 {
		t.Errorf("Messages = %d, want 3 (auto writer + kp + ask, events not counted)", out.Messages)
	}
}

// TestFloor_EventsOnlyRoundsStillStall 验证只有事件的轮次不重置停滞计数。
func TestFloor_EventsOnlyRoundsStillStall(t *testing.T) {
	const sid = 231
	newFloorSession(t, sid)
	prov := &scriptedDirectorProvider{script: func(n int, _ []llm.ChatMessage) (llm.ToolChatResult, error) {
		return toolRound(floorCall(fmt.Sprintf("l%d", n), "update_location", map[string]any{"character_name": "甲", "new_location": fmt.Sprintf("房间%d", n)})), nil
	}}
	setFloorHandles(t, sid, prov, false)

	var published []RoundOutputs
	out, err := run(context.Background(), floorGctx(sid, &published))
	if err != nil {
		t.Fatalf("run(): %v", err)
	}
	if out.Stop != StopStall || out.Messages != 0 {
		t.Fatalf("Stop=%q Messages=%d, want stall with no messages", out.Stop, out.Messages)
	}
	if len(published) == 0 || len(published[0].Items) != 0 || published[0].Events[0].Location != "房间1" {
		t.Errorf("events-only rounds should still publish, got %+v", published)
	}
}

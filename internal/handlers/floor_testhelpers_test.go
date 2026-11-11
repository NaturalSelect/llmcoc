package handlers

// NOTE: floor / 写作队列测试共用的替身：可编程的 AgentRunner，以及订阅房间 hub 事件的监听器。
// 每个启动了周期的测试都必须等到 floor_end 再返回，否则后台 goroutine 会在 models.DB 被还原之后继续读库。

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/llmcoc/server/internal/services/agent"
)

const testPNGDataURL = "data:image/png;base64,YWJj"

type fakeRunner struct {
	run     func(ctx context.Context, gctx agent.GameContext) (agent.RunOutput, error)
	writer  func(ctx context.Context, gctx agent.GameContext, direction string, nsfw bool, onToken func(string)) (string, error)
	painter func(ctx context.Context, gctx agent.GameContext, req agent.ImagePromptRequest) (string, error)

	mu          sync.Mutex
	runs        []agent.GameContext
	directions  []string
	writerCtxs  []agent.GameContext
	writerReqs  []agent.WriterRequest
	nsfwFlags   []bool
	inFlight    int
	maxInFlight int
}

func (f *fakeRunner) Run(ctx context.Context, gctx agent.GameContext) (agent.RunOutput, error) {
	f.mu.Lock()
	f.runs = append(f.runs, gctx)
	f.mu.Unlock()
	if f.run == nil {
		return agent.RunOutput{}, nil
	}
	return f.run(ctx, gctx)
}

func (f *fakeRunner) RunWriterStream(ctx context.Context, gctx agent.GameContext, req agent.WriterRequest, onToken func(string)) (string, error) {
	direction, nsfw := req.Direction, req.NSFW
	f.mu.Lock()
	f.directions = append(f.directions, direction)
	f.writerCtxs = append(f.writerCtxs, gctx)
	f.writerReqs = append(f.writerReqs, req)
	f.nsfwFlags = append(f.nsfwFlags, nsfw)
	f.inFlight++
	if f.inFlight > f.maxInFlight {
		f.maxInFlight = f.inFlight
	}
	f.mu.Unlock()
	defer func() {
		f.mu.Lock()
		f.inFlight--
		f.mu.Unlock()
	}()

	if f.writer != nil {
		return f.writer(ctx, gctx, direction, nsfw, onToken)
	}
	text := "正文:" + direction
	onToken(text)
	return text, nil
}

func (f *fakeRunner) RunPainter(ctx context.Context, gctx agent.GameContext, req agent.ImagePromptRequest) (string, error) {
	if f.painter != nil {
		return f.painter(ctx, gctx, req)
	}
	return testPNGDataURL, nil
}

func (f *fakeRunner) runCalls() []agent.GameContext {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]agent.GameContext(nil), f.runs...)
}

func (f *fakeRunner) writerDirections() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.directions...)
}

func (f *fakeRunner) maxWriterConcurrency() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.maxInFlight
}

// roomTap 订阅房间 hub，只收订阅之后发布的事件（replay 丢弃），用来断言事件顺序。
type roomTap struct {
	t   *testing.T
	sub *subscriber
	got []roomEvent
}

func tapRoom(t *testing.T, sessionID uint) *roomTap {
	t.Helper()
	hub := hubFor(sessionID)
	sub, _, _ := hub.Subscribe(0)
	t.Cleanup(func() { hub.Unsubscribe(sub) })
	return &roomTap{t: t, sub: sub}
}

func (r *roomTap) next() {
	r.t.Helper()
	select {
	case ev := <-r.sub.ch:
		r.got = append(r.got, ev)
	case <-r.sub.done:
		r.t.Fatalf("room subscriber was dropped; events so far: %v", r.types())
	case <-time.After(5 * time.Second):
		r.t.Fatalf("timed out waiting for room events; events so far: %v", r.types())
	}
}

// waitCount 一直读事件，直到累计收到 n 个指定类型的事件。
func (r *roomTap) waitCount(typ string, n int) {
	r.t.Helper()
	for len(r.ofType(typ)) < n {
		r.next()
	}
}

func (r *roomTap) ofType(typ string) []roomEvent {
	var out []roomEvent
	for _, ev := range r.got {
		if ev.Type == typ {
			out = append(out, ev)
		}
	}
	return out
}

func (r *roomTap) types() []string {
	out := make([]string, 0, len(r.got))
	for _, ev := range r.got {
		out = append(out, ev.Type)
	}
	return out
}

// persistentTypes 只列持久事件（seq>0），忽略 kp_status、writer_token 这类临时事件。
func (r *roomTap) persistentTypes() []string {
	var out []string
	for _, ev := range r.got {
		if ev.Seq > 0 {
			out = append(out, ev.Type)
		}
	}
	return out
}

func eventMessage(ev roomEvent) messageResponse {
	return ev.Data.(gin.H)["message"].(messageResponse)
}

func eventField(ev roomEvent, key string) any {
	return ev.Data.(gin.H)[key]
}

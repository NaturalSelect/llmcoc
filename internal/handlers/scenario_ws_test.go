package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"github.com/llmcoc/server/internal/middleware"
)

// scenarioWSServer 搭一个挂了模组 WebSocket 路由的服务；extra 可以追加自定义路由。
func scenarioWSServer(t *testing.T, role string, extra func(g *gin.RouterGroup)) *httptest.Server {
	t.Helper()
	r := gin.New()
	g := r.Group("/scenarios", withAuth(1, "tester", role), middleware.AdminRequired())
	g.GET("/generate/ws", GenerateScenarioWS)
	g.GET("/compile-story/ws", CompileStoryWS)
	if extra != nil {
		extra(g)
	}
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)
	return srv
}

func scenarioWSURL(srv *httptest.Server, path string) string {
	return "ws" + strings.TrimPrefix(srv.URL, "http") + path
}

// scenarioWSDial 建立连接并发出请求帧；body 为 string 时原样发送，否则按 JSON 编码。
func scenarioWSDial(t *testing.T, srv *httptest.Server, path string, body any) (*wsClient, *websocket.Conn) {
	t.Helper()
	conn, resp, err := websocket.DefaultDialer.Dial(scenarioWSURL(srv, path), nil)
	if err != nil {
		status := 0
		if resp != nil {
			status = resp.StatusCode
		}
		t.Fatalf("dial: %v (status %d)", err, status)
	}
	var payload []byte
	if s, ok := body.(string); ok {
		payload = []byte(s)
	} else {
		payload, _ = json.Marshal(body)
	}
	if err := conn.WriteMessage(websocket.TextMessage, payload); err != nil {
		t.Fatalf("write request frame: %v", err)
	}
	return newWSClient(t, conn), conn
}

// wsExpectClosed 断言连接被服务端正常关闭：不会再有帧，读通道随之关闭。
func wsExpectClosed(t *testing.T, c *wsClient) {
	t.Helper()
	select {
	case f, ok := <-c.frames:
		if ok {
			t.Fatalf("expected connection close, got frame %+v", f)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for the server to close the connection")
	}
}

func wsMessage(t *testing.T, f wsFrame) string {
	t.Helper()
	var d struct {
		Message string `json:"message"`
	}
	if err := json.Unmarshal(f.Data, &d); err != nil {
		t.Fatalf("decode frame data %s: %v", f.Data, err)
	}
	return d.Message
}

func TestScenarioWSRejectsBadRequests(t *testing.T) {
	initTestDB(t)
	srv := scenarioWSServer(t, "admin", nil)

	cases := []struct {
		name string
		path string
		body any
		want string
	}{
		{"编译缺少故事全文", "/scenarios/compile-story/ws", map[string]any{"name": "测试模组"}, "StoryDocument"},
		{"编译请求不是 JSON", "/scenarios/compile-story/ws", "not json", ""},
		{"编译文档超过上限", "/scenarios/compile-story/ws", map[string]any{"story_document": strings.Repeat("a", maxStoryDocumentBytes+1)}, "500KB"},
		{"生成数量超过上限", "/scenarios/generate/ws", map[string]any{"count": maxScenarioGenCount + 1}, "不能超过"},
		{"生成请求不是 JSON", "/scenarios/generate/ws", "{", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c, _ := scenarioWSDial(t, srv, tc.path, tc.body)
			f := wsRead(t, c)
			if f.Type != "rejected" {
				t.Fatalf("第一帧 type = %q, want rejected: %s", f.Type, f.Data)
			}
			if msg := wsMessage(t, f); msg == "" || !strings.Contains(msg, tc.want) {
				t.Errorf("拒绝原因 = %q, want 包含 %q", msg, tc.want)
			}
			wsExpectClosed(t, c)
		})
	}
}

func TestScenarioWSRejectsBinaryRequestFrame(t *testing.T) {
	initTestDB(t)
	srv := scenarioWSServer(t, "admin", nil)

	conn, _, err := websocket.DefaultDialer.Dial(scenarioWSURL(srv, "/scenarios/compile-story/ws"), nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	c := newWSClient(t, conn)
	if err := conn.WriteMessage(websocket.BinaryMessage, []byte(`{"story_document":"x"}`)); err != nil {
		t.Fatalf("write: %v", err)
	}
	if f := wsRead(t, c); f.Type != "rejected" {
		t.Fatalf("type = %q, want rejected", f.Type)
	}
	wsExpectClosed(t, c)
}

func TestScenarioWSNonAdminRejectedBeforeUpgrade(t *testing.T) {
	initTestDB(t)
	srv := scenarioWSServer(t, "user", nil)

	_, resp, err := websocket.DefaultDialer.Dial(scenarioWSURL(srv, "/scenarios/generate/ws"), nil)
	if err == nil {
		t.Fatal("非管理员不应能升级连接")
	}
	if resp == nil || resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status = %v, want 403", resp)
	}
}

// 没有配置任何 Agent 时流水线会立刻失败：用真实流水线验证事件顺序、批量汇总和连接收尾。
func TestScenarioWSGenerateWithoutAgentsReportsErrors(t *testing.T) {
	initTestDB(t)
	srv := scenarioWSServer(t, "admin", nil)

	c, _ := scenarioWSDial(t, srv, "/scenarios/generate/ws", map[string]any{"count": 2})
	var types []string
	for {
		f := wsRead(t, c)
		types = append(types, f.Type)
		if f.Type == "batch_done" {
			var d struct {
				Total     int `json:"total"`
				Succeeded int `json:"succeeded"`
				Failed    int `json:"failed"`
			}
			if err := json.Unmarshal(f.Data, &d); err != nil {
				t.Fatalf("decode batch_done: %v", err)
			}
			if d.Total != 2 || d.Succeeded != 0 || d.Failed != 2 {
				t.Errorf("batch_done = %+v, want total 2 / succeeded 0 / failed 2", d)
			}
			break
		}
	}
	want := "batch_progress,progress,error,batch_progress,progress,error,batch_done"
	if got := strings.Join(types, ","); got != want {
		t.Errorf("帧顺序 = %s, want %s", got, want)
	}
	wsExpectClosed(t, c)
}

func TestScenarioWSCompileWithoutAgentsReportsError(t *testing.T) {
	initTestDB(t)
	srv := scenarioWSServer(t, "admin", nil)

	c, _ := scenarioWSDial(t, srv, "/scenarios/compile-story/ws", map[string]any{"story_document": "# 雨夜\n正文", "name": "雨夜"})
	f := wsRead(t, c)
	if f.Type != "error" {
		t.Fatalf("type = %q, want error: %s", f.Type, f.Data)
	}
	if msg := wsMessage(t, f); !strings.HasPrefix(msg, "模组编译失败") {
		t.Errorf("message = %q, want 以「模组编译失败」开头", msg)
	}
	wsExpectClosed(t, c)
}

// 任务产生的事件应按顺序原样转发，type 是事件名，data 是事件载荷；任务结束后服务端正常关闭连接。
func TestScenarioWSForwardsJobEventsInOrder(t *testing.T) {
	srv := scenarioWSServer(t, "admin", func(g *gin.RouterGroup) {
		g.GET("/custom/ws", func(c *gin.Context) {
			serveScenarioWS(c, func(raw []byte) (scenarioJob, string) {
				if string(raw) != `{"hello":1}` {
					return nil, "unexpected request " + string(raw)
				}
				return func(emit scenarioEmit) {
					emit("progress", gin.H{"detail": "第一步"})
					emit("done", gin.H{"index": 1, "name": "雨夜档案"})
				}, ""
			})
		})
	})

	c, _ := scenarioWSDial(t, srv, "/scenarios/custom/ws", `{"hello":1}`)
	f := wsRead(t, c)
	if f.Type != "progress" || !strings.Contains(string(f.Data), "第一步") {
		t.Fatalf("第一帧 = %s %s", f.Type, f.Data)
	}
	f = wsRead(t, c)
	if f.Type != "done" || !strings.Contains(string(f.Data), "雨夜档案") {
		t.Fatalf("第二帧 = %s %s", f.Type, f.Data)
	}
	wsExpectClosed(t, c)
}

// 客户端中途断开时任务必须照常跑完：事件远多于队列容量，emit 不能被卡住。
func TestScenarioWSJobFinishesAfterClientDisconnects(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	var emitted atomic.Int32
	finished := make(chan struct{})
	srv := scenarioWSServer(t, "admin", func(g *gin.RouterGroup) {
		g.GET("/custom/ws", func(c *gin.Context) {
			serveScenarioWS(c, func(raw []byte) (scenarioJob, string) {
				return func(emit scenarioEmit) {
					defer close(finished)
					close(started)
					<-release
					for i := 0; i < scenarioEventQueue*4; i++ {
						emit("progress", gin.H{"detail": strings.Repeat("x", 256)})
						emitted.Add(1)
					}
				}, ""
			})
		})
	})

	_, conn := scenarioWSDial(t, srv, "/scenarios/custom/ws", `{}`)
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("任务没有启动")
	}
	_ = conn.Close()
	close(release)

	select {
	case <-finished:
	case <-time.After(5 * time.Second):
		t.Fatalf("客户端断开后任务被卡住，只发出了 %d 个事件", emitted.Load())
	}
	if got := emitted.Load(); got != scenarioEventQueue*4 {
		t.Errorf("emit 次数 = %d, want %d", got, scenarioEventQueue*4)
	}
}

package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"github.com/llmcoc/server/internal/models"
)

// wsFrame 是客户端解码后的一帧；Data 保持原始 JSON，由用例按需解析。
type wsFrame struct {
	Seq  uint64          `json:"seq"`
	Type string          `json:"type"`
	Data json.RawMessage `json:"data"`
}

// wsTestServer 搭一个只挂 WS 路由的 gin engine。userID==0 时不注入身份，模拟未登录请求。
func wsTestServer(t *testing.T, userID uint) *httptest.Server {
	t.Helper()
	r := gin.New()
	if userID != 0 {
		r.Use(withAuth(userID, fmt.Sprintf("ws_user_%d", userID), "user"))
	}
	r.GET("/api/sessions/:id/ws", NewSessionHandlers(nil).WS)
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)
	return srv
}

func wsURL(srv *httptest.Server, sessionID uint, query string) string {
	u := fmt.Sprintf("ws%s/api/sessions/%d/ws", strings.TrimPrefix(srv.URL, "http"), sessionID)
	if query != "" {
		u += "?" + query
	}
	return u
}

// wsClient 在独立 goroutine 里持续读帧。不能用读超时来断言"没有更多帧"：
// gorilla 读超时之后连接的读状态永久损坏，同一条连接后面就读不了了。
type wsClient struct {
	conn   *websocket.Conn
	frames chan wsFrame
}

// newWSClient 包装已建立的连接，并在用例结束时关闭，保证服务端的读循环能退出。
func newWSClient(t *testing.T, conn *websocket.Conn) *wsClient {
	t.Helper()
	c := &wsClient{conn: conn, frames: make(chan wsFrame, 1024)}
	go func() {
		defer close(c.frames)
		for {
			var f wsFrame
			if err := conn.ReadJSON(&f); err != nil {
				return
			}
			c.frames <- f
		}
	}()
	t.Cleanup(func() { _ = conn.Close() })
	return c
}

func wsDial(t *testing.T, srv *httptest.Server, sessionID uint, query string) *wsClient {
	t.Helper()
	conn, resp, err := websocket.DefaultDialer.Dial(wsURL(srv, sessionID, query), nil)
	if err != nil {
		status := 0
		if resp != nil {
			status = resp.StatusCode
		}
		t.Fatalf("dial: %v (status %d)", err, status)
	}
	return newWSClient(t, conn)
}

// wsDialRejected 期望握手被拒，返回 HTTP 状态码。
func wsDialRejected(t *testing.T, srv *httptest.Server, sessionID uint, query string, header http.Header) int {
	t.Helper()
	conn, resp, err := websocket.DefaultDialer.Dial(wsURL(srv, sessionID, query), header)
	if err == nil {
		_ = conn.Close()
		t.Fatal("dial succeeded, want handshake rejection")
	}
	if resp == nil {
		t.Fatalf("dial failed without an HTTP response: %v", err)
	}
	defer resp.Body.Close()
	return resp.StatusCode
}

func wsRead(t *testing.T, c *wsClient) wsFrame {
	t.Helper()
	select {
	case f, ok := <-c.frames:
		if !ok {
			t.Fatal("connection closed while waiting for a frame")
		}
		return f
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for a frame")
		return wsFrame{}
	}
}

// wsExpectQuiet 断言短时间内没有更多帧。
func wsExpectQuiet(t *testing.T, c *wsClient) {
	t.Helper()
	select {
	case f, ok := <-c.frames:
		if ok {
			t.Fatalf("unexpected extra frame: %+v", f)
		}
		t.Fatal("connection closed unexpectedly")
	case <-time.After(100 * time.Millisecond):
	}
}

func wsReadHello(t *testing.T, c *wsClient) wsHelloData {
	t.Helper()
	f := wsRead(t, c)
	if f.Type != "hello" || f.Seq != 0 {
		t.Fatalf("first frame = %+v, want hello with seq=0", f)
	}
	var hello wsHelloData
	if err := json.Unmarshal(f.Data, &hello); err != nil {
		t.Fatalf("decode hello: %v", err)
	}
	return hello
}

type wsRoom struct {
	sessionID uint
	memberID  uint
	outsider  uint
}

// seedWSRoom 建一个带密码的房间：memberID 是房间成员，outsider 不是。
func seedWSRoom(t *testing.T) wsRoom {
	t.Helper()
	resetHubs(t)
	ownerID := seedUser(t, "ws_owner", models.RoleUser, 0, 3)
	memberID := seedUser(t, "ws_member", models.RoleUser, 0, 3)
	outsiderID := seedUser(t, "ws_outsider", models.RoleUser, 0, 3)
	cardID := seedCard(t, memberID, "成员卡")
	scenarioID := seedScenario(t, "ws-scenario")

	sess := models.GameSession{
		Name: "WS Room", ScenarioID: scenarioID, Status: models.SessionStatusPlaying,
		MaxPlayers: 4, CreatedBy: ownerID, HasPassword: true, TurnRound: 1,
	}
	if err := models.DB.Create(&sess).Error; err != nil {
		t.Fatalf("create session: %v", err)
	}
	if err := models.DB.Create(&models.SessionPlayer{
		SessionID: sess.ID, UserID: memberID, CharacterCardID: cardID,
	}).Error; err != nil {
		t.Fatalf("create session player: %v", err)
	}
	return wsRoom{sessionID: sess.ID, memberID: memberID, outsider: outsiderID}
}

func TestWS_RejectsRequestWithoutUser(t *testing.T) {
	initTestDB(t)
	room := seedWSRoom(t)
	srv := wsTestServer(t, 0)

	if code := wsDialRejected(t, srv, room.sessionID, "", nil); code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", code)
	}
}

func TestWS_PasswordRoomNonMemberForbidden(t *testing.T) {
	initTestDB(t)
	room := seedWSRoom(t)
	srv := wsTestServer(t, room.outsider)

	if code := wsDialRejected(t, srv, room.sessionID, "", nil); code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", code)
	}
}

func TestWS_PasswordRoomAdminAllowed(t *testing.T) {
	initTestDB(t)
	room := seedWSRoom(t)
	adminID := seedUser(t, "ws_admin", models.RoleAdmin, 0, 3)
	srv := wsTestServer(t, adminID)

	conn := wsDial(t, srv, room.sessionID, "")
	wsReadHello(t, conn)
}

func TestWS_SessionNotFound(t *testing.T) {
	initTestDB(t)
	room := seedWSRoom(t)
	srv := wsTestServer(t, room.memberID)

	if code := wsDialRejected(t, srv, 99999, "", nil); code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", code)
	}
}

func TestWS_InvalidSinceRejected(t *testing.T) {
	initTestDB(t)
	room := seedWSRoom(t)
	srv := wsTestServer(t, room.memberID)

	if code := wsDialRejected(t, srv, room.sessionID, "since=abc", nil); code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", code)
	}
}

func TestWS_MemberReceivesHello(t *testing.T) {
	initTestDB(t)
	room := seedWSRoom(t)
	hubFor(room.sessionID).Publish("floor_start", map[string]any{"floor": 1})
	srv := wsTestServer(t, room.memberID)

	conn := wsDial(t, srv, room.sessionID, "")
	hello := wsReadHello(t, conn)
	if hello.Epoch == "" || hello.Epoch != wsEpoch {
		t.Fatalf("hello epoch = %q, want server epoch %q", hello.Epoch, wsEpoch)
	}
	if hello.Seq != 1 {
		t.Fatalf("hello seq = %d, want 1", hello.Seq)
	}
}

func TestWS_DeliversLiveEventsToAllSubscribers(t *testing.T) {
	initTestDB(t)
	room := seedWSRoom(t)
	srv := wsTestServer(t, room.memberID)

	a := wsDial(t, srv, room.sessionID, "")
	b := wsDial(t, srv, room.sessionID, "")
	wsReadHello(t, a)
	wsReadHello(t, b)

	hub := hubFor(room.sessionID)
	hub.Publish("message_new", map[string]any{"id": 7})
	hub.Ephemeral("kp_status", map[string]any{"text": "思考中"})

	for name, conn := range map[string]*wsClient{"a": a, "b": b} {
		if f := wsRead(t, conn); f.Seq != 1 || f.Type != "message_new" {
			t.Fatalf("%s: persistent frame = %+v", name, f)
		}
		f := wsRead(t, conn)
		if f.Seq != 0 || f.Type != "kp_status" || !strings.Contains(string(f.Data), "思考中") {
			t.Fatalf("%s: ephemeral frame = %+v data=%s", name, f, f.Data)
		}
	}
}

// NOTE: 连上时有 writer 正在生成，握手后紧跟一帧 offset=0 的全文，新连接不必等 message_update 才看到正文。
func TestWS_SendsLiveWriterCatchUp(t *testing.T) {
	initTestDB(t)
	room := seedWSRoom(t)
	lw := startLiveWriter(room.sessionID, 42)
	t.Cleanup(lw.stop)
	lw.append("雨夜，")
	lw.flush()
	lw.append("钟声")
	srv := wsTestServer(t, room.memberID)

	conn := wsDial(t, srv, room.sessionID, "")
	wsReadHello(t, conn)
	f := wsRead(t, conn)
	var tok writerTokenEvent
	if err := json.Unmarshal(f.Data, &tok); err != nil {
		t.Fatalf("decode writer_token: %v", err)
	}
	if f.Type != "writer_token" || f.Seq != 0 || tok.MessageID != 42 || tok.Offset != 0 || tok.Text != "雨夜，钟声" {
		t.Fatalf("catch-up frame = %+v data=%+v", f, tok)
	}

	lw.flush()
	f = wsRead(t, conn)
	if err := json.Unmarshal(f.Data, &tok); err != nil || tok.Offset != 3 || tok.Text != "钟声" {
		t.Fatalf("incremental frame = %+v data=%+v err=%v", f, tok, err)
	}
}

func TestWS_ReplaysEventsAfterSince(t *testing.T) {
	initTestDB(t)
	room := seedWSRoom(t)
	hub := hubFor(room.sessionID)
	hub.Publish("message_new", map[string]any{"id": 1})
	hub.Publish("message_new", map[string]any{"id": 2})
	hub.Publish("floor_end", map[string]any{"floor": 1})
	srv := wsTestServer(t, room.memberID)

	// 带上与服务端一致的 epoch：应当补发而不是 resync。
	conn := wsDial(t, srv, room.sessionID, "since=1&epoch="+wsEpoch)
	hello := wsReadHello(t, conn)
	if hello.Seq != 3 {
		t.Fatalf("hello seq = %d, want 3", hello.Seq)
	}
	if f := wsRead(t, conn); f.Seq != 2 || f.Type != "message_new" {
		t.Fatalf("replay[0] = %+v, want seq=2 message_new", f)
	}
	if f := wsRead(t, conn); f.Seq != 3 || f.Type != "floor_end" {
		t.Fatalf("replay[1] = %+v, want seq=3 floor_end", f)
	}
	wsExpectQuiet(t, conn)

	// 补发之后无缝衔接实时事件。
	hub.Publish("floor_start", map[string]any{"floor": 2})
	if f := wsRead(t, conn); f.Seq != 4 || f.Type != "floor_start" {
		t.Fatalf("live frame = %+v, want seq=4 floor_start", f)
	}
}

func TestWS_NoSinceDoesNotResync(t *testing.T) {
	initTestDB(t)
	room := seedWSRoom(t)
	hubFor(room.sessionID).Publish("message_new", map[string]any{"id": 1})
	srv := wsTestServer(t, room.memberID)

	// 首次连接没有游标：不是重连，没有 resync，缓冲内事件原样补发。
	conn := wsDial(t, srv, room.sessionID, "")
	wsReadHello(t, conn)
	if f := wsRead(t, conn); f.Seq != 1 || f.Type != "message_new" {
		t.Fatalf("frame = %+v, want replayed seq=1", f)
	}
}

func TestWS_EpochMismatchSendsResyncWithoutReplay(t *testing.T) {
	initTestDB(t)
	room := seedWSRoom(t)
	hub := hubFor(room.sessionID)
	hub.Publish("message_new", map[string]any{"id": 1})
	hub.Publish("message_new", map[string]any{"id": 2})
	srv := wsTestServer(t, room.memberID)

	conn := wsDial(t, srv, room.sessionID, "since=1&epoch=stale-epoch")
	hello := wsReadHello(t, conn)
	if hello.Epoch != wsEpoch || hello.Seq != 2 {
		t.Fatalf("hello = %+v, want current epoch and seq=2", hello)
	}
	if f := wsRead(t, conn); f.Type != "resync" || f.Seq != 0 {
		t.Fatalf("second frame = %+v, want resync", f)
	}
	wsExpectQuiet(t, conn) // 不重放 seq=2

	hub.Publish("floor_start", map[string]any{"floor": 1})
	if f := wsRead(t, conn); f.Seq != 3 || f.Type != "floor_start" {
		t.Fatalf("live frame after resync = %+v, want seq=3", f)
	}
}

func TestWS_GapSendsResyncWithoutReplay(t *testing.T) {
	initTestDB(t)
	room := seedWSRoom(t)
	hub := hubFor(room.sessionID)
	for i := 0; i < hubBufferSize+20; i++ {
		hub.Publish("message_new", map[string]any{"id": i})
	}
	srv := wsTestServer(t, room.memberID)

	conn := wsDial(t, srv, room.sessionID, "since=3&epoch="+wsEpoch)
	hello := wsReadHello(t, conn)
	if hello.Seq != hubBufferSize+20 {
		t.Fatalf("hello seq = %d, want %d", hello.Seq, hubBufferSize+20)
	}
	if f := wsRead(t, conn); f.Type != "resync" {
		t.Fatalf("second frame = %+v, want resync", f)
	}
	wsExpectQuiet(t, conn)
}

func TestWS_ClientDisconnectUnsubscribes(t *testing.T) {
	initTestDB(t)
	room := seedWSRoom(t)
	srv := wsTestServer(t, room.memberID)
	hub := hubFor(room.sessionID)

	conn := wsDial(t, srv, room.sessionID, "")
	wsReadHello(t, conn)
	if n := hub.numSubscribers(); n != 1 {
		t.Fatalf("subscribers = %d, want 1", n)
	}

	_ = conn.conn.Close()
	deadline := time.Now().Add(2 * time.Second)
	for hub.numSubscribers() != 0 {
		if time.Now().After(deadline) {
			t.Fatal("subscriber still registered after client disconnect")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestWS_ForeignOriginRejected(t *testing.T) {
	initTestDB(t)
	room := seedWSRoom(t)
	srv := wsTestServer(t, room.memberID)

	header := http.Header{"Origin": []string{"https://evil.example"}}
	if code := wsDialRejected(t, srv, room.sessionID, "", header); code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", code)
	}

	// 同源页面（Origin 的 host 等于请求的 Host）放行。
	header = http.Header{"Origin": []string{srv.URL}}
	raw, _, err := websocket.DefaultDialer.Dial(wsURL(srv, room.sessionID, ""), header)
	if err != nil {
		t.Fatalf("same-origin dial: %v", err)
	}
	wsReadHello(t, newWSClient(t, raw))
}

func TestWSCheckOrigin(t *testing.T) {
	cases := []struct {
		name   string
		origin string
		host   string
		want   bool
	}{
		{"no origin header", "", "game.example.com", true},
		{"same host", "https://game.example.com", "game.example.com", true},
		{"same host with port", "http://localhost:8080", "localhost:8080", true},
		{"host case-insensitive", "https://Game.Example.com", "game.example.com", true},
		{"different host", "https://evil.example", "game.example.com", false},
		{"same host different port", "http://localhost:3000", "localhost:8080", false},
		{"subdomain is not same origin", "https://a.game.example.com", "game.example.com", false},
		{"malformed origin", "://bad", "game.example.com", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			req.Host = tc.host
			if tc.origin != "" {
				req.Header.Set("Origin", tc.origin)
			}
			if got := wsCheckOrigin(req); got != tc.want {
				t.Fatalf("wsCheckOrigin(origin=%q host=%q) = %v, want %v", tc.origin, tc.host, got, tc.want)
			}
		})
	}
}

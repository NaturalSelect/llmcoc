package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/llmcoc/server/internal/models"
	"github.com/llmcoc/server/internal/services/agent"
	"github.com/llmcoc/server/internal/services/llm"
)

func publish(t *testing.T, gctx agent.GameContext, ro agent.RoundOutputs) {
	t.Helper()
	ro.WriterCtx = gctx
	if err := gctx.Publish(ro); err != nil {
		t.Errorf("Publish: %v", err)
	}
}

func hostRouter(t *testing.T, h *SessionHandlers, sessionID uint) *gin.Engine {
	t.Helper()
	return makeTestRouter(h, sessionCreator(t, sessionID), "creator_mp")
}

// ── session start ────────────────────────────────────────────────────────────

func TestStartSession_WritesEmptySpotlightAndNoMessage(t *testing.T) {
	initTestDB(t)
	sessID, ownerID := setupLobbySession(t)
	cardID := seedCard(t, ownerID, "Card")
	models.DB.Create(&models.SessionPlayer{SessionID: sessID, UserID: ownerID, CharacterCardID: cardID})

	w := httptest.NewRecorder()
	sessionCRUDRouter(ownerID, "owner", "user").ServeHTTP(w, jsonReq("POST", "/sessions/"+itoa(sessID)+"/start", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body.String())
	}

	if sp := loadSpotlight(t, sessID); sp == nil || len(sp.UserIDs) != 0 || sp.Stop != "" {
		t.Errorf("spotlight = %+v, want empty (KP holds the floor)", sp)
	}
	if msgs := loadMessages(t, sessID); len(msgs) != 0 {
		t.Fatalf("start must not write any message, got %+v", msgs)
	}
}

// ── nextSpotlight ────────────────────────────────────────────────────────────

func TestNextSpotlight(t *testing.T) {
	t.Run("点名后交给被点名的人", func(t *testing.T) {
		next := nextSpotlight(agent.RunOutput{Ask: &agent.AskResult{UserIDs: []uint{3}, Names: []string{"丙"}, Question: "q"}})
		if len(next.UserIDs) != 1 || next.UserIDs[0] != 3 || next.Question != "q" || next.Stop != "" {
			t.Errorf("got %+v", next)
		}
	})
	t.Run("被强制结束时 KP 持有并记录原因", func(t *testing.T) {
		for _, stop := range []agent.StopReason{agent.StopHostStop, agent.StopLoop, agent.StopStall, agent.StopError, agent.StopContext} {
			next := nextSpotlight(agent.RunOutput{Stop: stop})
			if len(next.UserIDs) != 0 || next.Stop != string(stop) {
				t.Errorf("stop %q: got %+v", stop, next)
			}
		}
	})
	t.Run("既没点名也没有 Stop 时 KP 持有", func(t *testing.T) {
		if next := nextSpotlight(agent.RunOutput{}); len(next.UserIDs) != 0 || next.Stop != "" {
			t.Errorf("got %+v", next)
		}
	})
	t.Run("点名优先于 Stop", func(t *testing.T) {
		next := nextSpotlight(agent.RunOutput{Stop: agent.StopLoop, Ask: &agent.AskResult{UserIDs: []uint{1}}})
		if len(next.UserIDs) != 1 || next.Stop != "" {
			t.Errorf("got %+v", next)
		}
	})
}

// ── KPTurn ───────────────────────────────────────────────────────────────────

func TestKPTurn_OpeningWithNullSpotlight(t *testing.T) {
	initTestDB(t)
	sessionID, _, _ := seedTwoPlayerSession(t, "爱丽丝", "鲍勃") // Spotlight 为 NULL：按 KP 持有处理

	runner := &fakeRunner{run: askRun("q", []uint{1}, []string{"爱丽丝"})}
	h := NewSessionHandlers(runner)
	tap := tapRoom(t, sessionID)

	w := postKPTurn(hostRouter(t, h, sessionID), sessionID)
	if body := decodeChatAccepted(t, w); !body.FloorStarted {
		t.Fatalf("body: %s", w.Body.String())
	}
	tap.waitCount("floor_end", 1)

	got := runner.runCalls()[0]
	if got.KPFloor != agent.KPFloorOpening || len(got.PendingActions) != 0 {
		t.Errorf("opening context: floor=%q pending=%v", got.KPFloor, got.PendingActions)
	}
	if reason := eventField(tap.ofType("floor_start")[0], "reason"); reason != agent.KPFloorOpening {
		t.Errorf("floor_start reason = %v", reason)
	}
}

func TestKPTurn_HostContinueSkipsUnresponsiveAndKeepsSubmitted(t *testing.T) {
	initTestDB(t)
	sessionID, user1ID, user2ID := seedTwoPlayerSession(t, "爱丽丝", "鲍勃")
	setSpotlight(t, sessionID, &models.SpotlightState{
		UserIDs: []uint{user1ID, user2ID}, Names: []string{"爱丽丝", "鲍勃"}, Question: "你们怎么办？", Stop: "loop",
	})
	uid := user1ID
	models.DB.Create(&models.Message{SessionID: sessionID, UserID: &uid, Role: models.MessageRoleUser, Kind: models.MessageKindPlayer, Content: "早先的行动", Username: "爱丽丝"})
	models.DB.Create(&models.SessionTurnAction{SessionID: sessionID, Round: 1, UserID: user1ID, Username: "爱丽丝", ActionSummary: "我推门"})

	runner := &fakeRunner{run: askRun("你看到什么？", []uint{user1ID}, []string{"爱丽丝"})}
	h := NewSessionHandlers(runner)
	tap := tapRoom(t, sessionID)

	decodeChatAccepted(t, postKPTurn(hostRouter(t, h, sessionID), sessionID))
	tap.waitCount("floor_end", 1)

	got := runner.runCalls()[0]
	if got.KPFloor != agent.KPFloorHostContinue || got.LastStop != agent.StopLoop {
		t.Errorf("floor=%q lastStop=%q, want host_continue/loop", got.KPFloor, got.LastStop)
	}
	if len(got.SkippedNames) != 1 || got.SkippedNames[0] != "鲍勃" {
		t.Errorf("SkippedNames = %v, want [鲍勃]", got.SkippedNames)
	}
	if len(got.PendingActions) != 1 || got.PendingActions[0].Content != "我推门" {
		t.Errorf("PendingActions = %+v", got.PendingActions)
	}
	if n := countMessages(sessionID, models.MessageRoleUser); n != 2 {
		t.Errorf("user messages = %d, want the earlier one plus the submitted declaration", n)
	}
}

func TestKPTurn_OnlyHostOrAdmin(t *testing.T) {
	initTestDB(t)
	sessionID, user1ID, _ := seedTwoPlayerSession(t, "爱丽丝", "鲍勃")
	runner := &fakeRunner{}
	h := NewSessionHandlers(runner)

	if w := postKPTurn(makeTestRouter(h, user1ID, "mp_user1"), sessionID); w.Code != http.StatusForbidden {
		t.Fatalf("non-host player got %d, want 403", w.Code)
	}
	if len(runner.runCalls()) != 0 {
		t.Fatal("a rejected request must not start a floor")
	}

	adminID := seedUser(t, "root", models.RoleAdmin, 0, 3)
	runner.run = askRun("q", []uint{user1ID}, []string{"爱丽丝"})
	tap := tapRoom(t, sessionID)
	if w := postKPTurn(roomRouter(h, adminID, "root", "admin"), sessionID); w.Code != http.StatusAccepted {
		t.Fatalf("admin got %d: %s", w.Code, w.Body.String())
	}
	tap.waitCount("floor_end", 1)
}

func TestKPTurn_Rejections(t *testing.T) {
	initTestDB(t)
	sessionID, _, _ := seedTwoPlayerSession(t, "爱丽丝", "鲍勃")
	h := NewSessionHandlers(&fakeRunner{})
	r := hostRouter(t, h, sessionID)

	if w := postKPTurn(r, 9999); w.Code != http.StatusNotFound {
		t.Errorf("missing room got %d, want 404", w.Code)
	}
	if !beginSessionProcessing(sessionID) {
		t.Fatal("beginSessionProcessing failed")
	}
	if w := postKPTurn(r, sessionID); w.Code != http.StatusConflict {
		t.Errorf("while processing got %d, want 409", w.Code)
	}
	finishSessionProcessing(sessionID)

	models.DB.Model(&models.GameSession{}).Where("id = ?", sessionID).Update("status", models.SessionStatusLobby)
	if w := postKPTurn(r, sessionID); w.Code != http.StatusBadRequest {
		t.Errorf("lobby room got %d, want 400", w.Code)
	}
}

// ── floor: multi-round publishing ────────────────────────────────────────────

func TestFloor_PublishesToolEventsBeforeRoundMessages(t *testing.T) {
	initTestDB(t)
	sessionID, user1ID, _ := seedTwoPlayerSession(t, "爱丽丝", "鲍勃")

	from, to := 10, 7
	roundEvents := []models.GameEvent{
		{Type: "dice", Character: "爱丽丝", Skill: "侦查", Roll: 45, Level: "困难"},
		{Type: "dice", Hidden: true},
	}
	statEvents := []models.GameEvent{{Type: "stat", Character: "爱丽丝", Field: "hp", From: &from, To: &to}}
	runner := &fakeRunner{}
	runner.run = func(_ context.Context, gctx agent.GameContext) (agent.RunOutput, error) {
		publish(t, gctx, agent.RoundOutputs{Floor: 1, Round: 1, Events: statEvents})
		publish(t, gctx, agent.RoundOutputs{Floor: 1, Round: 2, Events: roundEvents, Items: []agent.Output{
			{Kind: agent.OutputKP, Text: "你发现了脚印。"},
		}})
		return agent.RunOutput{Messages: 1, Ask: &agent.AskResult{UserIDs: []uint{user1ID}, Names: []string{"爱丽丝"}, Question: "怎么办？"}}, nil
	}
	tap := tapRoom(t, sessionID)

	decodeChatAccepted(t, postKPTurn(hostRouter(t, NewSessionHandlers(runner), sessionID), sessionID))
	tap.waitCount("floor_end", 1)
	tap.waitCount("session", 3)

	// NOTE: 只有属性事件的一轮也要通知刷新人物卡；事件消息排在同轮 kp 之前。
	got := fmt.Sprint(tap.persistentTypes())
	want := "[spotlight floor_start message_new session message_new message_new session spotlight floor_end session]"
	if got != want {
		t.Fatalf("events = %s, want %s", got, want)
	}

	msgs := loadMessages(t, sessionID)
	wantKinds := []models.MessageKind{models.MessageKindEvent, models.MessageKindEvent, models.MessageKindKP}
	if !equalKinds(messageKinds(msgs), wantKinds) {
		t.Fatalf("stored kinds = %v, want %v", messageKinds(msgs), wantKinds)
	}
	for _, m := range msgs[:2] {
		if m.Role != models.MessageRoleAssistant || m.Status != models.MessageStatusDone || m.Floor != 1 || m.Content != "" {
			t.Errorf("event message %d = role %s status %s floor %d content %q", m.ID, m.Role, m.Status, m.Floor, m.Content)
		}
	}
	if meta := msgs[0].Meta.Data; meta == nil || len(meta.Events) != 1 || *meta.Events[0].From != 10 || *meta.Events[0].To != 7 {
		t.Errorf("stat event meta = %+v", meta)
	}
	if meta := msgs[1].Meta.Data; meta == nil || len(meta.Events) != 2 || meta.Events[0].Roll != 45 || meta.Events[1] != (models.GameEvent{Type: "dice", Hidden: true}) {
		t.Errorf("dice event meta = %+v", meta)
	}
}

func TestRoundEventMessage_SkipsRoundsWithoutEvents(t *testing.T) {
	if _, ok := roundEventMessage(1, agent.RoundOutputs{Items: []agent.Output{{Kind: agent.OutputKP, Text: "x"}}}); ok {
		t.Fatal("round without events produced an event message")
	}
	if eventsTouchCards([]models.GameEvent{{Type: "dice"}, {Type: "time"}}) {
		t.Error("dice/time events should not refresh cards")
	}
	if !eventsTouchCards([]models.GameEvent{{Type: "dice"}, {Type: "item"}}) {
		t.Error("item event should refresh cards")
	}
}

func TestFloor_PublishesRoundsAsSeparateMessages(t *testing.T) {
	initTestDB(t)
	sessionID, user1ID, _ := seedTwoPlayerSession(t, "爱丽丝", "鲍勃")

	directorChain := []llm.ChatMessage{
		{Role: "system", Content: "director-system"},
		{Role: "assistant", ToolCalls: []llm.ToolCall{{ID: "call_w1", Name: "write", Arguments: `{}`}}},
		{Role: "tool", ToolCallID: "call_w1", Content: "已发布"},
	}
	runner := &fakeRunner{}
	runner.run = func(_ context.Context, gctx agent.GameContext) (agent.RunOutput, error) {
		publish(t, gctx, agent.RoundOutputs{Floor: 1, Round: 2, Steps: 2, ElapsedMs: 1200, Director: directorChain, Items: []agent.Output{
			{Kind: agent.OutputWriter, Text: "描述走廊", NSFW: true, CallID: "call_w1"},
			{Kind: agent.OutputKP, Text: "你站在走廊里。", TimePoint: "1924-09-03T08:00"},
		}})
		publish(t, gctx, agent.RoundOutputs{Floor: 1, Round: 3, Steps: 1, ElapsedMs: 10, Items: []agent.Output{
			{Kind: agent.OutputWriter, Text: "描述门后"},
			{Kind: agent.OutputAsk, Text: "你要开门吗？", Options: []string{"我推开门"}, Ask: &agent.AskResult{UserIDs: []uint{user1ID}, Names: []string{"爱丽丝"}, Question: "你要开门吗？"}},
		}})
		return agent.RunOutput{Messages: 4, Ask: &agent.AskResult{UserIDs: []uint{user1ID}, Names: []string{"爱丽丝"}, Question: "你要开门吗？"}}, nil
	}
	h := NewSessionHandlers(runner)
	tap := tapRoom(t, sessionID)

	decodeChatAccepted(t, postKPTurn(hostRouter(t, h, sessionID), sessionID))
	tap.waitCount("message_new", 4)
	tap.waitCount("message_update", 2)
	tap.waitCount("floor_end", 1)

	var news []models.MessageKind
	for _, ev := range tap.ofType("message_new") {
		news = append(news, eventMessage(ev).Kind)
	}
	wantKinds := []models.MessageKind{models.MessageKindWriter, models.MessageKindKP, models.MessageKindWriter, models.MessageKindAsk}
	if !equalKinds(news, wantKinds) {
		t.Fatalf("message_new kinds = %v, want %v", news, wantKinds)
	}

	msgs := loadMessages(t, sessionID)
	if !equalKinds(messageKinds(msgs), wantKinds) {
		t.Fatalf("stored kinds = %v, want %v", messageKinds(msgs), wantKinds)
	}
	for _, m := range msgs {
		if m.Floor != 1 || m.Status != models.MessageStatusDone || m.Role != models.MessageRoleAssistant {
			t.Errorf("message %d: floor=%d status=%s role=%s", m.ID, m.Floor, m.Status, m.Role)
		}
	}
	if msgs[0].Content != "正文:描述走廊" || msgs[2].Content != "正文:描述门后" {
		t.Errorf("writer contents = %q, %q", msgs[0].Content, msgs[2].Content)
	}
	if msgs[1].Content != "你站在走廊里。" || strings.Contains(msgs[1].Content, "KP:") {
		t.Errorf("kp content = %q, want the plain reply", msgs[1].Content)
	}
	if meta := msgs[1].Meta.Data; meta == nil || meta.Dice != "" || meta.TimePoint != "1924-09-03T08:00" || len(meta.Options) != 0 {
		t.Errorf("kp meta = %+v", meta)
	}
	if meta := msgs[0].Meta.Data; meta == nil || !meta.NSFW || meta.CallID != "call_w1" {
		t.Errorf("first writer meta = %+v, want nsfw with the write call id", meta)
	}
	if meta := msgs[2].Meta.Data; meta == nil || meta.NSFW || meta.CallID != "" {
		t.Errorf("second writer meta = %+v, want sfw without call id", meta)
	}
	if msgs[0].WriterDirection != "描述走廊" || msgs[1].WriterDirection != "" || msgs[2].WriterDirection != "描述门后" {
		t.Errorf("writer directions = %q, %q, %q", msgs[0].WriterDirection, msgs[1].WriterDirection, msgs[2].WriterDirection)
	}
	if meta := msgs[3].Meta.Data; meta == nil || meta.Ask == nil || len(meta.Ask.UserIDs) != 1 || meta.Ask.Names[0] != "爱丽丝" ||
		len(meta.Options) != 1 || meta.Options[0] != "我推开门" {
		t.Errorf("ask meta = %+v", meta)
	}
	if msgs[1].DirectorSteps == nil || *msgs[1].DirectorSteps != 2 || *msgs[1].DirectorElapsedMs != 1200 {
		t.Errorf("kp director metrics = %v/%v", msgs[1].DirectorSteps, msgs[1].DirectorElapsedMs)
	}
	if msgs[3].DirectorSteps == nil || *msgs[3].DirectorSteps != 1 {
		t.Errorf("ask director steps = %v", msgs[3].DirectorSteps)
	}
	if msgs[0].DirectorSteps != nil {
		t.Error("writer messages must not carry director metrics")
	}

	if got := runner.writerDirections(); len(got) != 2 || got[0] != "描述走廊" || got[1] != "描述门后" {
		t.Errorf("writer directions = %v, want FIFO order", got)
	}
	if runner.maxWriterConcurrency() != 1 {
		t.Errorf("writers ran concurrently: %d", runner.maxWriterConcurrency())
	}
	if flags := runner.nsfwFlags; len(flags) != 2 || !flags[0] || flags[1] {
		t.Errorf("nsfw flags = %v, want [true false]", flags)
	}
	if runner.writerCtxs[0].Session.ID != sessionID {
		t.Error("writer must receive the round's game context")
	}
	if req := runner.writerReqs[0]; req.CallID != "call_w1" || len(req.Director) != len(directorChain) || req.Direction != "描述走廊" {
		t.Errorf("first writer request = %+v, want call id, direction and the director chain snapshot", req)
	}
	if req := runner.writerReqs[1]; len(req.Director) != 0 {
		t.Errorf("second round published no director snapshot, got %d messages", len(req.Director))
	}

	sp := loadSpotlight(t, sessionID)
	if sp == nil || len(sp.UserIDs) != 1 || sp.UserIDs[0] != user1ID {
		t.Errorf("spotlight = %+v", sp)
	}
	if got := eventField(tap.ofType("floor_end")[0], "messages"); got != 4 {
		t.Errorf("floor_end messages = %v, want 4", got)
	}
}

func equalKinds(a, b []models.MessageKind) bool {
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

func TestFloor_BeatWaitsForQueuedWriters(t *testing.T) {
	initTestDB(t)
	sessionID, user1ID, _ := seedTwoPlayerSession(t, "爱丽丝", "鲍勃")

	writerStarted, releaseWriter := make(chan struct{}), make(chan struct{})
	var once sync.Once
	runner := &fakeRunner{}
	runner.writer = func(_ context.Context, _ agent.GameContext, direction string, _ bool, onToken func(string)) (string, error) {
		once.Do(func() { close(writerStarted) })
		<-releaseWriter
		onToken("正文")
		return "正文", nil
	}
	secondRound := make(chan struct{})
	runner.run = func(_ context.Context, gctx agent.GameContext) (agent.RunOutput, error) {
		// NOTE: 只有 writer 的一轮不等，Director 可以接着调用工具。
		publish(t, gctx, agent.RoundOutputs{Floor: 1, Items: []agent.Output{{Kind: agent.OutputWriter, Text: "第一拍"}}})
		close(secondRound)
		publish(t, gctx, agent.RoundOutputs{Floor: 1, Items: []agent.Output{
			{Kind: agent.OutputWriter, Text: "门后"},
			{Kind: agent.OutputAsk, Text: "你要开门吗？", Ask: &agent.AskResult{UserIDs: []uint{user1ID}, Names: []string{"爱丽丝"}}},
		}})
		return agent.RunOutput{Messages: 3, Ask: &agent.AskResult{UserIDs: []uint{user1ID}, Names: []string{"爱丽丝"}}}, nil
	}
	h := NewSessionHandlers(runner)
	tap := tapRoom(t, sessionID)

	decodeChatAccepted(t, postKPTurn(hostRouter(t, h, sessionID), sessionID))
	<-secondRound
	<-writerStarted
	tap.waitCount("message_new", 2)
	// NOTE: 等待期间只有两条 writer 对玩家可见，ask 还没落库。
	tap.waitCount("kp_status", 1)
	if msgs := loadMessages(t, sessionID); len(msgs) != 2 || msgs[0].Kind != models.MessageKindWriter || msgs[1].Kind != models.MessageKindWriter {
		t.Fatalf("before writers finish, stored = %v", messageKinds(msgs))
	}
	if _, running := getSessionProcessing(sessionID); !running {
		t.Error("floor must still hold the room while waiting for writers")
	}

	close(releaseWriter)
	tap.waitCount("floor_end", 1)

	var order []string
	for _, ev := range tap.got {
		switch ev.Type {
		case "message_new":
			order = append(order, "new:"+string(eventMessage(ev).Kind))
		case "message_update":
			order = append(order, "update")
		}
	}
	want := "[new:writer new:writer update update new:ask]"
	if got := fmt.Sprint(order); got != want {
		t.Errorf("event order = %s, want %s", got, want)
	}
	for _, m := range loadMessages(t, sessionID)[:2] {
		if m.Status != models.MessageStatusDone {
			t.Errorf("writer %d status = %s", m.ID, m.Status)
		}
	}
}

func TestFloor_HostStopReleasesBeatWaitingForWriter(t *testing.T) {
	initTestDB(t)
	sessionID, _, _ := seedTwoPlayerSession(t, "爱丽丝", "鲍勃")

	releaseWriter := make(chan struct{})
	runner := &fakeRunner{}
	runner.writer = func(context.Context, agent.GameContext, string, bool, func(string)) (string, error) {
		<-releaseWriter
		return "迟到的正文", nil
	}
	runner.run = func(ctx context.Context, gctx agent.GameContext) (agent.RunOutput, error) {
		publish(t, gctx, agent.RoundOutputs{Floor: 1, Items: []agent.Output{
			{Kind: agent.OutputWriter, Text: "描述"},
			{Kind: agent.OutputKP, Text: "你听见脸步声。"},
		}})
		return agent.RunOutput{Messages: 2, Stop: agent.StopHostStop}, context.Cause(ctx)
	}
	h := NewSessionHandlers(runner)
	tap := tapRoom(t, sessionID)

	decodeChatAccepted(t, postKPTurn(hostRouter(t, h, sessionID), sessionID))
	tap.waitCount("kp_status", 1)
	if w := postKPStop(hostRouter(t, h, sessionID), sessionID); w.Code != http.StatusAccepted {
		t.Fatalf("kp-stop = %d: %s", w.Code, w.Body.String())
	}
	tap.waitCount("floor_end", 1)

	msgs := loadMessages(t, sessionID)
	if len(msgs) != 2 || msgs[0].Status != models.MessageStatusPending || msgs[1].Kind != models.MessageKindKP {
		t.Fatalf("after host stop, stored = %+v", msgs)
	}

	// NOTE: 打断不取消 Writer，它仍然会写完。
	close(releaseWriter)
	tap.waitCount("message_update", 1)
	if m := loadMessages(t, sessionID)[0]; m.Status != models.MessageStatusDone || m.Content != "迟到的正文" {
		t.Errorf("writer after release = %+v", m)
	}
}

func TestFloor_WriterQueueKeepsOrderAcrossRounds(t *testing.T) {
	initTestDB(t)
	sessionID, user1ID, _ := seedTwoPlayerSession(t, "爱丽丝", "鲍勃")

	release := make(chan struct{})
	runner := &fakeRunner{}
	runner.writer = func(_ context.Context, _ agent.GameContext, direction string, _ bool, onToken func(string)) (string, error) {
		<-release
		onToken("[" + direction + "]")
		return "[" + direction + "]", nil
	}
	runner.run = func(_ context.Context, gctx agent.GameContext) (agent.RunOutput, error) {
		for _, d := range []string{"一", "二", "三"} {
			publish(t, gctx, agent.RoundOutputs{Floor: 1, Items: []agent.Output{{Kind: agent.OutputWriter, Text: d}}})
		}
		return agent.RunOutput{Messages: 3, Ask: &agent.AskResult{UserIDs: []uint{user1ID}, Names: []string{"爱丽丝"}}}, nil
	}
	tap := tapRoom(t, sessionID)

	decodeChatAccepted(t, postKPTurn(hostRouter(t, NewSessionHandlers(runner), sessionID), sessionID))
	tap.waitCount("floor_end", 1)
	close(release)
	tap.waitCount("message_update", 3)

	if got := runner.writerDirections(); strings.Join(got, "") != "一二三" {
		t.Errorf("writer order = %v", got)
	}
	var tokens []string
	for _, ev := range tap.ofType("writer_token") {
		tok := ev.Data.(writerTokenEvent)
		if tok.Offset != 0 {
			t.Errorf("token %+v: first flush of a message must start at offset 0", tok)
		}
		tokens = append(tokens, tok.Text)
		if ev.Seq != 0 {
			t.Error("writer_token must be an ephemeral event")
		}
	}
	if strings.Join(tokens, "") != "[一][二][三]" {
		t.Errorf("tokens = %v", tokens)
	}
	for i, m := range loadMessages(t, sessionID) {
		if m.Status != models.MessageStatusDone || m.Content != "["+[]string{"一", "二", "三"}[i]+"]" {
			t.Errorf("message %d = %+v", i, m)
		}
	}
}

// NOTE: Writer 流中途断开重试：reset 广播 writer_reset 并清空已生成正文，之后的 token 从 offset=0 重新开始，新连接补发的也只有重试后的正文。
func TestLiveWriter_ResetRestartsStreamFromOffsetZero(t *testing.T) {
	initTestDB(t)
	sessionID, _, _ := seedTwoPlayerSession(t, "爱丽丝", "鲍勃")
	tap := tapRoom(t, sessionID)

	lw := startLiveWriter(sessionID, 42)
	t.Cleanup(lw.stop)
	lw.append("半截")
	lw.flush()
	lw.resetTo(0)
	if _, ok := writerCatchUp(sessionID); ok {
		t.Error("catch-up after reset must have no text to replay")
	}
	lw.append("完整正文")
	lw.flush()
	if ev, ok := writerCatchUp(sessionID); !ok || ev.Data.(writerTokenEvent).Text != "完整正文" {
		t.Errorf("catch-up after restart = %+v ok=%v, want only the retried text", ev, ok)
	}

	tap.waitCount("writer_token", 2)
	tap.waitCount("writer_reset", 1)
	var seq []string
	for _, ev := range tap.got {
		if ev.Seq != 0 {
			t.Errorf("%s must be an ephemeral event", ev.Type)
		}
		switch ev.Type {
		case "writer_token":
			tok := ev.Data.(writerTokenEvent)
			seq = append(seq, fmt.Sprintf("token@%d:%s", tok.Offset, tok.Text))
		case "writer_reset":
			if got := ev.Data.(writerResetEvent).MessageID; got != 42 {
				t.Errorf("writer_reset message_id = %d, want 42", got)
			}
			seq = append(seq, "reset")
		}
	}
	if got, want := fmt.Sprint(seq), "[token@0:半截 reset token@0:完整正文]"; got != want {
		t.Errorf("stream events = %s, want %s", got, want)
	}
}

func TestFloor_WriterFailureMarksMessageFailed(t *testing.T) {
	initTestDB(t)
	sessionID, user1ID, _ := seedTwoPlayerSession(t, "爱丽丝", "鲍勃")
	runner := &fakeRunner{}
	runner.writer = func(context.Context, agent.GameContext, string, bool, func(string)) (string, error) {
		return "", errors.New("writer provider down")
	}
	runner.run = func(_ context.Context, gctx agent.GameContext) (agent.RunOutput, error) {
		publish(t, gctx, agent.RoundOutputs{Floor: 1, Items: []agent.Output{{Kind: agent.OutputWriter, Text: "描述"}}})
		return agent.RunOutput{Messages: 1, Ask: &agent.AskResult{UserIDs: []uint{user1ID}, Names: []string{"爱丽丝"}}}, nil
	}
	tap := tapRoom(t, sessionID)

	decodeChatAccepted(t, postKPTurn(hostRouter(t, NewSessionHandlers(runner), sessionID), sessionID))
	tap.waitCount("message_update", 1)
	tap.waitCount("floor_end", 1)

	m := loadMessages(t, sessionID)[0]
	if m.Status != models.MessageStatusFailed || m.Content != "" || m.Meta.Data == nil || m.Meta.Data.Error != writerFailedText {
		t.Fatalf("failed writer message = %+v meta=%+v", m, m.Meta.Data)
	}
	update := tap.ofType("message_update")[0].Data.(messageUpdateEvent)
	if update.Status != models.MessageStatusFailed || update.Error != writerFailedText {
		t.Errorf("message_update = %+v", update)
	}
}

func TestFloor_PublishErrorEndsFloorWithError(t *testing.T) {
	initTestDB(t)
	sessionID, _, _ := seedTwoPlayerSession(t, "爱丽丝", "鲍勃")
	var publishErr error
	runner := &fakeRunner{}
	runner.run = func(_ context.Context, gctx agent.GameContext) (agent.RunOutput, error) {
		if err := models.DB.Migrator().DropTable(&models.Message{}); err != nil {
			t.Errorf("drop table: %v", err)
		}
		publishErr = gctx.Publish(agent.RoundOutputs{Floor: 1, WriterCtx: gctx, Items: []agent.Output{{Kind: agent.OutputKP, Text: "无法落库"}}})
		return agent.RunOutput{Stop: agent.StopError}, publishErr
	}
	tap := tapRoom(t, sessionID)

	decodeChatAccepted(t, postKPTurn(hostRouter(t, NewSessionHandlers(runner), sessionID), sessionID))
	tap.waitCount("floor_end", 1)

	if publishErr == nil {
		t.Fatal("Publish must report the save failure to the Director")
	}
	if len(tap.ofType("message_new")) != 0 {
		t.Error("nothing may be broadcast when the save failed")
	}
	if sp := loadSpotlight(t, sessionID); sp == nil || sp.Stop != "error" || len(sp.UserIDs) != 0 {
		t.Errorf("spotlight = %+v", sp)
	}
}

func TestFloor_EndedRoomKeepsSpotlight(t *testing.T) {
	initTestDB(t)
	sessionID, _, _ := seedTwoPlayerSession(t, "爱丽丝", "鲍勃")
	setSpotlight(t, sessionID, &models.SpotlightState{})
	runner := &fakeRunner{}
	runner.run = func(_ context.Context, gctx agent.GameContext) (agent.RunOutput, error) {
		publish(t, gctx, agent.RoundOutputs{Floor: 1, Items: []agent.Output{{Kind: agent.OutputKP, Text: "全剧终。", Ended: true}}})
		models.DB.Model(&models.GameSession{}).Where("id = ?", sessionID).Update("status", models.SessionStatusEnded)
		return agent.RunOutput{Messages: 1, Ended: true}, nil
	}
	tap := tapRoom(t, sessionID)

	decodeChatAccepted(t, postKPTurn(hostRouter(t, NewSessionHandlers(runner), sessionID), sessionID))
	tap.waitCount("floor_end", 1)

	if reason := eventField(tap.ofType("floor_end")[0], "reason"); reason != "end_game" {
		t.Errorf("floor_end reason = %v", reason)
	}
	if len(tap.ofType("spotlight")) != 1 {
		t.Errorf("an ended room must not get a new spotlight event, got %d", len(tap.ofType("spotlight")))
	}
	if meta := loadMessages(t, sessionID)[0].Meta.Data; meta == nil || !meta.Ended {
		t.Errorf("end message meta = %+v", meta)
	}
}

// ── floor: stop, resume, shutdown ────────────────────────────────────────────

func TestKPStop_InterruptsFloorAndKPHolds(t *testing.T) {
	initTestDB(t)
	sessionID, user1ID, _ := seedTwoPlayerSession(t, "爱丽丝", "鲍勃")
	started := make(chan struct{})
	runner := &fakeRunner{}
	runner.run = func(ctx context.Context, gctx agent.GameContext) (agent.RunOutput, error) {
		if gctx.KPFloor != agent.KPFloorOpening {
			return agent.RunOutput{Ask: &agent.AskResult{UserIDs: []uint{user1ID}, Names: []string{"爱丽丝"}}}, nil
		}
		publish(t, gctx, agent.RoundOutputs{Floor: 1, Items: []agent.Output{{Kind: agent.OutputKP, Text: "先说一句。"}}})
		close(started)
		<-ctx.Done()
		if !errors.Is(context.Cause(ctx), agent.ErrHostStop) {
			t.Errorf("cancel cause = %v, want ErrHostStop", context.Cause(ctx))
		}
		return agent.RunOutput{Messages: 1, Stop: agent.StopHostStop}, nil
	}
	h := NewSessionHandlers(runner)
	host := hostRouter(t, h, sessionID)
	tap := tapRoom(t, sessionID)

	decodeChatAccepted(t, postKPTurn(host, sessionID))
	<-started

	if w := postKPStop(makeTestRouter(h, user1ID, "mp_user1"), sessionID); w.Code != http.StatusForbidden {
		t.Fatalf("player kp-stop got %d, want 403", w.Code)
	}
	running := getChatStatus(t, host, sessionID)
	if !running.CanKPStop || running.CanKPContinue || running.Floor == nil || running.Floor.Beats != 1 {
		t.Errorf("host status while running = %+v floor=%+v", running, running.Floor)
	}

	if w := postKPStop(host, sessionID); w.Code != http.StatusAccepted {
		t.Fatalf("kp-stop got %d: %s", w.Code, w.Body.String())
	}
	tap.waitCount("floor_end", 1)

	if reason := eventField(tap.ofType("floor_end")[0], "reason"); reason != "host_stop" {
		t.Errorf("floor_end reason = %v", reason)
	}
	sp := loadSpotlight(t, sessionID)
	if sp == nil || len(sp.UserIDs) != 0 || sp.Stop != "host_stop" {
		t.Fatalf("spotlight = %+v, want KP holding with stop=host_stop", sp)
	}
	if n := len(loadMessages(t, sessionID)); n != 1 {
		t.Errorf("already published messages must survive the interrupt, got %d", n)
	}
	if _, active := activeFloors.m[sessionID]; active {
		t.Error("the floor must be unregistered after it ends")
	}
	if w := postKPStop(host, sessionID); w.Code != http.StatusConflict {
		t.Errorf("kp-stop with no floor got %d, want 409", w.Code)
	}
	if w := postKPStop(host, 9999); w.Code != http.StatusNotFound {
		t.Errorf("kp-stop on a missing room got %d, want 404", w.Code)
	}

	paused := getChatStatus(t, host, sessionID)
	if paused.Phase != "kp_floor" || paused.Spotlight.Stop != "host_stop" || !paused.CanKPContinue {
		t.Errorf("paused status = %+v", paused)
	}
	decodeChatAccepted(t, postKPTurn(host, sessionID))
	tap.waitCount("floor_end", 2)
	if got := runner.runCalls()[1]; got.KPFloor != agent.KPFloorHostContinue || got.LastStop != agent.StopHostStop {
		t.Errorf("continue after stop: floor=%q lastStop=%q", got.KPFloor, got.LastStop)
	}
}

func TestFloor_ContextFullResumesWithNewFloor(t *testing.T) {
	initTestDB(t)
	sessionID, user1ID, _ := seedTwoPlayerSession(t, "爱丽丝", "鲍勃")
	runner := &fakeRunner{}
	runner.run = func(_ context.Context, gctx agent.GameContext) (agent.RunOutput, error) {
		if gctx.KPFloor == agent.KPFloorResume {
			publish(t, gctx, agent.RoundOutputs{Floor: 2, Items: []agent.Output{{Kind: agent.OutputKP, Text: "续写。"}}})
			return agent.RunOutput{Messages: 1, Ask: &agent.AskResult{UserIDs: []uint{user1ID}, Names: []string{"爱丽丝"}}}, nil
		}
		publish(t, gctx, agent.RoundOutputs{Floor: 1, Items: []agent.Output{{Kind: agent.OutputKP, Text: "开场。"}}})
		return agent.RunOutput{Messages: 1, Stop: agent.StopContext}, nil
	}
	tap := tapRoom(t, sessionID)

	decodeChatAccepted(t, postKPTurn(hostRouter(t, NewSessionHandlers(runner), sessionID), sessionID))
	tap.waitCount("floor_end", 2)

	if runs := runner.runCalls(); len(runs) != 2 || runs[1].KPFloor != agent.KPFloorResume {
		t.Fatalf("runs = %d, second floor = %q", len(runs), runs[len(runs)-1].KPFloor)
	}
	var reasons, ends []any
	for _, ev := range tap.ofType("floor_start") {
		reasons = append(reasons, eventField(ev, "reason"))
	}
	for _, ev := range tap.ofType("floor_end") {
		ends = append(ends, eventField(ev, "reason"))
	}
	if len(reasons) != 2 || reasons[0] != agent.KPFloorOpening || reasons[1] != agent.KPFloorResume {
		t.Errorf("floor_start reasons = %v", reasons)
	}
	if len(ends) != 2 || ends[0] != "context" || ends[1] != "ask" {
		t.Errorf("floor_end reasons = %v", ends)
	}
	if len(tap.ofType("spotlight")) != 2 {
		t.Errorf("only the first start and the final ask may change the spotlight, got %d events", len(tap.ofType("spotlight")))
	}
	if sp := loadSpotlight(t, sessionID); sp == nil || len(sp.UserIDs) != 1 || sp.Stop != "" {
		t.Errorf("final spotlight = %+v", sp)
	}
}

func TestFloor_ResumeThatMakesNoProgressDoesNotLoop(t *testing.T) {
	initTestDB(t)
	sessionID, _, _ := seedTwoPlayerSession(t, "爱丽丝", "鲍勃")
	runner := &fakeRunner{run: func(context.Context, agent.GameContext) (agent.RunOutput, error) {
		return agent.RunOutput{Stop: agent.StopContext}, nil
	}}
	tap := tapRoom(t, sessionID)

	decodeChatAccepted(t, postKPTurn(hostRouter(t, NewSessionHandlers(runner), sessionID), sessionID))
	tap.waitCount("floor_end", 2)

	if n := len(runner.runCalls()); n != 2 {
		t.Fatalf("Run called %d times, want the opening plus exactly one resume", n)
	}
	if sp := loadSpotlight(t, sessionID); sp == nil || sp.Stop != "context" || len(sp.UserIDs) != 0 {
		t.Errorf("spotlight = %+v, want KP holding with stop=context", sp)
	}
}

func TestStopAllFloors_CancelsAndWaitsForFloors(t *testing.T) {
	initTestDB(t)
	sessionID, _, _ := seedTwoPlayerSession(t, "爱丽丝", "鲍勃")
	started := make(chan struct{})
	var cause error
	runner := &fakeRunner{}
	runner.run = func(ctx context.Context, _ agent.GameContext) (agent.RunOutput, error) {
		close(started)
		<-ctx.Done()
		cause = context.Cause(ctx)
		return agent.RunOutput{Stop: agent.StopError}, errors.New("cancelled")
	}
	tap := tapRoom(t, sessionID)

	decodeChatAccepted(t, postKPTurn(hostRouter(t, NewSessionHandlers(runner), sessionID), sessionID))
	<-started

	done := make(chan struct{})
	go func() {
		StopAllFloors()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("StopAllFloors did not return")
	}

	if !errors.Is(cause, errServerShutdown) {
		t.Errorf("cancel cause = %v, want errServerShutdown", cause)
	}
	if sp := loadSpotlight(t, sessionID); sp == nil || sp.Stop != "error" {
		t.Errorf("spotlight = %+v, want stop=error", sp)
	}
	tap.waitCount("floor_end", 1)
	if _, running := getSessionProcessing(sessionID); running {
		t.Error("processing must be released")
	}
}

// ── message responses ────────────────────────────────────────────────────────

func TestGetMessages_ReturnsKindStatusFloorAndMeta(t *testing.T) {
	initTestDB(t)
	sessionID, userID := seedPlayingSession(t)
	models.DB.Create(&models.Message{
		SessionID: sessionID, Role: models.MessageRoleAssistant, Kind: models.MessageKindKP, Status: models.MessageStatusDone,
		Floor: 3, Content: "门开了。", Username: "KP",
		Meta: models.JSONField[*models.MessageMeta]{Data: &models.MessageMeta{Options: []string{"进去"}, Dice: "侦查 成功"}},
	})
	models.DB.Create(&models.Message{
		SessionID: sessionID, Role: models.MessageRoleAssistant, Kind: models.MessageKindWriter, Status: models.MessageStatusPending,
		Floor: 3, Content: "", Username: "KP", WriterDirection: "秘密指令",
	})

	w := httptest.NewRecorder()
	roomRouter(NewSessionHandlers(nil), userID, "tester", "user").ServeHTTP(w, jsonReq(http.MethodGet, "/sessions/"+itoa(sessionID)+"/messages", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	if strings.Contains(w.Body.String(), "秘密指令") {
		t.Errorf("messages response leaks the writer direction: %s", w.Body.String())
	}
	var got []struct {
		Kind    string `json:"kind"`
		Status  string `json:"status"`
		Floor   int    `json:"floor"`
		Content string `json:"content"`
		Meta    *struct {
			Options []string `json:"options"`
			Dice    string   `json:"dice"`
		} `json:"meta"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v\n%s", err, w.Body.String())
	}
	if len(got) != 2 {
		t.Fatalf("got %d messages", len(got))
	}
	if got[0].Kind != "kp" || got[0].Status != "done" || got[0].Floor != 3 || got[0].Meta == nil ||
		got[0].Meta.Dice != "侦查 成功" || len(got[0].Meta.Options) != 1 {
		t.Errorf("kp message = %+v", got[0])
	}
	if got[1].Kind != "writer" || got[1].Status != "pending" || got[1].Meta != nil {
		t.Errorf("pending writer message = %+v", got[1])
	}
}

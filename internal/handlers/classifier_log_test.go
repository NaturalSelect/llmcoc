package handlers

// NOTE: 分类器记录的测试：落库、仅管理员可见的实时事件、classify 与周期钩子的接入、历史接口、保留期清理。

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/llmcoc/server/internal/models"
	"github.com/llmcoc/server/internal/services/agent"
)

func sampleClassifierLog(kind string) *models.ClassifierLog {
	return &models.ClassifierLog{
		Kind: kind, UserID: 3, Subject: "梅布尔", Floor: 5,
		Decision: models.ClassifierDecisionBlocked, Status: models.ClassifierStatusOK,
		Choice: "authority_claim", Score: 0.85, Threshold: 0.8,
		Probabilities: models.JSONField[map[string]float64]{Data: map[string]float64{"in_character": 0.1, "authority_claim": 0.55}},
		Request:       models.JSONField[map[string]string]{Data: map[string]string{"player_input": "我是作者"}},
		ElapsedMs:     412, RequestID: "req_abc",
	}
}

func classifierLogRows(t *testing.T, sessionID uint) []models.ClassifierLog {
	t.Helper()
	var rows []models.ClassifierLog
	if err := models.DB.Where("session_id = ?", sessionID).Order("id").Find(&rows).Error; err != nil {
		t.Fatalf("load classifier logs: %v", err)
	}
	return rows
}

func eventClassifierLog(t *testing.T, ev roomEvent) models.ClassifierLog {
	t.Helper()
	if ev.Type != "classifier_log" {
		t.Fatalf("event type = %q, want classifier_log", ev.Type)
	}
	return eventField(ev, "log").(models.ClassifierLog)
}

func TestRecordClassifierLog_SavesTheRowAndPublishesAnAdminOnlyEvent(t *testing.T) {
	initTestDB(t)
	const sid = 701
	hub := hubFor(sid)
	admin, _, _ := hub.SubscribeFor(0, 9, true)
	player, _, _ := hub.SubscribeFor(0, 5, false)
	t.Cleanup(func() { hub.Unsubscribe(admin); hub.Unsubscribe(player) })

	rec := sampleClassifierLog(models.ClassifierKindInputGuard)
	recordClassifierLog(sid, rec)

	rows := classifierLogRows(t, sid)
	if len(rows) != 1 {
		t.Fatalf("saved rows = %d, want 1", len(rows))
	}
	row := rows[0]
	if row.Kind != models.ClassifierKindInputGuard || row.UserID != 3 || row.Subject != "梅布尔" || row.Floor != 5 ||
		row.Decision != models.ClassifierDecisionBlocked || row.Score != 0.85 || row.Threshold != 0.8 || row.RequestID != "req_abc" || row.ElapsedMs != 412 {
		t.Errorf("saved row = %+v", row)
	}
	if row.Probabilities.Data["authority_claim"] != 0.55 || row.Request.Data["player_input"] != "我是作者" {
		t.Errorf("probabilities/request did not round-trip: %v / %v", row.Probabilities.Data, row.Request.Data)
	}

	select {
	case ev := <-admin.ch:
		if got := eventClassifierLog(t, ev); got.ID != row.ID || got.Kind != models.ClassifierKindInputGuard {
			t.Errorf("admin event log = %+v, want the saved row %d", got, row.ID)
		}
	default:
		t.Fatal("admin subscriber did not receive the classifier_log event")
	}
	if n := len(player.ch); n != 0 {
		t.Errorf("a non-admin player received %d events, want none", n)
	}

	// NOTE: 临时事件不进缓冲，断线补发里谁都没有；管理员靠历史接口补拉。
	_, playerReplay, _ := hub.SubscribeFor(0, 5, false)
	_, adminReplay, _ := hub.SubscribeFor(0, 9, true)
	if len(playerReplay) != 0 || len(adminReplay) != 0 {
		t.Errorf("replay sizes player=%d admin=%d, want 0 and 0", len(playerReplay), len(adminReplay))
	}
}

// 回归：用持久事件发布时，非管理员的 seq 会跳号，客户端把跳号当成丢帧而整体重连。
func TestRecordClassifierLog_LeavesPlayersSeqContiguous(t *testing.T) {
	initTestDB(t)
	const sid = 704
	hub := hubFor(sid)
	admin, _, _ := hub.SubscribeFor(0, 9, true)
	player, _, _ := hub.SubscribeFor(0, 5, false)
	t.Cleanup(func() { hub.Unsubscribe(admin); hub.Unsubscribe(player) })

	hub.Publish("floor_start", gin.H{"floor": 1})
	recordClassifierLog(sid, sampleClassifierLog(models.ClassifierKindNSFWRoute))
	hub.Publish("floor_end", gin.H{"floor": 1})

	if got := hub.Seq(); got != 2 {
		t.Fatalf("hub seq = %d, want 2: the classifier event must not take a seq", got)
	}
	var seqs []uint64
	for len(player.ch) > 0 {
		seqs = append(seqs, (<-player.ch).Seq)
	}
	if !slices.Equal(seqs, []uint64{1, 2}) {
		t.Errorf("player saw seqs %v, want the contiguous [1 2]", seqs)
	}
	var adminTypes []string
	for len(admin.ch) > 0 {
		adminTypes = append(adminTypes, (<-admin.ch).Type)
	}
	if !slices.Equal(adminTypes, []string{"floor_start", "classifier_log", "floor_end"}) {
		t.Errorf("admin saw %v, want all three events in order", adminTypes)
	}
}

func TestRecordClassifierLog_EventJSONMatchesTheFrontendContract(t *testing.T) {
	initTestDB(t)
	const sid = 702
	hub := hubFor(sid)
	admin, _, _ := hub.SubscribeFor(0, 9, true)
	t.Cleanup(func() { hub.Unsubscribe(admin) })

	// NOTE: 调用失败的记录没有概率和请求：对外必须是空对象而不是 null。
	recordClassifierLog(sid, &models.ClassifierLog{Kind: models.ClassifierKindNSFWRoute, Decision: models.ClassifierDecisionFailed, Status: models.ClassifierStatusError, Error: "HTTP 401"})

	ev := <-admin.ch
	raw, err := json.Marshal(ev.Data)
	if err != nil {
		t.Fatalf("marshal event data: %v", err)
	}
	var data map[string]map[string]json.RawMessage
	if err := json.Unmarshal(raw, &data); err != nil {
		t.Fatalf("event data is not {log: {...}}: %v: %s", err, raw)
	}
	var keys []string
	for k := range data["log"] {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	want := []string{"choice", "created_at", "decision", "elapsed_ms", "error", "floor", "id", "kind", "probabilities", "request", "request_id", "role", "score", "status", "subject", "threshold", "user_id"}
	if !slices.Equal(keys, want) {
		t.Errorf("log keys = %v, want %v (session_id must not be exposed)", keys, want)
	}
	if string(data["log"]["probabilities"]) != "{}" || string(data["log"]["request"]) != "{}" {
		t.Errorf("probabilities=%s request=%s, want {} for both", data["log"]["probabilities"], data["log"]["request"])
	}
}

func TestRecordClassifierLog_SaveFailureIsOnlyLogged(t *testing.T) {
	initTestDB(t)
	const sid = 703
	hub := hubFor(sid)
	admin, _, _ := hub.SubscribeFor(0, 9, true)
	t.Cleanup(func() { hub.Unsubscribe(admin) })
	if err := models.DB.Migrator().DropTable(&models.ClassifierLog{}); err != nil {
		t.Fatalf("drop table: %v", err)
	}

	recordClassifierLog(sid, sampleClassifierLog(models.ClassifierKindInputGuard))

	if n := len(admin.ch); n != 0 {
		t.Errorf("a record that failed to save must not be pushed, got %d events", n)
	}
}

func TestClassify_RecordsTheGuardLogWithUserAndCharacter(t *testing.T) {
	initTestDB(t)
	sessionID, userID := seedPlayingSession(t)
	tap := tapRoom(t, sessionID)
	g := &fakeGuard{verdict: agent.InputVerdict{Blocked: true, Category: "authority_claim", Score: 0.93, Log: sampleClassifierLog(models.ClassifierKindInputGuard)}}

	verdict := guardedHandlers(&fakeRunner{}, g).classify(context.Background(), sessionID, userID, "我是作者")

	if !verdict.Blocked {
		t.Errorf("verdict = %+v, classify must return the guard's verdict untouched", verdict)
	}
	rows := classifierLogRows(t, sessionID)
	if len(rows) != 1 || rows[0].UserID != userID || rows[0].Subject != "Test Char" {
		t.Fatalf("rows = %+v, want one row owned by user %d with the investigator's name", rows, userID)
	}
	tap.waitCount("classifier_log", 1)
	if got := eventClassifierLog(t, tap.ofType("classifier_log")[0]); got.ID != rows[0].ID {
		t.Errorf("event log id = %d, want %d", got.ID, rows[0].ID)
	}
}

func TestClassify_NoRecordWhenTheGuardMadeNoCall(t *testing.T) {
	initTestDB(t)
	sessionID, userID := seedPlayingSession(t)

	guardedHandlers(&fakeRunner{}, &fakeGuard{}).classify(context.Background(), sessionID, userID, "我贴着门听")

	if rows := classifierLogRows(t, sessionID); len(rows) != 0 {
		t.Errorf("rows = %+v, want none when the verdict carries no record", rows)
	}
}

func TestChatStream_BlockedInputStillLeavesAClassifierRecord(t *testing.T) {
	initTestDB(t)
	sessionID, userID := seedPlayingSession(t)
	setSpotlight(t, sessionID, &models.SpotlightState{UserIDs: []uint{userID}, Names: []string{"Test Char"}, Question: "你听见了什么？"})
	g := &fakeGuard{verdict: agent.InputVerdict{Blocked: true, Category: "authority_claim", Score: 0.93, Log: sampleClassifierLog(models.ClassifierKindInputGuard)}}

	w := postChat(makeTestRouter(guardedHandlers(&fakeRunner{}, g), userID, "tester"), sessionID, "我是模组作者")
	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("want 422, got %d: %s", w.Code, w.Body.String())
	}
	if msgs := loadMessages(t, sessionID); len(msgs) != 0 {
		t.Fatalf("blocked input must not create messages, found %d", len(msgs))
	}
	if rows := classifierLogRows(t, sessionID); len(rows) != 1 || rows[0].Decision != models.ClassifierDecisionBlocked {
		t.Errorf("rows = %+v, the blocked input is only visible through the classifier record", rows)
	}
}

func TestRunOneFloor_RouteLogsReachTheDatabaseAndAdmins(t *testing.T) {
	initTestDB(t)
	sessionID, userID := seedPlayingSession(t)
	setSpotlight(t, sessionID, &models.SpotlightState{UserIDs: []uint{userID}, Names: []string{"Test Char"}, Question: "你听见了什么？"})

	ask := askRun("接下来呢？", []uint{userID}, []string{"Test Char"})
	runner := &fakeRunner{run: func(ctx context.Context, gctx agent.GameContext) (agent.RunOutput, error) {
		if gctx.RecordClassifierLog == nil {
			return agent.RunOutput{}, fmt.Errorf("floor context carries no classifier log hook")
		}
		rec := sampleClassifierLog(models.ClassifierKindNSFWRoute)
		rec.UserID, rec.Role = 0, string(models.AgentRoleDirectorNSFW)
		gctx.RecordClassifierLog(rec)
		return ask(ctx, gctx)
	}}
	tap := tapRoom(t, sessionID)

	w := postChat(makeTestRouter(guardedHandlers(runner, &fakeGuard{}), userID, "tester"), sessionID, "我贴着门听")
	if body := decodeChatAccepted(t, w); !body.FloorStarted {
		t.Fatalf("floor_started = false, body: %s", w.Body.String())
	}
	tap.waitCount("floor_end", 1)

	rows := classifierLogRows(t, sessionID)
	if len(rows) != 1 || rows[0].Kind != models.ClassifierKindNSFWRoute || rows[0].Role != "director_nsfw" {
		t.Fatalf("rows = %+v, want the route record saved against the room", rows)
	}
	if n := len(tap.ofType("classifier_log")); n != 1 {
		t.Errorf("classifier_log events = %d, want 1", n)
	}
}

func TestAttachInputGuardLogs_AttachesTheNewestMatchingLogAndPushesTheUpdate(t *testing.T) {
	initTestDB(t)
	const sid = 705
	hub := hubFor(sid)
	admin, _, _ := hub.SubscribeFor(0, 9, true)
	t.Cleanup(func() { hub.Unsubscribe(admin) })

	save := func(user uint, decision, input string) uint {
		rec := &models.ClassifierLog{
			Kind: models.ClassifierKindInputGuard, UserID: user, Decision: decision, Status: models.ClassifierStatusOK,
			Request: models.JSONField[map[string]string]{Data: map[string]string{"player_input": input}},
		}
		recordClassifierLog(sid, rec)
		return rec.ID
	}
	superseded := save(3, models.ClassifierDecisionPassed, "我贴着门听")
	blocked := save(3, models.ClassifierDecisionBlocked, "我贴着门听")
	accepted := save(3, models.ClassifierDecisionPassed, "我贴着门听")
	otherUser := save(4, models.ClassifierDecisionPassed, "我贴着门听")
	otherText := save(3, models.ClassifierDecisionPassed, "我换一句")
	for len(admin.ch) > 0 {
		<-admin.ch
	}

	attachInputGuardLogs(sid, 6, []models.SessionTurnAction{{UserID: 3, ActionSummary: "我贴着门听"}})

	floors := map[uint]int{}
	for _, row := range classifierLogRows(t, sid) {
		floors[row.ID] = row.Floor
	}
	if floors[accepted] != 6 {
		t.Errorf("accepted log floor = %d, want 6", floors[accepted])
	}
	for name, id := range map[string]uint{"superseded": superseded, "blocked": blocked, "other user": otherUser, "other text": otherText} {
		if floors[id] != 0 {
			t.Errorf("%s log floor = %d, want it left at 0", name, floors[id])
		}
	}
	if len(admin.ch) != 1 {
		t.Fatalf("admin events = %d, want exactly the one update", len(admin.ch))
	}
	if got := eventClassifierLog(t, <-admin.ch); got.ID != accepted || got.Floor != 6 {
		t.Errorf("pushed log = id %d floor %d, want id %d floor 6 (same id, so the client updates in place)", got.ID, got.Floor, accepted)
	}
}

func TestChatStream_AttachesTheInputLogToTheFloorItStarted(t *testing.T) {
	initTestDB(t)
	sessionID, userID := seedPlayingSession(t)
	setSpotlight(t, sessionID, &models.SpotlightState{UserIDs: []uint{userID}, Names: []string{"Test Char"}, Question: "你听见了什么？"})
	runner := &fakeRunner{run: askRun("接下来呢？", []uint{userID}, []string{"Test Char"})}
	tap := tapRoom(t, sessionID)
	rec := &models.ClassifierLog{
		Kind: models.ClassifierKindInputGuard, Decision: models.ClassifierDecisionPassed, Status: models.ClassifierStatusOK,
		Request: models.JSONField[map[string]string]{Data: map[string]string{"player_input": "我贴着门听"}},
	}
	g := &fakeGuard{verdict: agent.InputVerdict{Log: rec}}

	w := postChat(makeTestRouter(guardedHandlers(runner, g), userID, "tester"), sessionID, "我贴着门听")
	if body := decodeChatAccepted(t, w); !body.FloorStarted {
		t.Fatalf("floor_started = false, body: %s", w.Body.String())
	}
	tap.waitCount("floor_end", 1)

	var playerFloor int
	for _, m := range loadMessages(t, sessionID) {
		if m.Kind == models.MessageKindPlayer {
			playerFloor = m.Floor
		}
	}
	rows := classifierLogRows(t, sessionID)
	if len(rows) != 1 || playerFloor == 0 || rows[0].Floor != playerFloor {
		t.Fatalf("rows = %+v, player message floor = %d, want the record's floor to equal the message's", rows, playerFloor)
	}
	events := tap.ofType("classifier_log")
	if len(events) != 2 {
		t.Fatalf("classifier_log events = %d, want 2: the original (floor 0) and the update", len(events))
	}
	if first, last := eventClassifierLog(t, events[0]), eventClassifierLog(t, events[1]); first.ID != last.ID || first.Floor != 0 || last.Floor != playerFloor {
		t.Errorf("events = %+v then %+v, want the same id going from floor 0 to %d", first, last, playerFloor)
	}
}

func classifierLogsRouter() *gin.Engine {
	r := gin.New()
	admin := r.Group("/admin", withAuth(1, "admin", "admin"))
	admin.GET("/sessions/:id/classifier-logs", AdminListClassifierLogs)
	return r
}

func getClassifierLogs(t *testing.T, path string) (*httptest.ResponseRecorder, []models.ClassifierLog) {
	t.Helper()
	w := httptest.NewRecorder()
	classifierLogsRouter().ServeHTTP(w, jsonReq("GET", path, nil))
	var body struct {
		Logs []models.ClassifierLog `json:"logs"`
	}
	if w.Code == http.StatusOK {
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
			t.Fatalf("decode %s: %v: %s", path, err, w.Body.String())
		}
	}
	return w, body.Logs
}

func TestAdminListClassifierLogs_ReturnsTheLatestOnesOldestFirstForOneSession(t *testing.T) {
	initTestDB(t)
	// NOTE: 501 条：足够验证默认 200、上限 500 和 limit 解析。
	rows := make([]models.ClassifierLog, 0, 502)
	for i := 1; i <= 501; i++ {
		rows = append(rows, models.ClassifierLog{SessionID: 11, Kind: models.ClassifierKindInputGuard, Floor: i})
	}
	rows = append(rows, models.ClassifierLog{SessionID: 12, Kind: models.ClassifierKindInputGuard, Floor: 9999})
	if err := models.DB.CreateInBatches(&rows, 100).Error; err != nil {
		t.Fatalf("seed logs: %v", err)
	}

	cases := []struct {
		query     string
		wantCount int
	}{
		{"", 200},
		{"?limit=2", 2},
		{"?limit=9999", 500},
		{"?limit=abc", 200},
		{"?limit=0", 200},
		{"?limit=-5", 200},
	}
	for _, tc := range cases {
		w, logs := getClassifierLogs(t, "/admin/sessions/11/classifier-logs"+tc.query)
		if w.Code != http.StatusOK || len(logs) != tc.wantCount {
			t.Fatalf("query %q: status=%d logs=%d, want 200 and %d", tc.query, w.Code, len(logs), tc.wantCount)
		}
		if last := logs[len(logs)-1]; last.Floor != 501 {
			t.Errorf("query %q: newest log floor = %d, want 501 (the latest entries are kept)", tc.query, last.Floor)
		}
		if !slices.IsSortedFunc(logs, func(a, b models.ClassifierLog) int { return int(a.ID) - int(b.ID) }) {
			t.Errorf("query %q: logs are not ascending by id", tc.query)
		}
		for _, l := range logs {
			if l.Floor == 9999 {
				t.Fatalf("query %q: leaked a log from another session", tc.query)
			}
		}
	}
}

func TestAdminListClassifierLogs_EmptyAndInvalidID(t *testing.T) {
	initTestDB(t)

	w, logs := getClassifierLogs(t, "/admin/sessions/404/classifier-logs")
	if w.Code != http.StatusOK || len(logs) != 0 || !json.Valid(w.Body.Bytes()) {
		t.Fatalf("status=%d body=%s, want 200 with an empty list", w.Code, w.Body.String())
	}
	if got := w.Body.String(); got != `{"logs":[]}` {
		t.Errorf("body = %s, want an empty array rather than null", got)
	}

	if w, _ := getClassifierLogs(t, "/admin/sessions/abc/classifier-logs"); w.Code != http.StatusBadRequest {
		t.Errorf("invalid id: status = %d, want 400", w.Code)
	}
}

func TestPurgeExpiredClassifierLogs_DropsOnlyRowsPastRetention(t *testing.T) {
	initTestDB(t)
	now := time.Now()
	for age, floor := range map[time.Duration]int{15 * 24 * time.Hour: 1, 13 * 24 * time.Hour: 2, time.Minute: 3} {
		rec := models.ClassifierLog{SessionID: 21, Kind: models.ClassifierKindNSFWRoute, Floor: floor, CreatedAt: now.Add(-age)}
		if err := models.DB.Create(&rec).Error; err != nil {
			t.Fatalf("seed log: %v", err)
		}
	}

	n, err := purgeExpiredClassifierLogs(now)
	if err != nil || n != 1 {
		t.Fatalf("purged = %d, err = %v, want 1", n, err)
	}
	var floors []int
	for _, l := range classifierLogRows(t, 21) {
		floors = append(floors, l.Floor)
	}
	if !slices.Equal(floors, []int{2, 3}) && !slices.Equal(floors, []int{3, 2}) {
		t.Errorf("remaining floors = %v, want only the 13-day-old and fresh rows", floors)
	}
}

func TestPurgeOnce_RemovesExpiredClassifierLogsOfLiveRooms(t *testing.T) {
	f := newScenarioPurgeFixture(t)
	live := f.seedPurgeSession(t, models.SessionStatusPlaying, time.Hour)
	old := models.ClassifierLog{SessionID: live, Kind: models.ClassifierKindInputGuard, CreatedAt: time.Now().Add(-20 * 24 * time.Hour)}
	if err := models.DB.Create(&old).Error; err != nil {
		t.Fatalf("seed old log: %v", err)
	}
	var before int64
	models.DB.Model(&models.ClassifierLog{}).Where("session_id = ?", live).Count(&before)

	purgeOnce(time.Now())

	var after int64
	models.DB.Model(&models.ClassifierLog{}).Where("session_id = ?", live).Count(&after)
	if before != 2 || after != 1 {
		t.Errorf("logs before=%d after=%d, want 2 then 1: only the expired record goes, the room stays", before, after)
	}
	if !sessionExists(t, live) {
		t.Error("a playing room must not be purged")
	}
}

package handlers

// NOTE: 直接调用不依赖 gin 的核心函数（submitTurn、joinSessionPlayer、postOOC/saveOOC、createNote/saveNote、
// buildHandbook）的测试。它们是 AI 陪玩在后台协程里调用的入口，真人处理器走同一批函数，
// 处理器层面的行为由 session_test.go、session_table_test.go、session_crud_test.go 守着。
// 每个启动了周期的测试都要等到 floor_end 再返回。

import (
	"net/http"
	"strings"
	"testing"

	"github.com/llmcoc/server/internal/models"
	"github.com/llmcoc/server/internal/services/agent"
)

// drainTap 非阻塞地取走订阅队列里已有的事件：Publish 同步入队，核心函数返回时事件已经在队列里。
func drainTap(tap *roomTap) {
	for {
		select {
		case ev := <-tap.sub.ch:
			tap.got = append(tap.got, ev)
		default:
			return
		}
	}
}

// ── submitTurn ───────────────────────────────────────────────────────────────

func TestSubmitTurn_NotNamedReturnsNotAsked(t *testing.T) {
	initTestDB(t)
	resetHubs(t)
	sessionID, user1ID, user2ID := seedTwoPlayerSession(t, "爱丽丝", "鲍勃")
	runner := &fakeRunner{}
	h := NewSessionHandlers(runner)

	// 点名了别人：403，notAsked。
	setSpotlight(t, sessionID, &models.SpotlightState{UserIDs: []uint{user2ID}, Names: []string{"鲍勃"}, Question: "你打算怎么开门？"})
	started, _, terr := h.submitTurn(sessionID, user1ID, "", "我抢着说话")
	if started || terr == nil {
		t.Fatalf("started=%v terr=%v, want a rejection", started, terr)
	}
	if !terr.notAsked || terr.status != http.StatusForbidden ||
		!strings.Contains(terr.msg, "KP 正在等待 鲍勃 回应") || !strings.Contains(terr.msg, "你打算怎么开门？") {
		t.Errorf("terr = %+v, want notAsked 403 with the asked question", terr)
	}

	// KP 持有发言权：409，同样是 notAsked。
	setSpotlight(t, sessionID, &models.SpotlightState{})
	_, _, terr = h.submitTurn(sessionID, user1ID, "", "我再试一次")
	if terr == nil || !terr.notAsked || terr.status != http.StatusConflict || !strings.Contains(terr.msg, "KP 正在推进剧情") {
		t.Errorf("terr = %+v, want notAsked 409", terr)
	}

	if len(runner.runCalls()) != 0 || countTurnActions(t, sessionID) != 0 || countMessages(sessionID, models.MessageRoleUser) != 0 {
		t.Error("a rejected submit must not start a floor or persist anything")
	}
}

func TestSubmitTurn_OtherErrorsAreNotNotAsked(t *testing.T) {
	initTestDB(t)
	resetHubs(t)
	h := NewSessionHandlers(&fakeRunner{})

	if _, _, terr := h.submitTurn(9999, 1, "", "hi"); terr == nil || terr.status != http.StatusNotFound || terr.notAsked {
		t.Errorf("missing room: terr = %+v, want 404 and not notAsked", terr)
	}

	sessionID, userID := seedPlayingSession(t)
	if _, _, terr := h.submitTurn(sessionID, 999, "", "hi"); terr == nil || terr.status != http.StatusForbidden ||
		terr.notAsked || terr.msg != "观战模式下无法发言" {
		t.Errorf("spectator: terr = %+v, want 403 观战模式下无法发言 and not notAsked", terr)
	}

	beginSessionProcessing(sessionID)
	defer finishSessionProcessing(sessionID)
	if _, _, terr := h.submitTurn(sessionID, userID, "", "hi"); terr == nil || terr.status != http.StatusConflict || terr.notAsked {
		t.Errorf("busy KP: terr = %+v, want 409 and not notAsked", terr)
	}
}

func TestSubmitTurn_NamedPlayerStartsFloorWithoutUsername(t *testing.T) {
	initTestDB(t)
	resetHubs(t)
	sessionID, userID := seedPlayingSession(t)
	setSpotlight(t, sessionID, &models.SpotlightState{UserIDs: []uint{userID}, Names: []string{"Test Char"}, Question: "你听见了什么？"})

	runner := &fakeRunner{run: askRun("接下来呢？", []uint{userID}, []string{"Test Char"})}
	// submitTurn 不经过输入分类器：AI 路径依赖这一点，所以 Guard 被调用就是失败。
	g := &fakeGuard{verdict: agent.InputVerdict{Blocked: true, Category: "authority_claim", Score: 0.93}}
	h := guardedHandlers(runner, g)
	tap := tapRoom(t, sessionID)

	started, waiting, terr := h.submitTurn(sessionID, userID, "", "我贴着门听")
	if terr != nil {
		t.Fatalf("submitTurn: %+v", terr)
	}
	if !started || waiting.Pending != 0 || waiting.Total != 1 {
		t.Fatalf("started=%v waiting=%+v, want floor started with nobody pending", started, waiting)
	}
	tap.waitCount("floor_end", 1)

	if g.calls.Load() != 0 {
		t.Errorf("input guard was called %d times, submitTurn must skip it", g.calls.Load())
	}
	runs := runner.runCalls()
	if len(runs) != 1 || runs[0].UserInput != "我贴着门听" || runs[0].UserName != "Test Char" {
		t.Fatalf("runs = %+v, want one run with the card name as player name", runs)
	}
	var ta models.SessionTurnAction
	if err := models.DB.Where("session_id = ? AND user_id = ?", sessionID, userID).First(&ta).Error; err != nil {
		t.Fatalf("turn action: %v", err)
	}
	if ta.Username != "Test Char" || ta.ActionSummary != "我贴着门听" {
		t.Errorf("turn action = %+v, want display name from the character card", ta)
	}
	msgs := loadMessages(t, sessionID)
	if len(msgs) != 1 || msgs[0].Kind != models.MessageKindPlayer || msgs[0].Content != "我贴着门听" {
		t.Errorf("messages = %+v, want the player message", msgs)
	}
}

func TestSubmitTurn_EmptyUsernameFallsBackToAccountName(t *testing.T) {
	initTestDB(t)
	resetHubs(t)
	sessionID, userID := seedPlayingSession(t)
	// 角色名为空、调用方也没给用户名：显示名取账号用户名（seedPlayingSession 建的账号叫 tester）。
	models.DB.Model(&models.CharacterCard{}).Where("user_id = ?", userID).Update("name", "")
	setSpotlight(t, sessionID, &models.SpotlightState{UserIDs: []uint{userID}, Names: []string{"tester"}, Question: "怎么办？"})

	runner := &fakeRunner{run: askRun("然后呢？", []uint{userID}, []string{"tester"})}
	h := NewSessionHandlers(runner)
	tap := tapRoom(t, sessionID)

	if started, _, terr := h.submitTurn(sessionID, userID, "", "我敲门"); terr != nil || !started {
		t.Fatalf("started=%v terr=%+v", started, terr)
	}
	tap.waitCount("floor_end", 1)

	var ta models.SessionTurnAction
	if err := models.DB.Where("session_id = ? AND user_id = ?", sessionID, userID).First(&ta).Error; err != nil {
		t.Fatalf("turn action: %v", err)
	}
	if ta.Username != "tester" {
		t.Errorf("turn action username = %q, want tester", ta.Username)
	}
}

func TestSubmitTurn_WaitsForOtherNamedPlayers(t *testing.T) {
	initTestDB(t)
	resetHubs(t)
	sessionID, user1ID, user2ID := seedTwoPlayerSession(t, "爱丽丝", "鲍勃")
	setSpotlight(t, sessionID, &models.SpotlightState{
		UserIDs: []uint{user1ID, user2ID}, Names: []string{"爱丽丝", "鲍勃"}, Question: "你们怎么办？",
	})
	runner := &fakeRunner{}
	h := NewSessionHandlers(runner)
	tap := tapRoom(t, sessionID)

	started, waiting, terr := h.submitTurn(sessionID, user1ID, "", "我查探房间")
	if terr != nil {
		t.Fatalf("submitTurn: %+v", terr)
	}
	if started || waiting.Pending != 1 || waiting.Total != 2 ||
		len(waiting.SubmittedNames) != 1 || waiting.SubmittedNames[0] != "爱丽丝" ||
		len(waiting.PendingNames) != 1 || waiting.PendingNames[0] != "鲍勃" {
		t.Fatalf("started=%v waiting=%+v, want waiting on 鲍勃", started, waiting)
	}
	drainTap(tap)
	if len(tap.ofType("waiting")) != 1 {
		t.Errorf("events = %v, want one waiting broadcast", tap.types())
	}
	if len(runner.runCalls()) != 0 {
		t.Error("the floor must not start before everyone named has answered")
	}
}

// ── joinSessionPlayer ────────────────────────────────────────────────────────

func TestJoinSessionPlayer_JoinsAndAnnounces(t *testing.T) {
	initTestDB(t)
	resetHubs(t)
	sessID, _ := setupLobbySession(t)
	joinerID := seedUser(t, "joiner", "user", 0, 3)
	cardID := seedCard(t, joinerID, "阿卡姆调查员")
	tap := tapRoom(t, sessID)
	h := NewSessionHandlers(&fakeRunner{})

	handout, terr := h.joinSessionPlayer(sessID, joinerID, cardID, "", "joiner")
	if terr != nil || handout != "" {
		t.Fatalf("handout=%q terr=%+v", handout, terr)
	}

	var p models.SessionPlayer
	if err := models.DB.Where("session_id = ? AND user_id = ?", sessID, joinerID).First(&p).Error; err != nil {
		t.Fatalf("player row: %v", err)
	}
	if p.CharacterCardID != cardID || p.JoinedAt.IsZero() {
		t.Errorf("player = %+v", p)
	}
	msgs := loadMessages(t, sessID)
	if len(msgs) != 1 || msgs[0].Kind != models.MessageKindSystem || msgs[0].Username != "系统" ||
		msgs[0].Content != "「joiner」以调查员「阿卡姆调查员」的身份加入了房间。" {
		t.Errorf("messages = %+v, want the join announcement", msgs)
	}
	drainTap(tap)
	if len(tap.ofType("session")) != 1 || eventField(tap.ofType("session")[0], "reason") != "join" {
		t.Errorf("events = %v, want one session(join)", tap.types())
	}
}

func TestJoinSessionPlayer_RejectsForeignCard(t *testing.T) {
	initTestDB(t)
	resetHubs(t)
	sessID, ownerID := setupLobbySession(t)
	joinerID := seedUser(t, "joiner", "user", 0, 3)
	ownersCard := seedCard(t, ownerID, "别人的卡")

	if _, terr := joinSessionPlayer(sessID, joinerID, ownersCard, "", "joiner"); terr == nil ||
		terr.status != http.StatusForbidden || terr.msg != "无权使用此人物卡" {
		t.Fatalf("terr = %+v, want 403 无权使用此人物卡", terr)
	}
	if _, terr := joinSessionPlayer(sessID, joinerID, 9999, "", "joiner"); terr == nil ||
		terr.status != http.StatusBadRequest || terr.msg != "人物卡不存在" {
		t.Fatalf("terr = %+v, want 400 人物卡不存在", terr)
	}
	var n int64
	models.DB.Model(&models.SessionPlayer{}).Where("session_id = ?", sessID).Count(&n)
	if n != 0 {
		t.Errorf("rejected joins left %d players behind", n)
	}
}

func TestJoinSessionPlayer_ClaimsHandoutOnce(t *testing.T) {
	initTestDB(t)
	resetHubs(t)
	ownerID := seedUser(t, "owner", "user", 0, 3)
	sc := models.Scenario{
		Name: "HO 模组", IsActive: true,
		Content: models.JSONField[models.ScenarioContent]{Data: models.ScenarioContent{
			Handouts: []models.ScenarioHandout{{Code: "HO1", Title: "私家侦探"}, {Code: "HO2", Title: "家庭医生"}},
		}},
	}
	if err := models.DB.Create(&sc).Error; err != nil {
		t.Fatalf("create scenario: %v", err)
	}
	sess := models.GameSession{Name: "HO 房", ScenarioID: sc.ID, Status: models.SessionStatusLobby, MaxPlayers: 4, CreatedBy: ownerID}
	models.DB.Create(&sess)

	a := seedUser(t, "a", "user", 0, 3)
	b := seedUser(t, "b", "user", 0, 3)
	cardA := seedCard(t, a, "甲")
	cardB := seedCard(t, b, "乙")

	handout, terr := joinSessionPlayer(sess.ID, a, cardA, " ho1 ", "a")
	if terr != nil || handout != "HO1" {
		t.Fatalf("handout=%q terr=%+v, want HO1", handout, terr)
	}
	msgs := loadMessages(t, sess.ID)
	if len(msgs) != 1 || msgs[0].Content != "「a」以调查员「甲」的身份加入了房间，认领身份卡 HO1。" {
		t.Errorf("messages = %+v, want the claim announcement", msgs)
	}

	if _, terr := joinSessionPlayer(sess.ID, b, cardB, "HO1", "b"); terr == nil ||
		terr.status != http.StatusConflict || terr.msg != "身份卡 HO1 已被其他玩家认领" {
		t.Fatalf("terr = %+v, want 409 身份卡 HO1 已被其他玩家认领", terr)
	}
	var n int64
	models.DB.Model(&models.SessionPlayer{}).Where("session_id = ? AND user_id = ?", sess.ID, b).Count(&n)
	if n != 0 {
		t.Error("a failed claim must roll the join back")
	}
	if handout, terr := joinSessionPlayer(sess.ID, b, cardB, "random", "b"); terr != nil || handout != "HO2" {
		t.Errorf("random claim: handout=%q terr=%+v, want the only free HO2", handout, terr)
	}
}

// ── postOOC ──────────────────────────────────────────────────────────────────

func loadTableSession(t *testing.T, sessionID uint) models.GameSession {
	t.Helper()
	var s models.GameSession
	if err := models.DB.Preload("Players.CharacterCard").Preload("Players.User").First(&s, sessionID).Error; err != nil {
		t.Fatalf("load session: %v", err)
	}
	return s
}

func TestPostOOC_CoreCallWithoutGin(t *testing.T) {
	initTestDB(t)
	resetHubs(t)
	sessionID, user1ID, _ := seedTwoPlayerSession(t, "爱丽丝", "鲍勃")
	session := loadTableSession(t, sessionID)
	tap := tapRoom(t, sessionID)

	// username 为空时取库里的账号名，角色名取人物卡。
	msg, terr := postOOC(session, user1ID, "", "  先去图书馆吧  ")
	if terr != nil {
		t.Fatalf("postOOC: %+v", terr)
	}
	if msg.ID == 0 || msg.Username != "mp_user1" || msg.CharacterName != "爱丽丝" || msg.Content != "先去图书馆吧" {
		t.Errorf("msg = %+v", msg)
	}
	drainTap(tap)
	if len(tap.ofType("ooc_message")) != 1 {
		t.Errorf("events = %v, want one ooc_message", tap.types())
	}

	if _, terr := postOOC(session, user1ID, "mp_user1", "   "); terr == nil || terr.status != http.StatusBadRequest || terr.msg != "消息内容不能为空" {
		t.Errorf("empty: terr = %+v", terr)
	}
	if _, terr := postOOC(session, user1ID, "mp_user1", strings.Repeat("啊", maxOOCContentRunes+1)); terr == nil ||
		terr.status != http.StatusBadRequest || !strings.Contains(terr.msg, "桌边消息最多 500 字") {
		t.Errorf("overlong: terr = %+v", terr)
	}
	if _, terr := postOOC(session, user1ID+100, "路人", "插话"); terr == nil || terr.status != http.StatusForbidden {
		t.Errorf("stranger: terr = %+v, want 403", terr)
	}

	// 库里的管理员不是成员也能发言，没有角色名。
	adminID := seedUser(t, "root", "admin", 0, 3)
	if m, terr := postOOC(session, adminID, "", "巡房"); terr != nil || m.CharacterName != "" || m.Username != "root" {
		t.Errorf("admin: msg=%+v terr=%+v", m, terr)
	}
}

// ── createNote ───────────────────────────────────────────────────────────────

func TestCreateNote_CoreCallWithoutGin(t *testing.T) {
	initTestDB(t)
	resetHubs(t)
	sessionID, user1ID, _ := seedTwoPlayerSession(t, "爱丽丝", "鲍勃")
	session := loadTableSession(t, sessionID)
	tap := tapRoom(t, sessionID)

	shared, terr := createNote(session, user1ID, " 午夜去灯塔 ", true)
	if terr != nil {
		t.Fatalf("createNote: %+v", terr)
	}
	if shared.ID == 0 || shared.UserID != user1ID || shared.AuthorName != "爱丽丝" || shared.Content != "午夜去灯塔" || !shared.IsShared {
		t.Errorf("shared note = %+v", shared)
	}
	private, terr := createNote(session, user1ID, "我怀疑鲍勃", false)
	if terr != nil || private.IsShared {
		t.Fatalf("private note = %+v terr=%+v", private, terr)
	}
	drainTap(tap)
	if got := tap.ofType("handbook_update"); len(got) != 1 || eventField(got[0], "reason") != "note" {
		t.Errorf("events = %v, want exactly one handbook_update for the shared note", tap.types())
	}

	if _, terr := createNote(session, user1ID, "", true); terr == nil || terr.status != http.StatusBadRequest || terr.msg != "笔记内容不能为空" {
		t.Errorf("empty: terr = %+v", terr)
	}
	if _, terr := createNote(session, user1ID, strings.Repeat("字", maxNoteContentRunes+1), true); terr == nil ||
		terr.status != http.StatusBadRequest || !strings.Contains(terr.msg, "笔记最多 1000 字") {
		t.Errorf("overlong: terr = %+v", terr)
	}
	if _, terr := createNote(session, user1ID+100, "路人笔记", true); terr == nil || terr.status != http.StatusForbidden || terr.msg != "只有房间成员可以添加笔记" {
		t.Errorf("stranger: terr = %+v, want 403", terr)
	}

	// 房主不是调查员时没有角色名，署名取库里的账号名。
	hostNote, terr := createNote(session, session.CreatedBy, "我是房主", true)
	if terr != nil || hostNote.AuthorName != "creator_mp" {
		t.Errorf("host note = %+v terr=%+v, want author creator_mp", hostNote, terr)
	}
}

// ── buildHandbook ────────────────────────────────────────────────────────────

func TestBuildHandbook_ProjectsByViewer(t *testing.T) {
	initTestDB(t)
	resetHubs(t)
	sessionID, user1ID, user2ID := seedTwoPlayerSession(t, "爱丽丝", "鲍勃")
	models.DB.Model(&models.SessionPlayer{}).Where("session_id = ? AND user_id = ?", sessionID, user1ID).Update("location", "旧书房")
	models.DB.Model(&models.SessionPlayer{}).Where("session_id = ? AND user_id = ?", sessionID, user2ID).Update("location", "码头")
	models.DB.Model(&models.GameSession{}).Where("id = ?", sessionID).Update("clocks", models.JSONField[[]models.SessionClock]{Data: []models.SessionClock{
		{Name: "潮水上涨", Current: 1, Max: 6, Visible: true},
		{Name: "祭司苏醒", Current: 2, Max: 4, Visible: false},
	}})
	models.DB.Create(&models.SessionClue{SessionID: sessionID, Title: "撕碎的信纸", Content: "提到了午夜灯塔", AddedBy: "KP"})
	models.DB.Create(&models.SessionNPC{SessionID: sessionID, Name: "管家维克多", Occupation: "管家", Secret: "他就是幕后祭司", IsAlive: true})
	models.DB.Create(&models.SessionNote{SessionID: sessionID, UserID: user1ID, AuthorName: "爱丽丝", Content: "共享笔记", IsShared: true})
	models.DB.Create(&models.SessionNote{SessionID: sessionID, UserID: user1ID, AuthorName: "爱丽丝", Content: "爱丽丝的私密笔记", IsShared: false})
	models.DB.Create(&models.SessionNote{SessionID: sessionID, UserID: user2ID, AuthorName: "鲍勃", Content: "鲍勃的私密笔记", IsShared: false})
	session := loadTableSession(t, sessionID)

	// 鲍勃：共享笔记加自己的私密笔记，看不到暗钟。
	hb := buildHandbook(session, user2ID, false)
	if len(hb.Clues) != 1 || hb.Clues[0].Title != "撕碎的信纸" {
		t.Errorf("clues = %+v", hb.Clues)
	}
	if len(hb.Notes) != 2 || hb.Notes[0].Content != "共享笔记" || hb.Notes[1].Content != "鲍勃的私密笔记" {
		t.Errorf("bob notes = %+v, want shared + own private", hb.Notes)
	}
	if len(hb.Clocks) != 1 || hb.Clocks[0].Name != "潮水上涨" {
		t.Errorf("bob clocks = %+v, want only the visible clock", hb.Clocks)
	}
	if len(hb.Locations) != 2 || hb.Locations[0].Name != "旧书房" || hb.Locations[0].Occupants[0] != "爱丽丝" ||
		hb.Locations[1].Name != "码头" || hb.Locations[1].Occupants[0] != "鲍勃" {
		t.Errorf("locations = %+v", hb.Locations)
	}
	if len(hb.NPCs) != 1 || hb.NPCs[0].Name != "管家维克多" || hb.NPCs[0].Occupation != "管家" || !hb.NPCs[0].IsAlive {
		t.Errorf("npcs = %+v", hb.NPCs)
	}
	if hb.Combat != nil || hb.Chase != nil {
		t.Errorf("combat=%+v chase=%+v, want none when no encounter is active", hb.Combat, hb.Chase)
	}

	// 管理员看到全部笔记和暗钟。
	adminHB := buildHandbook(session, 0, true)
	if len(adminHB.Notes) != 3 || len(adminHB.Clocks) != 2 {
		t.Errorf("admin notes=%d clocks=%d, want 3 and 2", len(adminHB.Notes), len(adminHB.Clocks))
	}
}

func TestBuildHandbook_ActiveEncounterAndEmptyLists(t *testing.T) {
	initTestDB(t)
	resetHubs(t)
	sessionID, userID := seedPlayingSession(t)
	empty := buildHandbook(loadTableSession(t, sessionID), userID, false)
	if empty.Clues == nil || empty.Notes == nil || empty.Locations == nil || empty.NPCs == nil || empty.Clocks == nil {
		t.Errorf("handbook lists must be non-nil so JSON renders [] not null: %+v", empty)
	}

	models.DB.Model(&models.GameSession{}).Where("id = ?", sessionID).Updates(map[string]any{
		"combat_state": models.JSONField[*models.CombatState]{Data: &models.CombatState{
			Active: true, Round: 2, ActorIndex: 1,
			Participants: []models.CombatParticipant{{Name: "Test Char", DEX: 60, UserID: userID, HasActed: true}},
		}},
	})
	hb := buildHandbook(loadTableSession(t, sessionID), userID, false)
	if hb.Combat == nil || !hb.Combat.Active || hb.Combat.Round != 2 || len(hb.Combat.Participants) != 1 ||
		hb.Combat.Participants[0].Name != "Test Char" || !hb.Combat.Participants[0].IsAlive {
		t.Errorf("combat = %+v", hb.Combat)
	}
}

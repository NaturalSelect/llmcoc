package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/llmcoc/server/internal/models"
)

func sessionTableRouter(userID uint, username, role string) *gin.Engine {
	r := gin.New()
	auth := withAuth(userID, username, role)
	r.GET("/sessions/:id/ooc", auth, GetOOCMessages)
	r.POST("/sessions/:id/ooc", auth, PostOOCMessage)
	r.GET("/sessions/:id/handbook", auth, GetSessionHandbook)
	r.POST("/sessions/:id/clues", auth, CreateSessionClue)
	r.DELETE("/sessions/:id/clues/:clueId", auth, DeleteSessionClue)
	r.POST("/sessions/:id/notes", auth, CreateSessionNote)
	r.DELETE("/sessions/:id/notes/:noteId", auth, DeleteSessionNote)
	return r
}

func TestOOCMessages_CRUDPermissionsAndBroadcast(t *testing.T) {
	initTestDB(t)
	resetHubs(t)
	host := seedUser(t, "host", "user", 0, 3)
	p2 := seedUser(t, "p2", "user", 0, 3)
	stranger := seedUser(t, "stranger", "user", 0, 3)
	sid := seedScenario(t, "S")
	c1 := seedCard(t, host, "甲")
	c2 := seedCard(t, p2, "乙")

	sess := models.GameSession{
		Name: "OOC Room", ScenarioID: sid, Status: models.SessionStatusPlaying,
		MaxPlayers: 4, CreatedBy: host, HasPassword: true,
	}
	models.DB.Create(&sess)
	models.DB.Create(&models.SessionPlayer{SessionID: sess.ID, UserID: host, CharacterCardID: c1})
	models.DB.Create(&models.SessionPlayer{SessionID: sess.ID, UserID: p2, CharacterCardID: c2})

	sub, _, _ := hubFor(sess.ID).SubscribeFor(0, host, false)
	defer hubFor(sess.ID).Unsubscribe(sub)

	// 参与者发言成功并附带角色名、广播 ooc_message
	rP2 := sessionTableRouter(p2, "p2", "user")
	w := httptest.NewRecorder()
	rP2.ServeHTTP(w, jsonReq("POST", fmt.Sprintf("/sessions/%d/ooc", sess.ID), map[string]any{"content": " 等下我们先查图书馆？ "}))
	if w.Code != http.StatusCreated {
		t.Fatalf("post ooc want 201, got %d body=%s", w.Code, w.Body.String())
	}
	var created models.SessionOOCMessage
	json.NewDecoder(w.Body).Decode(&created)
	if created.CharacterName != "乙" || created.Content != "等下我们先查图书馆？" {
		t.Fatalf("unexpected created msg: %+v", created)
	}
	ev := recvEvent(t, sub)
	if ev.Type != "ooc_message" {
		t.Fatalf("expected ooc_message event, got %+v", ev)
	}

	// 空内容或超长内容被拒
	w = httptest.NewRecorder()
	rP2.ServeHTTP(w, jsonReq("POST", fmt.Sprintf("/sessions/%d/ooc", sess.ID), map[string]any{"content": "   "}))
	if w.Code != http.StatusBadRequest {
		t.Errorf("empty ooc want 400, got %d", w.Code)
	}
	w = httptest.NewRecorder()
	rP2.ServeHTTP(w, jsonReq("POST", fmt.Sprintf("/sessions/%d/ooc", sess.ID), map[string]any{"content": strings.Repeat("啊", 501)}))
	if w.Code != http.StatusBadRequest {
		t.Errorf("overlong ooc want 400, got %d", w.Code)
	}

	// 非成员在加密房间既不能发也不能看
	rStranger := sessionTableRouter(stranger, "stranger", "user")
	w = httptest.NewRecorder()
	rStranger.ServeHTTP(w, jsonReq("POST", fmt.Sprintf("/sessions/%d/ooc", sess.ID), map[string]any{"content": "插话"}))
	if w.Code != http.StatusForbidden {
		t.Errorf("stranger post want 403, got %d", w.Code)
	}
	w = httptest.NewRecorder()
	rStranger.ServeHTTP(w, jsonReq("GET", fmt.Sprintf("/sessions/%d/ooc", sess.ID), nil))
	if w.Code != http.StatusForbidden {
		t.Errorf("stranger get passworded room want 403, got %d", w.Code)
	}

	// 房主拉取历史，按时间正序返回
	rHost := sessionTableRouter(host, "host", "user")
	w = httptest.NewRecorder()
	rHost.ServeHTTP(w, jsonReq("POST", fmt.Sprintf("/sessions/%d/ooc", sess.ID), map[string]any{"content": "好主意"}))
	if w.Code != http.StatusCreated {
		t.Fatalf("host post want 201, got %d", w.Code)
	}
	w = httptest.NewRecorder()
	rHost.ServeHTTP(w, jsonReq("GET", fmt.Sprintf("/sessions/%d/ooc", sess.ID), nil))
	if w.Code != http.StatusOK {
		t.Fatalf("get ooc want 200, got %d", w.Code)
	}
	var list []models.SessionOOCMessage
	json.NewDecoder(w.Body).Decode(&list)
	if len(list) != 2 || list[0].Content != "等下我们先查图书馆？" || list[1].Content != "好主意" {
		t.Fatalf("unexpected ooc history: %+v", list)
	}
}

func TestHandbook_CluesNotesPrivacyAndAntiSpoilerProjection(t *testing.T) {
	initTestDB(t)
	resetHubs(t)
	host := seedUser(t, "host", "user", 0, 3)
	p2 := seedUser(t, "p2", "user", 0, 3)
	admin := seedUser(t, "admin", "admin", 0, 3)
	sid := seedScenario(t, "S")
	c1 := seedCard(t, host, "侦探甲")
	c2 := seedCard(t, p2, "医生乙")

	sess := models.GameSession{
		Name: "Handbook Room", ScenarioID: sid, Status: models.SessionStatusPlaying,
		MaxPlayers: 4, CreatedBy: host,
		CombatState: models.JSONField[*models.CombatState]{Data: &models.CombatState{
			Active:     true,
			Round:      2,
			ActorIndex: 1,
			Participants: []models.CombatParticipant{
				{Name: "侦探甲", DEX: 70, CombatSkill: 65, IsNPC: false, UserID: host, HasActed: true},
				{Name: "管家维克多", DEX: 55, CombatSkill: 88, IsNPC: true, PendingClarification: false},
			},
		}},
		ChaseState: models.JSONField[*models.ChaseState]{Data: &models.ChaseState{
			Active:     true,
			Round:      1,
			MinMOV:     7,
			ActorIndex: 0,
			Participants: []models.ChaseParticipant{
				{Name: "医生乙", DEX: 60, MOV: 8, Location: 3, IsNPC: false, IsPursuer: false},
				{Name: "黑影", DEX: 50, MOV: 9, Location: 1, IsNPC: true, IsPursuer: true},
			},
			Obstacles: []models.ChaseObstacle{{Name: "铁栅栏", Between: [2]int{2, 3}, HP: 10, MaxHP: 10}},
		}},
	}
	models.DB.Create(&sess)
	models.DB.Create(&models.SessionPlayer{SessionID: sess.ID, UserID: host, CharacterCardID: c1, Location: "旧书房"})
	models.DB.Create(&models.SessionPlayer{SessionID: sess.ID, UserID: p2, CharacterCardID: c2, Location: "码头"})
	models.DB.Create(&models.SessionNPC{
		SessionID: sess.ID, Name: "管家维克多", Race: "深潜者混血", Occupation: "管家",
		Location: "旧书房", Secret: "他就是幕后祭司", Goal: "献祭所有访客",
		Attitude: "表面恭顺实则敌视", SessionMemory: "准备下毒",
		Stats:   models.JSONField[map[string]int]{Data: map[string]int{"HP": 18, "STR": 80}},
		IsAlive: true,
	})
	models.DB.Create(&models.Message{
		SessionID: sess.ID, Role: models.MessageRoleAssistant, Kind: models.MessageKindEvent, Status: models.MessageStatusDone,
		Meta: models.JSONField[*models.MessageMeta]{Data: &models.MessageMeta{Events: []models.GameEvent{
			{Type: "location", Character: "侦探甲", Location: "门廊"},
			{Type: "item", Category: "relation", Op: "add", Name: "图书管理员老陈"},
		}}},
	})

	// 成员创建线索，另一普通成员不能删，本人能删
	rP2 := sessionTableRouter(p2, "p2", "user")
	rHost := sessionTableRouter(host, "host", "user")
	rAdmin := sessionTableRouter(admin, "admin", "admin")

	w := httptest.NewRecorder()
	rP2.ServeHTTP(w, jsonReq("POST", fmt.Sprintf("/sessions/%d/clues", sess.ID), map[string]any{
		"title": "撕碎的信纸", "content": "提到了午夜灯塔", "source": "旧书房",
	}))
	if w.Code != http.StatusCreated {
		t.Fatalf("create clue want 201, got %d body=%s", w.Code, w.Body.String())
	}
	var clue models.SessionClue
	json.NewDecoder(w.Body).Decode(&clue)
	if clue.AddedBy != "医生乙" {
		t.Errorf("clue AddedBy = %q, want 医生乙", clue.AddedBy)
	}

	// Host 写一条私密笔记 + 一条共享笔记
	w = httptest.NewRecorder()
	rHost.ServeHTTP(w, jsonReq("POST", fmt.Sprintf("/sessions/%d/notes", sess.ID), map[string]any{
		"content": "我怀疑医生乙有秘密", "is_shared": false,
	}))
	if w.Code != http.StatusCreated {
		t.Fatalf("create private note want 201, got %d", w.Code)
	}
	w = httptest.NewRecorder()
	rHost.ServeHTTP(w, jsonReq("POST", fmt.Sprintf("/sessions/%d/notes", sess.ID), map[string]any{
		"content": "午夜前去灯塔汇合", "is_shared": true,
	}))
	if w.Code != http.StatusCreated {
		t.Fatalf("create shared note want 201, got %d", w.Code)
	}

	// P2 拉取手册：不能看到 host 的私密笔记，也不能看到任何 NPC 剧透字段或 combat_skill
	w = httptest.NewRecorder()
	rP2.ServeHTTP(w, jsonReq("GET", fmt.Sprintf("/sessions/%d/handbook", sess.ID), nil))
	if w.Code != http.StatusOK {
		t.Fatalf("get handbook want 200, got %d body=%s", w.Code, w.Body.String())
	}
	raw := w.Body.String()
	for _, forbidden := range []string{"深潜者混血", "幕后祭司", "献祭所有访客", "表面恭顺", "准备下毒", "combat_skill", "怀疑医生乙"} {
		if strings.Contains(raw, forbidden) {
			t.Errorf("handbook leaked spoiler/private data %q in %s", forbidden, raw)
		}
	}
	var hb handbookResponse
	if err := json.Unmarshal([]byte(raw), &hb); err != nil {
		t.Fatalf("decode handbook: %v", err)
	}
	if len(hb.Clues) != 1 || hb.Clues[0].Title != "撕碎的信纸" {
		t.Errorf("clues = %+v", hb.Clues)
	}
	if len(hb.Notes) != 1 || hb.Notes[0].Content != "午夜前去灯塔汇合" {
		t.Errorf("p2 notes = %+v, want only shared note", hb.Notes)
	}
	if len(hb.Locations) != 3 {
		t.Errorf("locations = %+v, want 3 (门廊/旧书房/码头)", hb.Locations)
	}
	if len(hb.NPCs) != 2 || hb.NPCs[0].Name != "管家维克多" || hb.NPCs[1].Name != "图书管理员老陈" {
		t.Errorf("npcs = %+v", hb.NPCs)
	}
	if hb.Combat == nil || !hb.Combat.Active || hb.Combat.Round != 2 || len(hb.Combat.Participants) != 2 {
		t.Errorf("combat = %+v", hb.Combat)
	}
	if hb.Chase == nil || !hb.Chase.Active || len(hb.Chase.Obstacles) != 1 {
		t.Errorf("chase = %+v", hb.Chase)
	}

	// Admin 拉取手册可以看到全部 2 条笔记
	w = httptest.NewRecorder()
	rAdmin.ServeHTTP(w, jsonReq("GET", fmt.Sprintf("/sessions/%d/handbook", sess.ID), nil))
	var adminHB handbookResponse
	json.NewDecoder(w.Body).Decode(&adminHB)
	if len(adminHB.Notes) != 2 {
		t.Errorf("admin notes count = %d, want 2", len(adminHB.Notes))
	}

	// 删除线索权限：创建人可删
	w = httptest.NewRecorder()
	rP2.ServeHTTP(w, jsonReq("DELETE", fmt.Sprintf("/sessions/%d/clues/%d", sess.ID, clue.ID), nil))
	if w.Code != http.StatusOK {
		t.Errorf("creator delete clue want 200, got %d", w.Code)
	}
}

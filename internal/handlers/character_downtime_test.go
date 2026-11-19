package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/llmcoc/server/internal/models"
)

func downtimeRouter(userID uint, username, role string) *gin.Engine {
	r := gin.New()
	auth := withAuth(userID, username, role)
	r.POST("/characters/:id/sanatorium", auth, SanatoriumHeal)
	r.POST("/characters/:id/study-tome", auth, StudyTome)
	return r
}

func TestSanatoriumHeal_FlowAndGuards(t *testing.T) {
	initTestDB(t)
	owner := seedUser(t, "alice", "user", 100, 3)
	other := seedUser(t, "bob", "user", 500, 3)
	models.DB.Create(&models.SiteSetting{Key: "sanatorium_cost", Value: "60"})

	card := models.CharacterCard{
		UserID:         owner,
		Name:           "爱丽丝",
		IsActive:       true,
		MadnessState:   "indefinite",
		MadnessSymptom: "幻听钟声",
		Stats: models.JSONField[models.CharacterStats]{Data: models.CharacterStats{
			HP: 10, MaxHP: 12, SAN: 40, MaxSAN: 80, INT: 65,
		}},
	}
	models.DB.Create(&card)

	// 非本人禁止安排疗养
	rOther := downtimeRouter(other, "bob", "user")
	w := httptest.NewRecorder()
	rOther.ServeHTTP(w, jsonReq("POST", fmt.Sprintf("/characters/%d/sanatorium", card.ID), nil))
	if w.Code != http.StatusForbidden {
		t.Fatalf("non-owner sanatorium want 403, got %d", w.Code)
	}

	// 在房间中锁定的人物卡返回 409
	sid := seedScenario(t, "S")
	sess := models.GameSession{Name: "Playing", ScenarioID: sid, Status: models.SessionStatusPlaying, MaxPlayers: 4, CreatedBy: owner}
	models.DB.Create(&sess)
	models.DB.Create(&models.SessionPlayer{SessionID: sess.ID, UserID: owner, CharacterCardID: card.ID})

	rOwner := downtimeRouter(owner, "alice", "user")
	w = httptest.NewRecorder()
	rOwner.ServeHTTP(w, jsonReq("POST", fmt.Sprintf("/characters/%d/sanatorium", card.ID), nil))
	if w.Code != http.StatusConflict {
		t.Fatalf("locked card sanatorium want 409, got %d body=%s", w.Code, w.Body.String())
	}

	// 结团解锁后正常入住疗养院：扣 60 金币，恢复 3~8 点 SAN，并清除不定性疯狂
	models.DB.Model(&sess).Update("status", models.SessionStatusEnded)
	w = httptest.NewRecorder()
	rOwner.ServeHTTP(w, jsonReq("POST", fmt.Sprintf("/characters/%d/sanatorium", card.ID), nil))
	if w.Code != http.StatusOK {
		t.Fatalf("sanatorium want 200, got %d body=%s", w.Code, w.Body.String())
	}
	var resp struct {
		Card           models.CharacterCard `json:"card"`
		Healed         int                  `json:"healed"`
		ClearedMadness bool                 `json:"cleared_madness"`
		CoinsSpent     int                  `json:"coins_spent"`
		CoinsLeft      int                  `json:"coins_left"`
	}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode sanatorium resp: %v", err)
	}
	if resp.CoinsSpent != 60 || resp.CoinsLeft != 40 {
		t.Errorf("coins spent/left = %d/%d, want 60/40", resp.CoinsSpent, resp.CoinsLeft)
	}
	if resp.Healed < 3 || resp.Healed > 8 || resp.Card.Stats.Data.SAN != 40+resp.Healed {
		t.Errorf("healed=%d san=%d, want 3..8", resp.Healed, resp.Card.Stats.Data.SAN)
	}
	if !resp.ClearedMadness || resp.Card.MadnessState != "none" || resp.Card.MadnessSymptom != "" {
		t.Errorf("madness not cleared: %+v", resp.Card)
	}

	// 金币不足（剩 40 < 60）返回 402
	w = httptest.NewRecorder()
	rOwner.ServeHTTP(w, jsonReq("POST", fmt.Sprintf("/characters/%d/sanatorium", card.ID), nil))
	if w.Code != http.StatusPaymentRequired {
		t.Fatalf("insufficient coins want 402, got %d body=%s", w.Code, w.Body.String())
	}

	// 永久性疯狂拒绝入住
	models.DB.Model(&models.User{}).Where("id = ?", owner).Update("coins", 500)
	models.DB.Model(&card).Update("madness_state", "permanent")
	w = httptest.NewRecorder()
	rOwner.ServeHTTP(w, jsonReq("POST", fmt.Sprintf("/characters/%d/sanatorium", card.ID), nil))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("permanent madness want 400, got %d", w.Code)
	}

	// 满理智且无疯狂拒绝入住
	models.DB.Model(&card).Updates(map[string]any{
		"madness_state": "none",
		"stats":         models.JSONField[models.CharacterStats]{Data: models.CharacterStats{HP: 10, MaxHP: 12, SAN: 80, MaxSAN: 80}},
	})
	w = httptest.NewRecorder()
	rOwner.ServeHTTP(w, jsonReq("POST", fmt.Sprintf("/characters/%d/sanatorium", card.ID), nil))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("full SAN sanatorium want 400, got %d", w.Code)
	}
}

func TestStudyTome_FlowAndGuards(t *testing.T) {
	initTestDB(t)
	owner := seedUser(t, "scholar", "user", 0, 3)

	card := models.CharacterCard{
		UserID:             owner,
		Name:               "学者丙",
		IsActive:           true,
		CthulhuMythosSkill: 5,
		Stats: models.JSONField[models.CharacterStats]{Data: models.CharacterStats{
			HP: 10, MaxHP: 10, SAN: 60, MaxSAN: 94, INT: 75,
		}},
		Skills: models.JSONField[map[string]int]{Data: map[string]int{"图书馆使用": 70, "克苏鲁神话": 5}},
		Inventory: models.JSONField[[]models.Item]{Data: []models.Item{
			{Name: "死灵之书残本", Type: models.ItemTypeTome, Count: 1, Desc: "泛黄拉丁文手抄本"},
			{Name: "手电筒", Type: models.ItemTypeEquipment, Count: 1},
		}},
	}
	models.DB.Create(&card)

	r := downtimeRouter(owner, "scholar", "user")

	// 非典籍物品或不存在的典籍返回 404
	w := httptest.NewRecorder()
	r.ServeHTTP(w, jsonReq("POST", fmt.Sprintf("/characters/%d/study-tome", card.ID), map[string]any{"tome_name": "手电筒"}))
	if w.Code != http.StatusNotFound {
		t.Fatalf("non-tome want 404, got %d", w.Code)
	}

	// 正常研读典籍：掉 1~4 SAN，涨 1~3 克苏鲁神话，同步 Skills["克苏鲁神话"] 与 MaxSAN
	w = httptest.NewRecorder()
	r.ServeHTTP(w, jsonReq("POST", fmt.Sprintf("/characters/%d/study-tome", card.ID), map[string]any{"tome_name": "死灵之书残本"}))
	if w.Code != http.StatusOK {
		t.Fatalf("study tome want 200, got %d body=%s", w.Code, w.Body.String())
	}
	var resp struct {
		Card         models.CharacterCard `json:"card"`
		IntRoll      int                  `json:"int_roll"`
		IntTarget    int                  `json:"int_target"`
		IntSuccess   bool                 `json:"int_success"`
		SanLost      int                  `json:"san_lost"`
		MythosGained int                  `json:"mythos_gained"`
	}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode study tome: %v", err)
	}
	if resp.IntTarget != 75 || resp.IntRoll < 1 || resp.IntRoll > 100 || resp.IntSuccess != (resp.IntRoll <= 75) {
		t.Errorf("unexpected int roll result: %+v", resp)
	}
	if resp.SanLost < 1 || resp.SanLost > 4 || resp.Card.Stats.Data.SAN != 60-resp.SanLost {
		t.Errorf("unexpected san loss: lost=%d san=%d", resp.SanLost, resp.Card.Stats.Data.SAN)
	}
	if resp.MythosGained < 1 || resp.MythosGained > 3 {
		t.Errorf("unexpected mythos gain: %d", resp.MythosGained)
	}
	wantCM := 5 + resp.MythosGained
	if resp.Card.CthulhuMythosSkill != wantCM || resp.Card.Skills.Data["克苏鲁神话"] != wantCM {
		t.Errorf("mythos mismatch: field=%d skill=%d want=%d", resp.Card.CthulhuMythosSkill, resp.Card.Skills.Data["克苏鲁神话"], wantCM)
	}
	if resp.Card.Stats.Data.MaxSAN != 99-wantCM {
		t.Errorf("max_san = %d, want %d", resp.Card.Stats.Data.MaxSAN, 99-wantCM)
	}

	// 理智过低（<=4）拒绝研读
	models.DB.Model(&card).Update("stats", models.JSONField[models.CharacterStats]{Data: models.CharacterStats{
		HP: 10, MaxHP: 10, SAN: 4, MaxSAN: 90, INT: 75,
	}})
	w = httptest.NewRecorder()
	r.ServeHTTP(w, jsonReq("POST", fmt.Sprintf("/characters/%d/study-tome", card.ID), map[string]any{"tome_name": "死灵之书残本"}))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("low SAN study tome want 400, got %d", w.Code)
	}
}

package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/llmcoc/server/internal/models"
)

// companionRoomFixture 是一个 lobby 房间：房主已入座，bot 名下有一张卡池里的卡。
type companionRoomFixture struct {
	ownerID, botID, sessionID, poolCardID uint
}

func newCompanionRoomFixture(t *testing.T, maxPlayers int, enabled bool) companionRoomFixture {
	t.Helper()
	initTestDB(t)
	ownerID := seedUser(t, "host", models.RoleUser, 1000, 3)
	botID, err := models.EnsureCompanionUser()
	if err != nil {
		t.Fatalf("EnsureCompanionUser: %v", err)
	}
	if enabled {
		providerID := seedProvider(t, "p")
		cfg := models.AgentConfig{Role: models.AgentRoleCompanion, ProviderConfigID: &providerID, ModelName: "gpt-4o", MaxTokens: 1000, IsActive: true}
		if err := models.DB.Create(&cfg).Error; err != nil {
			t.Fatalf("seed companion config: %v", err)
		}
	}
	session := models.GameSession{
		Name: "room", ScenarioID: seedScenario(t, "s"), Status: models.SessionStatusLobby,
		MaxPlayers: maxPlayers, CreatedBy: ownerID, TurnRound: 1,
	}
	if err := models.DB.Create(&session).Error; err != nil {
		t.Fatalf("create session: %v", err)
	}
	ownerCard := seedCard(t, ownerID, "房主调查员")
	if err := models.DB.Create(&models.SessionPlayer{SessionID: session.ID, UserID: ownerID, CharacterCardID: ownerCard}).Error; err != nil {
		t.Fatalf("seat owner: %v", err)
	}
	return companionRoomFixture{ownerID: ownerID, botID: botID, sessionID: session.ID, poolCardID: seedCard(t, botID, "陪玩调查员")}
}

func (f companionRoomFixture) router(userID uint, role string) *gin.Engine {
	r := gin.New()
	auth := withAuth(userID, "u", role)
	r.GET("/sessions/:id/companion-cards", auth, GetCompanionCards)
	r.POST("/sessions/:id/companion", auth, AddCompanion)
	r.DELETE("/sessions/:id/companion", auth, RemoveCompanion)
	r.POST("/sessions/:id/leave", auth, LeaveSession)
	r.POST("/sessions/:id/start", auth, StartSession)
	r.POST("/sessions/:id/end", auth, EndSession)
	return r
}

func (f companionRoomFixture) do(r *gin.Engine, method, path string, body any) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	r.ServeHTTP(w, jsonReq(method, path, body))
	return w
}

func (f companionRoomFixture) botSeats(t *testing.T) int64 {
	t.Helper()
	var n int64
	models.DB.Model(&models.SessionPlayer{}).Where("session_id = ? AND user_id = ?", f.sessionID, f.botID).Count(&n)
	return n
}

func TestAddCompanion_AddsOnceAndRemoves(t *testing.T) {
	f := newCompanionRoomFixture(t, 4, true)
	r := f.router(f.ownerID, "user")
	base := "/sessions/" + itoa(f.sessionID) + "/companion"

	w := f.do(r, "POST", base, map[string]any{"character_card_id": f.poolCardID})
	if w.Code != http.StatusOK {
		t.Fatalf("add: want 200, got %d: %s", w.Code, w.Body.String())
	}
	if f.botSeats(t) != 1 {
		t.Fatalf("bot seats = %d, want 1", f.botSeats(t))
	}

	other := seedCard(t, f.botID, "第二张池卡")
	w = f.do(r, "POST", base, map[string]any{"character_card_id": other})
	if w.Code != http.StatusConflict {
		t.Fatalf("second add: want 409, got %d: %s", w.Code, w.Body.String())
	}
	if f.botSeats(t) != 1 {
		t.Fatalf("bot seats after rejected add = %d, want 1", f.botSeats(t))
	}

	w = f.do(r, "DELETE", base, nil)
	if w.Code != http.StatusOK || f.botSeats(t) != 0 {
		t.Fatalf("remove: code %d seats %d: %s", w.Code, f.botSeats(t), w.Body.String())
	}
	if w = f.do(r, "DELETE", base, nil); w.Code != http.StatusNotFound {
		t.Fatalf("remove again: want 404, got %d", w.Code)
	}
}

func TestAddCompanion_Rejections(t *testing.T) {
	t.Run("角色未启用", func(t *testing.T) {
		f := newCompanionRoomFixture(t, 4, false)
		w := f.do(f.router(f.ownerID, "user"), "POST", "/sessions/"+itoa(f.sessionID)+"/companion", map[string]any{"character_card_id": f.poolCardID})
		if w.Code != http.StatusBadRequest {
			t.Fatalf("want 400, got %d: %s", w.Code, w.Body.String())
		}
	})
	t.Run("非房主", func(t *testing.T) {
		f := newCompanionRoomFixture(t, 4, true)
		strangerID := seedUser(t, "stranger", models.RoleUser, 0, 3)
		w := f.do(f.router(strangerID, "user"), "POST", "/sessions/"+itoa(f.sessionID)+"/companion", map[string]any{"character_card_id": f.poolCardID})
		if w.Code != http.StatusForbidden {
			t.Fatalf("want 403, got %d: %s", w.Code, w.Body.String())
		}
	})
	t.Run("管理员可以代房主添加", func(t *testing.T) {
		f := newCompanionRoomFixture(t, 4, true)
		adminID := seedUser(t, "root", models.RoleAdmin, 0, 3)
		w := f.do(f.router(adminID, "admin"), "POST", "/sessions/"+itoa(f.sessionID)+"/companion", map[string]any{"character_card_id": f.poolCardID})
		if w.Code != http.StatusOK {
			t.Fatalf("want 200, got %d: %s", w.Code, w.Body.String())
		}
	})
	t.Run("房间已满", func(t *testing.T) {
		f := newCompanionRoomFixture(t, 1, true)
		w := f.do(f.router(f.ownerID, "user"), "POST", "/sessions/"+itoa(f.sessionID)+"/companion", map[string]any{"character_card_id": f.poolCardID})
		if w.Code != http.StatusBadRequest {
			t.Fatalf("want 400, got %d: %s", w.Code, w.Body.String())
		}
	})
	t.Run("不是卡池里的卡", func(t *testing.T) {
		f := newCompanionRoomFixture(t, 4, true)
		own := seedCard(t, f.ownerID, "房主另一张卡")
		w := f.do(f.router(f.ownerID, "user"), "POST", "/sessions/"+itoa(f.sessionID)+"/companion", map[string]any{"character_card_id": own})
		if w.Code != http.StatusForbidden {
			t.Fatalf("want 403, got %d: %s", w.Code, w.Body.String())
		}
		if f.botSeats(t) != 0 {
			t.Fatal("rejected add must not seat the bot")
		}
	})
	t.Run("池卡已被另一个房间占用", func(t *testing.T) {
		f := newCompanionRoomFixture(t, 4, true)
		lockCardInLobby(t, f.botID, f.poolCardID)
		w := f.do(f.router(f.ownerID, "user"), "POST", "/sessions/"+itoa(f.sessionID)+"/companion", map[string]any{"character_card_id": f.poolCardID})
		if w.Code != http.StatusConflict {
			t.Fatalf("want 409, got %d: %s", w.Code, w.Body.String())
		}
	})
	t.Run("已开始的房间", func(t *testing.T) {
		f := newCompanionRoomFixture(t, 4, true)
		models.DB.Model(&models.GameSession{}).Where("id = ?", f.sessionID).Update("status", models.SessionStatusPlaying)
		w := f.do(f.router(f.ownerID, "user"), "POST", "/sessions/"+itoa(f.sessionID)+"/companion", map[string]any{"character_card_id": f.poolCardID})
		if w.Code != http.StatusBadRequest {
			t.Fatalf("want 400, got %d: %s", w.Code, w.Body.String())
		}
	})
}

func TestGetCompanionCards_ListsOnlyFreeAliveCards(t *testing.T) {
	f := newCompanionRoomFixture(t, 4, true)
	busy := seedCard(t, f.botID, "占用中")
	lockCardInLobby(t, f.botID, busy)
	dead := seedCard(t, f.botID, "已死亡")
	models.DB.Model(&models.CharacterCard{}).Where("id = ?", dead).Update("wound_state", "dead")

	w := f.do(f.router(f.ownerID, "user"), "GET", "/sessions/"+itoa(f.sessionID)+"/companion-cards", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp struct {
		Enabled bool                `json:"enabled"`
		Cards   []companionCardView `json:"cards"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !resp.Enabled || len(resp.Cards) != 1 || resp.Cards[0].ID != f.poolCardID {
		t.Fatalf("resp = %+v, want enabled with only the idle alive card %d", resp, f.poolCardID)
	}

	strangerID := seedUser(t, "stranger", models.RoleUser, 0, 3)
	if w = f.do(f.router(strangerID, "user"), "GET", "/sessions/"+itoa(f.sessionID)+"/companion-cards", nil); w.Code != http.StatusForbidden {
		t.Fatalf("stranger: want 403, got %d", w.Code)
	}
}

func TestStartSession_NeedsAHuman(t *testing.T) {
	f := newCompanionRoomFixture(t, 4, true)
	r := f.router(f.ownerID, "user")
	// 房主的席位换成只剩 AI。
	models.DB.Where("session_id = ? AND user_id = ?", f.sessionID, f.ownerID).Delete(&models.SessionPlayer{})
	models.DB.Create(&models.SessionPlayer{SessionID: f.sessionID, UserID: f.botID, CharacterCardID: f.poolCardID})

	w := f.do(r, "POST", "/sessions/"+itoa(f.sessionID)+"/start", nil)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("only the AI seated: want 400, got %d: %s", w.Code, w.Body.String())
	}
	var s models.GameSession
	models.DB.First(&s, f.sessionID)
	if s.Status != models.SessionStatusLobby {
		t.Fatalf("status = %q, want the room to stay in lobby", s.Status)
	}

	humanCard := seedCard(t, f.ownerID, "房主调查员二号")
	models.DB.Create(&models.SessionPlayer{SessionID: f.sessionID, UserID: f.ownerID, CharacterCardID: humanCard})
	if w = f.do(r, "POST", "/sessions/"+itoa(f.sessionID)+"/start", nil); w.Code != http.StatusOK {
		t.Fatalf("human + AI: want 200, got %d: %s", w.Code, w.Body.String())
	}
}

func TestLeaveSession_LastHumanTakesTheCompanionAlong(t *testing.T) {
	f := newCompanionRoomFixture(t, 4, true)
	models.DB.Create(&models.SessionPlayer{SessionID: f.sessionID, UserID: f.botID, CharacterCardID: f.poolCardID})

	w := f.do(f.router(f.ownerID, "user"), "POST", "/sessions/"+itoa(f.sessionID)+"/leave", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body.String())
	}
	var rooms, seats int64
	models.DB.Model(&models.GameSession{}).Where("id = ?", f.sessionID).Count(&rooms)
	models.DB.Model(&models.SessionPlayer{}).Where("session_id = ?", f.sessionID).Count(&seats)
	if rooms != 0 || seats != 0 {
		t.Fatalf("rooms=%d seats=%d, want the room dissolved with no leftover AI seat", rooms, seats)
	}
}

func TestLeaveSession_OtherHumanKeepsTheRoom(t *testing.T) {
	f := newCompanionRoomFixture(t, 4, true)
	models.DB.Create(&models.SessionPlayer{SessionID: f.sessionID, UserID: f.botID, CharacterCardID: f.poolCardID})
	friendID := seedUser(t, "friend", models.RoleUser, 0, 3)
	models.DB.Create(&models.SessionPlayer{SessionID: f.sessionID, UserID: friendID, CharacterCardID: seedCard(t, friendID, "朋友的卡")})

	w := f.do(f.router(f.ownerID, "user"), "POST", "/sessions/"+itoa(f.sessionID)+"/leave", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body.String())
	}
	var rooms int64
	models.DB.Model(&models.GameSession{}).Where("id = ?", f.sessionID).Count(&rooms)
	if rooms != 1 || f.botSeats(t) != 1 {
		t.Fatalf("rooms=%d bot seats=%d, want the room and the AI seat kept", rooms, f.botSeats(t))
	}
}

func TestEndSession_DoesNotChargeTheCompanion(t *testing.T) {
	f := newCompanionRoomFixture(t, 4, true)
	models.DB.Create(&models.SessionPlayer{SessionID: f.sessionID, UserID: f.botID, CharacterCardID: f.poolCardID})

	w := f.do(f.router(f.ownerID, "user"), "POST", "/sessions/"+itoa(f.sessionID)+"/end", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("want 200 (the AI has no coins and must not block ending), got %d: %s", w.Code, w.Body.String())
	}
	var bot, owner models.User
	models.DB.First(&bot, f.botID)
	models.DB.First(&owner, f.ownerID)
	if bot.Coins != 0 {
		t.Errorf("bot coins = %d, want 0", bot.Coins)
	}
	// 手动结束扣 200 手续费，再收到 20 基础金币。
	if want := 1000 - 200 + 20; owner.Coins != want {
		t.Errorf("owner coins = %d, want %d (fee charged, base reward paid)", owner.Coins, want)
	}
}

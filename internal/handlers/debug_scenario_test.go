package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/llmcoc/server/internal/models"
)

func seedAdminOnlyScenario(t *testing.T, name string) uint {
	t.Helper()
	id := seedScenario(t, name)
	if err := models.DB.Model(&models.Scenario{}).Where("id = ?", id).Update("admin_only", true).Error; err != nil {
		t.Fatalf("mark scenario admin_only: %v", err)
	}
	return id
}

func TestCreateSession_AdminOnlyScenario_RejectsNormalUser(t *testing.T) {
	initTestDB(t)
	uid := seedUser(t, "u", "user", 0, 3)
	sid := seedAdminOnlyScenario(t, "Debug")

	r := sessionCRUDRouter(uid, "u", "user")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, jsonReq("POST", "/sessions", map[string]any{"name": "Sneaky", "scenario_id": sid}))

	if w.Code != http.StatusForbidden {
		t.Fatalf("want 403, got %d: %s", w.Code, w.Body.String())
	}
	var count int64
	models.DB.Model(&models.GameSession{}).Count(&count)
	if count != 0 {
		t.Errorf("no session should be created, got %d", count)
	}
}

func TestCreateSession_AdminOnlyScenario_AllowsAdmin(t *testing.T) {
	initTestDB(t)
	uid := seedUser(t, "admin", "admin", 0, 3)
	sid := seedAdminOnlyScenario(t, "Debug")

	r := sessionCRUDRouter(uid, "admin", "admin")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, jsonReq("POST", "/sessions", map[string]any{"name": "Debug Room", "scenario_id": sid}))

	if w.Code != http.StatusCreated {
		t.Fatalf("want 201, got %d: %s", w.Code, w.Body.String())
	}
}

func TestJoinSession_AdminOnlyScenario_RejectsNormalUser(t *testing.T) {
	initTestDB(t)
	adminID := seedUser(t, "admin", "admin", 0, 3)
	sid := seedAdminOnlyScenario(t, "Debug")
	sess := models.GameSession{
		Name: "Debug Room", ScenarioID: sid,
		Status: models.SessionStatusLobby, MaxPlayers: 4, CreatedBy: adminID,
	}
	models.DB.Create(&sess)

	uid := seedUser(t, "u", "user", 0, 3)
	cardID := seedCard(t, uid, "Card")
	r := sessionCRUDRouter(uid, "u", "user")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, jsonReq("POST", fmt.Sprintf("/sessions/%d/join", sess.ID), map[string]any{
		"character_card_id": cardID,
	}))

	if w.Code != http.StatusForbidden {
		t.Fatalf("want 403, got %d: %s", w.Code, w.Body.String())
	}
	var players int64
	models.DB.Model(&models.SessionPlayer{}).Where("session_id = ?", sess.ID).Count(&players)
	if players != 0 {
		t.Errorf("normal user must not be added, got %d players", players)
	}
}

func TestJoinSession_AdminOnlyScenario_AllowsAdmin(t *testing.T) {
	initTestDB(t)
	ownerID := seedUser(t, "owner", "admin", 0, 3)
	sid := seedAdminOnlyScenario(t, "Debug")
	sess := models.GameSession{
		Name: "Debug Room", ScenarioID: sid,
		Status: models.SessionStatusLobby, MaxPlayers: 4, CreatedBy: ownerID,
	}
	models.DB.Create(&sess)

	adminID := seedUser(t, "admin2", "admin", 0, 3)
	cardID := seedCard(t, adminID, "Card")
	r := sessionCRUDRouter(adminID, "admin2", "admin")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, jsonReq("POST", fmt.Sprintf("/sessions/%d/join", sess.ID), map[string]any{
		"character_card_id": cardID,
	}))

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body.String())
	}
}

func TestListSessions_HidesAdminOnlyRoomsFromNormalUsers(t *testing.T) {
	initTestDB(t)
	adminID := seedUser(t, "admin", "admin", 0, 3)
	normal := seedScenario(t, "Normal")
	debug := seedAdminOnlyScenario(t, "Debug")
	models.DB.Create(&models.GameSession{
		Name: "Public Room", ScenarioID: normal,
		Status: models.SessionStatusLobby, MaxPlayers: 4, CreatedBy: adminID,
	})
	models.DB.Create(&models.GameSession{
		Name: "Debug Room", ScenarioID: debug,
		Status: models.SessionStatusLobby, MaxPlayers: 4, CreatedBy: adminID,
	})
	uid := seedUser(t, "u", "user", 0, 3)

	list := func(userID uint, username, role string) PaginatedResponse[models.GameSession] {
		t.Helper()
		r := sessionCRUDRouter(userID, username, role)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, jsonReq("GET", "/sessions", nil))
		if w.Code != http.StatusOK {
			t.Fatalf("want 200, got %d", w.Code)
		}
		var resp PaginatedResponse[models.GameSession]
		if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
			t.Fatalf("decode: %v", err)
		}
		return resp
	}

	normalResp := list(uid, "u", "user")
	if normalResp.Total != 1 || len(normalResp.Items) != 1 || normalResp.Items[0].Name != "Public Room" {
		t.Errorf("normal user should only see the public room, got total=%d items=%d", normalResp.Total, len(normalResp.Items))
	}
	adminResp := list(adminID, "admin", "admin")
	if adminResp.Total != 2 || len(adminResp.Items) != 2 {
		t.Errorf("admin should see both rooms, got total=%d items=%d", adminResp.Total, len(adminResp.Items))
	}
}

func TestListScenarios_HidesAdminOnlyFromNormalUsers(t *testing.T) {
	initTestDB(t)
	seedScenario(t, "Normal")
	seedAdminOnlyScenario(t, "Debug")
	adminID := seedUser(t, "admin", "admin", 0, 3)
	uid := seedUser(t, "u", "user", 0, 3)

	list := func(userID uint, isAdmin bool) ScenarioListResponse {
		t.Helper()
		r := scenarioRouter(userID, isAdmin)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, jsonReq("GET", "/scenarios", nil))
		if w.Code != http.StatusOK {
			t.Fatalf("want 200, got %d", w.Code)
		}
		var resp ScenarioListResponse
		if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
			t.Fatalf("decode: %v", err)
		}
		return resp
	}

	if resp := list(uid, false); resp.Total != 1 || len(resp.Items) != 1 || resp.Items[0].Name != "Normal" {
		t.Errorf("normal user should only see the normal scenario, got total=%d items=%d", resp.Total, len(resp.Items))
	}
	if resp := list(adminID, true); resp.Total != 2 || len(resp.Items) != 2 {
		t.Errorf("admin should see both scenarios, got total=%d items=%d", resp.Total, len(resp.Items))
	}
}

func TestClearScenarios_KeepsAdminOnlyScenario(t *testing.T) {
	initTestDB(t)
	seedScenario(t, "Normal")
	debugID := seedAdminOnlyScenario(t, "Debug")
	uid := seedUser(t, "admin", "admin", 0, 3)

	r := scenarioRouter(uid, true)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, jsonReq("DELETE", "/scenarios", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body.String())
	}

	var debug models.Scenario
	if err := models.DB.First(&debug, debugID).Error; err != nil {
		t.Fatalf("load debug scenario: %v", err)
	}
	if !debug.IsActive {
		t.Errorf("admin-only scenario must survive ClearScenarios")
	}
}

func TestSeedDebugScenario_CreatesAndRestores(t *testing.T) {
	initTestDB(t)

	SeedDebugScenario()
	var s models.Scenario
	if err := models.DB.Where("name = ?", debugScenarioName).First(&s).Error; err != nil {
		t.Fatalf("debug scenario not created: %v", err)
	}
	if !s.IsActive || !s.AdminOnly {
		t.Fatalf("want active+admin_only, got active=%v admin_only=%v", s.IsActive, s.AdminOnly)
	}
	c := s.Content.Data
	if len(c.Scenes) != 0 || len(c.NPCs) != 0 || len(c.Clues) != 0 || len(c.Endings) != 0 {
		t.Errorf("debug scenario must be blank, got %+v", c)
	}

	models.DB.Model(&s).Updates(map[string]any{"is_active": false, "admin_only": false})
	SeedDebugScenario()

	var restored models.Scenario
	models.DB.First(&restored, s.ID)
	if !restored.IsActive || !restored.AdminOnly {
		t.Errorf("seed should restore flags, got active=%v admin_only=%v", restored.IsActive, restored.AdminOnly)
	}
	var count int64
	models.DB.Model(&models.Scenario{}).Where("name = ?", debugScenarioName).Count(&count)
	if count != 1 {
		t.Errorf("seed must be idempotent, got %d rows", count)
	}
}

func TestSeedScenarios_StillImportsWhenOnlyDebugScenarioExists(t *testing.T) {
	initTestDB(t)
	SeedDebugScenario()

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.json"), []byte(`{"name":"导入模组","content":{"setting":"s"}}`), 0o644); err != nil {
		t.Fatalf("write scenario file: %v", err)
	}
	SeedScenarios(dir)

	var count int64
	models.DB.Model(&models.Scenario{}).Where("name = ?", "导入模组").Count(&count)
	if count != 1 {
		t.Errorf("SeedScenarios should import when only admin-only scenarios exist, got %d", count)
	}
}

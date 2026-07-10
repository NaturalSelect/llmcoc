package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/llmcoc/server/internal/models"
)

func adminSettingsRouter() *gin.Engine {
	r := gin.New()
	adm := r.Group("/admin/config", withAuth(1, "admin", "admin"))
	adm.GET("/settings", AdminGetSiteSettings)
	adm.PUT("/settings/:key", AdminUpdateSiteSetting)
	return r
}

// TestAdminUpdateBalanceRules_2000Succeeds verifies that exactly 2000 runes is accepted.
func TestAdminUpdateBalanceRules_2000Succeeds(t *testing.T) {
	initTestDB(t)
	r := adminSettingsRouter()

	val := strings.Repeat("平", 2000)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, jsonReq("PUT", "/admin/config/settings/balance_rules", map[string]any{"value": val}))

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp map[string]any
	json.NewDecoder(w.Body).Decode(&resp)
	if resp["key"] != "balance_rules" {
		t.Errorf("key = %v, want balance_rules", resp["key"])
	}
}

// TestAdminUpdateBalanceRules_2001Fails verifies that 2001 runes returns 400 with Chinese error.
func TestAdminUpdateBalanceRules_2001Fails(t *testing.T) {
	initTestDB(t)
	r := adminSettingsRouter()

	val := strings.Repeat("平", 2001)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, jsonReq("PUT", "/admin/config/settings/balance_rules", map[string]any{"value": val}))

	if w.Code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d: %s", w.Code, w.Body.String())
	}
	var resp map[string]any
	json.NewDecoder(w.Body).Decode(&resp)
	if errMsg, _ := resp["error"].(string); !strings.Contains(errMsg, "2000") {
		t.Errorf("error should mention 2000, got %q", errMsg)
	}
}

// TestAdminUpdateBalanceRules_EmptySucceeds verifies that empty string is accepted (no rules).
func TestAdminUpdateBalanceRules_EmptySucceeds(t *testing.T) {
	initTestDB(t)
	r := adminSettingsRouter()

	w := httptest.NewRecorder()
	r.ServeHTTP(w, jsonReq("PUT", "/admin/config/settings/balance_rules", map[string]any{"value": ""}))

	if w.Code != http.StatusOK {
		t.Fatalf("want 200 for empty value, got %d: %s", w.Code, w.Body.String())
	}

	stored := models.GetSiteSetting("balance_rules", "MISSING")
	if stored != "" {
		t.Errorf("stored balance_rules = %q, want empty string", stored)
	}
}

// TestAdminUpdateSiteSetting_OtherKeySucceeds verifies other settings are unaffected.
func TestAdminUpdateSiteSetting_OtherKeySucceeds(t *testing.T) {
	initTestDB(t)
	r := adminSettingsRouter()

	w := httptest.NewRecorder()
	r.ServeHTTP(w, jsonReq("PUT", "/admin/config/settings/initial_coins", map[string]any{"value": "999"}))

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body.String())
	}
	stored := models.GetSiteSetting("initial_coins", "0")
	if stored != "999" {
		t.Errorf("initial_coins = %q, want 999", stored)
	}
}

// TestAdminUpdateSiteSetting_OtherKeyRejectsEmpty verifies other settings still reject empty value.
func TestAdminUpdateSiteSetting_OtherKeyRejectsEmpty(t *testing.T) {
	initTestDB(t)
	r := adminSettingsRouter()

	w := httptest.NewRecorder()
	r.ServeHTTP(w, jsonReq("PUT", "/admin/config/settings/initial_coins", map[string]any{"value": ""}))

	if w.Code != http.StatusBadRequest {
		t.Fatalf("want 400 for empty value on non-balance key, got %d", w.Code)
	}
}

// TestAdminUpdateBalanceRules_DefaultFallback verifies GetSiteSetting returns
// models.DefaultBalanceRules when the key is absent from the database.
func TestAdminUpdateBalanceRules_DefaultFallback(t *testing.T) {
	initTestDB(t)
	got := models.GetSiteSetting("balance_rules", models.DefaultBalanceRules)
	if got != models.DefaultBalanceRules {
		t.Errorf("expected default balance rules when key missing, got %q", got)
	}
}

// TestAdminUpdateBalanceRules_TrimsWhitespace verifies leading/trailing spaces are trimmed.
func TestAdminUpdateBalanceRules_TrimsWhitespace(t *testing.T) {
	initTestDB(t)
	r := adminSettingsRouter()

	w := httptest.NewRecorder()
	r.ServeHTTP(w, jsonReq("PUT", "/admin/config/settings/balance_rules", map[string]any{"value": "  规则文本  "}))

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body.String())
	}
	stored := models.GetSiteSetting("balance_rules", "MISSING")
	if stored != "规则文本" {
		t.Errorf("stored value should be trimmed, got %q", stored)
	}
}

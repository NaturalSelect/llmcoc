package handlers

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/llmcoc/server/internal/models"
)

func TestAdminUpdateSiteSetting_SessionRetentionAcceptsNonNegativeInteger(t *testing.T) {
	for _, val := range []string{"0", "30", "365"} {
		t.Run(val, func(t *testing.T) {
			initTestDB(t)
			r := adminSettingsRouter()

			w := httptest.NewRecorder()
			r.ServeHTTP(w, jsonReq("PUT", "/admin/config/settings/session_retention_days", map[string]any{"value": val}))
			if w.Code != http.StatusOK {
				t.Fatalf("value %q: want 200, got %d: %s", val, w.Code, w.Body.String())
			}
			if stored := models.GetSiteSetting("session_retention_days", "MISSING"); stored != val {
				t.Errorf("stored = %q, want %q", stored, val)
			}
		})
	}
}

func TestAdminUpdateSiteSetting_SessionRetentionTrimsStoredValue(t *testing.T) {
	initTestDB(t)
	r := adminSettingsRouter()

	w := httptest.NewRecorder()
	r.ServeHTTP(w, jsonReq("PUT", "/admin/config/settings/session_retention_days", map[string]any{"value": " 30 "}))
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body.String())
	}
	if stored := models.GetSiteSetting("session_retention_days", "MISSING"); stored != "30" {
		t.Errorf("stored = %q, want trimmed 30", stored)
	}
}

func TestAdminUpdateSiteSetting_SessionRetentionRejectsInvalid(t *testing.T) {
	for _, val := range []string{"-1", "abc", "1.5", "3天"} {
		t.Run(val, func(t *testing.T) {
			initTestDB(t)
			r := adminSettingsRouter()
			before := models.GetSiteSetting("session_retention_days", "MISSING")

			w := httptest.NewRecorder()
			r.ServeHTTP(w, jsonReq("PUT", "/admin/config/settings/session_retention_days", map[string]any{"value": val}))
			if w.Code != http.StatusBadRequest {
				t.Fatalf("value %q: want 400, got %d: %s", val, w.Code, w.Body.String())
			}
			if after := models.GetSiteSetting("session_retention_days", "MISSING"); after != before {
				t.Errorf("value %q: rejected update must not change the stored value, %q -> %q", val, before, after)
			}
		})
	}
}

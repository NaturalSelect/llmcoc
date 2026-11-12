package handlers

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/llmcoc/server/internal/models"
)

func TestAdminUpdateSiteSetting_LawyerSimilarityAcceptsValidRange(t *testing.T) {
	for _, val := range []string{"0.3", "0", "-1", "1", "-0.25", " 0.45 "} {
		t.Run(val, func(t *testing.T) {
			initTestDB(t)
			r := adminSettingsRouter()

			w := httptest.NewRecorder()
			r.ServeHTTP(w, jsonReq("PUT", "/admin/config/settings/lawyer_cache_min_similarity", map[string]any{"value": val}))
			if w.Code != http.StatusOK {
				t.Fatalf("value %q: want 200, got %d: %s", val, w.Code, w.Body.String())
			}
		})
	}
}

func TestAdminUpdateSiteSetting_LawyerSimilarityTrimsStoredValue(t *testing.T) {
	initTestDB(t)
	r := adminSettingsRouter()

	w := httptest.NewRecorder()
	r.ServeHTTP(w, jsonReq("PUT", "/admin/config/settings/lawyer_cache_min_similarity", map[string]any{"value": " 0.45 "}))
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body.String())
	}
	if stored := models.GetSiteSetting("lawyer_cache_min_similarity", "MISSING"); stored != "0.45" {
		t.Errorf("stored = %q, want trimmed 0.45", stored)
	}
}

func TestAdminUpdateSiteSetting_LawyerSimilarityRejectsInvalid(t *testing.T) {
	for _, val := range []string{"1.01", "-1.01", "abc", "NaN", "Inf", "0,5"} {
		t.Run(val, func(t *testing.T) {
			initTestDB(t)
			r := adminSettingsRouter()
			before := models.GetSiteSetting("lawyer_cache_min_similarity", "MISSING")

			w := httptest.NewRecorder()
			r.ServeHTTP(w, jsonReq("PUT", "/admin/config/settings/lawyer_cache_min_similarity", map[string]any{"value": val}))
			if w.Code != http.StatusBadRequest {
				t.Fatalf("value %q: want 400, got %d: %s", val, w.Code, w.Body.String())
			}
			if after := models.GetSiteSetting("lawyer_cache_min_similarity", "MISSING"); after != before {
				t.Errorf("value %q: rejected update must not change the stored value, %q -> %q", val, before, after)
			}
		})
	}
}

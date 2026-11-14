package handlers

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/llmcoc/server/internal/models"
)

func TestAdminUpdateSiteSetting_InputGuardThresholdAcceptsValidRange(t *testing.T) {
	for _, val := range []string{"0.8", "0.01", "0.5", "1", " 0.65 "} {
		t.Run(val, func(t *testing.T) {
			initTestDB(t)
			r := adminSettingsRouter()

			w := httptest.NewRecorder()
			r.ServeHTTP(w, jsonReq("PUT", "/admin/config/settings/input_guard_block_threshold", map[string]any{"value": val}))
			if w.Code != http.StatusOK {
				t.Fatalf("value %q: want 200, got %d: %s", val, w.Code, w.Body.String())
			}
		})
	}
}

func TestAdminUpdateSiteSetting_InputGuardThresholdTrimsStoredValue(t *testing.T) {
	initTestDB(t)
	r := adminSettingsRouter()

	w := httptest.NewRecorder()
	r.ServeHTTP(w, jsonReq("PUT", "/admin/config/settings/input_guard_block_threshold", map[string]any{"value": " 0.65 "}))
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body.String())
	}
	if stored := models.GetSiteSetting("input_guard_block_threshold", "MISSING"); stored != "0.65" {
		t.Errorf("stored = %q, want trimmed 0.65", stored)
	}
}

func TestAdminUpdateSiteSetting_InputGuardThresholdRejectsInvalid(t *testing.T) {
	// 0 会拦下所有输入，所以下界是开区间；负数、超过 1、非数字都拒绝。
	for _, val := range []string{"0", "-0.1", "1.01", "2", "abc", "NaN", "Inf", "0,5"} {
		t.Run(val, func(t *testing.T) {
			initTestDB(t)
			r := adminSettingsRouter()
			before := models.GetSiteSetting("input_guard_block_threshold", "MISSING")

			w := httptest.NewRecorder()
			r.ServeHTTP(w, jsonReq("PUT", "/admin/config/settings/input_guard_block_threshold", map[string]any{"value": val}))
			if w.Code != http.StatusBadRequest {
				t.Fatalf("value %q: want 400, got %d: %s", val, w.Code, w.Body.String())
			}
			if after := models.GetSiteSetting("input_guard_block_threshold", "MISSING"); after != before {
				t.Errorf("value %q: rejected update must not change the stored value, %q -> %q", val, before, after)
			}
		})
	}
}

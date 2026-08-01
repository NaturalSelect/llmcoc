package game

import (
	"strings"
	"testing"

	"github.com/llmcoc/server/internal/models"
)

func TestGenerateStatsForAge_Boundaries(t *testing.T) {
	cases := []struct {
		age     int
		wantErr bool
	}{
		{14, true},
		{15, false},
		{17, false},
		{19, false},
		{20, false},
		{89, false},
		{90, false},
		{91, true},
	}
	for _, tc := range cases {
		_, _, err := GenerateStatsForAge(tc.age)
		if tc.wantErr && err == nil {
			t.Errorf("age=%d: expected error, got nil", tc.age)
		}
		if !tc.wantErr && err != nil {
			t.Errorf("age=%d: unexpected error: %v", tc.age, err)
		}
	}
}

func TestRollLuck_YouthDoubleDice(t *testing.T) {
	for _, age := range []int{15, 17, 19} {
		result := rollLuck(age)
		if len(result.Rolls) != 2 {
			t.Errorf("age=%d: expected 2 rolls for youth, got %d", age, len(result.Rolls))
		}
		if !strings.Contains(result.Formula, "两次取高") {
			t.Errorf("age=%d: expected double-roll formula, got %q", age, result.Formula)
		}
	}
	for _, age := range []int{14, 20, 80} {
		result := rollLuck(age)
		if len(result.Rolls) != 1 {
			t.Errorf("age=%d: expected 1 roll outside youth range, got %d", age, len(result.Rolls))
		}
	}
}

func TestApplyAgeRules_Youth(t *testing.T) {
	for _, age := range []int{15, 17, 19} {
		_, raw, err := GenerateStatsForAge(age)
		if err != nil {
			t.Fatalf("age=%d: %v", age, err)
		}
		found := false
		for _, entry := range raw.AgeLog {
			if strings.HasPrefix(entry, "15-19岁") {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("age=%d: missing 15-19 age log entry, got %v", age, raw.AgeLog)
		}
	}
}

func TestApplyAgeRules_OldAge90(t *testing.T) {
	_, raw, err := GenerateStatsForAge(90)
	if err != nil {
		t.Fatalf("age=90: %v", err)
	}
	found := false
	for _, entry := range raw.AgeLog {
		if strings.HasPrefix(entry, "80-90岁") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("age=90: missing 80-90 age log entry, got %v", raw.AgeLog)
	}
}

func TestComputeMaxSAN_Human(t *testing.T) {
	cases := []struct {
		cthulhuMythos int
		want          int
	}{
		{0, 99},
		{10, 89},
		{99, 0},
		{100, 0}, // clamp below 0
	}
	for _, tc := range cases {
		got := ComputeMaxSAN(tc.cthulhuMythos, true)
		if got != tc.want {
			t.Errorf("ComputeMaxSAN(%d, true) = %d, want %d", tc.cthulhuMythos, got, tc.want)
		}
	}
}

func TestComputeMaxSAN_NonHuman(t *testing.T) {
	for _, cm := range []int{0, 10, 50, 99} {
		got := ComputeMaxSAN(cm, false)
		if got != 99 {
			t.Errorf("ComputeMaxSAN(%d, false) = %d, want 99 (non-human ignores CthulhuMythos)", cm, got)
		}
	}
}

func TestComputeMaxMP(t *testing.T) {
	cases := []struct {
		pow  int
		want int
	}{
		{50, 10},
		{4, 1},  // below 5, floor at 1
		{0, 1},  // floor at 1
		{100, 20},
	}
	for _, tc := range cases {
		got := ComputeMaxMP(tc.pow)
		if got != tc.want {
			t.Errorf("ComputeMaxMP(%d) = %d, want %d", tc.pow, got, tc.want)
		}
	}
}

// TestApplyDerivedStats_MaxSANFollowsCthulhuMythos 验证 ApplyDerivedStats 会
// 按传入的克苏鲁神话技能值重新计算 MaxSAN，而不是像修复前那样恒为99。
func TestApplyDerivedStats_MaxSANFollowsCthulhuMythos(t *testing.T) {
	stats := models.CharacterStats{STR: 50, CON: 50, SIZ: 50, DEX: 50, POW: 50}
	ApplyDerivedStats(&stats, 30, 15, true, false)
	if stats.MaxSAN != 84 {
		t.Errorf("MaxSAN: got %d, want 84 (99-15)", stats.MaxSAN)
	}

	// 非人类不受克苏鲁神话技能影响。
	stats2 := models.CharacterStats{STR: 50, CON: 50, SIZ: 50, DEX: 50, POW: 50}
	ApplyDerivedStats(&stats2, 30, 15, false, false)
	if stats2.MaxSAN != 99 {
		t.Errorf("MaxSAN (non-human): got %d, want 99", stats2.MaxSAN)
	}
}

// TestApplyDerivedStats_ResetCurrent_SANClampedToMaxSAN 验证 resetCurrent=true
// 重置当前值时，SAN 不会超过按克苏鲁神话技能算出的 MaxSAN。
func TestApplyDerivedStats_ResetCurrent_SANClampedToMaxSAN(t *testing.T) {
	stats := models.CharacterStats{STR: 50, CON: 50, SIZ: 50, DEX: 50, POW: 90}
	ApplyDerivedStats(&stats, 30, 20, true, true)
	// MaxSAN=79，POW=90 应被 clamp 到 79。
	if stats.MaxSAN != 79 {
		t.Errorf("MaxSAN: got %d, want 79", stats.MaxSAN)
	}
	if stats.SAN != 79 {
		t.Errorf("SAN: got %d, want clamped to 79", stats.SAN)
	}
}

package game

import (
	"strings"
	"testing"
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

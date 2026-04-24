package game

import (
	"fmt"
	"math/rand"
	"strings"
	"testing"
)

func TestRollMadnessSymptom(t *testing.T) {
	rand.Seed(1)

	// Test instantaneous madness
	t.Run("Instantaneous", func(t *testing.T) {
		for i := 0; i < 50; i++ {
			symptom := RollMadnessSymptom(true)
			if !symptom.IsInstantaneous {
				t.Errorf("Expected IsInstantaneous to be true")
			}
			if symptom.Duration != "10 轮" {
				t.Errorf("Expected duration to be '10 轮', got %q", symptom.Duration)
			}
			found := false
			for _, desc := range instantaneousSymptoms {
				if symptom.Description == desc {
					found = true
					break
				}
			}
			if !found {
				t.Errorf("Returned description not found in instantaneousSymptoms table: %q", symptom.Description)
			}
		}
	})

	// Test summary madness
	t.Run("Summary", func(t *testing.T) {
		for i := 0; i < 50; i++ {
			symptom := RollMadnessSymptom(false)
			if symptom.IsInstantaneous {
				t.Errorf("Expected IsInstantaneous to be false")
			}
			if !strings.HasPrefix(symptom.Duration, "持续约 ") || !strings.HasSuffix(symptom.Duration, " 小时") {
				t.Errorf("Expected duration to be like '持续约 N 小时', got %q", symptom.Duration)
			}
			found := false
			for _, desc := range summarySymptoms {
				if symptom.Description == desc {
					found = true
					break
				}
			}
			if !found {
				t.Errorf("Returned description not found in summarySymptoms table: %q", symptom.Description)
			}
		}
	})
}

func TestEvalMadness(t *testing.T) {
	testCases := []struct {
		name      string
		loss      int
		newSAN    int
		dailyLoss int
		maxSAN    int
		expected  MadnessKind
	}{
		{"No Madness", 4, 76, 4, 80, MadnessNone},
		{"Permanent Madness (SAN <= 0)", 10, 0, 10, 80, MadnessPermanent},
		{"Permanent Madness (SAN < 0)", 10, -1, 10, 80, MadnessPermanent},
		{"Indefinite Madness (Daily Loss)", 3, 62, 16, 80, MadnessIndefinite}, // 16 >= 80/5
		{"Temporary Madness (Single Loss >= 5)", 5, 75, 5, 80, MadnessTemporary},
		{"Indefinite has priority", 6, 60, 16, 80, MadnessIndefinite}, // dailyLoss rule has priority
		{"Permanent over all", 20, 0, 20, 80, MadnessPermanent},
		{"Zero maxSAN edge case", 5, 50, 5, 0, MadnessTemporary},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result := EvalMadness(tc.loss, tc.newSAN, tc.dailyLoss, tc.maxSAN)
			if result != tc.expected {
				t.Errorf("EvalMadness(%d, %d, %d, %d) = %v, want %v",
					tc.loss, tc.newSAN, tc.dailyLoss, tc.maxSAN, result, tc.expected)
			}
		})
	}
}

func TestItoa(t *testing.T) {
	for i := 1; i <= 10; i++ {
		expected := fmt.Sprintf("%d", i)
		if got := itoa(i); got != expected {
			t.Errorf("itoa(%d) = %q, want %q", i, got, expected)
		}
	}
}

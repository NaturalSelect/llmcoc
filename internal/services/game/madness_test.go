package game

import (
	"fmt"
	"testing"
)

func TestRollMadnessSymptom(t *testing.T) {
	// Test instantaneous madness
	t.Run("Instantaneous", func(t *testing.T) {
		for i := 0; i < 50; i++ {
			symptom := RollMadnessSymptom(true)
			if !symptom.IsInstantaneous {
				t.Errorf("Expected IsInstantaneous to be true")
			}
			if symptom.Duration != 10 {
				t.Errorf("Expected duration to be 10, got %d", symptom.Duration)
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
			if symptom.Duration <= 0 {
				t.Errorf("Expected duration to be positive, got %d", symptom.Duration)
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

func TestItoa(t *testing.T) {
	for i := 1; i <= 10; i++ {
		expected := fmt.Sprintf("%d", i)
		if got := itoa(i); got != expected {
			t.Errorf("itoa(%d) = %q, want %q", i, got, expected)
		}
	}
}

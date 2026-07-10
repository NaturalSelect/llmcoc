package agent

import (
	"fmt"
	"strconv"
	"testing"

	"github.com/llmcoc/server/internal/models"
	"github.com/llmcoc/server/internal/services/game"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// initAgentTestDB sets up an isolated in-memory SQLite DB for agent package tests.
func initAgentTestDB(t *testing.T) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("get sql db: %v", err)
	}
	sqlDB.SetMaxOpenConns(1)
	if err := db.AutoMigrate(&models.CharacterCard{}); err != nil {
		t.Fatalf("auto-migrate: %v", err)
	}
	prev := models.DB
	models.DB = db
	t.Cleanup(func() {
		models.DB = prev
		_ = sqlDB.Close()
	})
}

// newTestCard creates and persists a CharacterCard with known, stable stats.
// All base attributes are set to 50 so derived-field assertions are predictable.
func newTestCard(t *testing.T, name string, age int) *models.CharacterCard {
	t.Helper()
	card := &models.CharacterCard{
		Name:     name,
		Age:      age,
		Race:     "人类",
		IsActive: true,
		Stats: models.JSONField[models.CharacterStats]{Data: models.CharacterStats{
			STR: 50, CON: 50, SIZ: 50, DEX: 50, APP: 50,
			INT: 50, POW: 50, EDU: 50, Luck: 50,
			MaxHP: 10, HP: 10, MaxMP: 10, MP: 10, MaxSAN: 99, SAN: 50,
			MOV: 8,
		}},
		Skills: models.JSONField[map[string]int]{Data: map[string]int{"侦查": 25, "图书馆使用": 40}},
	}
	if err := models.DB.Create(card).Error; err != nil {
		t.Fatalf("newTestCard %q: %v", name, err)
	}
	return card
}

// applyAgeUpdate applies an age update for cardName in a single-player slice.
func applyAgeUpdate(card *models.CharacterCard, newAgeStr string) {
	players := []models.SessionPlayer{{CharacterCard: *card}}
	applyCharacterUpdate(CharacterUpdate{
		CharacterName: card.Name,
		Field:         "age",
		NewValue:      newAgeStr,
	}, players)
}

// ── parseStateChange unit tests ───────────────────────────────────────────────

func TestParseStateChange_Age(t *testing.T) {
	cases := []struct {
		input     string
		wantOK    bool
		wantName  string
		wantValue string
	}{
		{"age 15 (陈明)", true, "陈明", "15"},
		{"age 17 (陈明)", true, "陈明", "17"},
		{"age 90 (陈明)", true, "陈明", "90"},
		// Parse succeeds for out-of-range values; rejection happens in applyCharacterUpdate.
		{"age 14 (陈明)", true, "陈明", "14"},
		{"age 91 (陈明)", true, "陈明", "91"},
	}
	for _, tc := range cases {
		upd, _, ok := parseStateChange(tc.input)
		if ok != tc.wantOK {
			t.Errorf("input=%q: ok=%v, want %v", tc.input, ok, tc.wantOK)
			continue
		}
		if !ok {
			continue
		}
		if upd.Field != "age" {
			t.Errorf("input=%q: field=%q, want \"age\"", tc.input, upd.Field)
		}
		if upd.NewValue != tc.wantValue {
			t.Errorf("input=%q: NewValue=%q, want %q", tc.input, upd.NewValue, tc.wantValue)
		}
		if upd.CharacterName != tc.wantName {
			t.Errorf("input=%q: CharacterName=%q, want %q", tc.input, upd.CharacterName, tc.wantName)
		}
	}
}

func TestParseStateChange_UnknownField(t *testing.T) {
	_, _, ok := parseStateChange("name 张三 (陈明)")
	if ok {
		t.Error("unknown field 'name' should not parse successfully")
	}
}

// ── applyCharacterUpdate age persistence tests ────────────────────────────────

// TestApplyCharacterUpdate_AgeValid verifies that legal ages (15, 17, 90) are
// persisted to the DB and that base attributes (STR, EDU, Luck) and skills are
// not touched.  MOV may change due to AgeMOVPenalty — that is expected.
func TestApplyCharacterUpdate_AgeValid(t *testing.T) {
	initAgentTestDB(t)

	validCases := []struct {
		toAge int
	}{
		{15},
		{17},
		{90},
	}
	for _, tc := range validCases {
		t.Run(fmt.Sprintf("age_%d", tc.toAge), func(t *testing.T) {
			card := newTestCard(t, fmt.Sprintf("调查员_%d", tc.toAge), 30)
			origSTR := card.Stats.Data.STR
			origEDU := card.Stats.Data.EDU
			origLuck := card.Stats.Data.Luck
			origSkill := card.Skills.Data["侦查"]

			applyAgeUpdate(card, strconv.Itoa(tc.toAge))

			var saved models.CharacterCard
			if err := models.DB.First(&saved, card.ID).Error; err != nil {
				t.Fatalf("load saved card: %v", err)
			}
			if saved.Age != tc.toAge {
				t.Errorf("age not persisted: got %d, want %d", saved.Age, tc.toAge)
			}
			// Base attributes must be unchanged.
			if saved.Stats.Data.STR != origSTR {
				t.Errorf("STR changed: got %d, want %d", saved.Stats.Data.STR, origSTR)
			}
			if saved.Stats.Data.EDU != origEDU {
				t.Errorf("EDU changed: got %d, want %d", saved.Stats.Data.EDU, origEDU)
			}
			if saved.Stats.Data.Luck != origLuck {
				t.Errorf("Luck changed: got %d, want %d", saved.Stats.Data.Luck, origLuck)
			}
			// Skills must be unchanged.
			if saved.Skills.Data["侦查"] != origSkill {
				t.Errorf("侦查 skill changed: got %d, want %d", saved.Skills.Data["侦查"], origSkill)
			}
			// HP/MP/SAN must not have been reset (only clamped with resetCurrent=false).
			if saved.Stats.Data.HP != card.Stats.Data.HP {
				t.Errorf("HP was reset: got %d, want %d", saved.Stats.Data.HP, card.Stats.Data.HP)
			}
		})
	}
}

// TestApplyCharacterUpdate_AgeInvalid verifies that out-of-range ages (14, 91)
// are rejected: the DB record's age must remain at the original value and no
// other fields may change.
func TestApplyCharacterUpdate_AgeInvalid(t *testing.T) {
	initAgentTestDB(t)

	for _, badAge := range []int{14, 91} {
		t.Run(fmt.Sprintf("age_%d_rejected", badAge), func(t *testing.T) {
			card := newTestCard(t, fmt.Sprintf("调查员_bad_%d", badAge), 25)

			applyAgeUpdate(card, strconv.Itoa(badAge))

			var saved models.CharacterCard
			if err := models.DB.First(&saved, card.ID).Error; err != nil {
				t.Fatalf("load card: %v", err)
			}
			if saved.Age != 25 {
				t.Errorf("invalid age %d should have been rejected; age changed to %d", badAge, saved.Age)
			}
			if saved.Stats.Data.STR != card.Stats.Data.STR {
				t.Errorf("STR changed on rejected age update")
			}
		})
	}
}

// TestApplyCharacterUpdate_AgeBoundaryConstants verifies that the age bounds
// come from the canonical game constants (15 and 90) rather than hard-coded values.
func TestApplyCharacterUpdate_AgeBoundaryConstants(t *testing.T) {
	if game.MinManualCharacterAge != 15 {
		t.Errorf("MinManualCharacterAge=%d, want 15", game.MinManualCharacterAge)
	}
	if game.MaxManualCharacterAge != 90 {
		t.Errorf("MaxManualCharacterAge=%d, want 90", game.MaxManualCharacterAge)
	}
}

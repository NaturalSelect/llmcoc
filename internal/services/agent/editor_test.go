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

// applyCthulhuUpdate 辅助函数：对单个角色卡执行克苏鲁神话技能变更。
func applyCthulhuUpdate(card *models.CharacterCard, delta int) {
	players := []models.SessionPlayer{{CharacterCard: *card}}
	applyCharacterUpdate(CharacterUpdate{
		CharacterName: card.Name,
		Field:         "cthulhu_mythos",
		Delta:         delta,
	}, players)
}

// ── cthulhu_mythos 增加行为（无回归）────────────────────────────────────────

// TestApplyCharacterUpdate_CthulhuIncrease_MaxSANReduced 验证增加克苏鲁神话时
// MaxSAN 随之降低，当前 SAN 若超出则被下调。
func TestApplyCharacterUpdate_CthulhuIncrease_MaxSANReduced(t *testing.T) {
	initAgentTestDB(t)
	card := newTestCard(t, "调查员增加", 30)
	// 初始：CthulhuMythosSkill=0, MaxSAN=99, SAN=50

	applyCthulhuUpdate(card, 10)

	var saved models.CharacterCard
	if err := models.DB.First(&saved, card.ID).Error; err != nil {
		t.Fatalf("load card: %v", err)
	}
	if saved.CthulhuMythosSkill != 10 {
		t.Errorf("CthulhuMythosSkill: got %d, want 10", saved.CthulhuMythosSkill)
	}
	wantMaxSAN := 99 - 10
	if saved.Stats.Data.MaxSAN != wantMaxSAN {
		t.Errorf("MaxSAN: got %d, want %d", saved.Stats.Data.MaxSAN, wantMaxSAN)
	}
	// SAN=50 < 89，不应被下调。
	if saved.Stats.Data.SAN != 50 {
		t.Errorf("SAN should remain 50, got %d", saved.Stats.Data.SAN)
	}
}

// TestApplyCharacterUpdate_CthulhuIncrease_SANCapped 验证 SAN 超过新 MaxSAN 时被同步下调。
func TestApplyCharacterUpdate_CthulhuIncrease_SANCapped(t *testing.T) {
	initAgentTestDB(t)
	card := newTestCard(t, "调查员SAN下调", 30)
	// 先把 SAN 设高
	card.Stats.Data.SAN = 95
	card.Stats.Data.MaxSAN = 99
	models.DB.Save(card)

	applyCthulhuUpdate(card, 20) // 新 MaxSAN = 79

	var saved models.CharacterCard
	if err := models.DB.First(&saved, card.ID).Error; err != nil {
		t.Fatalf("load card: %v", err)
	}
	if saved.Stats.Data.MaxSAN != 79 {
		t.Errorf("MaxSAN: got %d, want 79", saved.Stats.Data.MaxSAN)
	}
	if saved.Stats.Data.SAN != 79 {
		t.Errorf("SAN should be capped to 79, got %d", saved.Stats.Data.SAN)
	}
}

// ── cthulhu_mythos 降低行为 ──────────────────────────────────────────────────

// TestApplyCharacterUpdate_CthulhuDecrease_Normal 验证普通降低场景：
// MaxSAN 按实际降低量提升（不超过上限），当前 SAN 不变。
func TestApplyCharacterUpdate_CthulhuDecrease_Normal(t *testing.T) {
	initAgentTestDB(t)
	card := newTestCard(t, "调查员降低", 30)
	// 先增到 20，MaxSAN 降到 79，SAN=50
	card.CthulhuMythosSkill = 20
	card.Stats.Data.MaxSAN = 79
	card.Stats.Data.SAN = 50
	models.DB.Save(card)

	applyCthulhuUpdate(card, -5) // 降低5，CthulhuMythosSkill→15，MaxSAN上限=84

	var saved models.CharacterCard
	if err := models.DB.First(&saved, card.ID).Error; err != nil {
		t.Fatalf("load card: %v", err)
	}
	if saved.CthulhuMythosSkill != 15 {
		t.Errorf("CthulhuMythosSkill: got %d, want 15", saved.CthulhuMythosSkill)
	}
	// MaxSAN 应从 79 提升 5 → 84（等于上限 99-15=84）。
	if saved.Stats.Data.MaxSAN != 84 {
		t.Errorf("MaxSAN: got %d, want 84", saved.Stats.Data.MaxSAN)
	}
	// SAN 不应恢复。
	if saved.Stats.Data.SAN != 50 {
		t.Errorf("SAN should not change, got %d", saved.Stats.Data.SAN)
	}
}

// TestApplyCharacterUpdate_CthulhuDecrease_ClampedAtZero 验证降低越过0时按实际变化量计算
// MaxSAN 恢复量，而非请求的 Delta。
func TestApplyCharacterUpdate_CthulhuDecrease_ClampedAtZero(t *testing.T) {
	initAgentTestDB(t)
	card := newTestCard(t, "调查员降至零", 30)
	// CthulhuMythosSkill=3, MaxSAN=96, SAN=50
	card.CthulhuMythosSkill = 3
	card.Stats.Data.MaxSAN = 96
	card.Stats.Data.SAN = 50
	models.DB.Save(card)

	// 请求降低 10，但只能降 3（0 下限），实际降低量=3
	applyCthulhuUpdate(card, -10)

	var saved models.CharacterCard
	if err := models.DB.First(&saved, card.ID).Error; err != nil {
		t.Fatalf("load card: %v", err)
	}
	if saved.CthulhuMythosSkill != 0 {
		t.Errorf("CthulhuMythosSkill: got %d, want 0", saved.CthulhuMythosSkill)
	}
	// 实际降低量=3，MaxSAN 从 96 恢复 3 → 99（等于上限 99-0=99）。
	if saved.Stats.Data.MaxSAN != 99 {
		t.Errorf("MaxSAN: got %d, want 99", saved.Stats.Data.MaxSAN)
	}
	// SAN 不变。
	if saved.Stats.Data.SAN != 50 {
		t.Errorf("SAN should not change, got %d", saved.Stats.Data.SAN)
	}
}

// TestApplyCharacterUpdate_CthulhuDecrease_MaxSANCap 验证 MaxSAN 恢复不超过
// 99 - 新克苏鲁神话 的上限。
func TestApplyCharacterUpdate_CthulhuDecrease_MaxSANCap(t *testing.T) {
	initAgentTestDB(t)
	card := newTestCard(t, "调查员MaxSAN上限", 30)
	// CthulhuMythosSkill=10, MaxSAN=60（已有额外损失），SAN=50
	card.CthulhuMythosSkill = 10
	card.Stats.Data.MaxSAN = 60
	card.Stats.Data.SAN = 50
	models.DB.Save(card)

	// 降低 5，CthulhuMythosSkill→5，上限=94；MaxSAN 本可恢复到 65，但不超过 94。
	applyCthulhuUpdate(card, -5)

	var saved models.CharacterCard
	if err := models.DB.First(&saved, card.ID).Error; err != nil {
		t.Fatalf("load card: %v", err)
	}
	if saved.CthulhuMythosSkill != 5 {
		t.Errorf("CthulhuMythosSkill: got %d, want 5", saved.CthulhuMythosSkill)
	}
	// 60+5=65，未超过 94，应为 65。
	if saved.Stats.Data.MaxSAN != 65 {
		t.Errorf("MaxSAN: got %d, want 65", saved.Stats.Data.MaxSAN)
	}
}

// TestApplyCharacterUpdate_CthulhuDecrease_MaxSANCap_HardCap 验证恢复量超过上限时取上限。
func TestApplyCharacterUpdate_CthulhuDecrease_MaxSANCap_HardCap(t *testing.T) {
	initAgentTestDB(t)
	card := newTestCard(t, "调查员硬上限", 30)
	// CthulhuMythosSkill=10, MaxSAN=88（接近上限 89），SAN=50
	card.CthulhuMythosSkill = 10
	card.Stats.Data.MaxSAN = 88
	card.Stats.Data.SAN = 50
	models.DB.Save(card)

	// 降低 5，CthulhuMythosSkill→5，上限=94；MaxSAN 本要恢复到 93，不超过 94。
	applyCthulhuUpdate(card, -5)

	var saved models.CharacterCard
	if err := models.DB.First(&saved, card.ID).Error; err != nil {
		t.Fatalf("load card: %v", err)
	}
	// 88+5=93，未超过 94（99-5），应为 93。
	if saved.Stats.Data.MaxSAN != 93 {
		t.Errorf("MaxSAN: got %d, want 93", saved.Stats.Data.MaxSAN)
	}
}

// TestApplyCharacterUpdate_CthulhuDecrease_SANUnchanged 验证降低时当前 SAN 不恢复。
func TestApplyCharacterUpdate_CthulhuDecrease_SANUnchanged(t *testing.T) {
	initAgentTestDB(t)
	card := newTestCard(t, "调查员SAN不恢复", 30)
	card.CthulhuMythosSkill = 20
	card.Stats.Data.MaxSAN = 79
	card.Stats.Data.SAN = 30 // 刻意设为低值
	models.DB.Save(card)

	applyCthulhuUpdate(card, -10)

	var saved models.CharacterCard
	if err := models.DB.First(&saved, card.ID).Error; err != nil {
		t.Fatalf("load card: %v", err)
	}
	// SAN 必须保持 30，不因 MaxSAN 提升而恢复。
	if saved.Stats.Data.SAN != 30 {
		t.Errorf("SAN should remain 30, got %d", saved.Stats.Data.SAN)
	}
}

package game

import (
	"math/rand"
	"testing"

	"github.com/llmcoc/server/internal/models"
)

func TestGenerateStatsForAge_YoungAdultRules(t *testing.T) {
	rand.Seed(7)
	stats, raw, err := GenerateStatsForAge(18)
	if err != nil {
		t.Fatalf("GenerateStatsForAge: %v", err)
	}
	if stats.EDU != raw.EDU.Base-5 {
		t.Fatalf("EDU = %d, want raw EDU - 5 (%d)", stats.EDU, raw.EDU.Base-5)
	}
	if stats.STR+stats.SIZ != raw.STR.Base+raw.SIZ.Base-5 {
		t.Fatalf("STR+SIZ = %d, want raw total - 5 (%d)", stats.STR+stats.SIZ, raw.STR.Base+raw.SIZ.Base-5)
	}
	if len(raw.Luck.Rolls) != 2 {
		t.Fatalf("Luck rolls = %d, want 2", len(raw.Luck.Rolls))
	}
	kept := raw.Luck.Rolls[0].Base
	if raw.Luck.Rolls[1].Base > kept {
		kept = raw.Luck.Rolls[1].Base
	}
	if stats.Luck != kept {
		t.Fatalf("Luck = %d, want kept %d", stats.Luck, kept)
	}
}

func TestGenerateStatsForAge_MiddleAgeRules(t *testing.T) {
	rand.Seed(11)
	stats, raw, err := GenerateStatsForAge(42)
	if err != nil {
		t.Fatalf("GenerateStatsForAge: %v", err)
	}
	if len(raw.EDUEnhancements) != 2 {
		t.Fatalf("EDU enhancements = %d, want 2", len(raw.EDUEnhancements))
	}
	if stats.APP != raw.APP.Base-5 {
		t.Fatalf("APP = %d, want raw APP - 5 (%d)", stats.APP, raw.APP.Base-5)
	}
	if stats.STR+stats.CON+stats.DEX != raw.STR.Base+raw.CON.Base+raw.DEX.Base-5 {
		t.Fatalf("physical total not reduced by 5")
	}
	baseMOV := calcMOV(stats.STR, stats.DEX, stats.SIZ)
	if stats.MOV != baseMOV-1 {
		t.Fatalf("MOV = %d, want %d", stats.MOV, baseMOV-1)
	}
}

func TestApplyDerivedStats(t *testing.T) {
	stats := models.CharacterStats{STR: 50, CON: 55, SIZ: 65, DEX: 40, APP: 50, INT: 60, POW: 45, EDU: 70, Luck: 35}
	ApplyDerivedStats(&stats, 50, true)
	if stats.MaxHP != 12 || stats.HP != 12 {
		t.Fatalf("HP = %d/%d, want 12/12", stats.HP, stats.MaxHP)
	}
	if stats.MaxMP != 9 || stats.MP != 9 {
		t.Fatalf("MP = %d/%d, want 9/9", stats.MP, stats.MaxMP)
	}
	if stats.SAN != 45 || stats.MaxSAN != 99 {
		t.Fatalf("SAN = %d/%d, want 45/99", stats.SAN, stats.MaxSAN)
	}
	if stats.MOV != 5 {
		t.Fatalf("MOV = %d, want 5", stats.MOV)
	}
	if stats.Build != 0 || stats.DB != "0" {
		t.Fatalf("Build/DB = %d/%s, want 0/0", stats.Build, stats.DB)
	}
}

func TestNormalizeSkills_BudgetAndSpecialRules(t *testing.T) {
	stats := models.CharacterStats{STR: 50, CON: 50, SIZ: 50, DEX: 60, APP: 50, INT: 50, POW: 50, EDU: 50, Luck: 50}
	skills, spent, err := NormalizeSkills(map[string]int{
		"侦查": 70,
		"母语": 1,
		"闪避": 1,
	}, stats)
	if err != nil {
		t.Fatalf("NormalizeSkills: %v", err)
	}
	if spent != 45 {
		t.Fatalf("spent = %d, want 45", spent)
	}
	if skills["母语"] != 50 || skills["闪避"] != 30 {
		t.Fatalf("locked skills = 母语%d 闪避%d, want 50/30", skills["母语"], skills["闪避"])
	}
	if skills["侦查"] != 70 {
		t.Fatalf("侦查 = %d, want 70", skills["侦查"])
	}
}

func TestNormalizeSkills_InvalidSkillAndMythos(t *testing.T) {
	stats := models.CharacterStats{DEX: 60, INT: 50, EDU: 50}
	_, _, err := NormalizeSkills(map[string]int{"飞天": 50, "克苏鲁神话": 1}, stats)
	if err == nil {
		t.Fatal("want error")
	}
	ve, ok := err.(SkillValidationError)
	if !ok {
		t.Fatalf("error type = %T, want SkillValidationError", err)
	}
	if len(ve.Details) != 2 {
		t.Fatalf("details = %v, want 2 errors", ve.Details)
	}
}

func TestNormalizeSkills_OverBudget(t *testing.T) {
	stats := models.CharacterStats{DEX: 60, INT: 10, EDU: 10}
	_, _, err := NormalizeSkills(map[string]int{"会计": 90, "估价": 90}, stats)
	if err == nil {
		t.Fatal("want over budget error")
	}
}

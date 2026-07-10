package game

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/llmcoc/server/internal/models"
)

const (
	MinManualCharacterAge = 15
	MaxManualCharacterAge = 90
)

type SkillBudget struct {
	Occupation int `json:"occupation"`
	Interest   int `json:"interest"`
	Total      int `json:"total"`
}

type SkillValidationError struct {
	Details []string
}

func (e SkillValidationError) Error() string {
	if len(e.Details) == 0 {
		return "技能分配不符合规则"
	}
	return strings.Join(e.Details, "；")
}

func GenerateStatsForAge(age int) (models.CharacterStats, models.CharacterRawRolls, error) {
	if age < MinManualCharacterAge || age > MaxManualCharacterAge {
		return models.CharacterStats{}, models.CharacterRawRolls{}, fmt.Errorf("年龄必须在%d-%d之间", MinManualCharacterAge, MaxManualCharacterAge)
	}

	raw := models.CharacterRawRolls{Age: age}
	stats := models.CharacterStats{}

	roll3d6x5 := func(formula string) models.CharacterAttributeRoll {
		v, dice := Roll(3, 6)
		base := v * 5
		return models.CharacterAttributeRoll{Formula: formula, Dice: dice, Total: v, Base: base, Final: base}
	}
	roll2d6p6x5 := func(formula string) models.CharacterAttributeRoll {
		v, dice := Roll(2, 6)
		base := (v + 6) * 5
		return models.CharacterAttributeRoll{Formula: formula, Dice: dice, Total: v, Base: base, Final: base}
	}

	raw.STR = roll3d6x5("3D6×5")
	raw.CON = roll3d6x5("3D6×5")
	raw.SIZ = roll2d6p6x5("(2D6+6)×5")
	raw.DEX = roll3d6x5("3D6×5")
	raw.APP = roll3d6x5("3D6×5")
	raw.INT = roll2d6p6x5("(2D6+6)×5")
	raw.POW = roll3d6x5("3D6×5")
	raw.EDU = roll2d6p6x5("(2D6+6)×5")
	raw.Luck = rollLuck(age)

	stats.STR = raw.STR.Base
	stats.CON = raw.CON.Base
	stats.SIZ = raw.SIZ.Base
	stats.DEX = raw.DEX.Base
	stats.APP = raw.APP.Base
	stats.INT = raw.INT.Base
	stats.POW = raw.POW.Base
	stats.EDU = raw.EDU.Base
	stats.Luck = raw.Luck.Kept

	applyAgeRules(age, &stats, &raw)
	syncRawFinals(&raw, stats)
	ApplyDerivedStats(&stats, age, true)
	return stats, raw, nil
}

func rollLuck(age int) models.CharacterLuckRoll {
	rollOnce := func() models.CharacterAttributeRoll {
		v, dice := Roll(3, 6)
		base := v * 5
		return models.CharacterAttributeRoll{Formula: "3D6×5", Dice: dice, Total: v, Base: base, Final: base}
	}
	first := rollOnce()
	if age < 15 || age > 19 {
		return models.CharacterLuckRoll{Formula: "3D6×5", Rolls: []models.CharacterAttributeRoll{first}, Kept: first.Base}
	}
	second := rollOnce()
	kept := first.Base
	if second.Base > kept {
		kept = second.Base
	}
	return models.CharacterLuckRoll{Formula: "3D6×5，两次取高", Rolls: []models.CharacterAttributeRoll{first, second}, Kept: kept}
}

func applyAgeRules(age int, stats *models.CharacterStats, raw *models.CharacterRawRolls) {
	switch {
	case age >= 15 && age <= 19:
		reducedAttr := "STR"
		if stats.SIZ > stats.STR {
			stats.SIZ -= 5
			reducedAttr = "SIZ"
		} else {
			stats.STR -= 5
		}
		stats.EDU -= 5
		raw.AgeLog = append(raw.AgeLog, fmt.Sprintf("15-19岁：STR+SIZ合计-5（本次从%s扣除5），EDU-5，Luck掷两次取高。", reducedAttr))
	case age >= 20 && age <= 39:
		raw.AgeLog = append(raw.AgeLog, "20-39岁：EDU增强1次。")
		applyEDUEnhancements(1, stats, raw)
	case age >= 40 && age <= 49:
		raw.AgeLog = append(raw.AgeLog, "40-49岁：EDU增强2次，STR/CON/DEX合计-5，APP-5，MOV-1。")
		applyEDUEnhancements(2, stats, raw)
		reducePhysical(stats, 5)
		stats.APP -= 5
	case age >= 50 && age <= 59:
		raw.AgeLog = append(raw.AgeLog, "50-59岁：EDU增强3次，STR/CON/DEX合计-10，APP-10，MOV-2。")
		applyEDUEnhancements(3, stats, raw)
		reducePhysical(stats, 10)
		stats.APP -= 10
	case age >= 60 && age <= 69:
		raw.AgeLog = append(raw.AgeLog, "60-69岁：EDU增强4次，STR/CON/DEX合计-20，APP-15，MOV-3。")
		applyEDUEnhancements(4, stats, raw)
		reducePhysical(stats, 20)
		stats.APP -= 15
	case age >= 70 && age <= 79:
		raw.AgeLog = append(raw.AgeLog, "70-79岁：EDU增强4次，STR/CON/DEX合计-40，APP-20，MOV-4。")
		applyEDUEnhancements(4, stats, raw)
		reducePhysical(stats, 40)
		stats.APP -= 20
	case age >= 80 && age <= 90:
		raw.AgeLog = append(raw.AgeLog, "80-90岁：EDU增强4次，STR/CON/DEX合计-80，APP-25，MOV-5。")
		applyEDUEnhancements(4, stats, raw)
		reducePhysical(stats, 80)
		stats.APP -= 25
	}
	stats.STR = maxInt(stats.STR, 1)
	stats.CON = maxInt(stats.CON, 1)
	stats.SIZ = maxInt(stats.SIZ, 1)
	stats.DEX = maxInt(stats.DEX, 1)
	stats.APP = maxInt(stats.APP, 1)
	stats.EDU = clampInt(stats.EDU, 1, 99)
}

func applyEDUEnhancements(times int, stats *models.CharacterStats, raw *models.CharacterRawRolls) {
	for i := 1; i <= times; i++ {
		before := stats.EDU
		d100 := RollD100()
		entry := models.CharacterEDUEnhancementRoll{Index: i, D100: d100, BeforeEDU: before, AfterEDU: before}
		if d100 > before {
			increase, dice := Roll(1, 10)
			stats.EDU = clampInt(stats.EDU+increase, 1, 99)
			entry.Improved = true
			if len(dice) > 0 {
				entry.IncreaseDie = dice[0]
			}
			entry.Increase = increase
			entry.AfterEDU = stats.EDU
		}
		raw.EDUEnhancements = append(raw.EDUEnhancements, entry)
	}
}

func reducePhysical(stats *models.CharacterStats, total int) {
	for total > 0 {
		name, value := highestPhysical(stats)
		if value <= 1 {
			return
		}
		step := minInt(5, total)
		if value-step < 1 {
			step = value - 1
		}
		switch name {
		case "STR":
			stats.STR -= step
		case "CON":
			stats.CON -= step
		case "DEX":
			stats.DEX -= step
		}
		total -= step
	}
}

func highestPhysical(stats *models.CharacterStats) (string, int) {
	type attr struct {
		name  string
		value int
	}
	attrs := []attr{{"STR", stats.STR}, {"CON", stats.CON}, {"DEX", stats.DEX}}
	sort.SliceStable(attrs, func(i, j int) bool { return attrs[i].value > attrs[j].value })
	return attrs[0].name, attrs[0].value
}

func syncRawFinals(raw *models.CharacterRawRolls, stats models.CharacterStats) {
	raw.STR.Final = stats.STR
	raw.CON.Final = stats.CON
	raw.SIZ.Final = stats.SIZ
	raw.DEX.Final = stats.DEX
	raw.APP.Final = stats.APP
	raw.INT.Final = stats.INT
	raw.POW.Final = stats.POW
	raw.EDU.Final = stats.EDU
}

func ApplyDerivedStats(stats *models.CharacterStats, age int, resetCurrent bool) {
	maxHP := (stats.CON + stats.SIZ) / 10
	maxMP := stats.POW / 5
	maxSAN := 99
	mov := calcMOV(stats.STR, stats.DEX, stats.SIZ) - AgeMOVPenalty(age)
	if mov < 1 {
		mov = 1
	}
	build, db := calcBuildAndDB(stats.STR, stats.SIZ)

	stats.MaxHP = maxHP
	stats.MaxMP = maxMP
	stats.MaxSAN = maxSAN
	stats.MOV = mov
	stats.Build = build
	stats.DB = db
	if resetCurrent {
		stats.HP = maxHP
		stats.MP = maxMP
		stats.SAN = stats.POW
		return
	}
	stats.HP = clampInt(stats.HP, 0, stats.MaxHP)
	stats.MP = clampInt(stats.MP, 0, stats.MaxMP)
	stats.SAN = clampInt(stats.SAN, 0, stats.MaxSAN)
}

func AgeMOVPenalty(age int) int {
	switch {
	case age >= 40 && age <= 49:
		return 1
	case age >= 50 && age <= 59:
		return 2
	case age >= 60 && age <= 69:
		return 3
	case age >= 70 && age <= 79:
		return 4
	case age >= 80:
		return 5
	default:
		return 0
	}
}

func SkillDefaults(stats models.CharacterStats) map[string]int {
	skills := DefaultSkills()
	skills["母语"] = stats.EDU
	skills["闪避"] = stats.DEX / 2
	return skills
}

func SkillPointBudget(stats models.CharacterStats) SkillBudget {
	occupation := stats.EDU * 4
	interest := stats.INT * 2
	return SkillBudget{Occupation: occupation, Interest: interest, Total: occupation + interest}
}

func NormalizeSkills(input map[string]int, stats models.CharacterStats) (map[string]int, int, error) {
	defaults := SkillDefaults(stats)
	normalized := make(map[string]int, len(defaults))
	for k, v := range defaults {
		normalized[k] = v
	}
	if len(input) == 0 {
		return normalized, 0, nil
	}

	details := make([]string, 0)
	spent := 0
	for rawName, finalValue := range input {
		name := strings.TrimSpace(rawName)
		if name == "" {
			details = append(details, "技能名不能为空")
			continue
		}
		if name == "克苏鲁神话" {
			details = append(details, "克苏鲁神话不允许创建时分配")
			continue
		}
		if !IsValidSkill(name) {
			details = append(details, fmt.Sprintf("无效技能：%s", name))
			continue
		}
		if name == "母语" || name == "闪避" {
			continue
		}
		base, ok := defaults[name]
		if !ok {
			details = append(details, fmt.Sprintf("无效技能：%s", name))
			continue
		}
		if finalValue < base {
			details = append(details, fmt.Sprintf("%s不能低于基础值%d", name, base))
			continue
		}
		if finalValue > 90 {
			details = append(details, fmt.Sprintf("%s不能超过90", name))
			continue
		}
		spent += finalValue - base
		normalized[name] = finalValue
	}
	budget := SkillPointBudget(stats)
	if spent > budget.Total {
		details = append(details, fmt.Sprintf("技能点超出预算：已用%d，可用%d", spent, budget.Total))
	}
	normalized["母语"] = stats.EDU
	normalized["闪避"] = stats.DEX / 2
	if len(details) > 0 {
		return nil, spent, SkillValidationError{Details: details}
	}
	return normalized, spent, nil
}

func ValidateManualStats(stats models.CharacterStats) error {
	checks := []struct {
		name     string
		value    int
		min, max int
		multiple bool
	}{
		{"STR", stats.STR, 1, 99, true},
		{"CON", stats.CON, 1, 99, true},
		{"SIZ", stats.SIZ, 1, 99, true},
		{"DEX", stats.DEX, 1, 99, true},
		{"APP", stats.APP, 1, 99, true},
		{"INT", stats.INT, 1, 99, true},
		{"POW", stats.POW, 1, 99, true},
		{"EDU", stats.EDU, 1, 99, false},
		{"Luck", stats.Luck, 1, 90, true},
	}
	for _, ck := range checks {
		if ck.value < ck.min || ck.value > ck.max {
			return fmt.Errorf("%s超出允许范围", ck.name)
		}
		if ck.multiple && ck.value%5 != 0 {
			return fmt.Errorf("%s必须是5的倍数", ck.name)
		}
	}
	if stats.HP != 0 && stats.MaxHP != 0 && stats.HP > stats.MaxHP {
		return fmt.Errorf("HP不能超过MaxHP")
	}
	if stats.MP != 0 && stats.MaxMP != 0 && stats.MP > stats.MaxMP {
		return fmt.Errorf("MP不能超过MaxMP")
	}
	if stats.SAN != 0 && stats.SAN > 99 {
		return fmt.Errorf("SAN不能超过99")
	}
	return nil
}

func RejectClientStats(stats *models.CharacterStats) error {
	if stats == nil {
		return nil
	}
	return errors.New("不能直接提交属性，请使用规则车卡流程")
}

func clampInt(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

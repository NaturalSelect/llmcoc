// NOTE: Implements core game mechanics such as dice rolling.
package game

import (
	"fmt"
	"math/rand"
	"strconv"
	"strings"

	"github.com/llmcoc/server/internal/models"
)

// Roll rolls NdM and returns the result and individual dice
func Roll(n, m int) (int, []int) {
	dice := make([]int, n)
	total := 0
	for i := range dice {
		dice[i] = rand.Intn(m) + 1
		total += dice[i]
	}
	return total, dice
}

// RollD100 returns a D100 result
func RollD100() int {
	v, _ := Roll(1, 100)
	return v
}

type SkillCheckResult struct {
	Value   int    `json:"value"` // dice result
	Skill   int    `json:"skill"` // skill value
	Success bool   `json:"success"`
	Level   string `json:"level"` // fumble / fail / success / hard / extreme / critical
	Message string `json:"message"`
}

// SkillCheck performs a COC 7th edition D100 skill check
func SkillCheck(skillValue int) SkillCheckResult {
	roll := RollD100()
	result := SkillCheckResult{
		Value: roll,
		Skill: skillValue,
	}

	extreme := skillValue / 5
	hard := skillValue / 2

	switch {
	case roll == 1:
		result.Success = true
		result.Level = "critical"
		result.Message = fmt.Sprintf("🎯 大成功！(掷出 %d)", roll)
	case roll <= extreme:
		result.Success = true
		result.Level = "extreme"
		result.Message = fmt.Sprintf("✨ 极难成功！(掷出 %d，极难值 %d)", roll, extreme)
	case roll <= hard:
		result.Success = true
		result.Level = "hard"
		result.Message = fmt.Sprintf("✅ 困难成功！(掷出 %d，困难值 %d)", roll, hard)
	case roll <= skillValue:
		result.Success = true
		result.Level = "success"
		result.Message = fmt.Sprintf("✅ 普通成功！(掷出 %d，技能值 %d)", roll, skillValue)
	case roll == 100 || (roll >= 96 && skillValue < 50):
		result.Success = false
		result.Level = "fumble"
		result.Message = fmt.Sprintf("💀 大失败！(掷出 %d)", roll)
	default:
		result.Success = false
		result.Level = "fail"
		result.Message = fmt.Sprintf("❌ 失败！(掷出 %d，技能值 %d)", roll, skillValue)
	}
	return result
}

// RollDiceExpr parses and evaluates a simple dice expression such as "0", "1",
// "1D6", "1D6+1", "2D10", "1D10+2". Returns 0 for empty or invalid input.
// Case-insensitive ("1d6" and "1D6" are equivalent).
func RollDiceExpr(expr string) int {
	expr = strings.TrimSpace(strings.ToUpper(expr))
	if expr == "" {
		return 0
	}
	// Plain integer
	if v, err := strconv.Atoi(expr); err == nil {
		return v
	}

	// Use the same tokenizer as DamageRoll
	tokens := tokenizeDamageFormula(expr)
	total := 0
	for _, tok := range tokens {
		// evalDamageToken handles "1D6", "+2D4", "-1", etc.
		val, err := evalDamageToken(tok)
		if err != nil {
			// On simple dice expression error, return 0 as per original function's contract
			return 0
		}
		total += val
	}
	return total
}

// GenerateStats generates COC 7th edition character statistics
func GenerateStats() models.CharacterStats {
	roll3d6x5 := func() int {
		v, _ := Roll(3, 6)
		return v * 5
	}
	roll2d6p6x5 := func() int {
		v, _ := Roll(2, 6)
		return (v + 6) * 5
	}
	roll3d6 := func() int {
		v, _ := Roll(3, 6)
		return v * 5
	}
	_ = roll3d6

	str := roll3d6x5()
	con := roll3d6x5()
	siz := roll2d6p6x5()
	dex := roll3d6x5()
	app := roll3d6x5()
	intel := roll2d6p6x5()
	pow := roll3d6x5()
	edu := roll2d6p6x5()

	luckVal, _ := Roll(3, 6)
	luck := luckVal * 5

	hp := (con + siz) / 10
	mp := pow / 5
	san := pow
	// COC 7th: 最大理智值固定为99(规则书"每位调查员能拥有的最大理智值都是99")，
	// 随克苏鲁神话技能增长而降低(99 - cthulhu_mythos)。
	maxSAN := 99
	mov := calcMOV(str, dex, siz)
	build, db := calcBuildAndDB(str, siz)

	return models.CharacterStats{
		STR: str, CON: con, SIZ: siz, DEX: dex,
		APP: app, INT: intel, POW: pow, EDU: edu,
		HP: hp, MaxHP: hp,
		MP: mp, MaxMP: mp,
		SAN: san, MaxSAN: maxSAN,
		Luck:  luck,
		MOV:   mov,
		Build: build,
		DB:    db,
	}
}

func calcMOV(str, dex, siz int) int {
	if str > siz && dex > siz {
		return 9
	}
	if str < siz && dex < siz {
		return 7
	}
	return 8
}

func calcBuildAndDB(str, siz int) (int, string) {
	combined := str + siz
	switch {
	case combined <= 64:
		return -2, "-2"
	case combined <= 84:
		return -1, "-1"
	case combined <= 124:
		return 0, "0"
	case combined <= 164:
		return 1, "1D4"
	case combined <= 204:
		return 2, "1D6"
	case combined <= 284:
		return 3, "2D6"
	default:
		// 规则书：合计值≥285时每超过80点(不足80按80算)增加+1体格和+1D6伤害加值
		build := 4 + (combined-285)/80
		return build, fmt.Sprintf("%dD6", build-1)
	}
}

// ── 奖励骰/惩罚骰 ────────────────────────────────────────────────────────────

// SkillCheckWithModifier performs a COC skill check with bonus/penalty dice.
// bonus and penalty dice cancel each other; net effect determines which ten-die roll to keep.
// Net positive bonus: keep the lower tens die (better result).
// Net positive penalty: keep the higher tens die (worse result).
func SkillCheckWithModifier(skillValue, bonusDice, penaltyDice int) SkillCheckResult {
	net := bonusDice - penaltyDice
	if net == 0 {
		return SkillCheck(skillValue)
	}

	// Roll units die once (0-9)
	units := rand.Intn(10)

	// Roll abs(net)+1 tens dice (0,10,20,...,90)
	count := abs(net) + 1
	tensDice := make([]int, count)
	for i := range tensDice {
		tensDice[i] = rand.Intn(10) * 10
	}

	// Pick the tens die: bonus→lowest, penalty→highest
	tens := tensDice[0]
	for _, t := range tensDice[1:] {
		if net > 0 && t < tens {
			tens = t
		} else if net < 0 && t > tens {
			tens = t
		}
	}

	// Assemble roll: 00+0 = 100
	roll := tens + units
	if roll == 0 {
		roll = 100
	}

	result := SkillCheckResult{Value: roll, Skill: skillValue}
	extreme := skillValue / 5
	hard := skillValue / 2

	switch {
	case roll == 1:
		result.Success, result.Level = true, "critical"
		result.Message = fmt.Sprintf("🎯 大成功！(掷出 %d)", roll)
	case roll <= extreme:
		result.Success, result.Level = true, "extreme"
		result.Message = fmt.Sprintf("✨ 极难成功！(掷出 %d，极难值 %d)", roll, extreme)
	case roll <= hard:
		result.Success, result.Level = true, "hard"
		result.Message = fmt.Sprintf("✅ 困难成功！(掷出 %d，困难值 %d)", roll, hard)
	case roll <= skillValue:
		result.Success, result.Level = true, "success"
		result.Message = fmt.Sprintf("✅ 普通成功！(掷出 %d，技能值 %d)", roll, skillValue)
	case roll == 100 || (roll >= 96 && skillValue < 50):
		result.Success, result.Level = false, "fumble"
		result.Message = fmt.Sprintf("💀 大失败！(掷出 %d)", roll)
	default:
		result.Success, result.Level = false, "fail"
		result.Message = fmt.Sprintf("❌ 失败！(掷出 %d，技能值 %d)", roll, skillValue)
	}
	return result
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

// ── 运气检定 ──────────────────────────────────────────────────────────────────

// LuckCheck performs a Luck check (treated as a standard D100 check against luck value).
func LuckCheck(luckValue int) SkillCheckResult {
	return SkillCheck(luckValue)
}

// ── 理智检定 ──────────────────────────────────────────────────────────────────

// SanCheck rolls D100 against currentSAN. Returns the raw check result.
// The sanity loss amounts should be determined separately (by the caller using sanLossSuccess/sanLossFail).
func SanCheck(currentSAN int) SkillCheckResult {
	return SkillCheck(currentSAN)
}

// ── 对立检定 ──────────────────────────────────────────────────────────────────

// successLevelOrder maps level strings to numeric order for comparison.
var successLevelOrder = map[string]int{
	"fumble":   0,
	"fail":     1,
	"success":  2,
	"hard":     3,
	"extreme":  4,
	"critical": 5,
}

// OpposedResult holds the result of a COC opposed check.
type OpposedResult struct {
	AttackerCheck SkillCheckResult `json:"attacker_check"`
	DefenderCheck SkillCheckResult `json:"defender_check"`
	// AttackerWins is true when the attacker's success level is strictly higher,
	// or equal level but higher raw skill value (ties in dodge favour defender).
	AttackerWins bool   `json:"attacker_wins"`
	Reason       string `json:"reason"`
}

// OpposedCheck compares two skill checks. dodgeMode=true means ties favour the defender
// (COC rule: "if both fail, attacker wins; if equal success, for dodge defender wins").
func OpposedCheck(attackerSkill, defenderSkill int, dodgeMode bool) OpposedResult {
	atkRes := SkillCheck(attackerSkill)
	defRes := SkillCheck(defenderSkill)

	atkLvl := successLevelOrder[atkRes.Level]
	defLvl := successLevelOrder[defRes.Level]

	var attWins bool
	var reason string

	switch {
	case atkLvl > defLvl:
		attWins = true
		reason = fmt.Sprintf("攻击方成功等级更高(%s > %s)", atkRes.Level, defRes.Level)
	case defLvl > atkLvl:
		attWins = false
		reason = fmt.Sprintf("防守方成功等级更高(%s > %s)", defRes.Level, atkRes.Level)
	default: // equal levels
		if dodgeMode {
			// Tie in dodge → defender wins
			attWins = false
			reason = fmt.Sprintf("平手(%s)，闪避平手防守方获胜", atkRes.Level)
		} else if atkRes.Skill > defRes.Skill {
			// Tie in combat (counter-attack) → higher skill wins
			attWins = true
			reason = fmt.Sprintf("平手(%s)，攻击方技能值更高(%d > %d)", atkRes.Level, atkRes.Skill, defRes.Skill)
		} else {
			// Tie, equal or defender has higher skill → attacker wins (COC rule for combat tie)
			attWins = true
			reason = fmt.Sprintf("平手(%s)，反击平手攻击方获胜", atkRes.Level)
		}
	}

	return OpposedResult{
		AttackerCheck: atkRes,
		DefenderCheck: defRes,
		AttackerWins:  attWins,
		Reason:        reason,
	}
}

// ── 伤害计算 ──────────────────────────────────────────────────────────────────

// DamageRoll parses and rolls a damage formula like "1D6", "2D6+2", "1D4+1D6", "-2".
// Returns the total damage (minimum 0) and any parse/roll error.
func DamageRoll(formula string) (int, error) {
	formula = strings.ToUpper(strings.ReplaceAll(formula, " ", ""))
	if formula == "" || formula == "0" {
		return 0, nil
	}

	total := 0
	// Split by + and - while keeping the sign
	// Simple tokenizer: split on + keeping sign
	tokens := tokenizeDamageFormula(formula)
	for _, tok := range tokens {
		val, err := evalDamageToken(tok)
		if err != nil {
			return 0, err
		}
		total += val
	}
	if total < 0 {
		total = 0
	}
	return total, nil
}

// tokenizeDamageFormula splits "2D6+1D4-1" into ["+2D6", "+1D4", "-1"].
func tokenizeDamageFormula(f string) []string {
	var tokens []string
	cur := ""
	for i, ch := range f {
		if (ch == '+' || ch == '-') && i > 0 {
			tokens = append(tokens, cur)
			cur = string(ch)
		} else {
			cur += string(ch)
		}
	}
	if cur != "" {
		tokens = append(tokens, cur)
	}
	return tokens
}

// evalDamageToken evaluates one token like "+2D6", "-1", "1D4", "D6".
func evalDamageToken(tok string) (int, error) {
	sign := 1
	tok = strings.TrimSpace(tok)
	if strings.HasPrefix(tok, "-") {
		sign = -1
		tok = tok[1:]
	} else if strings.HasPrefix(tok, "+") {
		tok = tok[1:]
	}
	if dIdx := strings.Index(tok, "D"); dIdx >= 0 {
		nStr := tok[:dIdx]
		mStr := tok[dIdx+1:]
		var n int
		var err error
		if nStr == "" {
			n = 1
		} else {
			n, err = strconv.Atoi(nStr)
			if err != nil {
				return 0, fmt.Errorf("invalid dice count in token: %q", tok)
			}
		}

		m, err := strconv.Atoi(mStr)
		if err != nil || n <= 0 || m <= 0 {
			return 0, fmt.Errorf("invalid damage token: %q", tok)
		}
		sum, _ := Roll(n, m)
		return sign * sum, nil
	}
	// Plain number
	v, err := strconv.Atoi(tok)
	if err != nil {
		return 0, fmt.Errorf("invalid damage token: %q", tok)
	}
	return sign * v, nil
}

// CheckMajorWound returns true when a single hit deals major wound damage (≥ ceil(maxHP/2)).
// COC 7th example: maxHP=15 → threshold=8(ceil(15/2)=8)，not 7.
func CheckMajorWound(damage, maxHP int) bool {
	return maxHP > 0 && damage >= (maxHP+1)/2
}

// CheckInstantDeath returns true when a single hit exceeds maxHP (instant death).
func CheckInstantDeath(damage, maxHP int) bool {
	return damage > maxHP
}

var AllSkills = []string{
	"会计", "人类学", "估价", "考古学", "魅惑", "攀爬", "计算机使用", "信用评级",
	"乔装", "驾驶(汽车)", "电气维修", "电子学", "话术", "急救", "历史", "恐吓",
	"跳跃", "母语", "法律", "图书馆使用", "聆听", "锁匠", "机械维修",
	"医学", "博物学", "领航(陆地)", "神秘学", "操作重型机械",
	"说服", "药学", "摄影", "物理学", "精神分析",
	"心理学", "骑术", "科学(地质学)", "潜行", "游泳",
	"投掷", "追踪", "驾驶(船)", "侦查", "斗殴", "闪避", "手枪", "步枪/霰弹枪", "冲锋枪",
}

var allSkillsSet = func() map[string]struct{} {
	s := make(map[string]struct{})
	for _, skill := range AllSkills {
		s[skill] = struct{}{}
	}
	return s
}()

func IsValidSkill(skill string) bool {
	_, ok := allSkillsSet[skill]
	return ok
}

// DefaultSkills returns the default COC 7th skill list with base values
func DefaultSkills() map[string]int {
	return map[string]int{
		"会计":      5,
		"人类学":     1,
		"估价":      5,
		"考古学":     1,
		"魅惑":      15,
		"攀爬":      20,
		"计算机使用":   5,
		"信用评级":    0,
		"乔装":      5,
		"驾驶(汽车)":  20,
		"电气维修":    10,
		"电子学":     1,
		"话术":      5,
		"急救":      30,
		"历史":      5,
		"恐吓":      15,
		"跳跃":      20,
		"母语":      0, // EDU*5
		"法律":      5,
		"图书馆使用":   20,
		"聆听":      20,
		"锁匠":      1,
		"机械维修":    10,
		"医学":      1,
		"博物学":     10,
		"领航(陆地)":  10,
		"神秘学":     5,
		"操作重型机械":  1,
		"说服":      10,
		"药学":      1,
		"摄影":      5,
		"物理学":     1,
		"精神分析":    1,
		"心理学":     10,
		"骑术":      5,
		"科学(地质学)": 1,
		"潜行":      20,
		"游泳":      20,
		"投掷":      20,
		"追踪":      10,
		"驾驶(船)":   1,
		"侦查":      25,
		"斗殴":      25,
		"闪避":      0, // DEX/2
		"手枪":      20,
		"步枪/霰弹枪":  25,
		"冲锋枪":     15,
	}
}

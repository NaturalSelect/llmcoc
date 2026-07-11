package game

import (
	"testing"
)

func TestRollDiceExpr(t *testing.T) {
	testCases := []struct {
		expr     string
		min, max int
	}{
		{"1D6", 1, 6},
		{"2d10", 2, 20},
		{"1d6+1", 2, 7},
		{"3D8-2", 1, 22},
		{"", 0, 0},
		{"0", 0, 0},
		{"5", 5, 5},
		{"d6", 1, 6},
		{"D100", 1, 100},
		{"1D6+1D4", 2, 10},
		{"2D6-1D4", -2, 11},
	}

	for _, tc := range testCases {
		t.Run(tc.expr, func(t *testing.T) {
			for i := 0; i < 100; i++ {
				result := RollDiceExpr(tc.expr)
				if result < tc.min || result > tc.max {
					t.Errorf("RollDiceExpr(%q) = %d, want value in range [%d, %d]", tc.expr, result, tc.min, tc.max)
					break
				}
			}
		})
	}
}

func TestSkillCheck(t *testing.T) {
	// Test logical consistency of the result rather than mocking the roll
	skillValue := 50
	extreme := skillValue / 5
	hard := skillValue / 2

	for i := 0; i < 1000; i++ {
		res := SkillCheck(skillValue)

		if res.Value < 1 || res.Value > 100 {
			t.Errorf("SkillCheck roll out of bounds: %d", res.Value)
		}

		switch {
		case res.Value == 1:
			if !res.Success || res.Level != "critical" {
				t.Errorf("Roll 1 should be critical success, got %v %s", res.Success, res.Level)
			}
		case res.Value <= extreme:
			if !res.Success || res.Level != "extreme" && res.Level != "critical" {
				t.Errorf("Roll %d <= %d should be extreme success, got %v %s", res.Value, extreme, res.Success, res.Level)
			}
		case res.Value <= hard:
			if !res.Success || res.Level != "hard" && res.Level != "extreme" && res.Level != "critical" {
				t.Errorf("Roll %d <= %d should be hard success, got %v %s", res.Value, hard, res.Success, res.Level)
			}
		case res.Value <= skillValue:
			if !res.Success || res.Level != "success" && res.Level != "hard" && res.Level != "extreme" && res.Level != "critical" {
				t.Errorf("Roll %d <= %d should be success, got %v %s", res.Value, skillValue, res.Success, res.Level)
			}
		case res.Value == 100 || (res.Value >= 96 && skillValue < 50):
			if res.Success || res.Level != "fumble" {
				t.Errorf("Roll %d should be fumble, got %v %s", res.Value, res.Success, res.Level)
			}
		default:
			if res.Success || res.Level != "fail" && res.Level != "fumble" {
				t.Errorf("Roll %d should be fail, got %v %s", res.Value, res.Success, res.Level)
			}
		}
	}
}

func TestDamageRoll(t *testing.T) {
	testCases := []struct {
		formula  string
		min, max int
		hasError bool
	}{
		{"1D6", 1, 6, false},
		{"2D8+2", 4, 18, false},
		{"1d4-1", 0, 3, false},
		{"1D6+1D4", 2, 10, false},
		{"2D6 - 1D4", -2, 11, false},
		{"10", 10, 10, false},
		{"-2", 0, 0, false},
		{"", 0, 0, false},
		{"invalid", 0, 0, true},
		{"1D", 0, 0, true},
		{"D6", 1, 6, false},
	}

	for _, tc := range testCases {
		t.Run(tc.formula, func(t *testing.T) {
			for i := 0; i < 50; i++ {
				result, err := DamageRoll(tc.formula)

				if tc.hasError {
					if err == nil && i == 0 {
						t.Errorf("DamageRoll(%q) expected error but got none", tc.formula)
					}
					continue
				}

				if err != nil {
					t.Fatalf("DamageRoll(%q) unexpected error: %v", tc.formula, err)
				}
				if result < tc.min || result > tc.max {
					t.Fatalf("DamageRoll(%q) = %d, want [%d, %d]", tc.formula, result, tc.min, tc.max)
				}
			}
		})
	}
}

// TestRollRange 验证 Roll(n,m) 结果始终落在 [n, n*m] 范围内。
func TestRollRange(t *testing.T) {
	cases := [][2]int{{1, 6}, {2, 10}, {3, 6}, {1, 100}}
	for _, c := range cases {
		n, m := c[0], c[1]
		for i := 0; i < 200; i++ {
			total, dice := Roll(n, m)
			if total < n || total > n*m {
				t.Errorf("Roll(%d,%d) total=%d out of [%d,%d]", n, m, total, n, n*m)
			}
			if len(dice) != n {
				t.Errorf("Roll(%d,%d) dice count=%d want %d", n, m, len(dice), n)
			}
			for _, d := range dice {
				if d < 1 || d > m {
					t.Errorf("Roll(%d,%d) die=%d out of [1,%d]", n, m, d, m)
				}
			}
		}
	}
}

// TestRollD100Range 验证 RollD100 结果始终落在 [1,100]。
func TestRollD100Range(t *testing.T) {
	for i := 0; i < 500; i++ {
		v := RollD100()
		if v < 1 || v > 100 {
			t.Errorf("RollD100() = %d, want [1,100]", v)
		}
	}
}

// TestSkillCheckWithModifierRange 验证奖励/惩罚骰结果始终落在 [1,100]，且成功等级与骰值一致。
func TestSkillCheckWithModifierRange(t *testing.T) {
	cases := []struct {
		bonus, penalty int
	}{
		{1, 0}, // 奖励骰
		{2, 0}, // 双奖励骰
		{0, 1}, // 惩罚骰
		{0, 2}, // 双惩罚骰
		{1, 1}, // 相互抵消→普通检定
	}
	skillValue := 50
	for _, c := range cases {
		for i := 0; i < 200; i++ {
			res := SkillCheckWithModifier(skillValue, c.bonus, c.penalty)
			if res.Value < 1 || res.Value > 100 {
				t.Errorf("SkillCheckWithModifier(%d,%d,%d) value=%d out of [1,100]",
					skillValue, c.bonus, c.penalty, res.Value)
			}
			// 验证成功/失败标志与骰值一致
			if res.Value <= skillValue && !res.Success {
				t.Errorf("roll %d <= skill %d but Success=false", res.Value, skillValue)
			}
			if res.Value > skillValue && res.Value < 96 && res.Value != 100 && res.Success {
				t.Errorf("roll %d > skill %d but Success=true", res.Value, skillValue)
			}
		}
	}
}

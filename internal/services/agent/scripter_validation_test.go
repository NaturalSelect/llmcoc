// NOTE: scripter_validation_test.go 验证 setting 日期检查逻辑与 validateDraftCompatibility 行为。
// 禁止真实网络/真实LLM；所有断言仅操作内存数据结构。
package agent

import (
	"strings"
	"testing"

	"github.com/llmcoc/server/internal/models"
)

// TestSettingHasDate 验证 settingHasDate 能正确识别嵌入的具体年月日。
func TestSettingHasDate(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  bool
	}{
		{"标准中文日期", "1923年10月15日，阴雨的清晨，你们抵达小镇。", true},
		{"日期在句中", "你们于1950年3月7日受邀来到庄园，管家在门口等候。", true},
		{"使用号后缀", "1924年9月3号，初秋的傍晚，图书馆灯火通明。", true},
		{"单位数月日", "1920年1月1日，新年伊始，雪还未化。", true},
		{"三位年份", "920年5月12日，某个遥远的年代。", true},
		{"仅有年份", "1920s的英格兰，小镇沉浸在晨雾里。", false},
		{"仅有年月", "1923年10月，秋风已凉。", false},
		{"无日期", "初秋的傍晚，你们受邀前来协助整理藏书。", false},
		{"空字符串", "", false},
		{"只有时刻", "傍晚六点，你们抵达车站。", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := settingHasDate(c.input)
			if got != c.want {
				t.Errorf("settingHasDate(%q) = %v，预期 %v", c.input, got, c.want)
			}
		})
	}
}

// TestValidateDraftCompatibility_SettingDate 验证 validateDraftCompatibility 对 setting 日期的检查。
func TestValidateDraftCompatibility_SettingDate(t *testing.T) {
	base := func(setting string) ScenarioDraft {
		return ScenarioDraft{
			Name:        "测试剧本",
			Description: "测试简介",
			Difficulty:  "normal",
			Content: models.ScenarioContent{
				SystemPrompt:   "KP协议",
				Setting:        setting,
				Intro:          "你们抵达此地，可以四处走走。",
				GameStartSlot:  16,
				MapDescription: "【文字地图】A→B",
				Scenes: []models.SceneData{
					{ID: "a", Name: "场景A", Description: "描述"},
				},
				NPCs: []models.NPCData{
					{Name: "NPC甲", Description: "某人", Attitude: "友好"},
				},
				Clues:         []string{"[真实]线索一：内容。"},
				WinCondition:  "如果完成，则成功。",
				LoseCondition: "如果失败，则结束。",
			},
		}
	}

	t.Run("setting含具体年月日不报问题", func(t *testing.T) {
		issues := validateDraftCompatibility(base("1923年10月15日，初秋的小镇，你们受邀前来。"))
		for _, issue := range issues {
			if strings.Contains(issue, "setting") && strings.Contains(issue, "年月日") {
				t.Errorf("含日期的 setting 不应报日期缺失问题，实际：%v", issues)
			}
		}
	})

	t.Run("setting仅含年份时报日期缺失", func(t *testing.T) {
		issues := validateDraftCompatibility(base("1920s的英格兰，秋日清晨，你们抵达小镇。"))
		found := false
		for _, issue := range issues {
			if strings.Contains(issue, "年月日") {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("setting 仅含年份（无月日）时应报日期缺失问题，实际 issues：%v", issues)
		}
	})

	t.Run("setting为空时报空值不报日期", func(t *testing.T) {
		issues := validateDraftCompatibility(base(""))
		hasEmpty := false
		hasDate := false
		for _, issue := range issues {
			if strings.Contains(issue, "setting 为空") {
				hasEmpty = true
			}
			if strings.Contains(issue, "年月日") {
				hasDate = true
			}
		}
		if !hasEmpty {
			t.Errorf("setting 为空时应报 'setting 为空'，实际 issues：%v", issues)
		}
		if hasDate {
			t.Errorf("setting 为空时不应额外报日期缺失（已被空值检查覆盖），实际 issues：%v", issues)
		}
	})
}

// TestOneshotResultExampleSettingHasDate 验证内置示例 setting 满足日期要求（示例作为模型参考）。
func TestOneshotResultExampleSettingHasDate(t *testing.T) {
	if !settingHasDate(oneshotResultExample.Content.Setting) {
		t.Errorf("oneshotResultExample.Content.Setting 缺少具体年月日，setting=%q", oneshotResultExample.Content.Setting)
	}
}

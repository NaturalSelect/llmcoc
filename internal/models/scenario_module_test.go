// NOTE: scenario_module_test.go 验证 ScenarioContent.UnmarshalJSON 对旧版模组 JSON
// （clues 为 []string、结局为 win_condition/lose_condition/partial_wins）的兼容迁移逻辑，
// 以及新格式（clues 为对象数组、结局为 endings 数组）的直接解析。
package models

import (
	"encoding/json"
	"testing"
)

func TestScenarioContent_UnmarshalJSON_LegacyClues(t *testing.T) {
	raw := `{
		"clues": [
			"[真实]窗台上的泥土：与墓地土质一致。",
			"[隐藏]神话本质：取书者是食尸鬼。",
			"[误导]守墓人的判断：坚称是活人盗贼。",
			"没有前缀的线索"
		]
	}`
	var c ScenarioContent
	if err := json.Unmarshal([]byte(raw), &c); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if len(c.Clues) != 4 {
		t.Fatalf("len(Clues) = %d, want 4", len(c.Clues))
	}
	wantNatures := []string{"真实", "隐藏", "误导", "真实"}
	wantSummaries := []string{"窗台上的泥土：与墓地土质一致。", "神话本质：取书者是食尸鬼。", "守墓人的判断：坚称是活人盗贼。", "没有前缀的线索"}
	for i, clue := range c.Clues {
		if clue.Nature != wantNatures[i] {
			t.Errorf("Clues[%d].Nature = %q, want %q", i, clue.Nature, wantNatures[i])
		}
		if clue.Summary != wantSummaries[i] {
			t.Errorf("Clues[%d].Summary = %q, want %q", i, clue.Summary, wantSummaries[i])
		}
	}
}

func TestScenarioContent_UnmarshalJSON_LegacyEndings(t *testing.T) {
	raw := `{
		"win_condition": "如果调查员揭露真相，则局势平息。",
		"lose_condition": "如果调查员全灭，则邪教得逞。",
		"partial_wins": ["如果只阻止了一半，则代价减半。", ""]
	}`
	var c ScenarioContent
	if err := json.Unmarshal([]byte(raw), &c); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if len(c.Endings) != 3 {
		t.Fatalf("len(Endings) = %d, want 3 (win+lose+1 non-empty partial)", len(c.Endings))
	}
	if c.Endings[0].Name != "胜利" || c.Endings[0].IsFailure {
		t.Errorf("Endings[0] = %+v, want 胜利/非失败", c.Endings[0])
	}
	if c.Endings[1].Name != "失败" || !c.Endings[1].IsFailure {
		t.Errorf("Endings[1] = %+v, want 失败/IsFailure=true", c.Endings[1])
	}
	if c.Endings[2].Name != "部分胜利1" || c.Endings[2].Trigger != "如果只阻止了一半，则代价减半。" {
		t.Errorf("Endings[2] = %+v, want 部分胜利1", c.Endings[2])
	}
}

func TestScenarioContent_UnmarshalJSON_NewFormatPassesThrough(t *testing.T) {
	raw := `{
		"clues": [
			{"summary": "失窃书目的共同点", "source": "书架区", "skill_check": "图书馆使用", "on_success": "锁定目标", "nature": "真实"}
		],
		"endings": [
			{"name": "书归其主", "trigger": "如果调查员让Douglas重获藏书", "san_reward": "恢复1d4"}
		],
		"win_condition": "旧字段不应覆盖新endings"
	}`
	var c ScenarioContent
	if err := json.Unmarshal([]byte(raw), &c); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if len(c.Clues) != 1 || c.Clues[0].Summary != "失窃书目的共同点" || c.Clues[0].SkillCheck != "图书馆使用" {
		t.Fatalf("Clues 未按新结构解析: %+v", c.Clues)
	}
	if len(c.Endings) != 1 || c.Endings[0].Name != "书归其主" || c.Endings[0].SANReward != "恢复1d4" {
		t.Fatalf("新endings存在时不应被旧win_condition覆盖: %+v", c.Endings)
	}
}

func TestScenarioContent_UnmarshalJSON_EmptyOptionalFields(t *testing.T) {
	raw := `{"clues": [{"summary": "唯一线索", "nature": "真实"}], "endings": [{"name": "结局", "trigger": "条件"}]}`
	var c ScenarioContent
	if err := json.Unmarshal([]byte(raw), &c); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if c.Timeline != nil || c.KeeperAppendix != nil || c.EntryIdentities != nil || c.Mechanics != nil {
		t.Errorf("未提供的可选字段应保持零值(nil)，实际: timeline=%v keeper_appendix=%v entry_identities=%v mechanics=%v",
			c.Timeline, c.KeeperAppendix, c.EntryIdentities, c.Mechanics)
	}
}

// TestScenarioContent_UnmarshalJSON_LegacyHandoutsIgnored 锁定兼容契约：旧模组数据中
// 残留的 handouts 字段（该功能已删除）应被静默忽略，不影响其余字段解析、不报错。
func TestScenarioContent_UnmarshalJSON_LegacyHandoutsIgnored(t *testing.T) {
	raw := `{
		"setting": "背景设定原文",
		"clues": [{"summary": "唯一线索", "nature": "真实"}],
		"handouts": [{"title": "捐赠登记簿摘抄", "content": "手卡正文"}]
	}`
	var c ScenarioContent
	if err := json.Unmarshal([]byte(raw), &c); err != nil {
		t.Fatalf("Unmarshal() error = %v，旧数据中的 handouts 字段应被安全忽略", err)
	}
	if c.Setting != "背景设定原文" {
		t.Errorf("Setting = %q, want 背景设定原文", c.Setting)
	}
	if len(c.Clues) != 1 {
		t.Errorf("len(Clues) = %d, want 1", len(c.Clues))
	}
}

// TestScenario_DecodeData_DescriptionFallsBackToSetting 验证简介与背景设定合并后的
// 派生回填规则：description 为空时自动取 content.setting；description 已有值（旧数据）
// 时保持原值不被覆盖。
func TestScenario_DecodeData_DescriptionFallsBackToSetting(t *testing.T) {
	t.Run("空description回填为setting", func(t *testing.T) {
		raw := `{"description": "", "content": {"setting": "背景设定原文"}}`
		s := Scenario{Data: JSONField[json.RawMessage]{Data: json.RawMessage(raw)}}
		if err := s.DecodeData(); err != nil {
			t.Fatalf("DecodeData() error = %v", err)
		}
		if s.Description != "背景设定原文" {
			t.Errorf("Description = %q, want 背景设定原文", s.Description)
		}
	})

	t.Run("旧数据已有description时保持原值", func(t *testing.T) {
		raw := `{"description": "旧的简介原文", "content": {"setting": "背景设定原文"}}`
		s := Scenario{Data: JSONField[json.RawMessage]{Data: json.RawMessage(raw)}}
		if err := s.DecodeData(); err != nil {
			t.Fatalf("DecodeData() error = %v", err)
		}
		if s.Description != "旧的简介原文" {
			t.Errorf("Description = %q, want 旧的简介原文（不应被覆盖）", s.Description)
		}
	})
}

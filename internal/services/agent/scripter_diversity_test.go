// NOTE: scripter_diversity_test.go 验证 tone tags 映射（含难度维度的威胁规模标签）与
// era/theme 维度的 fallback 行为。
// 禁止真实网络/真实LLM；所有断言仅操作内存数据结构。
package agent

import (
	"testing"
)

// TestToneTagsIncludeDifficultyScale 验证：三档难度各自产出预期的威胁规模标签（难度取代
// 原horror_mode成为标签主要来源之一），且标签数不比移除horror_mode前更稀疏（至少2个）。
func TestToneTagsIncludeDifficultyScale(t *testing.T) {
	expectedScaleTagByDifficulty := map[string]string{
		"easy":   "intimate-scale",
		"normal": "escalating-dread",
		"hard":   "large-scale-threat",
	}

	for difficulty, wantTag := range expectedScaleTagByDifficulty {
		req := ScenarioCreationRequest{Difficulty: difficulty}
		tags := toneTagsForDiversity(req)

		if len(tags) < 2 {
			t.Errorf("difficulty=%q 产生的 tone tags 过于稀疏，预期至少2个，实际 %v", difficulty, tags)
		}
		found := false
		for _, tag := range tags {
			if tag == wantTag {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("difficulty=%q 缺少预期的威胁规模标签 %q，实际标签：%v", difficulty, wantTag, tags)
		}
	}

	// NOTE: 时代维度仍应生效，且标签总数不超过4（addTag上限）。
	req := ScenarioCreationRequest{Difficulty: "hard", Era: "1920s"}
	tags := toneTagsForDiversity(req)
	if len(tags) > 4 {
		t.Errorf("tone tags 不应超过4个，实际 %d 个：%v", len(tags), tags)
	}
	hasNoir := false
	for _, tag := range tags {
		if tag == "noir" {
			hasNoir = true
		}
	}
	if !hasNoir {
		t.Errorf("Era=1920s 时应包含 noir 标签，实际标签：%v", tags)
	}
}

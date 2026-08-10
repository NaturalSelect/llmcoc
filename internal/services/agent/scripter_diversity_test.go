// NOTE: scripter_diversity_test.go 验证调查入口（invest_focus）候选池、tone tags 映射
// （含难度维度的威胁规模标签）与 fallback 行为。
// 禁止真实网络/真实LLM；所有断言仅操作内存数据结构。
package agent

import (
	"strings"
	"testing"
)

// TestInvestFocusCandidatePool 验证：无历史记录时候选池等于 scripterInvestFocuses 全集，
// 且候选池内的每一项都合法、不重复。
func TestInvestFocusCandidatePool(t *testing.T) {
	// NOTE: models.DB 为 nil 时 loadRecentInvestFocuses 返回空，等价于无历史，候选池退化为全集。
	candidates := buildInvestFocusCandidates("test-pool-size")

	if len(candidates) == 0 {
		t.Fatal("buildInvestFocusCandidates 不应返回空候选池")
	}
	if len(candidates) != len(scripterInvestFocuses) {
		t.Errorf("无历史记录时候选池应等于全集，预期 %d 个，实际 %d 个", len(scripterInvestFocuses), len(candidates))
	}

	validSet := make(map[string]bool, len(scripterInvestFocuses))
	for _, f := range scripterInvestFocuses {
		validSet[f] = true
	}
	seen := make(map[string]bool, len(candidates))
	for _, c := range candidates {
		if !validSet[c] {
			t.Errorf("候选 %q 不在 scripterInvestFocuses 中", c)
		}
		if seen[c] {
			t.Errorf("候选池中出现重复项 %q", c)
		}
		seen[c] = true
	}
}

// TestSelectDiversityConstraintsReturnsValidFocus 验证：selectDiversityConstraints 返回的
// invest_focus 落在候选池内，且 tone_tags 非空。
func TestSelectDiversityConstraintsReturnsValidFocus(t *testing.T) {
	req := ScenarioCreationRequest{}
	investFocus, tags := selectDiversityConstraints(req, "test-fallback")

	found := false
	for _, f := range scripterInvestFocuses {
		if investFocus == f {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("invest_focus=%q 不在候选池 %v 中", investFocus, scripterInvestFocuses)
	}
	if len(tags) == 0 {
		t.Errorf("tone tags 不应为空（invest_focus=%q）", investFocus)
	}
}

// TestInvestFocusChineseLabelsComplete 验证：每个 invest_focus 都有非空中文标签。
func TestInvestFocusChineseLabelsComplete(t *testing.T) {
	for _, focus := range scripterInvestFocuses {
		label := investFocusChineseLabels[focus]
		if strings.TrimSpace(label) == "" {
			t.Errorf("invest_focus=%q 缺少中文标签", focus)
		}
	}
}

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
		tags := toneTagsForDiversity("disappearance", req)

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
	tags := toneTagsForDiversity("disappearance", req)
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

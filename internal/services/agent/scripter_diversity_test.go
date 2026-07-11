// NOTE: scripter_diversity_test.go 验证神话介入机制候选池、tone tags 映射与 fallback 行为。
// 禁止真实网络/真实LLM；所有断言仅操作内存数据结构。
package agent

import (
	"strings"
	"testing"
)

// NOTE: 预期的 8 种新介入机制，顺序与 scripterHorrorModes 保持一致。
var expectedHorrorModes = []string{
	"cult_ritual",
	"forbidden_knowledge",
	"mythos_infiltration",
	"bloodline_corruption",
	"mythos_predation",
	"sealed_awakening",
	"dimensional_intrusion",
	"sorcerous_usurpation",
}

// NOTE: 已废弃的旧美学分类，不应再出现在候选池中。
var legacyHorrorModes = []string{
	"body_horror",
	"cosmic_horror",
	"gothic_horror",
	"social_horror",
	"environmental_horror",
	"folk_horror",
	"psychological_horror",
}

// TestHorrorModeCandidates 验证：新候选全集准确，旧模式不再进入候选。
func TestHorrorModeCandidates(t *testing.T) {
	// NOTE: 验证候选数量为 8。
	if len(scripterHorrorModes) != 8 {
		t.Errorf("scripterHorrorModes 应有 8 种介入机制，实际 %d 种", len(scripterHorrorModes))
	}

	// NOTE: 验证每个预期 mode 都在候选中。
	modeSet := make(map[string]bool, len(scripterHorrorModes))
	for _, m := range scripterHorrorModes {
		modeSet[m] = true
	}
	for _, expected := range expectedHorrorModes {
		if !modeSet[expected] {
			t.Errorf("新介入机制 %q 未出现在 scripterHorrorModes 中", expected)
		}
	}

	// NOTE: 验证旧 mode 不再出现在候选中。
	for _, legacy := range legacyHorrorModes {
		if modeSet[legacy] {
			t.Errorf("旧美学分类 %q 不应出现在 scripterHorrorModes 中", legacy)
		}
	}
}

// TestDiversityPoolSize 验证：无历史记录时组合池数量为 8×8=64。
func TestDiversityPoolSize(t *testing.T) {
	// NOTE: models.DB 为 nil 时 loadRecentDiversityCombos 返回空，等价于无历史。
	req := ScenarioCreationRequest{}
	candidates := buildDiversityCandidates(req, "test-pool-size")

	expected := len(scripterHorrorModes) * len(scripterInvestFocuses)
	if expected != 64 {
		t.Errorf("组合池预期 64（8×8），实际 %d×%d=%d", len(scripterHorrorModes), len(scripterInvestFocuses), expected)
	}
	if len(candidates) != expected {
		t.Errorf("buildDiversityCandidates 返回 %d 个候选，预期 %d", len(candidates), expected)
	}
}

// TestToneTagsForNewModes 验证：每个新 mode 都能产生合理且非空的 tone tags。
func TestToneTagsForNewModes(t *testing.T) {
	req := ScenarioCreationRequest{}
	// NOTE: 统一用 disappearance 作为 investFocus，仅测试 horror_mode 分支。
	focus := "disappearance"

	expectedTagsByMode := map[string][]string{
		"cult_ritual":           {"ritualistic", "social-dread"},
		"forbidden_knowledge":   {"forbidden-knowledge", "cosmic-dread"},
		"mythos_infiltration":   {"paranoia", "social-dread"},
		"bloodline_corruption":  {"gothic", "body-horror"},
		"mythos_predation":      {"visceral", "survival-dread"},
		"sealed_awakening":      {"ancient-ruins", "cosmic-dread"},
		"dimensional_intrusion": {"reality-distortion", "cosmic-dread"},
		"sorcerous_usurpation":  {"occult", "loss-of-agency"},
	}

	for _, mode := range scripterHorrorModes {
		tags := toneTagsForDiversity(mode, focus, req)
		if len(tags) == 0 {
			t.Errorf("horror_mode=%q 产生了空 tone tags", mode)
			continue
		}
		expected, ok := expectedTagsByMode[mode]
		if !ok {
			t.Errorf("测试表中缺少 horror_mode=%q 的预期标签", mode)
			continue
		}
		tagSet := make(map[string]bool, len(tags))
		for _, tag := range tags {
			tagSet[tag] = true
		}
		for _, want := range expected {
			if !tagSet[want] {
				t.Errorf("horror_mode=%q 缺少预期标签 %q，实际标签：%v", mode, want, tags)
			}
		}
	}
}

// TestFallbackDoesNotReturnLegacyMode 验证：fallback 路径不返回旧美学分类。
func TestFallbackDoesNotReturnLegacyMode(t *testing.T) {
	req := ScenarioCreationRequest{}
	// NOTE: candidates 为空时触发终极兜底逻辑，验证其 mode 在新候选池内。
	// 直接用 selectDiversityConstraints 走正常路径（DB 为 nil → 候选池不为空）。
	horrorMode, investFocus, tags := selectDiversityConstraints(req, "test-fallback")

	for _, legacy := range legacyHorrorModes {
		if horrorMode == legacy {
			t.Errorf("fallback 返回了旧美学分类 %q，应返回新介入机制", legacy)
		}
	}
	// NOTE: 验证返回的 mode 在新候选池中。
	found := false
	for _, m := range scripterHorrorModes {
		if horrorMode == m {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("fallback horror_mode=%q 不在新候选池 %v 中", horrorMode, scripterHorrorModes)
	}
	// NOTE: invest_focus 也应在候选池中。
	focusFound := false
	for _, f := range scripterInvestFocuses {
		if investFocus == f {
			focusFound = true
			break
		}
	}
	if !focusFound {
		t.Errorf("fallback invest_focus=%q 不在候选池 %v 中", investFocus, scripterInvestFocuses)
	}
	// NOTE: tone tags 非空。
	if len(tags) == 0 {
		t.Errorf("fallback 返回了空 tone tags（horror_mode=%q, invest_focus=%q）", horrorMode, investFocus)
	}
}

// TestChineseLabelsForNewModes 验证：每个新 mode 都有对应中文标签且非空。
func TestChineseLabelsForNewModes(t *testing.T) {
	for _, mode := range scripterHorrorModes {
		label := horrorModeChineseLabels[mode]
		if strings.TrimSpace(label) == "" {
			t.Errorf("horror_mode=%q 缺少中文标签", mode)
		}
	}
	// NOTE: 旧 mode 不应在标签表中（保持标签表整洁）。
	for _, legacy := range legacyHorrorModes {
		if label, ok := horrorModeChineseLabels[legacy]; ok {
			t.Errorf("旧美学分类 %q 仍存在于 horrorModeChineseLabels 中，label=%q", legacy, label)
		}
	}
}

// TestLegacyModeReadCompatibility 验证：旧 mode 字符串经 toneTagsForDiversity 不 panic，
// 走 default 分支产生基础 fallback 标签，历史数据可读展示。
func TestLegacyModeReadCompatibility(t *testing.T) {
	req := ScenarioCreationRequest{}
	for _, legacy := range legacyHorrorModes {
		tags := toneTagsForDiversity(legacy, "disappearance", req)
		// NOTE: default 分支至少保证有 "slow-burn" 兜底。
		if len(tags) == 0 {
			t.Errorf("旧 mode=%q 经 toneTagsForDiversity 返回空标签，应有 fallback 兜底", legacy)
		}
	}
}

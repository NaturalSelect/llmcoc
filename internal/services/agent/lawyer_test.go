package agent

import (
	"strings"
	"testing"

	"github.com/llmcoc/server/internal/models"
	"github.com/llmcoc/server/internal/services/rulebook"
)

func TestAppendGrepResultsKeepsKeywordAsOneRegexp(t *testing.T) {
	var gotKeyword string
	var sb strings.Builder
	ok := appendGrepResults(&sb, "grep", "理智 .* 检定", "规则书", func(keyword string) []rulebook.GrepResult {
		gotKeyword = keyword
		return []rulebook.GrepResult{{LineNum: 42, Text: "理智 损失 检定"}}
	})

	if !ok {
		t.Fatal("appendGrepResults should accept non-empty keyword")
	}
	if gotKeyword != "理智 .* 检定" {
		t.Fatalf("keyword should be passed as one regexp, got %q", gotKeyword)
	}
	if text := sb.String(); !strings.Contains(text, "【grep:理智 .* 检定】") || strings.Contains(text, "【grep:理智】") {
		t.Fatalf("unexpected grep output: %q", text)
	}
}

// TestBuildLawyerPromptDefaultRulesContainBannedSpells verifies that the five
// banned spells appear in the assembled prompt when using default rules, and
// that they no longer appear hardcoded in lawyerSystemPromptBase.
func TestBuildLawyerPromptDefaultRulesContainBannedSpells(t *testing.T) {
	prompt := BuildLawyerPrompt(models.DefaultBalanceRules)

	bannedSpells := []string{"精神转移术", "精神交换术", "内心灵光唤醒术", "完善术", "伊格的尖牙"}
	for _, spell := range bannedSpells {
		if !strings.Contains(prompt, spell) {
			t.Errorf("default prompt should contain banned spell %q", spell)
		}
	}

	// Balance section markers must be present.
	if !strings.Contains(prompt, "【KP平衡调整规则（管理员配置）】") {
		t.Error("prompt should contain balance section header")
	}
	if !strings.Contains(prompt, "【平衡调整规则结束】") {
		t.Error("prompt should contain balance section footer")
	}

	// The hardcoded ban must have been removed from the base prompt.
	for _, spell := range bannedSpells {
		if strings.Contains(lawyerSystemPromptBase, spell) {
			t.Errorf("hardcoded ban should be removed from lawyerSystemPromptBase; found %q", spell)
		}
	}
}

// TestBuildLawyerPromptCustomRulesReplacesDefault verifies that a custom rule
// replaces (not appends to) the default banned-spell list.
func TestBuildLawyerPromptCustomRulesReplacesDefault(t *testing.T) {
	custom := "自定义平衡规则：调查员不得使用大克苏鲁信仰。"
	prompt := BuildLawyerPrompt(custom)

	if !strings.Contains(prompt, custom) {
		t.Error("prompt should contain custom balance rule")
	}
	if strings.Contains(prompt, "精神转移术") {
		t.Error("custom rules should replace default; default banned spells should not appear")
	}
	if !strings.Contains(prompt, "【KP平衡调整规则（管理员配置）】") {
		t.Error("balance section header should be present")
	}
}

// TestBuildLawyerPromptEmptyRulesHasNoBalanceSection verifies that an empty
// balanceRules value produces no balance section at all.
func TestBuildLawyerPromptEmptyRulesHasNoBalanceSection(t *testing.T) {
	prompt := BuildLawyerPrompt("")

	if strings.Contains(prompt, "【KP平衡调整规则（管理员配置）】") {
		t.Error("empty rules should produce no balance section")
	}
	if strings.Contains(prompt, "精神转移术") {
		t.Error("empty rules should produce no banned spell text")
	}
	// JSON-only constraint must still be present.
	if !strings.Contains(prompt, "仅输出JSON数组") {
		t.Error("JSON-only constraint should always be present")
	}
}

// TestBuildLawyerPromptJSONConstraintIsLast verifies the JSON-only tail always
// follows the balance rules section.
func TestBuildLawyerPromptJSONConstraintIsLast(t *testing.T) {
	prompt := BuildLawyerPrompt(models.DefaultBalanceRules)

	balanceEnd := strings.Index(prompt, "【平衡调整规则结束】")
	jsonOnly := strings.Index(prompt, "仅输出JSON数组")
	if balanceEnd < 0 || jsonOnly < 0 {
		t.Fatal("both sections must be present in prompt")
	}
	if jsonOnly < balanceEnd {
		t.Error("JSON-only constraint must appear after the balance rules section")
	}
}

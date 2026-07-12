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
// banned spells appear in the balance section when using default rules, and
// that they no longer appear hardcoded in lawyerSystemPromptBase.
func TestBuildLawyerPromptDefaultRulesContainBannedSpells(t *testing.T) {
	section := BuildLawyerPrompt(models.DefaultBalanceRules)

	bannedSpells := []string{"精神转移术", "精神交换术", "内心灵光唤醒术", "完善术", "伊格的尖牙"}
	for _, spell := range bannedSpells {
		if !strings.Contains(section, spell) {
			t.Errorf("balance section should contain banned spell %q", spell)
		}
	}

	// XML section tags must be present.
	if !strings.Contains(section, "<kp_balance_rules>") {
		t.Error("balance section should contain opening tag <kp_balance_rules>")
	}
	if !strings.Contains(section, "</kp_balance_rules>") {
		t.Error("balance section should contain closing tag </kp_balance_rules>")
	}

	// The hardcoded ban must have been removed from the base system prompt.
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
	section := BuildLawyerPrompt(custom)

	if !strings.Contains(section, custom) {
		t.Error("balance section should contain custom balance rule")
	}
	if strings.Contains(section, "精神转移术") {
		t.Error("custom rules should replace default; default banned spells should not appear")
	}
	if !strings.Contains(section, "<kp_balance_rules>") {
		t.Error("balance section opening tag should be present")
	}
}

// TestBuildLawyerPromptEmptyRulesReturnsEmpty verifies that an empty
// balanceRules value returns an empty string, producing no balance section.
func TestBuildLawyerPromptEmptyRulesReturnsEmpty(t *testing.T) {
	section := BuildLawyerPrompt("")

	if section != "" {
		t.Errorf("empty rules should return empty string, got %q", section)
	}
}

// TestLawyerSystemPromptAlwaysHasJSONConstraint verifies that the Lawyer system
// prompt (base + tail) always includes the JSON-only output constraint.
func TestLawyerSystemPromptAlwaysHasJSONConstraint(t *testing.T) {
	fullPrompt := lawyerSystemPromptBase + lawyerSystemPromptTail

	if !strings.Contains(fullPrompt, "仅输出JSON数组") {
		t.Error("Lawyer system prompt must always contain JSON-only constraint")
	}
}

// TestBuildLawyerPromptOpenTagBeforeCloseTag verifies XML open tag appears before close tag.
func TestBuildLawyerPromptOpenTagBeforeCloseTag(t *testing.T) {
	section := BuildLawyerPrompt(models.DefaultBalanceRules)

	openIdx := strings.Index(section, "<kp_balance_rules>")
	closeIdx := strings.Index(section, "</kp_balance_rules>")
	if openIdx < 0 || closeIdx < 0 {
		t.Fatal("both opening and closing tags must be present")
	}
	if closeIdx < openIdx {
		t.Error("closing tag must appear after opening tag")
	}
}

package agent

import (
	"strings"
	"testing"

	"github.com/llmcoc/server/internal/models"
)

// TestBuildDirectorPromptNonEmptyRulesInjected 验证非空 balanceRules 产生包含规则内容和 XML 标签的段落。
func TestBuildDirectorPromptNonEmptyRulesInjected(t *testing.T) {
	custom := "自定义KP平衡规则：禁止使用大克苏鲁信仰。"
	section := BuildDirectorPrompt(custom)

	if !strings.Contains(section, custom) {
		t.Error("balance section should contain the injected rule text")
	}
	if !strings.Contains(section, "<kp_balance_rules>") {
		t.Error("balance section should contain opening tag <kp_balance_rules>")
	}
	if !strings.Contains(section, "</kp_balance_rules>") {
		t.Error("balance section should contain closing tag </kp_balance_rules>")
	}
}

// TestBuildDirectorPromptDefaultRulesInjected 验证 DefaultBalanceRules 时段落包含默认规则内容。
func TestBuildDirectorPromptDefaultRulesInjected(t *testing.T) {
	section := BuildDirectorPrompt(models.DefaultBalanceRules)

	if !strings.Contains(section, "<kp_balance_rules>") {
		t.Error("default rules: opening tag should be present")
	}
	if !strings.Contains(section, models.DefaultBalanceRules) {
		t.Error("default rules: balance rule text should be present in section")
	}
}

// TestBuildDirectorPromptEmptyRulesReturnsEmpty 验证 balanceRules 为空时返回空字符串，不产生任何段落。
func TestBuildDirectorPromptEmptyRulesReturnsEmpty(t *testing.T) {
	section := BuildDirectorPrompt("")

	if section != "" {
		t.Errorf("empty rules should return empty string, got %q", section)
	}
}

// TestBuildDirectorPromptOpenTagBeforeCloseTag 验证 XML 开标签在闭标签之前。
func TestBuildDirectorPromptOpenTagBeforeCloseTag(t *testing.T) {
	section := BuildDirectorPrompt("测试规则")

	openIdx := strings.Index(section, "<kp_balance_rules>")
	closeIdx := strings.Index(section, "</kp_balance_rules>")
	if openIdx < 0 || closeIdx < 0 {
		t.Fatal("both opening and closing tags must be present")
	}
	if closeIdx < openIdx {
		t.Error("closing tag must appear after opening tag")
	}
}

func TestKPSystemPromptUsesActivePacingWithoutTemperament(t *testing.T) {
	for _, want := range []string{
		"[ACTIVE-PACING]",
		"文字风格不是节奏",
		"[KP-REPLY]",
		"[TABLE-TALK]",
	} {
		if !strings.Contains(kpSystemPrompt, want) {
			t.Errorf("kp system prompt should contain %q", want)
		}
	}

	for _, removed := range []string{
		"<kp_temperament",
		"[KP-HABITS]",
		"[KP-VOICE]",
	} {
		if strings.Contains(kpSystemPrompt, removed) {
			t.Errorf("kp system prompt should not contain removed style rule %q", removed)
		}
	}
}

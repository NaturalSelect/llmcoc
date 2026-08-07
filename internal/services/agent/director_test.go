package agent

import (
	"encoding/json"
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
		"[UNKNOWN]",
		"[OPTIONS]",
	} {
		if !strings.Contains(kpSystemPrompt, want) {
			t.Errorf("kp system prompt should contain %q", want)
		}
	}

	for _, removed := range []string{
		"<kp_temperament",
		"[KP-HABITS]",
		"[KP-VOICE]",
		"no numbered lists, no analyst jargon",
	} {
		if strings.Contains(kpSystemPrompt, removed) {
			t.Errorf("kp system prompt should not contain removed style rule %q", removed)
		}
	}
}

// TestKPSystemPromptCarriesUnknownToneCharter 验证"未知的恐惧"基调宪章与 [UNKNOWN] 操作块的核心表述已注入。
func TestKPSystemPromptCarriesUnknownToneCharter(t *testing.T) {
	for _, want := range []string{
		// 宪章（<instruction>）
		"知道一切却说出很少",
		"这份契约是双边的",
		// 信息经济三层
		"现象免费",
		"解释收费",
		"全貌分期",
		// 通道全覆盖
		"通道全覆盖",
		"image_prompt",
		// 命名与描写技法
		"命名三阶段",
		"比较失败法",
		"违和感预算",
		// 调和公式与理智接口
		"明账疯狂",
		"案子会破，宇宙不会",
		"出路是程序性的",
		// options 基调
		"风味中立",
		"评价性副词",
	} {
		if !strings.Contains(kpSystemPrompt, want) {
			t.Errorf("kp system prompt should contain unknown-tone phrase %q", want)
		}
	}
}

// TestGenerateImageToolBlocksUnidentifiedEntityPortrait 验证配图工具描述已收紧未鉴定实体的正面描绘规则。
func TestGenerateImageToolBlocksUnidentifiedEntityPortrait(t *testing.T) {
	desc := generateImageTool().def.Description

	for _, want := range []string{
		"[UNKNOWN]",
		"正面全貌",
		"背光剪影",
		"应积极主动地使用",
	} {
		if !strings.Contains(desc, want) {
			t.Errorf("generate_image description should contain %q", want)
		}
	}

	if strings.Contains(desc, "重要NPC或怪物首次登场") {
		t.Error("generate_image description should not encourage portraying unidentified monsters on first appearance")
	}
}

// TestResponseToolOptionsDescriptionConsistent 验证 response 工具的 Description 与 options 参数 schema 数量表述一致，且 schema 仍是合法 JSON。
func TestResponseToolOptionsDescriptionConsistent(t *testing.T) {
	tool := responseTool()
	desc := tool.def.Description
	params := string(tool.def.Parameters)

	for _, want := range []string{"0-2条", "[OPTIONS]"} {
		if !strings.Contains(desc, want) {
			t.Errorf("response tool description should contain %q", want)
		}
	}
	if strings.Contains(desc, "给出2个") {
		t.Error("response tool description should not keep the stale '给出2个' wording")
	}

	if !strings.Contains(params, "宁可给0条也不要泄露") {
		t.Error("response tool options schema should contain '宁可给0条也不要泄露'")
	}
	if !json.Valid(tool.def.Parameters) {
		t.Error("response tool Parameters should remain valid JSON")
	}
}

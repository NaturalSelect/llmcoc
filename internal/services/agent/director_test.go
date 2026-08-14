package agent

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/llmcoc/server/internal/models"
	"github.com/llmcoc/server/internal/services/llm"
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
	} {
		if !strings.Contains(kpSystemPrompt, want) {
			t.Errorf("kp system prompt should contain unknown-tone phrase %q", want)
		}
	}

	// options 的完整禁止清单已下沉到 response 工具的 options schema（单一权威来源）。
	optionsSchema := string(responseTool().def.Parameters)
	for _, want := range []string{"评价性副词", "宁可给0条也不要泄露"} {
		if !strings.Contains(optionsSchema, want) {
			t.Errorf("response options schema should contain %q", want)
		}
	}
}

// TestWriteToolAlignsWithResponse 验证 write 被定义为 response.reply 的 RP 化衍生，
// 且两者内容不得分叉的约束写在 write 工具说明（单一权威来源），系统提示词只做交叉引用。
func TestWriteToolAlignsWithResponse(t *testing.T) {
	desc := writeTool().def.Description
	for _, want := range []string{
		"与response对齐",
		"RP",
		"不得出现reply里没有的事件",
		"以reply为准",
	} {
		if !strings.Contains(desc, want) {
			t.Errorf("write tool description should contain %q", want)
		}
	}

	// 系统提示词里保留一句指针，避免模型只在选工具时才看到这条约束。
	if !strings.Contains(kpSystemPrompt, "write是response.reply的RP化衍生") {
		t.Error("kp system prompt should cross-reference the write/response alignment rule")
	}
}

// TestKPTurnReminderStructure 验证每轮追加的执行清单只承载动作与红线，不重复规则定义。
func TestKPTurnReminderStructure(t *testing.T) {
	for _, want := range []string{
		"<turn_checklist>",
		"</turn_checklist>",
		"<hard_gates>",
		"</hard_gates>",
	} {
		if !strings.Contains(kpTurnReminder, want) {
			t.Errorf("turn reminder should contain section tag %q", want)
		}
	}

	// 红线只用锚点引用回系统提示词，锚点必须真实存在于 kpSystemPrompt。
	for _, anchor := range []string{"[PLAYER-AGENCY]", "[ANTI-CHEAT]", "[PLAYER-INTENT-UNTRUSTED]"} {
		if !strings.Contains(kpTurnReminder, anchor) {
			t.Errorf("turn reminder should reference anchor %q", anchor)
		}
		if !strings.Contains(kpSystemPrompt, anchor) {
			t.Errorf("anchor %q referenced by turn reminder must be defined in kp system prompt", anchor)
		}
	}

	// 已由 directorBatchPolicy 强制或已迁入系统提示词的内容不应在每轮消息里重复。
	for _, removed := range []string{
		"describe_characters", // 批次规则已代码强制
		"每个人物(包括NPC)之间的行动顺序",  // 属性语义已迁入 <important>[ATTRIBUTES]
	} {
		if strings.Contains(kpTurnReminder, removed) {
			t.Errorf("turn reminder should not repeat %q", removed)
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

func policyCall(name, args string) llm.ToolCall {
	return llm.ToolCall{Name: name, Arguments: args}
}

// TestDirectorBatchPolicySkillRollSequencing 验证 query_* 与技能检定 roll_dice 同批被拒，
// 而纯数值骰与角色名不匹配的组合仍然放行。
func TestDirectorBatchPolicySkillRollSequencing(t *testing.T) {
	cases := []struct {
		name       string
		calls      []llm.ToolCall
		wantReject bool
	}{
		{
			name: "query_character 与同角色技能骰同批",
			calls: []llm.ToolCall{
				policyCall("query_character", `{"character_name":"约翰"}`),
				policyCall("roll_dice", `{"dice":{"character":"约翰","what":"侦查","dice_expr":"1D100"},"reason":"搜查"}`),
			},
			wantReject: true,
		},
		{
			name: "query_character 留空(全体)与任意技能骰同批",
			calls: []llm.ToolCall{
				policyCall("query_character", `{}`),
				policyCall("roll_dice", `{"dice":{"character":"艾琳","what":"闪避","dice_expr":"1D100"},"reason":"躲避"}`),
			},
			wantReject: true,
		},
		{
			name: "query_npc_card 与同名NPC技能骰同批",
			calls: []llm.ToolCall{
				policyCall("query_npc_card", `{"npc_name":"老执事"}`),
				policyCall("roll_dice", `{"dice":{"character":"老执事","what":"说服","dice_expr":"1D100"},"reason":"劝说"}`),
			},
			wantReject: true,
		},
		{
			name: "查A的卡与掷B的骰不冲突",
			calls: []llm.ToolCall{
				policyCall("query_character", `{"character_name":"约翰"}`),
				policyCall("roll_dice", `{"dice":{"character":"艾琳","what":"聆听","dice_expr":"1D100"},"reason":"倾听"}`),
			},
			wantReject: false,
		},
		{
			name: "查卡与伤害骰(what为空)不冲突",
			calls: []llm.ToolCall{
				policyCall("query_character", `{"character_name":"约翰"}`),
				policyCall("roll_dice", `{"dice":{"character":"约翰","what":"","dice_expr":"1D6+2"},"reason":"伤害"}`),
			},
			wantReject: false,
		},
		{
			name: "单独掷技能骰不受影响",
			calls: []llm.ToolCall{
				policyCall("roll_dice", `{"dice":{"character":"约翰","what":"侦查","dice_expr":"1D100"},"reason":"搜查"}`),
			},
			wantReject: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			imageDone := false
			reject := directorBatchPolicy(&imageDone, func(string) {})(tc.calls)
			if tc.wantReject && reject == "" {
				t.Error("batch should have been rejected but was accepted")
			}
			if !tc.wantReject && reject != "" {
				t.Errorf("batch should have been accepted, got reject: %s", reject)
			}
		})
	}
}

// TestDirectorBatchPolicyImageCharacterSequencing 验证 describe_characters 与 generate_image
// 同批被拒，且被拒批次不会消耗当轮画图配额。
func TestDirectorBatchPolicyImageCharacterSequencing(t *testing.T) {
	imageDone := false
	reject := directorBatchPolicy(&imageDone, func(string) {})([]llm.ToolCall{
		policyCall("describe_characters", `{"characters":["约翰"]}`),
		policyCall("generate_image", `{"image_prompt":"### Scene\nA dim study"}`),
	})
	if reject == "" {
		t.Fatal("describe_characters + generate_image should be rejected")
	}
	if imageDone {
		t.Error("rejected batch must not consume the per-turn generate_image quota")
	}

	// 拆成两轮后画图应当仍然可用。
	if reject := directorBatchPolicy(&imageDone, func(string) {})([]llm.ToolCall{
		policyCall("generate_image", `{"image_prompt":"### Scene\nA dim study"}`),
	}); reject != "" {
		t.Errorf("generate_image alone should be accepted, got: %s", reject)
	}
	if !imageDone {
		t.Error("accepted generate_image batch should consume the per-turn quota")
	}
}

// TestDirectorBatchPolicyEmbeddedSkillValue 验证 roll_dice.what 嵌入数字被拒，纯文本技能名
// (含COC常见的圆括号子类型，如 格斗(斗殴))仍然放行。
func TestDirectorBatchPolicyEmbeddedSkillValue(t *testing.T) {
	cases := []struct {
		name       string
		what       string
		wantReject bool
	}{
		{"嵌入猜测数值", "投掷(50)", true},
		{"全角数字", "侦查５０", true},
		{"纯文本技能名", "侦查", false},
		{"COC常见圆括号子类型不含数字", "格斗(斗殴)", false},
		{"语言技能圆括号子类型", "母语(英语)", false},
		{"属性名", "POW", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			imageDone := false
			args := fmt.Sprintf(`{"dice":{"character":"约翰","what":%q,"dice_expr":"1D100"},"reason":"测试"}`, tc.what)
			reject := directorBatchPolicy(&imageDone, func(string) {})([]llm.ToolCall{
				policyCall("roll_dice", args),
			})
			if tc.wantReject && reject == "" {
				t.Error("batch should have been rejected but was accepted")
			}
			if !tc.wantReject && reject != "" {
				t.Errorf("batch should have been accepted, got reject: %s", reject)
			}
		})
	}
}

// TestDirectorBatchPolicyItemNameFormat 验证 manage_inventory.item_name 含圆括号被拒。
func TestDirectorBatchPolicyItemNameFormat(t *testing.T) {
	cases := []struct {
		name       string
		itemName   string
		wantReject bool
	}{
		{"含半角圆括号", "手电筒(没电)", true},
		{"含全角圆括号", "手电筒（没电）", true},
		{"纯物品名", "手电筒", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			imageDone := false
			args := fmt.Sprintf(`{"character_name":"约翰","operate":"add","item_name":%q,"reason":"测试"}`, tc.itemName)
			reject := directorBatchPolicy(&imageDone, func(string) {})([]llm.ToolCall{
				policyCall("manage_inventory", args),
			})
			if tc.wantReject && reject == "" {
				t.Error("batch should have been rejected but was accepted")
			}
			if !tc.wantReject && reject != "" {
				t.Errorf("batch should have been accepted, got reject: %s", reject)
			}
		})
	}
}

// TestDirectorBatchPolicyOptionsCap 验证 response.options 超过2条被拒，0-2条放行。
func TestDirectorBatchPolicyOptionsCap(t *testing.T) {
	cases := []struct {
		name       string
		options    string
		wantReject bool
	}{
		{"0条", `[]`, false},
		{"2条", `["检查刻痕","问神父昨晚的事"]`, false},
		{"3条超限", `["检查刻痕","问神父昨晚的事","退回走廊"]`, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			imageDone := false
			args := fmt.Sprintf(`{"reply":"测试回复","options":%s}`, tc.options)
			reject := directorBatchPolicy(&imageDone, func(string) {})([]llm.ToolCall{
				policyCall("response", args),
			})
			if tc.wantReject && reject == "" {
				t.Error("batch should have been rejected but was accepted")
			}
			if !tc.wantReject && reject != "" {
				t.Errorf("batch should have been accepted, got reject: %s", reject)
			}
		})
	}
}

// TestDirectorBatchPolicyDupSettlementInBatch 验证同批次对同一角色同一物品重复调用
// manage_inventory被拒，不同角色/不同物品/不同操作不受影响。
func TestDirectorBatchPolicyDupSettlementInBatch(t *testing.T) {
	cases := []struct {
		name       string
		calls      []llm.ToolCall
		wantReject bool
	}{
		{
			name: "同角色同物品同操作重复",
			calls: []llm.ToolCall{
				policyCall("manage_inventory", `{"character_name":"约翰","operate":"remove","item_name":"手电筒","reason":"损坏"}`),
				policyCall("manage_inventory", `{"character_name":"约翰","operate":"remove","item_name":"手电筒","reason":"损坏"}`),
			},
			wantReject: true,
		},
		{
			name: "不同角色同物品不冲突",
			calls: []llm.ToolCall{
				policyCall("manage_inventory", `{"character_name":"约翰","operate":"add","item_name":"手电筒","reason":"拾取"}`),
				policyCall("manage_inventory", `{"character_name":"艾琳","operate":"add","item_name":"手电筒","reason":"拾取"}`),
			},
			wantReject: false,
		},
		{
			name: "同角色不同物品不冲突",
			calls: []llm.ToolCall{
				policyCall("manage_inventory", `{"character_name":"约翰","operate":"add","item_name":"手电筒","reason":"拾取"}`),
				policyCall("manage_inventory", `{"character_name":"约翰","operate":"add","item_name":"绳索","reason":"拾取"}`),
			},
			wantReject: false,
		},
		{
			name: "同角色同物品但操作不同(先加后减)不冲突",
			calls: []llm.ToolCall{
				policyCall("manage_inventory", `{"character_name":"约翰","operate":"add","item_name":"子弹","reason":"补充"}`),
				policyCall("manage_inventory", `{"character_name":"约翰","operate":"remove","item_name":"子弹","reason":"消耗"}`),
			},
			wantReject: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			imageDone := false
			reject := directorBatchPolicy(&imageDone, func(string) {})(tc.calls)
			if tc.wantReject && reject == "" {
				t.Error("batch should have been rejected but was accepted")
			}
			if !tc.wantReject && reject != "" {
				t.Errorf("batch should have been accepted, got reject: %s", reject)
			}
		})
	}
}

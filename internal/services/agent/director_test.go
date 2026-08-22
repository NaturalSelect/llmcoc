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

// TestProceduresBlockCoversComplexFlows 验证复杂流程样板存在，且其中与 COC_kp.md 对应的
// 关键机制没有被后续编辑改坏——尤其是两处反直觉判定，改错不会报错但会让裁定系统性出错。
func TestProceduresBlockCoversComplexFlows(t *testing.T) {
	start := strings.Index(kpSystemPrompt, "<procedures>")
	end := strings.Index(kpSystemPrompt, "</procedures>")
	if start < 0 || end < 0 {
		t.Fatal("kp system prompt should contain a <procedures> block")
	}
	proc := kpSystemPrompt[start:end]

	for _, name := range []string{"[战斗轮]", "[SAN级联]", "[追逐]", "[多玩家分歧意图]"} {
		if !strings.Contains(proc, name) {
			t.Errorf("procedures block should contain flow %q", name)
		}
	}

	// COC_kp.md:6296-6298 —— 智力检定"通过"才进入临时性疯狂，失败是抑制记忆不疯。
	// 这条与直觉相反，是最容易被"顺手改顺"的地方。
	if !strings.Contains(proc, "智力检定**通过**＝角色意识到自己经历了什么＝陷入临时性疯狂") {
		t.Error("SAN cascade must keep the counter-intuitive INT-check direction (pass -> madness)")
	}
	if !strings.Contains(proc, "智力检定**失败**＝记忆被抑制＝不进入疯狂") {
		t.Error("SAN cascade must keep that a failed INT check suppresses memory instead of causing madness")
	}
	// COC_kp.md:4352 —— 成功等级相同时由攻击方命中。
	if !strings.Contains(proc, "成功等级相同时是攻击方命中") {
		t.Error("combat flow must keep the tie-goes-to-attacker rule")
	}
	// COC_kp.md:6239 —— 理智检定不适用奖励骰/惩罚骰。
	if !strings.Contains(proc, "奖励骰与惩罚骰不适用于理智检定") {
		t.Error("SAN cascade must keep the bonus/penalty die exclusion")
	}
	// COC_kp.md:5377-5379 —— 逃离者更快则当场脱离，不进入追逐轮。
	if !strings.Contains(proc, "逃离者高于追逐者→当场逃脱") {
		t.Error("chase flow must keep the early-escape branch")
	}

	// 样板引用的锚点必须在系统提示词里真实存在。
	for _, anchor := range []string{"[PLAYER-AGENCY]", "[PLAYER-TO-PLAYER]", "[MADNESS-EFFECT]", "[KP-REPLY]"} {
		if !strings.Contains(proc, anchor) {
			continue
		}
		if strings.Count(kpSystemPrompt, anchor) < 2 {
			t.Errorf("anchor %q referenced by procedures must also be defined as a rule", anchor)
		}
	}

	// 每轮清单要把模型引到样板，否则样板不会被读。
	if !strings.Contains(kpTurnReminder, "<procedures>") {
		t.Error("turn checklist should route complex turns to the procedures block")
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

// noEncounterBatchPolicy 是不关心战斗/追逐状态的测试用例的默认构造：没有激活的
// 战斗/追逐，本回合也未推进过任何战斗/追逐轮次。
func noEncounterBatchPolicy(imageDone *bool) toolBatchPolicy {
	var combat *models.CombatState
	var chase *models.ChaseState
	roundClosed := false
	return directorBatchPolicy(imageDone, &combat, &chase, &roundClosed, nil, func(string) {})
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
			reject := noEncounterBatchPolicy(&imageDone)(tc.calls)
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
	reject := noEncounterBatchPolicy(&imageDone)([]llm.ToolCall{
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
	if reject := noEncounterBatchPolicy(&imageDone)([]llm.ToolCall{
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
			reject := noEncounterBatchPolicy(&imageDone)([]llm.ToolCall{
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
			reject := noEncounterBatchPolicy(&imageDone)([]llm.ToolCall{
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
			reject := noEncounterBatchPolicy(&imageDone)([]llm.ToolCall{
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
			reject := noEncounterBatchPolicy(&imageDone)(tc.calls)
			if tc.wantReject && reject == "" {
				t.Error("batch should have been rejected but was accepted")
			}
			if !tc.wantReject && reject != "" {
				t.Errorf("batch should have been accepted, got reject: %s", reject)
			}
		})
	}
}

// TestDirectorBatchPolicyEncounterSequencing 覆盖6d新增的5条战斗/追逐批次规则:
// 独占成批、无激活状态时拒绝推进/结束、战斗与追逐互斥、advance_time互斥、
// response完整性前置(可推进者未结算拒绝/剩余全无声明PC放行/待澄清放行/
// roundClosed放行/end_combat同批放行)。
func TestDirectorBatchPolicyEncounterSequencing(t *testing.T) {
	freshCombat := func() *models.CombatState {
		return &models.CombatState{
			Active: true, Round: 1, ActorIndex: 0,
			Participants: []models.CombatParticipant{
				{Name: "甲", DEX: 80, HasActed: false},
				{Name: "乙", DEX: 50, HasActed: false},
			},
		}
	}
	freshChase := func() *models.ChaseState {
		return &models.ChaseState{
			Active: true, Round: 1, ActorIndex: 0,
			Participants: []models.ChaseParticipant{{Name: "丙", DEX: 60}},
		}
	}

	t.Run("combat_act与report同批放行", func(t *testing.T) {
		imageDone := false
		combat, chase, roundClosed := freshCombat(), (*models.ChaseState)(nil), false
		reject := directorBatchPolicy(&imageDone, &combat, &chase, &roundClosed, nil, func(string) {})([]llm.ToolCall{
			policyCall("combat_act", `{"combat_actor_name":"甲","combat_action":{"type":"attack"}}`),
			policyCall("report", `{"report":"备注"}`),
		})
		if reject != "" {
			t.Errorf("combat_act+report应放行,got reject: %s", reject)
		}
	})

	t.Run("combat_act与其他工具同批被拒", func(t *testing.T) {
		imageDone := false
		combat, chase, roundClosed := freshCombat(), (*models.ChaseState)(nil), false
		reject := directorBatchPolicy(&imageDone, &combat, &chase, &roundClosed, nil, func(string) {})([]llm.ToolCall{
			policyCall("combat_act", `{"combat_actor_name":"甲","combat_action":{"type":"attack"}}`),
			policyCall("roll_dice", `{"dice":{"character":"甲","what":"格斗","dice_expr":"1D100"},"reason":"攻击"}`),
		})
		if reject == "" {
			t.Error("combat_act与非report工具同批应被拒")
		}
	})

	t.Run("combat_act调用两次(含彼此)被拒", func(t *testing.T) {
		imageDone := false
		combat, chase, roundClosed := freshCombat(), (*models.ChaseState)(nil), false
		reject := directorBatchPolicy(&imageDone, &combat, &chase, &roundClosed, nil, func(string) {})([]llm.ToolCall{
			policyCall("combat_act", `{"combat_actor_name":"甲","combat_action":{"type":"attack"}}`),
			policyCall("combat_act", `{"combat_actor_name":"乙","combat_action":{"type":"attack"}}`),
		})
		if reject == "" {
			t.Error("combat_act同批调用两次应被拒")
		}
	})

	t.Run("无激活战斗时combat_act被拒", func(t *testing.T) {
		imageDone := false
		var nilCombat *models.CombatState
		var nilChase *models.ChaseState
		roundClosed := false
		reject := directorBatchPolicy(&imageDone, &nilCombat, &nilChase, &roundClosed, nil, func(string) {})([]llm.ToolCall{
			policyCall("combat_act", `{"combat_actor_name":"甲","combat_action":{"type":"attack"}}`),
		})
		if reject == "" {
			t.Error("没有激活战斗时combat_act应被拒")
		}
	})

	t.Run("追逐激活时start_combat被拒(互斥)", func(t *testing.T) {
		imageDone := false
		var nilCombat *models.CombatState
		chase := freshChase()
		roundClosed := false
		reject := directorBatchPolicy(&imageDone, &nilCombat, &chase, &roundClosed, nil, func(string) {})([]llm.ToolCall{
			policyCall("start_combat", `{"combat_participants":[{"name":"甲","is_npc":true}]}`),
		})
		if reject == "" {
			t.Error("追逐激活时start_combat应被拒(战斗与追逐互斥)")
		}
	})

	t.Run("战斗激活时advance_time被拒", func(t *testing.T) {
		imageDone := false
		combat, chase, roundClosed := freshCombat(), (*models.ChaseState)(nil), false
		reject := directorBatchPolicy(&imageDone, &combat, &chase, &roundClosed, nil, func(string) {})([]llm.ToolCall{
			policyCall("advance_time", `{"time_rounds":1,"time_reason":"等待"}`),
		})
		if reject == "" {
			t.Error("战斗激活时advance_time应被拒")
		}
	})

	t.Run("可推进者未结算时response被拒并列出未行动者", func(t *testing.T) {
		imageDone := false
		combat, chase, roundClosed := freshCombat(), (*models.ChaseState)(nil), false
		// 甲乙均已提交本轮声明(在当前批次内),因此未行动即视为可推进,应挡住response。
		declared := []PlayerAction{{PlayerName: "甲", Content: "攻击"}, {PlayerName: "乙", Content: "闪避"}}
		reject := directorBatchPolicy(&imageDone, &combat, &chase, &roundClosed, declared, func(string) {})([]llm.ToolCall{
			policyCall("response", `{"reply":"战斗继续"}`),
		})
		if reject == "" {
			t.Fatal("还有已声明但未行动的可推进参战者时response应被拒")
		}
		if !strings.Contains(reject, "乙") {
			t.Errorf("reject = %q, want 提及还未行动的乙", reject)
		}
	})

	t.Run("剩余全是未提交声明的PC时response放行", func(t *testing.T) {
		imageDone := false
		actedCombat := freshCombat()
		actedCombat.Participants[0].HasActed = true // 甲已行动(当前批次内唯一成员)
		combat, chase, roundClosed := actedCombat, (*models.ChaseState)(nil), false
		// pendingActions为nil:乙不在当前批次,本轮未提交任何声明,不应卡住response。
		reject := directorBatchPolicy(&imageDone, &combat, &chase, &roundClosed, nil, func(string) {})([]llm.ToolCall{
			policyCall("response", `{"reply":"甲行动完毕,轮到乙,但乙尚未到批次"}`),
		})
		if reject != "" {
			t.Errorf("剩余全是未提交声明的PC时response应放行,got reject: %s", reject)
		}
	})

	t.Run("存在待澄清时response放行", func(t *testing.T) {
		imageDone := false
		pendingCombat := freshCombat()
		pendingCombat.Participants[1].PendingClarification = true
		pendingCombat.Participants[1].PendingQuestion = "闪避还是反击？"
		combat, chase, roundClosed := pendingCombat, (*models.ChaseState)(nil), false
		reject := directorBatchPolicy(&imageDone, &combat, &chase, &roundClosed, nil, func(string) {})([]llm.ToolCall{
			policyCall("response", `{"reply":"你被攻击了,闪避还是反击？"}`),
		})
		if reject != "" {
			t.Errorf("存在PendingClarification时response应放行,got reject: %s", reject)
		}
	})

	t.Run("roundClosed为true时response放行", func(t *testing.T) {
		imageDone := false
		combat, chase, roundClosed := freshCombat(), (*models.ChaseState)(nil), true
		reject := directorBatchPolicy(&imageDone, &combat, &chase, &roundClosed, nil, func(string) {})([]llm.ToolCall{
			policyCall("response", `{"reply":"本轮战斗结束"}`),
		})
		if reject != "" {
			t.Errorf("roundClosed=true时response应放行,got reject: %s", reject)
		}
	})

	t.Run("end_combat与response同批放行(即使未结算完毕)", func(t *testing.T) {
		imageDone := false
		combat, chase, roundClosed := freshCombat(), (*models.ChaseState)(nil), false
		reject := directorBatchPolicy(&imageDone, &combat, &chase, &roundClosed, nil, func(string) {})([]llm.ToolCall{
			policyCall("end_combat", `{"combat_end_reason":"敌人逃离"}`),
			policyCall("response", `{"reply":"战斗结束,敌人逃走了"}`),
		})
		if reject != "" {
			t.Errorf("end_combat+response应放行,got reject: %s", reject)
		}
	})
}

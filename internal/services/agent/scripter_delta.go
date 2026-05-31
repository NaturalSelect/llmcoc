// scripter_delta.go — δ-operator taxonomy and IronyCore type.
//
// IronyCore is kept for ScenarioCreationOutput backward compatibility.
// To add a new δ-operator: append one DeltaOperator entry to DeltaOperators.
package agent

import (
	"fmt"
	"strings"
)

// ---------------------------------------------------------------------------
// δ-operator extensible taxonomy
// ---------------------------------------------------------------------------

// DeltaOperator describes one transformation operator in the δ-framework.
type DeltaOperator struct {
	ID          string   // machine-readable identifier, e.g. "identity_collapse"
	Name        string   // display name
	Description string   // semantic dimension being transformed
	Examples    []string // literary / film archetypes (non-CoC)
}

// DeltaOperators is the canonical operator table.
// Append new entries here to extend the taxonomy; prompts are rendered
// dynamically from this slice, so no prompt templates need editing.
var DeltaOperators = []DeltaOperator{
	{
		ID: "identity_collapse", Name: "身份坍缩",
		Description: "施事身份错——X不是我们认为的X；身份被替换、伪装或分裂",
		Examples:    []string{"《蝴蝶梦》女管家真实身份", "双重人格凶手"},
	},
	{
		ID: "role_swap", Name: "角色互换",
		Description: "施事/受事互换——受害者与施害者的关系被倒置",
		Examples:    []string{"看似加害者实为被保护者", "举报者是真正的危险源"},
	},
	{
		ID: "causal_inversion", Name: "因果倒置",
		Description: "原因与结果方向错——我们观察到的后果被误认为原因",
		Examples:    []string{"症状被当作疾病本身", "防御行为被解读为攻击"},
	},
	{
		ID: "scale_lift", Name: "尺度跃升",
		Description: "场所/范围层级错——个人事件是宏观力量的截面",
		Examples:    []string{"一桩谋杀是整个制度性压迫的直接产物", "个人悲剧指向社会结构"},
	},
	{
		ID: "temporal_shift", Name: "时间移位",
		Description: "时间位置/序列被混淆——过去、未来或事件顺序与表象不符",
		Examples:    []string{"以为是预言的已经发生", "以为是历史的还在继续"},
	},
	{
		ID: "agency_collapse", Name: "能动性坍缩",
		Description: "主动/被动互换——主语和宾语的行动力被颠倒",
		Examples:    []string{"以为在追查的人其实是被引导的", "猎人与猎物角色互换"},
	},
	{
		ID: "moral_inversion", Name: "道德反转",
		Description: "善恶/受害判断被颠覆——规范性评价与外部表象相反",
		Examples:    []string{"公认的善举造成了真正的伤害", "\"怪物\"是真正的受害者"},
	},
	{
		ID: "intent_shift", Name: "意图偏移",
		Description: "行动表面正确但目的指向完全不同——不是\"谁做的\"而是\"为了什么\"发生了根本转变",
		Examples:    []string{"看似复仇实为自我牺牲", "看似背叛实为保护"},
	},
	{
		ID: "existence_inversion", Name: "存在反转",
		Description: "X的本体论层级被颠覆——以为活着的早已死亡，以为是人的是另一种存在，以为是真实事件的是虚构或幻觉（或反之）；翻转的不是\"X是谁\"而是\"X是什么/X是否存在\"",
		Examples:    []string{"《第六感》主角全程是鬼魂", "\"幸存者\"早在第一幕已死，读者一直在跟随一个亡者的视角"},
	},
	{
		ID: "perception_collapse", Name: "感知坍缩",
		Description: "目击者/叙述者的感知本身不可信——关键事件是幻觉、梦境、催眠或记忆篡改的产物，读者随叙述者一起经历了一套不真实的事件链",
		Examples:    []string{"《穆赫兰道》全片是死前意识的幻构", "主角亲历的「谋杀现场」是解离状态下的自我投射"},
	},
	{
		ID: "overread_trap", Name: "过读陷阱",
		Description: "真相从未隐藏——表层陈述就是事实，但观察者的模式识别本能驱使其构建了一个「更深层」的解读，而该解读恰恰是错误的；揭示时的翻转是「谜从来不存在」本身",
		Examples:    []string{"空城计：诸葛亮开城鼓琴，真相就是城中无兵，司马懿因过度解读而退兵", "目击者第一句话就是真相，侦探以为那是烟幕"},
	},
	{
		ID: "connection_inversion", Name: "关联反转",
		Description: "事件/实体之间的因果联系被颠覆——以为相互独立的事件实际出自同一意志/起源，或以为有关联的系列事件实际是彼此无关的巧合叠加；翻转的是「是否存在统一的幕后」",
		Examples:    []string{"三起看似偶发失踪实为同一捕猎者的连续猎食", "「连环凶案」实为三个不相关人分别独立作案在时间上的偶然重叠"},
	},
	{
		ID: "knowledge_reversal", Name: "知识反转",
		Description: "知情与不知情的状态互换——自以为掌握真相的人实为最深的蒙蔽者，自以为无知的人却拥有关键信息而不自知",
		Examples: []string{
			"所有人都认为最聪明的侦探洞悉一切，实际上他被告知的全是精心编排的假象",
			"被当作傻瓜的配角一直无意中说出真相，却没人相信他",
		},
	},
	{
		ID: "signal_noise_reversal", Name: "信号-噪声反转",
		Description: "信息显著性颠倒——所有被强调的「线索」都是干扰，唯一真实的线索被当作背景噪声忽略",
		Examples: []string{
			"侦探反复调查「神秘脚印」，那是凶手故意伪造的；真正的线索是每天准时出现的送奶工",
			"法医报告里加粗标注的异常数据全是误导，角落里一个不起眼的正常值才是突破口",
		},
	},
	{
		ID: "false_pattern", Name: "伪模式陷阱",
		Description: "独立实体因行为特征上的相似性被误判为同一源头或相关联，实际彼此毫无因果联系",
		Examples: []string{
			"三个街区手法一模一样的盗窃案，警方推断是连环作案，结果三人互不认识，只是独立模仿网上教程",
			"主角收到风格与已入狱罪犯完全相同的威胁信，所有人以为是同伙所为，最后发现是一个无关粉丝在模仿字迹",
		},
	},
	// Future operators: append here ↓
}

// formatDeltaOperatorTable renders DeltaOperators as a human-readable block
// for injection into LLM prompts.
func formatDeltaOperatorTable() string {
	var sb strings.Builder
	sb.WriteString("【认知翻转类型参考表】\n")
	sb.WriteString("每个故事的揭示结构依赖一种「认知翻转」——当真相揭晓时，读者对某件事的理解发生了什么根本性变化。\n")
	sb.WriteString("delta_operator 字段填写下表中的 ID（或自定义一个新 ID，同时在 delta_operator_desc 写明其含义）。\n")
	for _, op := range DeltaOperators {
		sb.WriteString(fmt.Sprintf("- %s（%s）：%s", op.ID, op.Name, op.Description))
		if len(op.Examples) > 0 {
			sb.WriteString("  例：" + strings.Join(op.Examples, "；"))
		}
		sb.WriteString("\n")
	}
	sb.WriteString("以上类型不够用时，自定义新 ID（英文下划线格式）并在 delta_operator_desc 写明含义即可。\n")
	return sb.String()
}

// knownDeltaOperatorID returns true if id matches a registered operator.
func knownDeltaOperatorID(id string) bool {
	for _, op := range DeltaOperators {
		if op.ID == id {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------------------
// IronyCore — kept for ScenarioCreationOutput backward compatibility.
// In the single-shot pipeline, fields are extracted from oneshotResult.
// ---------------------------------------------------------------------------

type IronyCore struct {
	DeltaOperator     string   `json:"delta_operator"`
	DeltaOperatorDesc string   `json:"delta_operator_desc,omitempty"`
	SurfaceReading    string   `json:"surface_reading"`
	DeepTruth         string   `json:"deep_truth"`
	Entities          []string `json:"entities,omitempty"`
	FalseDelta        string   `json:"false_delta,omitempty"`
	SharedEvidence    string   `json:"shared_evidence,omitempty"`
	EmotionalWeight   string   `json:"emotional_weight,omitempty"`
}

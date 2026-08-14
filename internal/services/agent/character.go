package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"

	"github.com/llmcoc/server/internal/models"
	"github.com/llmcoc/server/internal/services/llm"
)

// GenerateCharacterReq is the input for AI character generation.
type GenerateCharacterReq struct {
	Name       string
	Occupation string
	Background string
	Era        string
	Gender     string
	Age        int
	Stats      models.CharacterStats
}

// GeneratedCharacter is the output from AI character generation.
type GeneratedCharacter struct {
	Backstory  string                 `json:"backstory"`
	Appearance string                 `json:"appearance"`
	Traits     string                 `json:"traits"`
	Stats      *models.CharacterStats `json:"stats"`
}

// characterAskLawyer 是外貌/角色生成流程共用的 ask_lawyer 工具执行逻辑：委托给
// runLawyer 做规则书自主检索，返回格式化后的裁定文本，供 respond 前确认属性数值含义。
func characterAskLawyer(ctx context.Context, lawyerHandle agentHandle, question string) string {
	question = strings.TrimSpace(question)
	if question == "" {
		return `<ask_lawyer_result error="question字段为空"/>`
	}
	log.Printf("[agent] character ask_lawyer question=%q", truncateRunes(question, 300))
	results := runLawyer(ctx, lawyerHandle, question)
	if len(results) == 0 {
		return fmt.Sprintf(`<ask_lawyer_result question=%q status="no_result">规则书中未找到相关内容；可换用更具体的问题重新提问。</ask_lawyer_result>`, question)
	}
	return fmt.Sprintf(`<ask_lawyer_result question=%q status="found">%s</ask_lawyer_result>`, question, formatLawyerResults(results))
}

// appearanceAgentSystemPrompt 是 RegenerateAppearance 的系统提示：要求先查规则书再 respond。
const appearanceAgentSystemPrompt = `<role>COC7外貌描写专家</role>
<task>根据调查员的姓名、职业、性别、年龄和STR/CON/SIZ/DEX/APP属性数值，撰写一段外貌描述。通过ask_lawyer工具向规则书专家查询这些属性数值及年龄对应的体格、气色、身手、外貌等级含义，然后通过respond工具返回最终外貌描述。</task>
<design_rules>
- respond前必须至少调用一次ask_lawyer，确认属性数值的规则书含义，不得凭印象直接respond。
- 外貌描述100字以内，只描述身体特征(发色、发型、眼睛颜色、肤色、身高、体型、女性还包括胸部特征等)和气质，不包括服饰。
- 描述需与查询到的属性数值、年龄含义相符。
</design_rules>`

// appearanceRespondTool 是 RegenerateAppearance 的 respond 工具定义（solo，终止本轮循环）。
func appearanceRespondTool() scripterTool {
	return scripterTool{
		solo: true,
		def: llm.ToolDefinition{
			Name:        toolNameRespond,
			Description: "返回最终外貌描述并结束；必须在至少一次ask_lawyer之后调用；必须单独一轮调用。",
			Parameters: jsonSchemaObject(`{
				"type": "object",
				"properties": {
					"appearance": {"type": "string", "description": "100字以内的外貌描述，只描述身体特征和气质，不包括服饰"}
				},
				"required": ["appearance"]
			}`),
		},
	}
}

// RegenerateAppearance uses the Evaluator agent to produce a fresh appearance description
// for an existing character. It queries the rulebook via the Lawyer agent (ask_lawyer tool)
// so the description is grounded in the actual attribute-value meanings, not guesswork.
func RegenerateAppearance(ctx context.Context, card *models.CharacterCard) (string, error) {
	handle, err := loadSingleAgent(models.AgentRoleEvaluator)
	if err != nil {
		return "", err
	}
	lawyerHandle, err := loadSingleAgent(models.AgentRoleLawyer)
	if err != nil {
		return "", err
	}

	name := card.Name
	if name == "" {
		name = "(未指定)"
	}
	occupation := card.Occupation
	if occupation == "" {
		occupation = "调查员"
	}
	gender := card.Gender
	if gender == "" {
		gender = "(未指定)"
	}
	age := "(未指定)"
	if card.Age > 0 {
		age = fmt.Sprintf("%d", card.Age)
	}

	prompt := fmt.Sprintf(`请为克苏鲁神话TRPG(COC第七版)调查员重新生成外貌描述。

调查员信息:
- 姓名:%s
- 职业:%s
- 性别:%s
- 年龄:%s
- 属性:STR=%d CON=%d SIZ=%d DEX=%d APP=%d

请先通过ask_lawyer查询规则书中这些属性数值及年龄对应的体格、气色、身手、外貌等级含义，再据此撰写外貌描述，与之前不同。`,
		name, occupation, gender, age,
		card.Stats.Data.STR, card.Stats.Data.CON, card.Stats.Data.SIZ,
		card.Stats.Data.DEX, card.Stats.Data.APP,
	)

	msgs := []llm.ChatMessage{
		{Role: "system", Content: appearanceAgentSystemPrompt},
		{Role: "user", Content: prompt},
	}

	tools := []scripterTool{
		askLawyerTool("向COC7规则书专家提出一个具体规则书问题；查询STR/CON/SIZ/DEX/APP等属性数值或年龄对外貌、体格的影响；可多次调用"),
		appearanceRespondTool(),
	}

	askedLawyer := false
	var appearance string
	dispatch := func(ctx context.Context, call llm.ToolCall) toolOutcome {
		switch call.Name {
		case toolNameAskLawyer:
			var args askLawyerArgs
			if err := json.Unmarshal([]byte(call.Arguments), &args); err != nil {
				return toolOutcome{reject: "SYSTEM REJECT: ask_lawyer参数不是合法JSON，请重新调用。"}
			}
			askedLawyer = true
			return toolOutcome{result: characterAskLawyer(ctx, lawyerHandle, args.Question)}
		case toolNameRespond:
			if !askedLawyer {
				return toolOutcome{reject: "SYSTEM REJECT: respond前必须至少调用一次ask_lawyer。"}
			}
			var args struct {
				Appearance string `json:"appearance"`
			}
			if err := json.Unmarshal([]byte(call.Arguments), &args); err != nil {
				return toolOutcome{reject: "SYSTEM REJECT: respond参数不是合法JSON，请重新调用。"}
			}
			if strings.TrimSpace(args.Appearance) == "" {
				return toolOutcome{reject: "SYSTEM REJECT: respond的appearance字段不能为空。"}
			}
			appearance = args.Appearance
			return toolOutcome{result: "已收到，外貌描述已提交。", done: true}
		default:
			return toolOutcome{reject: fmt.Sprintf("SYSTEM REJECT: 只允许ask_lawyer/respond，不允许%s。", call.Name)}
		}
	}

	const maxRounds = 8
	if err := runScripterToolLoop(ctx, nil, handle, "regenerate_appearance", msgs, tools, maxRounds, dispatch); err != nil {
		return "", err
	}
	if appearance == "" {
		return "", fmt.Errorf("LLM returned empty appearance")
	}
	return appearance, nil
}

// RegenerateBackstory uses the Writer agent to produce a fresh backstory
// for an existing character.
func RegenerateBackstory(ctx context.Context, card *models.CharacterCard) (string, error) {
	handle, err := loadSingleAgent(models.AgentRoleEvaluator)
	if err != nil {
		return "", err
	}

	name := card.Name
	if name == "" {
		name = "(未指定)"
	}
	occupation := card.Occupation
	if occupation == "" {
		occupation = "调查员"
	}
	gender := card.Gender
	if gender == "" {
		gender = "(未指定)"
	}

	prompt := fmt.Sprintf(`请为克苏鲁神话TRPG(COC第七版)调查员重新生成个人经历,以JSON格式返回,不要有任何额外文字。

调查员信息:
- 姓名:%s
- 职业:%s
- 性别:%s

要求:个人经历200字以内,记录成长经历等人生轨迹,与之前的内容不同。

请返回如下JSON格式:
{"backstory": "个人经历"}`,
		name, occupation, gender,
	)

	msgs := []llm.ChatMessage{
		{Role: "system", Content: "你是一名克苏鲁神话TRPG专家,只输出JSON,不输出任何其他内容。"},
		{Role: "user", Content: prompt},
	}

	resp, err := handle.provider.JsonChat(ctx, "nosession:evaluator", msgs)
	if err != nil {
		return "", err
	}

	var out struct {
		Backstory string `json:"backstory"`
	}
	if err := json.Unmarshal([]byte(resp), &out); err != nil {
		return "", fmt.Errorf("parse LLM response failed: %w (raw: %s)", err, resp)
	}
	if out.Backstory == "" {
		return "", fmt.Errorf("LLM returned empty backstory (raw: %s)", resp)
	}
	return out.Backstory, nil
}

// RegenerateTraits uses the Writer agent to produce fresh personality traits
// for an existing character.
func RegenerateTraits(ctx context.Context, card *models.CharacterCard) (string, error) {
	handle, err := loadSingleAgent(models.AgentRoleEvaluator)
	if err != nil {
		return "", err
	}

	name := card.Name
	if name == "" {
		name = "(未指定)"
	}
	occupation := card.Occupation
	if occupation == "" {
		occupation = "调查员"
	}
	gender := card.Gender
	if gender == "" {
		gender = "(未指定)"
	}
	backstory := card.Backstory
	if backstory == "" {
		backstory = "(无)"
	}

	prompt := fmt.Sprintf(`请为克苏鲁神话TRPG(COC第七版)调查员重新生成性格特征,以JSON格式返回,不要有任何额外文字。

调查员信息:
- 姓名:%s
- 职业:%s
- 性别:%s
- 背景故事:%s

要求:性格特征以空格分隔,1-5个标签,包含语言风格、性格特点等，二次元风格, 如:雌小鬼 大和抚子等,与之前的特征不同。

请返回如下JSON格式:
{"traits": "特征1 特征2 特征3"}`,
		name, occupation, gender, backstory,
	)

	msgs := []llm.ChatMessage{
		{Role: "system", Content: "你是一名克苏鲁神话TRPG专家,只输出JSON,不输出任何其他内容。"},
		{Role: "user", Content: prompt},
	}

	resp, err := handle.provider.JsonChat(ctx, "nosession:evaluator", msgs)
	if err != nil {
		return "", err
	}

	var out struct {
		Traits string `json:"traits"`
	}
	if err := json.Unmarshal([]byte(resp), &out); err != nil {
		return "", fmt.Errorf("parse LLM response failed: %w (raw: %s)", err, resp)
	}
	if out.Traits == "" {
		return "", fmt.Errorf("LLM returned empty traits (raw: %s)", resp)
	}
	return out.Traits, nil
}

// AdjustSkillsReq is the input for AI skill adjustment.
type AdjustSkillsReq struct {
	Name       string
	Occupation string
	Background string
	Era        string
	Stats      models.CharacterStats
	BaseSkills map[string]int
}

// generateCharacterSystemPrompt 是 GenerateCharacter 的系统提示：要求先查规则书再 respond。
const generateCharacterSystemPrompt = `<role>COC7调查员生成专家</role>
<task>根据调查员的姓名、时代背景、年龄、职业、性别、角色构思和骰子已生成的基础属性，生成完整的调查员信息（背景故事、外貌描述、性格特征），并可按用户消息给出的规则重新分配属性点。通过ask_lawyer工具向规则书专家查询属性数值对应的体格、气色、身手、外貌等级含义，然后通过respond工具返回最终结果。</task>
<design_rules>
- respond前必须至少调用一次ask_lawyer，确认属性数值的规则书含义，不得凭印象直接respond。
- 外貌描述需与查询到的属性数值含义相符，100字以内，只描述身体特征和气质，不包括服饰。
- 属性重分配必须遵守用户消息中给出的分组总和与数值范围约束。
</design_rules>`

// generateCharacterRespondTool 是 GenerateCharacter 的 respond 工具定义（solo，终止本轮循环）。
func generateCharacterRespondTool() scripterTool {
	return scripterTool{
		solo: true,
		def: llm.ToolDefinition{
			Name:        toolNameRespond,
			Description: "返回调查员的背景故事、外貌、性格特征与最终属性并结束；必须在至少一次ask_lawyer之后调用；必须单独一轮调用。",
			Parameters: jsonSchemaObject(`{
				"type": "object",
				"properties": {
					"backstory": {"type": "string", "description": "200字以内的个人经历（成长背景、人生轨迹等）"},
					"appearance": {"type": "string", "description": "100字以内的外貌描述(发色、发型、眼睛颜色、肤色、身高、体型、女性还包括胸部特征等)和气质，不包括服饰"},
					"traits": {"type": "string", "description": "性格特征，以空格分隔，1-5个标签，包含语言风格、性格特点等，二次元风格，如：雌小鬼 大和抚子等"},
					"stats": {
						"type": "object",
						"properties": {
							"STR": {"type": "integer"}, "CON": {"type": "integer"}, "SIZ": {"type": "integer"}, "DEX": {"type": "integer"},
							"APP": {"type": "integer"}, "INT": {"type": "integer"}, "POW": {"type": "integer"}, "EDU": {"type": "integer"}
						},
						"required": ["STR", "CON", "SIZ", "DEX", "APP", "INT", "POW", "EDU"]
					}
				},
				"required": ["backstory", "appearance", "traits", "stats"]
			}`),
		},
	}
}

// GenerateCharacter uses the Writer agent to fill in character backstory, appearance,
// traits, and optionally redistributes base stats. It queries the rulebook via the
// Lawyer agent (ask_lawyer tool) so the appearance is grounded in the actual
// attribute-value meanings, not guesswork.
func GenerateCharacter(ctx context.Context, req GenerateCharacterReq) (*GeneratedCharacter, error) {
	handle, err := loadSingleAgent(models.AgentRoleEvaluator)
	if err != nil {
		return nil, err
	}
	lawyerHandle, err := loadSingleAgent(models.AgentRoleLawyer)
	if err != nil {
		return nil, err
	}

	era := req.Era
	if era == "" {
		era = "1920年代"
	}
	occupation := req.Occupation
	if occupation == "" {
		occupation = "调查员"
	}
	name := req.Name
	if name == "" {
		name = "(未指定)"
	}
	gender := req.Gender
	if gender == "" {
		gender = "(未指定)"
	}
	age := "(未指定)"
	if req.Age > 0 {
		age = fmt.Sprintf("%d", req.Age)
	}

	prompt := fmt.Sprintf(`请为克苏鲁神话TRPG(COC第七版)生成一名调查员的详细信息。

要求:
- 调查员姓名:%s
- 时代背景:%s
- 年龄:%s
- 职业:%s
- 性别:%s
- 角色构思:%s
- 骰子已生成的基础属性:STR=%d CON=%d SIZ=%d DEX=%d APP=%d INT=%d POW=%d EDU=%d

【属性重分配规则】
你可以在不改变以下两组属性总和的前提下,将属性点在组内重新分配,以更符合职业和背景:
  - 第一组(可自由互换):STR、CON、DEX、APP、POW — 当前总和=%d
  - 第二组(可自由互换):SIZ、INT、EDU — 当前总和=%d
  - 约束:每个属性均为5的倍数；STR/CON/DEX/APP/POW 范围 15-90；SIZ/INT/EDU 范围 40-90
  - 若无需调整,原样返回即可

请先通过ask_lawyer查询规则书中STR/CON/SIZ/DEX/APP等属性数值对应的体格、气色、身手、外貌等级含义，再据此撰写外貌描述，与体型、气质相符。`,
		name, era, age, occupation, gender, req.Background,
		req.Stats.STR, req.Stats.CON, req.Stats.SIZ,
		req.Stats.DEX, req.Stats.APP, req.Stats.INT,
		req.Stats.POW, req.Stats.EDU,
		req.Stats.STR+req.Stats.CON+req.Stats.DEX+req.Stats.APP+req.Stats.POW,
		req.Stats.SIZ+req.Stats.INT+req.Stats.EDU,
	)

	msgs := []llm.ChatMessage{
		{Role: "system", Content: generateCharacterSystemPrompt},
		{Role: "user", Content: prompt},
	}

	tools := []scripterTool{
		askLawyerTool("向COC7规则书专家提出一个具体规则书问题；查询属性数值对应的体格、气色、身手、外貌等级含义；可多次调用"),
		generateCharacterRespondTool(),
	}

	askedLawyer := false
	var out GeneratedCharacter
	dispatch := func(ctx context.Context, call llm.ToolCall) toolOutcome {
		switch call.Name {
		case toolNameAskLawyer:
			var args askLawyerArgs
			if err := json.Unmarshal([]byte(call.Arguments), &args); err != nil {
				return toolOutcome{reject: "SYSTEM REJECT: ask_lawyer参数不是合法JSON，请重新调用。"}
			}
			askedLawyer = true
			return toolOutcome{result: characterAskLawyer(ctx, lawyerHandle, args.Question)}
		case toolNameRespond:
			if !askedLawyer {
				return toolOutcome{reject: "SYSTEM REJECT: respond前必须至少调用一次ask_lawyer。"}
			}
			var args GeneratedCharacter
			if err := json.Unmarshal([]byte(call.Arguments), &args); err != nil {
				return toolOutcome{reject: fmt.Sprintf("SYSTEM REJECT: respond参数不是合法JSON：%v，请重新调用。", err)}
			}
			if strings.TrimSpace(args.Backstory) == "" || strings.TrimSpace(args.Appearance) == "" ||
				strings.TrimSpace(args.Traits) == "" || args.Stats == nil {
				return toolOutcome{reject: "SYSTEM REJECT: respond的backstory/appearance/traits/stats字段不能为空。"}
			}
			out = args
			return toolOutcome{result: "已收到，调查员信息已提交。", done: true}
		default:
			return toolOutcome{reject: fmt.Sprintf("SYSTEM REJECT: 只允许ask_lawyer/respond，不允许%s。", call.Name)}
		}
	}

	const maxRounds = 10
	if err := runScripterToolLoop(ctx, nil, handle, "generate_character", msgs, tools, maxRounds, dispatch); err != nil {
		return nil, err
	}
	return &out, nil
}

// AdjustSkills uses the Evaluator agent to redistribute skill points to fit the
// character's occupation and background.
func AdjustSkills(ctx context.Context, req AdjustSkillsReq) (map[string]int, error) {
	handle, err := loadSingleAgent(models.AgentRoleEvaluator)
	if err != nil {
		return nil, err
	}

	era := req.Era
	if era == "" {
		era = "现代"
	}
	occupation := req.Occupation
	if occupation == "" {
		occupation = "调查员"
	}

	var sb strings.Builder
	for k, v := range req.BaseSkills {
		sb.WriteString(fmt.Sprintf("  %s: %d\n", k, v))
	}

	occPoints := req.Stats.EDU * 4
	intPoints := req.Stats.INT * 2

	prompt := fmt.Sprintf(`你是COC第七版规则专家。请根据调查员的职业和背景,合理分配技能加成点,输出调整后的完整技能列表(JSON对象)。

【调查员信息】
- 姓名:%s
- 时代:%s
- 职业:%s
- 背景提示:%s
- 属性:STR=%d CON=%d SIZ=%d DEX=%d APP=%d INT=%d POW=%d EDU=%d

【当前技能基础值】
%s

【技能分配规则】

1. 职业技能点(共 %d 点 = EDU×4):分配给与职业强相关的技能(例如医生必须高医学、急救、心理学等)
2. 兴趣技能点(共 %d 点 = INT×2):分配给调查员个人兴趣或背景相关技能
3. 每项技能最终值(基础值 + 加成点)上限 90
4. 加成点只能加在现有技能列表中的技能上,不得新增技能名称
5. 把所有职业技能点和兴趣技能点完整分配出去,不要剩余

请直接输出完整技能JSON对象(包含所有技能,包括未改动的),格式示例:

{"医学":75,"急救":60,"心理学":50,...}

只输出JSON,不要任何其他文字。`,
		req.Name, era, occupation, req.Background,
		req.Stats.STR, req.Stats.CON, req.Stats.SIZ,
		req.Stats.DEX, req.Stats.APP, req.Stats.INT,
		req.Stats.POW, req.Stats.EDU,
		sb.String(),
		occPoints, intPoints,
	)

	debugf("skills", "prompt: %v", prompt)

	msgs := []llm.ChatMessage{
		{Role: "system", Content: "你是COC第七版规则专家。请根据调查员的职业和背景,合理分配技能加成点,输出调整后的完整技能列表(JSON对象)"},
		{Role: "user", Content: prompt},
	}

	resp, err := handle.provider.JsonChat(ctx, "nosession:evaluator", msgs)
	if err != nil {
		return nil, err
	}
	debugf("skills", "raw resp %v", resp)

	var raw map[string]int
	if err := json.Unmarshal([]byte(resp), &raw); err != nil {
		for i := 0; i < 30; i++ {
			resp, err = RepairJSON(ctx, resp, err, `{"A":1,"B":2,"C":3}`)
			if err == nil {
				err = json.Unmarshal([]byte(resp), &raw)
				if err == nil {
					break
				}
			}
			log.Printf("[agent] AdjustSkills JSON parse error attempt %d: %v", i+1, err)
		}
		if err != nil {
			return nil, fmt.Errorf("AdjustSkills parse failed: %w (raw: %s)", err, resp)
		}
	}
	log.Printf("[agent] AdjustSkills done, skills=%d", len(raw))
	return raw, nil
}

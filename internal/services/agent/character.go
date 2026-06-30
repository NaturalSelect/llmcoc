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
	Stats      *models.CharacterStats `json:"stats,omitempty"`
}

// RegenerateAppearance uses the Evaluator agent to produce a fresh appearance description
// for an existing character. It only needs the fields already stored on the card.
func RegenerateAppearance(ctx context.Context, card *models.CharacterCard) (string, error) {
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
	age := "(未指定)"
	if card.Age > 0 {
		age = fmt.Sprintf("%d", card.Age)
	}

	prompt := fmt.Sprintf(`请为克苏鲁神话TRPG(COC第七版)调查员重新生成外貌描述,以JSON格式返回,不要有任何额外文字。

调查员信息:
- 姓名:%s
- 职业:%s
- 性别:%s
- 年龄:%s
- 属性:STR=%d CON=%d SIZ=%d DEX=%d APP=%d

【属性与外貌的对应关系(来自COC第七版规则书)】
力量(STR): 15=虚弱, 50=普通人, 90=你见过的力气最大的人, 99=世界水平(举重冠军)
体质(CON): 15=体弱多病, 50=普通人, 90=不惧寒冷强壮精神, 99=钢铁之躯
体型(SIZ): 15=孩童或身短体瘦(约15kg), 65=普通体型(约75kg), 80=非常高强健或非常胖(约110kg), 99=超大号(约150kg)
敏捷(DEX): 15=缓慢笨拙, 50=普通人, 90=高速灵活(杂技演员), 99=世界级运动员
外貌(APP): 0=十分难看令人恐惧厌恶, 15=挫(受伤或先天), 50=普通人, 90=最漂亮的人天然吸引力, 99=魅力巅峰(超级名模/世界影星)
伤害加值与体格由STR+SIZ决定: 2-64=-2伤害体格-2(瘦小), 65-84=-1伤害体格-1, 85-124=无加值体格0(普通), 125-164=+1d4伤害体格1(壮实), 165-204=+1d6伤害体格2(魁梧)

【年龄对外貌的影响(来自COC第七版规则书)】
15-19岁: 少年体态,略显青涩
20-39岁: 成年全盛期
40-49岁: 开始显老,外貌(APP)减5
50-59岁: 明显老态,外貌(APP)减10
60-69岁: 老年貌,外貌(APP)减15
70-79岁: 年迈苍老,外貌(APP)减20
80+岁: 风烛残年,外貌(APP)减25

请严格参照上述属性数值和年龄来描写体型和气质:
- STR/SIZ高→身材高大壮实,体格魁梧;STR/SIZ低→身材瘦小纤细
- CON高→面色红润精力充沛;CON低→面色苍白体弱多病
- DEX高→动作矫健姿态灵活;DEX低→动作迟缓笨拙
- APP高→容貌出众气质迷人;APP低→相貌平平甚至丑陋
- 年龄大→白发皱纹老态;年龄小→青春稚嫩

要求:外貌描述100字以内,只描述身体特征(发色、发型、眼睛颜色、肤色、身高、体型、女性还包括胸部特征等)和气质,不包括服饰,与之前不同。

请返回如下JSON格式:
{"appearance": "外貌描述"}`,
		name, occupation, gender, age,
		card.Stats.Data.STR, card.Stats.Data.CON, card.Stats.Data.SIZ,
		card.Stats.Data.DEX, card.Stats.Data.APP,
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
		Appearance string `json:"appearance"`
	}
	if err := json.Unmarshal([]byte(resp), &out); err != nil {
		return "", fmt.Errorf("parse LLM response failed: %w (raw: %s)", err, resp)
	}
	if out.Appearance == "" {
		return "", fmt.Errorf("LLM returned empty appearance (raw: %s)", resp)
	}
	return out.Appearance, nil
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

	prompt := fmt.Sprintf(`请为克苏鲁神话TRPG(COC第七版)调查员重新生成背景故事,以JSON格式返回,不要有任何额外文字。

调查员信息:
- 姓名:%s
- 职业:%s
- 性别:%s

要求:背景故事200字以内,包含成长经历、进入调查行业的契机等,与之前的故事不同。

请返回如下JSON格式:
{"backstory": "背景故事"}`,
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

// GenerateCharacter uses the Writer agent to fill in character backstory, appearance,
// traits, and optionally redistributes base stats.
func GenerateCharacter(ctx context.Context, req GenerateCharacterReq) (*GeneratedCharacter, error) {
	handle, err := loadSingleAgent(models.AgentRoleEvaluator)
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

	prompt := fmt.Sprintf(`请为克苏鲁神话TRPG(COC第七版)生成一名调查员的详细信息,以JSON格式返回,不要有任何额外文字。

要求:
- 调查员姓名:%s
- 时代背景:%s
- 年龄:%s
- 职业:%s
- 性别:%s
- 玩家背景提示:%s
- 骰子已生成的基础属性:STR=%d CON=%d SIZ=%d DEX=%d APP=%d INT=%d POW=%d EDU=%d

【属性重分配规则】
你可以在不改变以下两组属性总和的前提下,将属性点在组内重新分配,以更符合职业和背景:
  - 第一组(可自由互换):STR、CON、DEX、APP、POW — 当前总和=%d
  - 第二组(可自由互换):SIZ、INT、EDU — 当前总和=%d
  - 约束:每个属性均为5的倍数；STR/CON/DEX/APP/POW 范围 15-90；SIZ/INT/EDU 范围 40-90
  - 若无需调整,原样返回即可

【属性与外貌的对应关系(来自COC第七版规则书)】
力量(STR): 15=虚弱, 50=普通人, 90=你见过的力气最大的人, 99=世界水平(举重冠军)
体质(CON): 15=体弱多病, 50=普通人, 90=不惧寒冷强壮精神, 99=钢铁之躯
体型(SIZ): 15=孩童或身短体瘦(约15kg), 65=普通体型(约75kg), 80=非常高强健或非常胖(约110kg), 99=超大号(约150kg)
敏捷(DEX): 15=缓慢笨拙, 50=普通人, 90=高速灵活(杂技演员), 99=世界级运动员
外貌(APP): 0=十分难看令人恐惧厌恶, 15=挫(受伤或先天), 50=普通人, 90=最漂亮的人天然吸引力, 99=魅力巅峰(超级名模/世界影星)
伤害加值与体格由STR+SIZ决定: 2-64=-2伤害体格-2(瘦小), 65-84=-1伤害体格-1, 85-124=无加值体格0(普通), 125-164=+1d4伤害体格1(壮实), 165-204=+1d6伤害体格2(魁梧)

请严格参照上述属性数值来描写体型和气质:
- STR/SIZ高→身材高大壮实,体格魁梧;STR/SIZ低→身材瘦小纤细
- CON高→面色红润精力充沛;CON低→面色苍白体弱多病
- DEX高→动作矫健姿态灵活;DEX低→动作迟缓笨拙
- APP高→容貌出众气质迷人;APP低→相貌平平甚至丑陋

请返回如下JSON格式(所有字段都用中文):
{
  "backstory": "200字以内的背景故事",
  "appearance": "100字以内的外貌描述(发色、发型、眼睛颜色、肤色、身高、体型、女性还包括胸部特征等)和气质,不包括服饰",
  "traits": "性格特征(以空格分隔,1-5个标签,包含语言风格、性格特点等，二次元风格, 如:雌小鬼 大和抚子等)",
  "stats": {"STR":N,"CON":N,"SIZ":N,"DEX":N,"APP":N,"INT":N,"POW":N,"EDU":N}
}`,
		name, era, age, occupation, gender, req.Background,
		req.Stats.STR, req.Stats.CON, req.Stats.SIZ,
		req.Stats.DEX, req.Stats.APP, req.Stats.INT,
		req.Stats.POW, req.Stats.EDU,
		req.Stats.STR+req.Stats.CON+req.Stats.DEX+req.Stats.APP+req.Stats.POW,
		req.Stats.SIZ+req.Stats.INT+req.Stats.EDU,
	)

	msgs := []llm.ChatMessage{
		{Role: "system", Content: "你是一名克苏鲁神话TRPG专家,只输出JSON,不输出任何其他内容。"},
		{Role: "user", Content: prompt},
	}

	resp, err := handle.provider.JsonChat(ctx, "nosession:evaluator", msgs)
	if err != nil {
		return nil, err
	}

	var out GeneratedCharacter
	if err := json.Unmarshal([]byte(resp), &out); err != nil {
		maxTry := 30
		for i := 0; i < maxTry; i++ {
			resp, err = RepairJSON(ctx, resp, err,
				`{
  "backstory": "200字以内的背景故事",
  "appearance": "100字以内的外貌描述(发色、发型、眼睛颜色、肤色、身高、体型、女性还包括胸部特征等)和气质,不包括服饰",
  "traits": "性格特征(以空格分隔,1-5个标签,包含语言风格、性格特点等，二次元风格, 如:雌小鬼 大和抚子等)",
  "stats": {"STR":N,"CON":N,"SIZ":N,"DEX":N,"APP":N,"INT":N,"POW":N,"EDU":N}
}`)
			if err == nil {
				err = json.Unmarshal([]byte(resp), &out)
				if err == nil {
					break
				}
			}
			log.Printf("[agent] GenerateCharacter JSON parse error attempt %d: %v", i+1, err)
		}
		if err != nil {
			return nil, fmt.Errorf("parse LLM response failed: %w (raw: %s)", err, resp)
		}
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

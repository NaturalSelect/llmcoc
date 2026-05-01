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
	Stats      models.CharacterStats
}

// GeneratedCharacter is the output from AI character generation.
type GeneratedCharacter struct {
	Backstory  string                 `json:"backstory"`
	Appearance string                 `json:"appearance"`
	Traits     string                 `json:"traits"`
	Stats      *models.CharacterStats `json:"stats,omitempty"`
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
	handle, err := loadSingleAgent(models.AgentRoleWriter)
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

	prompt := fmt.Sprintf(`请为克苏鲁神话TRPG(COC第七版)生成一名调查员的详细信息,以JSON格式返回,不要有任何额外文字。

要求:
- 调查员姓名:%s
- 时代背景:%s
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

请返回如下JSON格式(所有字段都用中文):
{
  "backstory": "200字以内的背景故事",
  "appearance": "100字以内的外貌描述",
  "traits": "性格特征与信念,50字以内",
  "stats": {"STR":N,"CON":N,"SIZ":N,"DEX":N,"APP":N,"INT":N,"POW":N,"EDU":N}
}`,
		name, era, occupation, gender, req.Background,
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

	resp, err := handle.provider.Chat(ctx, msgs)
	if err != nil {
		return nil, err
	}

	var out GeneratedCharacter
	if err := json.Unmarshal([]byte(resp), &out); err != nil {
		return nil, fmt.Errorf("parse LLM response failed: %w (raw: %s)", err, resp)
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

	msgs := []llm.ChatMessage{
		{Role: "system", Content: "你是COC第七版规则专家。请根据调查员的职业和背景,合理分配技能加成点,输出调整后的完整技能列表(JSON对象)"},
		{Role: "user", Content: prompt},
	}

	resp, err := handle.provider.Chat(ctx, msgs)
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

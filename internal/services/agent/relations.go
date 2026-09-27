// NOTE: Defines AI agent roles and their interactions.
package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/llmcoc/server/internal/models"
	"github.com/llmcoc/server/internal/services/llm"
)

const relationPromotionPrompt = `你是COC TRPG的人际关系档案管理员。本局已结束，调查员在本局中新结识了一些人物/组织(候选关系)。人物卡的社会关系栏只记录会在今后的冒险中对该调查员产生明显影响的关系。请根据本局聊天记录，从每位调查员的候选关系中挑选值得长期写入人物卡的条目。

提升标准(须在聊天记录中有具体依据)：
- 该人物/组织在本局结束时仍存活/存续，且与调查员形成了持续的羁绊或冲突：结下仇怨、欠下或被欠下重大人情、立下承诺或契约、成为盟友/导师/雇主/亲密关系、掌握调查员的把柄或秘密等
- 该人物/组织有明确理由在今后主动寻找、帮助或威胁调查员

不得提升：
- 只打过照面、一次性交易或问询、功能性NPC(店员、路人、向导、接待员等)
- 已死亡、已被消灭，或本局结束后显然不会再与调查员产生交集的人物
- 与人物卡已有关系重复的条目
宁缺毋滥：没有符合标准的候选时返回空列表。name必须与候选列表中的名称完全一致。note可选，填写时应精简为对今后有用的长期信息(种族、关系要点、态度)，不超过60字。

仅输出JSON,不要任何额外文字:
{"characters":[{"character_name":"角色名","promote":[{"name":"候选名称","note":"可选的精简备注","reason":"长期影响的依据"}]}]}`

// relationPromotion 是 AI 从本局候选关系中挑选出的、值得写进人物卡的条目。
type relationPromotion struct {
	Name   string `json:"name"`
	Note   string `json:"note"`
	Reason string `json:"reason"`
}

type relationPromotionLLMOutput struct {
	Characters []struct {
		CharacterName string `json:"character_name"`
		Promote       []struct {
			Name   string `json:"name"`
			Note   string `json:"note"`
			Reason string `json:"reason"`
		} `json:"promote"`
	} `json:"characters"`
}

// upsertSessionRelation 按 Name 对 tier1(本局)关系列表做 upsert。add 时覆盖同名条目并清除墓碑标记；
// remove 统一写成墓碑(Removed=true)而不是物理删除，这样即使对应名字在人物卡上原本就不存在，
// 结算时也能知道"本局内曾经加了又移除过"，不会被误判为新关系候选。
func upsertSessionRelation(list []models.SessionRelation, rel models.SocialRelation, removed bool) []models.SessionRelation {
	for i := range list {
		if list[i].Name == rel.Name {
			list[i].SocialRelation = rel
			list[i].Removed = removed
			return list
		}
	}
	return append(list, models.SessionRelation{SocialRelation: rel, Removed: removed})
}

// effectiveRelations 返回人物卡(tier2)叠加本局会话(tier1)之后的当前有效关系视图，
// 供 query_character 等运行时读取路径展示"结算前的实时状态"。
func effectiveRelations(card []models.SocialRelation, sess []models.SessionRelation) []models.SocialRelation {
	sessByName := make(map[string]models.SessionRelation, len(sess))
	for _, r := range sess {
		sessByName[r.Name] = r
	}

	cardNames := make(map[string]bool, len(card))
	result := make([]models.SocialRelation, 0, len(card)+len(sess))
	for _, r := range card {
		cardNames[r.Name] = true
		if s, ok := sessByName[r.Name]; ok {
			if s.Removed {
				continue
			}
			result = append(result, s.SocialRelation)
			continue
		}
		result = append(result, r)
	}
	for _, r := range sess {
		if r.Removed || cardNames[r.Name] {
			continue
		}
		result = append(result, r.SocialRelation)
	}
	return result
}

// sessionRelationCandidates 返回本局内新结识、且未被移除的关系——这些是结算时需要 AI 判断
// 是否提升进人物卡的"新关系候选"。人物卡已有名字的更新/移除不需要 AI 判断，结算时直接生效。
func sessionRelationCandidates(card []models.SocialRelation, sess []models.SessionRelation) []models.SocialRelation {
	cardNames := make(map[string]bool, len(card))
	for _, r := range card {
		cardNames[r.Name] = true
	}
	var out []models.SocialRelation
	for _, r := range sess {
		if r.Removed || cardNames[r.Name] {
			continue
		}
		out = append(out, r.SocialRelation)
	}
	return out
}

// mergeSessionRelations 把本局(tier1)的关系变更合并进人物卡(tier2)：
//   - 名字已在人物卡里的：本局有更新就覆盖，本局标了墓碑就删除——这部分不需要 AI 判断；
//   - 名字不在人物卡里的：只有出现在 promoted 中的才追加，promoted 带非空 Note 时用它覆盖原 note；
//   - 名字不在人物卡里、且本局又加又删的候选：忽略。
func mergeSessionRelations(card []models.SocialRelation, sess []models.SessionRelation, promoted map[string]relationPromotion) []models.SocialRelation {
	sessByName := make(map[string]models.SessionRelation, len(sess))
	for _, r := range sess {
		sessByName[r.Name] = r
	}

	cardNames := make(map[string]bool, len(card))
	result := make([]models.SocialRelation, 0, len(card)+len(promoted))
	for _, r := range card {
		cardNames[r.Name] = true
		s, changed := sessByName[r.Name]
		if !changed {
			result = append(result, r)
			continue
		}
		if s.Removed {
			continue
		}
		result = append(result, s.SocialRelation)
	}

	for _, r := range sess {
		if r.Removed || cardNames[r.Name] {
			continue
		}
		p, ok := promoted[r.Name]
		if !ok {
			continue
		}
		final := r.SocialRelation
		if p.Note != "" {
			final.Note = p.Note
		}
		result = append(result, final)
	}
	return result
}

// RunRelationPromotion 让 AI 从每位存活调查员本局新结识的候选关系中，挑选出值得长期写入
// 人物卡的条目。尽力而为：没有候选、agent 未配置、LLM 调用或 JSON 解析失败时都返回 nil，
// 不影响结算流程，只是这一局新增的关系不会提升进人物卡（已有关系的更新/移除不受影响）。
// 返回值外层 key 是角色名，内层 key 是关系名。
func RunRelationPromotion(ctx context.Context, session *models.GameSession, messages []models.Message) map[string]map[string]relationPromotion {
	type aliveCandidate struct {
		card       *models.CharacterCard
		candidates []models.SocialRelation
	}
	var alive []aliveCandidate
	hasCandidates := false
	for i := range session.Players {
		p := &session.Players[i]
		card := &p.CharacterCard
		if card.WoundState == "dead" || card.Stats.Data.HP <= 0 {
			continue // 已死亡角色不参与关系提升
		}
		cands := sessionRelationCandidates(card.SocialRelations.Data, p.SessionRelations.Data)
		if len(cands) > 0 {
			hasCandidates = true
		}
		alive = append(alive, aliveCandidate{card: card, candidates: cands})
	}
	if !hasCandidates {
		return nil
	}

	handle, err := loadSingleAgent(models.AgentRoleEvaluator)
	if err != nil {
		alog.Warn("relation promotion agent unavailable, skipping", "err", err)
		return nil
	}

	var charInfo strings.Builder
	for _, ac := range alive {
		if len(ac.candidates) == 0 {
			continue
		}
		charInfo.WriteString(fmt.Sprintf("调查员【%s】\n", ac.card.Name))
		if ac.card.Backstory != "" {
			charInfo.WriteString(fmt.Sprintf("背景故事: %s\n", ac.card.Backstory))
		}
		if len(ac.card.SocialRelations.Data) > 0 {
			var existing []string
			for _, r := range ac.card.SocialRelations.Data {
				existing = append(existing, fmt.Sprintf("%s(%s)", r.Name, r.Relationship))
			}
			charInfo.WriteString("人物卡已有关系(仅供参考,勿重复提升): " + strings.Join(existing, "、") + "\n")
		}
		var cand []string
		for _, r := range ac.candidates {
			cand = append(cand, fmt.Sprintf("%s|%s|%s", r.Name, r.Relationship, r.Note))
		}
		charInfo.WriteString("本局候选关系(格式:名称|关系类型|备注): " + strings.Join(cand, "；") + "\n\n")
	}

	msgs := []llm.ChatMessage{
		{Role: "system", Content: handle.systemPrompt(relationPromotionPrompt)},
		{Role: "user", Content: charInfo.String()},
		{Role: "user", Content: "聊天记录:\n" + buildSessionChatLog(messages)},
	}

	resp, err := handle.provider.JsonChat(ctx, fmt.Sprintf("%v:evaluator", session.ID), msgs)
	if err != nil {
		alog.Error("relation promotion LLM call failed, skipping", "err", err)
		return nil
	}

	var llmOut relationPromotionLLMOutput
	if jsonErr := json.Unmarshal([]byte(resp), &llmOut); jsonErr != nil {
		for i := 0; i < 30; i++ {
			resp, jsonErr = RepairJSON(ctx, resp, jsonErr, `{"characters":[{"character_name":"...","promote":[{"name":"候选名称","note":"...","reason":"..."}]}]}`)
			if jsonErr == nil {
				jsonErr = json.Unmarshal([]byte(resp), &llmOut)
				if jsonErr == nil {
					break
				}
			}
			alog.Warn("relation promotion JSON parse retry", "attempt", i+1, "err", jsonErr)
		}
		if jsonErr != nil {
			alog.Error("relation promotion JSON parse failed, skipping", "err", jsonErr)
			return nil
		}
	}

	candByChar := make(map[string]map[string]bool, len(alive))
	for _, ac := range alive {
		names := make(map[string]bool, len(ac.candidates))
		for _, r := range ac.candidates {
			names[r.Name] = true
		}
		candByChar[ac.card.Name] = names
	}

	return filterValidPromotions(llmOut, candByChar)
}

// filterValidPromotions 只保留 AI 输出中确实位于候选列表里的条目，防止 AI 编造、写错角色名，
// 或对非候选(人物卡已有/未出现过的名字)条目产生 promotion，导致结算时写进不该写的关系。
func filterValidPromotions(llmOut relationPromotionLLMOutput, candByChar map[string]map[string]bool) map[string]map[string]relationPromotion {
	result := make(map[string]map[string]relationPromotion)
	for _, ce := range llmOut.Characters {
		validNames := candByChar[ce.CharacterName]
		if len(validNames) == 0 {
			continue
		}
		for _, p := range ce.Promote {
			if !validNames[p.Name] {
				alog.Warn("relation promotion name not in candidates, discarding", "character", ce.CharacterName, "name", p.Name)
				continue
			}
			if result[ce.CharacterName] == nil {
				result[ce.CharacterName] = make(map[string]relationPromotion)
			}
			result[ce.CharacterName][p.Name] = relationPromotion{Name: p.Name, Note: p.Note, Reason: p.Reason}
		}
	}
	return result
}

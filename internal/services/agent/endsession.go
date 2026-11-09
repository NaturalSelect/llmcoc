// NOTE: Defines AI agent roles and their interactions.
package agent

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"sync"

	"github.com/llmcoc/server/internal/models"
	"github.com/llmcoc/server/internal/services/game"
	"gorm.io/gorm"
)

const imageDataURLTagOpenPrefix = "<image_data_url"
const imageDataURLEndTag = "</image_data_url>"
const imageRefTagOpenPrefix = "<image_ref"

var imageRefTagPattern = regexp.MustCompile(`(?is)<image_ref\b[^>]*(?:/>|>\s*</image_ref>)`)

// NOTE: 每位调查员固定拿基础金币，评分再按 0–100 折算成 0–50 的加成。
const settlementBaseCoins = 20

// NOTE: 克苏鲁神话和信用评级不靠使用成长，Director 填了也忽略。
var growthExcludedSkills = map[string]bool{"克苏鲁神话": true, "信用评级": true}

// NOTE: 没有结算条目(手动结束)时 score 视为 0，只发基础金币。
func (s PlayerSettlement) score() int {
	if s.Score == nil {
		return 0
	}
	return clamp(*s.Score, 0, 100)
}

// NOTE: 返回空串表示通过，否则是写给 Director 的拒绝原因；通过时 settlement 恰好覆盖 players。
func validateSettlement(players []models.SessionPlayer, settlement []PlayerSettlement) string {
	names := make([]string, 0, len(players))
	known := make(map[string]bool, len(players))
	for _, p := range players {
		names = append(names, p.CharacterCard.Name)
		known[p.CharacterCard.Name] = true
	}

	seen := make(map[string]bool, len(settlement))
	var problems []string
	for _, s := range settlement {
		switch {
		case !known[s.CharacterName]:
			problems = append(problems, fmt.Sprintf("%q 不是本局调查员", s.CharacterName))
		case seen[s.CharacterName]:
			problems = append(problems, fmt.Sprintf("%q 重复", s.CharacterName))
		case s.Score == nil || *s.Score < 0 || *s.Score > 100:
			problems = append(problems, fmt.Sprintf("%q 的 score 必须是 0-100 的整数", s.CharacterName))
		}
		if known[s.CharacterName] {
			seen[s.CharacterName] = true
		}
	}
	var missing []string
	for _, name := range names {
		if !seen[name] {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		problems = append(problems, "缺少调查员 "+strings.Join(missing, "、"))
	}
	if len(problems) == 0 {
		return ""
	}
	return fmt.Sprintf("SYSTEM REJECT: end_game.settlement 不合格：%s。settlement 必须为本局每位调查员(包括已死亡或疯狂的，当前：%s)各写一条，character_name 用角色卡真实姓名，score 为 0-100 的整数。请修正后重发，游戏尚未结束。",
		strings.Join(problems, "；"), strings.Join(names, "、"))
}

// NOTE: 对 Director 报告用过的技能逐个做成长检定：d100 大于当前值才成长，上限 99。
// 不在人物卡上的技能和不可成长的技能直接忽略；返回实际成长的技能事件。
func applySkillGrowth(card *models.CharacterCard, skills []string) []models.GameEvent {
	data := card.Skills.Data
	done := make(map[string]bool, len(skills))
	var events []models.GameEvent
	for _, skill := range skills {
		current, onCard := data[skill]
		if !onCard || done[skill] || growthExcludedSkills[skill] {
			continue
		}
		done[skill] = true
		if current >= 99 {
			continue
		}
		base := max(current, 1)
		if game.RollD100() <= base {
			continue
		}
		gain, _ := game.Roll(1, 3)
		data[skill] = min(base+gain, 99)
		debugf("growth", "character %s skill %s %d -> %d", card.Name, skill, current, data[skill])
		events = append(events, models.GameEvent{Type: "stat", Character: card.Name, Field: "skill", Skill: skill, From: intPtr(current), To: intPtr(data[skill])})
	}
	return events
}

// RunEndSession 执行结算：发金币、胜利时做成长、清疯狂、撕卡、恢复存活角色，并合并社交关系。
// 房间开启 EvolveBackstory 且 win=true 时，还会改写存活调查员的个人经历。
// 评分和用过的技能来自 Director 在 end_game 里填写的 settlement；手动结束传 nil，只发基础金币。
// 返回玩家可见的结算事件（金币、技能成长、数值恢复、疯狂解除、经历改写），结算失败时为空。
// 调用方保证 session 已预加载 Players 和 CharacterCard。
func RunEndSession(ctx context.Context, session *models.GameSession, messages []models.Message, win bool, settlement []PlayerSettlement) ([]models.GameEvent, error) {
	defer deleteCachedAgents(session.ID)
	stripMessageImageDataURLTags(messages)

	// NOTE: 背景演变与关系提升互不依赖，并行执行，避免结算多等一次 LLM 调用；两者都只读 session。
	var evolved map[string]string
	var wg sync.WaitGroup
	if win && session.EvolveBackstory {
		wg.Add(1)
		go func() {
			defer wg.Done()
			evolved = backstoryEvolver(ctx, session, messages)
		}()
	}
	// NOTE: 本局新结识的关系先存在 session 里，结算时才由 AI 判断哪些值得长期写入人物卡
	// （已有关系的更新/移除不需要 AI 判断，直接生效），胜负均执行。
	relationPromotions := RunRelationPromotion(ctx, session, messages)
	wg.Wait()

	settlementByChar := make(map[string]PlayerSettlement, len(settlement))
	for _, s := range settlement {
		settlementByChar[s.CharacterName] = s
	}

	var events []models.GameEvent
	txErr := models.DB.Transaction(func(tx *gorm.DB) error {
		var evalContent models.EvaluationContent
		for i := range session.Players {
			player := &session.Players[i]
			card := &player.CharacterCard
			before := *card
			hadMadness := card.MadnessState == "temporary" || card.MadnessState == "indefinite"

			s, hasSettlement := settlementByChar[card.Name]
			bonus := 0
			if hasSettlement {
				bonus = s.score() / 2
				evalContent.Players = append(evalContent.Players, models.PlayerEvalContent{
					CharacterName: card.Name,
					Comment:       s.Comment,
					Score:         s.score(),
					BaseCoins:     settlementBaseCoins,
					BonusCoins:    bonus,
				})
			}
			award := settlementBaseCoins + bonus
			if err := tx.Model(&models.User{}).
				Where("id = ?", player.UserID).
				Update("coins", gorm.Expr("coins + ?", award)).Error; err != nil {
				return err
			}
			debugf("award", "player %d (%s) awarded %d coins (base %d + bonus %d)", player.ID, card.Name, award, settlementBaseCoins, bonus)
			events = append(events, models.GameEvent{Type: "coins", Character: card.Name, Amount: award})

			// NOTE: 失败没有角色成长：技能成长和 POW 增长只在 win=true 时执行。
			if win {
				events = append(events, applySkillGrowth(card, s.Skills)...)

				limit := card.Stats.Data.POW % 100
				if game.RollD100() > limit {
					point := 5 - card.Stats.Data.POW/100
					if point > 0 {
						total, _ := game.Roll(1, point)
						card.Stats.Data.POW = clamp(card.Stats.Data.POW+total, 0, 500)
					}
				}
			}

			// NOTE: 清疯狂保留永久性疯狂。
			ResetMadnessAfterSession(card)

			// 撕卡:死亡的调查员失活。
			newBackstory := ""
			if card.WoundState == "dead" || card.Stats.Data.HP <= 0 {
				card.IsActive = false
			} else {
				// 已死亡角色不合并关系，卡即将被撕。
				card.SocialRelations.Data = mergeSessionRelations(card.SocialRelations.Data, player.SessionRelations.Data, relationPromotions[card.Name])

				card.Stats.Data.HP = card.Stats.Data.MaxHP
				card.Stats.Data.MP = card.Stats.Data.MaxMP
				if card.Stats.Data.SAN > 0 {
					card.Stats.Data.SAN = clamp(card.Stats.Data.SAN+game.RollDiceExpr("3D6"), card.Stats.Data.SAN, card.Stats.Data.MaxSAN)
				}
				card.WoundState = "none"
				card.IsUnconscious = false

				// NOTE: evolved 只在 win 且房间开启开关时才有内容，死亡的调查员不参与。
				if newBackstory = evolved[card.Name]; newBackstory != "" {
					card.Backstory = newBackstory
				}
			}

			events = append(events, cardStatEvents(before, *card)...)
			if hadMadness {
				events = append(events, models.GameEvent{Type: "madness", Character: card.Name, State: "none"})
			}
			if newBackstory != "" {
				events = append(events, models.GameEvent{Type: "backstory", Character: card.Name, Detail: newBackstory})
			}

			if err := tx.Save(card).Error; err != nil {
				return err
			}
		}

		// NOTE: 手动结束没有评分，不写评价记录。
		if len(evalContent.Players) == 0 {
			return nil
		}
		gameEval := models.GameEvaluation{SessionID: session.ID}
		gameEval.Content.Data = evalContent
		return tx.
			Where(models.GameEvaluation{SessionID: session.ID}).
			Assign(models.GameEvaluation{Content: gameEval.Content}).
			FirstOrCreate(&gameEval).Error
	})

	if txErr != nil {
		alog.Error("end session transaction failed", "session", session.ID, "err", txErr)
		return nil, txErr
	}
	return events, nil
}

func stripMessageImageDataURLTags(messages []models.Message) {
	for i := range messages {
		messages[i].Content = stripInternalImageTags(messages[i].Content)
	}
}

func stripInternalImageTags(content string) string {
	content = stripImageDataURLTags(content)
	content = stripImageRefTags(content)
	return strings.TrimSpace(content)
}

func stripImageDataURLTags(content string) string {
	if !strings.Contains(content, imageDataURLTagOpenPrefix) {
		return strings.TrimSpace(content)
	}
	var b strings.Builder
	rest := content
	for {
		start := strings.Index(rest, imageDataURLTagOpenPrefix)
		if start < 0 {
			b.WriteString(rest)
			break
		}
		b.WriteString(rest[:start])
		tagEnd := strings.Index(rest[start:], ">")
		if tagEnd < 0 {
			break
		}
		afterStart := rest[start+tagEnd+1:]
		end := strings.Index(afterStart, imageDataURLEndTag)
		if end < 0 {
			break
		}
		rest = afterStart[end+len(imageDataURLEndTag):]
	}
	return strings.TrimSpace(b.String())
}

func stripImageRefTags(content string) string {
	if !strings.Contains(strings.ToLower(content), imageRefTagOpenPrefix) {
		return strings.TrimSpace(content)
	}
	return strings.TrimSpace(imageRefTagPattern.ReplaceAllString(content, ""))
}

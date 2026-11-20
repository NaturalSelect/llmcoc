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

// NOTE: 结算时自动恢复的数值：HP 与 MP 回满，SAN 按结局或默认 3D6 结算。
var settlementRestoredFields = map[string]bool{"hp": true, "mp": true, "san": true}

var endingSANDicePattern = regexp.MustCompile(`(?i)(\d+\s*d\s*\d+(?:\s*[+-]\s*\d+)?|\d+)`)

// parseEndingSANReward 解析结局条目里的 san_reward 文案（如 "恢复1d6"、"损失1d10"、"1D6+1"、"0"/"无"）。
// 返回骰子表达式、符号(+1 恢复 / -1 损失 / 0 无变化)以及是否识别成功。
func parseEndingSANReward(raw string) (expr string, sign int, ok bool) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return "", 0, false
	}
	switch s {
	case "0", "0点", "无", "无恢复", "不恢复", "无变化", "无奖励":
		return "0", 0, true
	}
	sign = 1
	if strings.HasPrefix(s, "-") || strings.Contains(s, "损失") || strings.Contains(s, "扣除") || strings.Contains(s, "失去") || strings.Contains(s, "减少") {
		sign = -1
	}
	m := endingSANDicePattern.FindString(s)
	if m == "" {
		return "", 0, false
	}
	clean := strings.ToUpper(strings.ReplaceAll(m, " ", ""))
	if clean == "0" {
		return "0", 0, true
	}
	return clean, sign, true
}

// resolveEndingSANReward 按 endingName（优先）或 endSummary 匹配模组里对应的 EndingData，
// 解析其 SANReward；未匹配或未配置有效 SANReward 时返回 matched=false，由调用方回退到默认 3D6 恢复。
func resolveEndingSANReward(scenario *models.Scenario, endingName, endSummary string) (matchedName, expr string, sign int, matched bool) {
	if scenario == nil || len(scenario.Content.Data.Endings) == 0 {
		return "", "", 0, false
	}
	endings := scenario.Content.Data.Endings
	name := strings.TrimSpace(endingName)
	if name != "" {
		for _, e := range endings {
			if strings.TrimSpace(e.Name) == name {
				if ex, sg, ok := parseEndingSANReward(e.SANReward); ok {
					return e.Name, ex, sg, true
				}
				return e.Name, "", 0, false
			}
		}
	}
	summary := strings.TrimSpace(endSummary)
	if summary != "" {
		bestIdx := -1
		bestLen := 0
		for i, e := range endings {
			en := strings.TrimSpace(e.Name)
			if en != "" && strings.Contains(summary, en) && len([]rune(en)) > bestLen {
				bestIdx = i
				bestLen = len([]rune(en))
			}
		}
		if bestIdx >= 0 {
			e := endings[bestIdx]
			if ex, sg, ok := parseEndingSANReward(e.SANReward); ok {
				return e.Name, ex, sg, true
			}
		}
	}
	return "", "", 0, false
}

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
		roll := game.RollD100()
		if roll <= base {
			continue
		}
		gain, _ := game.Roll(1, 3)
		data[skill] = min(base+gain, 99)
		debugf("growth", "character %s skill %s %d -> %d", card.Name, skill, current, data[skill])
		events = append(events, models.GameEvent{
			Type: "stat", Character: card.Name, Field: "skill", Skill: skill, From: intPtr(current), To: intPtr(data[skill]),
			Detail: fmt.Sprintf("成长检定 骰值 %d 高于当前值 %d，提升 %d", roll, base, data[skill]-current),
		})
	}
	return events
}

// RunEndSession 执行结算：发金币、胜利时做成长、清疯狂、撕卡、恢复存活角色，并合并社交关系。
// 房间开启 EvolveBackstory 且 win=true 时，还会改写存活调查员的个人经历。
// 评分和用过的技能来自 Director 在 end_game 里填写的 settlement；手动结束传 nil，只发基础金币。
// 返回玩家可见的结算事件（金币、技能成长、数值恢复、疯狂解除、经历改写），结算失败时为空。
// 调用方保证 session 已预加载 Players 和 CharacterCard。
func RunEndSession(ctx context.Context, session *models.GameSession, messages []models.Message, win bool, settlement []PlayerSettlement) ([]models.GameEvent, error) {
	return RunEndSessionWithEnding(ctx, session, messages, win, settlement, "", "")
}

// RunEndSessionWithEnding 在 RunEndSession 的基础上按触发结局应用模组定义的 SAN 奖惩（匹配不到时回退 3D6 恢复）。
func RunEndSessionWithEnding(ctx context.Context, session *models.GameSession, messages []models.Message, win bool, settlement []PlayerSettlement, endingName, endSummary string) ([]models.GameEvent, error) {
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

	endingMatchedName, sanExpr, sanSign, hasEndingSAN := resolveEndingSANReward(&session.Scenario, endingName, endSummary)

	// NOTE: 必须在事务外取 bot 用户 ID：事务占着连接时再用全局 DB 查询，SQLite 单连接下会死锁。
	companionID := models.CompanionUserID()

	var events []models.GameEvent
	txErr := models.DB.Transaction(func(tx *gorm.DB) error {
		var evalContent models.EvaluationContent
		for i := range session.Players {
			player := &session.Players[i]
			card := &player.CharacterCard
			before := *card
			hadMadness := card.MadnessState == "temporary" || card.MadnessState == "indefinite"
			// NOTE: AI 陪玩席位不发金币、不成长、不合并关系、不改写经历，只做状态恢复，让卡池里的卡能反复使用。
			isAI := companionID != 0 && player.UserID == companionID

			s, hasSettlement := settlementByChar[card.Name]
			bonus := 0
			baseCoins := settlementBaseCoins
			if hasSettlement {
				bonus = s.score() / 2
				if isAI {
					baseCoins, bonus = 0, 0
				}
				evalContent.Players = append(evalContent.Players, models.PlayerEvalContent{
					CharacterName: card.Name,
					Comment:       s.Comment,
					Score:         s.score(),
					BaseCoins:     baseCoins,
					BonusCoins:    bonus,
				})
			}
			if !isAI {
				award := settlementBaseCoins + bonus
				if err := tx.Model(&models.User{}).
					Where("id = ?", player.UserID).
					Update("coins", gorm.Expr("coins + ?", award)).Error; err != nil {
					return err
				}
				debugf("award", "player %d (%s) awarded %d coins (base %d + bonus %d)", player.ID, card.Name, award, settlementBaseCoins, bonus)
				events = append(events, models.GameEvent{
					Type: "coins", Character: card.Name, Amount: award,
					Detail: fmt.Sprintf("基础 %d + 评分奖励 %d", settlementBaseCoins, bonus),
				})
			}

			// NOTE: 失败没有角色成长：技能成长和 POW 增长只在 win=true 时执行。
			powGrowth := false
			luckGrowth := false
			if win && !isAI {
				events = append(events, applySkillGrowth(card, s.Skills)...)

				limit := card.Stats.Data.POW % 100
				if game.RollD100() > limit {
					point := 5 - card.Stats.Data.POW/100
					if point > 0 {
						total, _ := game.Roll(1, point)
						card.Stats.Data.POW = clamp(card.Stats.Data.POW+total, 0, 500)
						powGrowth = true
					}
				}
			}

			// NOTE: 清疯狂保留永久性疯狂。
			ResetMadnessAfterSession(card)

			// 撕卡:死亡的调查员失活。
			newBackstory := ""
			sanDetail := "结算恢复"
			if card.WoundState == "dead" || card.Stats.Data.HP <= 0 {
				card.IsActive = false
			} else {
				// 已死亡角色不合并关系，卡即将被撕。
				if !isAI {
					card.SocialRelations.Data = mergeSessionRelations(card.SocialRelations.Data, player.SessionRelations.Data, relationPromotions[card.Name])
				}

				card.Stats.Data.HP = card.Stats.Data.MaxHP
				card.Stats.Data.MP = card.Stats.Data.MaxMP
				if card.Stats.Data.SAN > 0 {
					if hasEndingSAN {
						switch {
						case sanSign > 0:
							gain := game.RollDiceExpr(sanExpr)
							card.Stats.Data.SAN = clamp(card.Stats.Data.SAN+gain, card.Stats.Data.SAN, card.Stats.Data.MaxSAN)
							sanDetail = fmt.Sprintf("结局理智奖励（%s）", endingMatchedName)
						case sanSign < 0:
							loss := game.RollDiceExpr(sanExpr)
							card.Stats.Data.SAN = clamp(card.Stats.Data.SAN-loss, 0, card.Stats.Data.MaxSAN)
							sanDetail = fmt.Sprintf("结局理智损失（%s）", endingMatchedName)
						default:
							sanDetail = fmt.Sprintf("结局理智结算（%s）", endingMatchedName)
						}
					} else {
						card.Stats.Data.SAN = clamp(card.Stats.Data.SAN+game.RollDiceExpr("3D6"), card.Stats.Data.SAN, card.Stats.Data.MaxSAN)
					}
				}
				// NOTE: COC7 通关后幸运恢复检定：d100 大于当前幸运则恢复 1D10（上限 99）。
				if win && !isAI && card.Stats.Data.Luck < 99 && game.RollD100() > card.Stats.Data.Luck {
					gain, _ := game.Roll(1, 10)
					card.Stats.Data.Luck = clamp(card.Stats.Data.Luck+gain, 0, 99)
					luckGrowth = true
				}
				card.WoundState = "none"
				card.IsUnconscious = false

				// NOTE: evolved 只在 win 且房间开启开关时才有内容，死亡的调查员不参与。
				if !isAI {
					if newBackstory = evolved[card.Name]; newBackstory != "" {
						card.Backstory = newBackstory
					}
				}
			}

			// NOTE: 结算时的回满是规则内的恢复，不是游戏中的损益，事件里写明原因，避免日志里最后一次 SAN 和卡面对不上。
			statEvents := cardStatEvents(before, *card)
			for i := range statEvents {
				if statEvents[i].Type != "stat" {
					continue
				}
				switch {
				case statEvents[i].Field == "san":
					statEvents[i].Detail = sanDetail
				case settlementRestoredFields[statEvents[i].Field]:
					statEvents[i].Detail = "结算恢复"
				case statEvents[i].Field == "pow" && powGrowth:
					statEvents[i].Detail = "通关后意志增长检定通过"
				case statEvents[i].Field == "luck" && luckGrowth:
					statEvents[i].Detail = "通关后幸运恢复检定通过"
				}
			}
			events = append(events, statEvents...)
			if hadMadness {
				events = append(events, models.GameEvent{Type: "madness", Character: card.Name, State: "none"})
			}
			if newBackstory != "" {
				events = append(events, models.GameEvent{Type: "backstory", Character: card.Name, Detail: newBackstory})
			}

			// NOTE: 剧情物品只属于本模组，结算时不论存活与否、胜负都整件收回。
			if kept, story := models.SplitStoryItems(card.Inventory.Data); len(story) > 0 {
				card.Inventory.Data = kept
				for _, it := range story {
					events = append(events, models.GameEvent{
						Type: "item", Character: card.Name, Category: "inventory", Op: "remove", Name: it.Label(), Detail: storyItemReclaimedNote,
					})
				}
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

// NOTE: Defines AI agent roles and their interactions.
package agent

import (
	"context"
	"log"
	"regexp"
	"strings"

	"github.com/llmcoc/server/internal/models"
	"github.com/llmcoc/server/internal/services/game"
	"gorm.io/gorm"
)

const imageDataURLTagOpenPrefix = "<image_data_url"
const imageDataURLEndTag = "</image_data_url>"
const imageRefTagOpenPrefix = "<image_ref"

var imageRefTagPattern = regexp.MustCompile(`(?is)<image_ref\b[^>]*(?:/>|>\s*</image_ref>)`)

// EndSessionResult bundles the evaluation and growth results from RunEndSession.
type EndSessionResult struct {
	Evaluation EvaluationResult
	Growth     GrowthResult
}

// RunEndSession executes the full end-of-session settlement:
//  1. Runs the Evaluator agent to score players and suggest rewards.
//  2. 若 win=true：运行 Growth 计算技能成长，并对每个存活角色运行背景演变。
//     若 win=false：跳过技能成长、POW 增长及背景演变（失败无角色成长）。
//  3. Applies coins, madness cleanup, card teardown (dead), and state restore
//     in a single DB transaction. If win=true also applies skill growth, POW,
//     and backstory evolution.
//
// It is called by both the EndSession HTTP handler and ToolEndGame (async).
// The session must have Players and CharacterCard pre-loaded.
// NOTE: win 参数由调用方传入；HTTP 手动结束固定传 false，Director end_game 显式填写。
func RunEndSession(ctx context.Context, session *models.GameSession, messages []models.Message, win bool) (EndSessionResult, error) {
	defer deleteCachedAgents(session.ID)
	stripMessageImageDataURLTags(messages)
	// ── Evaluator ────────────────────────────────────────────────────────────
	evalResult, err := RunEvaluator(ctx, session, messages)
	if err != nil {
		// RunEvaluator already falls back internally; this branch is a safety net.
		evalResult = fallbackEvaluation(session)
	}

	// ── Growth & Evolution（仅 win=true 时执行）───────────────────────────────
	var growthResult GrowthResult
	type evolutionEntry struct {
		cardIdx      int
		newBackstory string
	}
	var evolutions []evolutionEntry

	if win {
		// NOTE: win=true：执行技能成长检验。
		growthResult, _ = RunGrowth(ctx, session, messages)

		// NOTE: win=true：对每个存活角色执行背景演变（writer agent，best-effort）。
		writerHistory := session.WriterHistory.Data // []models.ChatMsg
		for i := range session.Players {
			card := &session.Players[i].CharacterCard
			if card.WoundState == "dead" || card.Stats.Data.HP <= 0 {
				continue // dead investigators do not get an evolution
			}
			evo, evoErr := RunCharacterEvolution(ctx, card, writerHistory)
			if evoErr != nil {
				log.Printf("[agent] character evolution skipped for %q: %v", card.Name, evoErr)
				continue
			}
			evolutions = append(evolutions, evolutionEntry{
				cardIdx:      i,
				newBackstory: evo.NewBackstory,
			})
		}
	}
	// NOTE: win=false：跳过 RunGrowth / RunCharacterEvolution，growthResult 保持零值。

	// Build lookup maps for fast access.
	evalByChar := make(map[string]PlayerEvaluation, len(evalResult.Players))
	for _, pe := range evalResult.Players {
		evalByChar[pe.CharacterName] = pe
	}
	growthByChar := make(map[string]CharacterGrowth, len(growthResult.Characters))
	for _, cg := range growthResult.Characters {
		growthByChar[cg.CharacterName] = cg
	}
	evoByIdx := make(map[int]evolutionEntry, len(evolutions))
	for _, e := range evolutions {
		evoByIdx[e.cardIdx] = e
	}

	// ── DB transaction ───────────────────────────────────────────────────────
	txErr := models.DB.Transaction(func(tx *gorm.DB) error {
		for i := range session.Players {
			player := &session.Players[i]
			card := &player.CharacterCard

			// Award coins（win=true/false 均执行）.
			if pe, ok := evalByChar[card.Name]; ok {
				award := pe.BaseCoins + pe.BonusCoins
				if award > 0 {
					if err := tx.Model(&models.User{}).
						Where("id = ?", player.UserID).
						Update("coins", gorm.Expr("coins + ?", award)).Error; err != nil {
						return err
					}
					debugf("award", "player %d (%s) awarded %d coins (base %d + bonus %d)", player.ID, card.Name, award, pe.BaseCoins, pe.BonusCoins)
				}
			} else {
				// Fallback: 20 base coins even without an evaluation entry.
				if err := tx.Model(&models.User{}).
					Where("id = ?", player.UserID).
					Update("coins", gorm.Expr("coins + ?", 20)).Error; err != nil {
					return err
				}
				debugf("award", "player %d (%s) awarded fallback 20 coins (no evaluation entry)", player.ID, card.Name)
			}

			if win {
				// NOTE: win=true：应用技能成长（上限99）。
				if cg, ok := growthByChar[card.Name]; ok && len(cg.SkillChanges) > 0 {
					skills := card.Skills.Data
					if skills == nil {
						skills = make(map[string]int)
					}
					for _, sc := range cg.SkillChanges {
						current := skills[sc.Skill]
						newVal := current + sc.Delta
						if newVal > 99 {
							newVal = 99
						}
						skills[sc.Skill] = newVal
					}
					card.Skills.Data = skills
				}

				// NOTE: win=true：执行 POW 增长检验。
				limit := card.Stats.Data.POW % 100
				if game.RollD100() > limit {
					point := 5 - card.Stats.Data.POW/100
					if point > 0 {
						total, _ := game.Roll(1, point)
						card.Stats.Data.POW = clamp(card.Stats.Data.POW+total, 0, 500)
					}
				}

				// NOTE: win=true：应用背景演变。
				if e, ok := evoByIdx[i]; ok {
					if e.newBackstory != "" {
						card.Backstory = e.newBackstory
					}
				}
			}
			// NOTE: win=false：不执行技能成长、POW 增长、背景演变。

			// End-of-session cleanup: clear temporary/indefinite madness,
			// while preserving permanent madness（win=true/false 均执行）.
			ResetMadnessAfterSession(card)

			// 撕卡（win=true/false 均执行）:dead investigators are soft-deleted (IsActive = false).
			if card.WoundState == "dead" || card.Stats.Data.HP <= 0 {
				card.IsActive = false
			} else {
				card.Stats.Data.HP = card.Stats.Data.MaxHP // Heal to full HP at session end for living investigators.
				card.Stats.Data.MP = card.Stats.Data.MaxMP // Restore MP as well.
				if card.Stats.Data.SAN > 0 {
					card.Stats.Data.SAN = clamp(card.Stats.Data.SAN+game.RollDiceExpr("3D6"), card.Stats.Data.SAN, card.Stats.Data.MaxSAN)
				}
				card.WoundState = "none" // Clear wounds for the next session.
				card.IsUnconscious = false
			}

			// Always save the character card to persist all in-game changes.
			if err := tx.Save(card).Error; err != nil {
				return err
			}
		}

		// Persist evaluation record (upsert by session_id)（win=true/false 均执行）.
		evalContent := models.EvaluationContent{
			Summary: evalResult.Summary,
		}
		for _, pe := range evalResult.Players {
			evalContent.Players = append(evalContent.Players, models.PlayerEvalContent{
				CharacterName: pe.CharacterName,
				Comment:       pe.Comment,
				Score:         pe.Score,
				BaseCoins:     pe.BaseCoins,
				BonusCoins:    pe.BonusCoins,
			})
		}
		gameEval := models.GameEvaluation{
			SessionID: session.ID,
		}
		gameEval.Content.Data = evalContent
		return tx.
			Where(models.GameEvaluation{SessionID: session.ID}).
			Assign(models.GameEvaluation{Content: gameEval.Content}).
			FirstOrCreate(&gameEval).Error
	})

	if txErr != nil {
		log.Printf("[agent] RunEndSession transaction error for session %d: %v", session.ID, txErr)
	}
	return EndSessionResult{Evaluation: evalResult, Growth: growthResult}, txErr
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

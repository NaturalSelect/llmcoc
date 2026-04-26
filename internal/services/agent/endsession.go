// NOTE: Defines AI agent roles and their interactions.
package agent

import (
	"context"
	"log"

	"github.com/llmcoc/server/internal/models"
	"gorm.io/gorm"
)

// EndSessionResult bundles the evaluation and growth results from RunEndSession.
type EndSessionResult struct {
	Evaluation EvaluationResult
	Growth     GrowthResult
}

// RunEndSession executes the full end-of-session settlement:
//  1. Runs the Evaluator agent to score players and suggest rewards.
//  2. Runs the Growth agent to determine skill improvements.
//  3. Runs CharacterEvolution for each living character using WriterHistory.
//  4. Applies coins, skill growth, madness cleanup, card teardown (dead), and evolution
//     in a single DB transaction.
//
// It is called by both the EndSession HTTP handler and ToolEndGame (async).
// The session must have Players and CharacterCard pre-loaded.
func RunEndSession(ctx context.Context, session *models.GameSession, messages []models.Message) (EndSessionResult, error) {
	// ── Evaluator ────────────────────────────────────────────────────────────
	evalResult, err := RunEvaluator(ctx, session, messages)
	if err != nil {
		// RunEvaluator already falls back internally; this branch is a safety net.
		evalResult = fallbackEvaluation(session)
	}

	// ── Growth ───────────────────────────────────────────────────────────────
	growthResult, _ := RunGrowth(ctx, session, messages)

	// ── Character Evolution (writer agent, best-effort per character) ─────────
	writerHistory := session.WriterHistory.Data // []models.ChatMsg

	type evolutionEntry struct {
		cardIdx      int
		newBackstory string
		newTraits    string
	}
	var evolutions []evolutionEntry

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
			newTraits:    evo.NewTraits,
		})
	}

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

			// Award coins.
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

			// Apply skill growth (capped at 99).
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

			// Apply character evolution for living characters.
			if e, ok := evoByIdx[i]; ok {
				if e.newBackstory != "" {
					card.Backstory = e.newBackstory
				}
				if e.newTraits != "" {
					card.Traits = e.newTraits
				}
			}

			// End-of-session cleanup: clear temporary/indefinite madness,
			// while preserving permanent madness.
			ResetMadnessAfterSession(card)

			// 撕卡：dead investigators are soft-deleted (IsActive = false).
			if card.WoundState == "dead" || card.Stats.Data.HP <= 0 {
				card.IsActive = false
			}

			// Always save the character card to persist all in-game changes.
			if err := tx.Save(card).Error; err != nil {
				return err
			}
		}

		// Persist evaluation record (upsert by session_id).
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
		return EndSessionResult{Evaluation: evalResult, Growth: growthResult}, txErr
	}

	return EndSessionResult{Evaluation: evalResult, Growth: growthResult}, nil
}

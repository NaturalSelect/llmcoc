// NOTE: orchestrator_test.go 验证clearConsumedTurnActions(D4精确清理)——回归"战斗
// 待澄清暂停时,DEX序靠后、combat_act没处理到的玩家,其本轮输入被静默删除"这一bug:
// 旧的clearTurnActions在每次run结束后无差别删光当轮全部SessionTurnAction。
package agent

import (
	"testing"

	"github.com/llmcoc/server/internal/models"
)

func seedTurnAction(t *testing.T, sessionID uint, round int, userID uint, username string) {
	t.Helper()
	if err := models.DB.AutoMigrate(&models.SessionTurnAction{}); err != nil {
		t.Fatalf("auto-migrate SessionTurnAction: %v", err)
	}
	if err := models.DB.Create(&models.SessionTurnAction{
		SessionID: sessionID, Round: round, UserID: userID, Username: username, ActionSummary: "(测试声明)",
	}).Error; err != nil {
		t.Fatalf("seed turn action: %v", err)
	}
}

func countTurnActions(t *testing.T, sessionID uint, round int) []uint {
	t.Helper()
	var rows []models.SessionTurnAction
	if err := models.DB.Where("session_id = ? AND round = ?", sessionID, round).Find(&rows).Error; err != nil {
		t.Fatalf("query turn actions: %v", err)
	}
	ids := make([]uint, 0, len(rows))
	for _, r := range rows {
		ids = append(ids, r.UserID)
	}
	return ids
}

func TestClearConsumedTurnActions_KeepsUnresolvedDropsRest(t *testing.T) {
	initAgentTestDB(t)
	const sessionID, round = 1, 3
	players := []models.SessionPlayer{
		{UserID: 1, CharacterCard: models.CharacterCard{Name: "甲"}},
		{UserID: 2, CharacterCard: models.CharacterCard{Name: "乙"}},
		{UserID: 3, CharacterCard: models.CharacterCard{Name: "丙"}},
	}
	seedTurnAction(t, sessionID, round, 1, "甲") // 已行动
	seedTurnAction(t, sessionID, round, 2, "乙") // 被打了待澄清标记
	seedTurnAction(t, sessionID, round, 3, "丙") // 本轮尚未被combat_act推进到

	gctx := GameContext{Session: models.GameSession{ID: sessionID, TurnRound: round, Players: players}}
	combat := &models.CombatState{
		Active: true, Round: 1, ActorIndex: 2,
		Participants: []models.CombatParticipant{
			{Name: "甲", UserID: 1, HasActed: true},
			{Name: "乙", UserID: 2, PendingClarification: true, PendingQuestion: "闪避还是反击？"},
			{Name: "丙", UserID: 3},
		},
	}

	clearConsumedTurnActions(gctx, combat, nil)

	remaining := countTurnActions(t, sessionID, round)
	if len(remaining) != 1 || remaining[0] != 3 {
		t.Errorf("remaining turn actions = %v, want只保留丙(UserID 3,本轮未行动且非待澄清)", remaining)
	}
}

func TestClearConsumedTurnActions_DeadParticipantDropped(t *testing.T) {
	initAgentTestDB(t)
	const sessionID, round = 1, 1
	players := []models.SessionPlayer{
		{UserID: 1, CharacterCard: models.CharacterCard{Name: "甲", WoundState: "dead"}},
	}
	seedTurnAction(t, sessionID, round, 1, "甲")

	gctx := GameContext{Session: models.GameSession{ID: sessionID, TurnRound: round, Players: players}}
	combat := &models.CombatState{
		Active: true, Round: 1,
		Participants: []models.CombatParticipant{{Name: "甲", UserID: 1, HasActed: false}},
	}

	clearConsumedTurnActions(gctx, combat, nil)

	if remaining := countTurnActions(t, sessionID, round); len(remaining) != 0 {
		t.Errorf("remaining = %v, want已死亡参战者的声明应被清理", remaining)
	}
}

func TestClearConsumedTurnActions_NoEncounterClearsAll(t *testing.T) {
	initAgentTestDB(t)
	const sessionID, round = 1, 1
	players := []models.SessionPlayer{
		{UserID: 1, CharacterCard: models.CharacterCard{Name: "甲"}},
		{UserID: 2, CharacterCard: models.CharacterCard{Name: "乙"}},
	}
	seedTurnAction(t, sessionID, round, 1, "甲")
	seedTurnAction(t, sessionID, round, 2, "乙")

	gctx := GameContext{Session: models.GameSession{ID: sessionID, TurnRound: round, Players: players}}

	clearConsumedTurnActions(gctx, nil, nil)

	if remaining := countTurnActions(t, sessionID, round); len(remaining) != 0 {
		t.Errorf("remaining = %v, want无遭遇时全删(与今天行为一致)", remaining)
	}
}

func TestClearConsumedTurnActions_ChaseUnresolvedKept(t *testing.T) {
	initAgentTestDB(t)
	const sessionID, round = 1, 1
	players := []models.SessionPlayer{
		{UserID: 1, CharacterCard: models.CharacterCard{Name: "甲"}},
		{UserID: 2, CharacterCard: models.CharacterCard{Name: "乙"}},
	}
	seedTurnAction(t, sessionID, round, 1, "甲")
	seedTurnAction(t, sessionID, round, 2, "乙")

	gctx := GameContext{Session: models.GameSession{ID: sessionID, TurnRound: round, Players: players}}
	chase := &models.ChaseState{
		Active: true, Round: 1,
		Participants: []models.ChaseParticipant{
			{Name: "甲", UserID: 1, HasActed: true},
			{Name: "乙", UserID: 2, HasActed: false},
		},
	}

	clearConsumedTurnActions(gctx, nil, chase)

	remaining := countTurnActions(t, sessionID, round)
	if len(remaining) != 1 || remaining[0] != 2 {
		t.Errorf("remaining = %v, want只保留乙(追逐中本轮未行动)", remaining)
	}
}

// NOTE: encounter_turn_test.go 验证BuildTurnCollection:发言权完全由KP点名决定,
// Spotlight为nil与点名列表为空都视为KP持有,点名之后死亡的玩家不再被等待。
// models.DB保持nil——这个函数不应触碰DB。
package agent

import (
	"testing"

	"github.com/llmcoc/server/internal/models"
)

func spotlightSession(sp *models.SpotlightState, players ...models.SessionPlayer) models.GameSession {
	return models.GameSession{
		ID:        1,
		Players:   players,
		Spotlight: models.JSONField[*models.SpotlightState]{Data: sp},
	}
}

func TestBuildTurnCollection_Spotlight_OnlyNamedPlayers(t *testing.T) {
	session := spotlightSession(
		&models.SpotlightState{UserIDs: []uint{2}, Names: []string{"乙"}, Question: "你怎么办？"},
		encounterTestPC(1, "甲", 80, 10, "none"),
		encounterTestPC(2, "乙", 60, 10, "none"),
	)
	got := BuildTurnCollection(session)
	if got.Question != "你怎么办？" {
		t.Errorf("Question = %q", got.Question)
	}
	if len(got.UserIDs) != 1 || got.UserIDs[0] != 2 {
		t.Errorf("UserIDs = %v, want [2](只收被点名者)", got.UserIDs)
	}
}

func TestBuildTurnCollection_Spotlight_FiltersDead(t *testing.T) {
	session := spotlightSession(
		&models.SpotlightState{UserIDs: []uint{1, 2}},
		encounterTestPC(1, "甲", 80, 10, "none"),
		encounterTestPC(2, "乙", 60, 10, "dead"),
	)
	got := BuildTurnCollection(session)
	if len(got.UserIDs) != 1 || got.UserIDs[0] != 1 {
		t.Errorf("UserIDs = %v, want [1](点名后死亡者不再被等待)", got.UserIDs)
	}
}

func TestBuildTurnCollection_Spotlight_EmptyMeansKPHoldsFloor(t *testing.T) {
	session := spotlightSession(&models.SpotlightState{}, encounterTestPC(1, "甲", 80, 10, "none"))
	got := BuildTurnCollection(session)
	if len(got.UserIDs) != 0 {
		t.Errorf("UserIDs = %v, want 空(KP持有发言权)", got.UserIDs)
	}
}

func TestBuildTurnCollection_NilSpotlightMeansKPHoldsFloor(t *testing.T) {
	session := spotlightSession(nil,
		encounterTestPC(1, "甲", 80, 10, "none"),
		encounterTestPC(2, "乙", 60, 10, "none"),
	)
	got := BuildTurnCollection(session)
	if got.UserIDs == nil || len(got.UserIDs) != 0 {
		t.Errorf("UserIDs = %v, want 非nil的空切片(老房间一律视为KP持有)", got.UserIDs)
	}
}

func TestBuildTurnCollection_Spotlight_AllNamedDeadMeansKPHoldsFloor(t *testing.T) {
	session := spotlightSession(
		&models.SpotlightState{UserIDs: []uint{1}},
		encounterTestPC(1, "甲", 80, 10, "dead"),
	)
	got := BuildTurnCollection(session)
	if len(got.UserIDs) != 0 {
		t.Errorf("UserIDs = %v, want 空(被点名者全部死亡)", got.UserIDs)
	}
}

// NOTE: encounter_turn_test.go 验证BuildTurnCollection的批次判定算法:连续PC合批、
// NPC打断批次、前导NPC跳过、死亡/已行动者跳过、不回绕、待澄清批次优先、无PC参战/无
// 遭遇时退回全员收集,以及"无遭遇时不触碰DB"的边界。
package agent

import (
	"testing"

	"github.com/llmcoc/server/internal/models"
)

func containsUint(ids []uint, id uint) bool {
	for _, v := range ids {
		if v == id {
			return true
		}
	}
	return false
}

func TestBuildTurnCollection_NoEncounter_AllActivePlayersNoDBQuery(t *testing.T) {
	// models.DB 保持nil,验证无遭遇分支完全不触碰DB(否则这里会panic)。
	session := models.GameSession{
		ID: 1,
		Players: []models.SessionPlayer{
			encounterTestPC(1, "甲", 80, 10, "none"),
			encounterTestPC(2, "乙", 60, 10, "dead"),
		},
	}
	got := BuildTurnCollection(session)
	if got.Batched {
		t.Error("无遭遇时Batched应为false")
	}
	if len(got.UserIDs) != 1 || got.UserIDs[0] != 1 {
		t.Errorf("UserIDs = %v, want [1](死亡玩家不计入)", got.UserIDs)
	}
}

func TestBuildTurnCollection_InactiveCombatStateSkipsNPCQuery(t *testing.T) {
	// CombatState.Data非nil但Active=false(已结束/未初始化);models.DB保持nil,
	// 验证一定先判Active再决定要不要查NPC,否则这里会panic。
	session := models.GameSession{
		ID:      1,
		Players: []models.SessionPlayer{encounterTestPC(1, "甲", 80, 10, "none")},
		CombatState: models.JSONField[*models.CombatState]{Data: &models.CombatState{
			Active: false,
		}},
	}
	got := BuildTurnCollection(session)
	if got.Batched {
		t.Error("Active=false时Batched应为false")
	}
	if len(got.UserIDs) != 1 || got.UserIDs[0] != 1 {
		t.Errorf("UserIDs = %v, want [1]", got.UserIDs)
	}
}

func TestBuildTurnCollection_Combat_ConsecutivePCsBatchTogether(t *testing.T) {
	initAgentTestDB(t)
	session := models.GameSession{
		ID: 1,
		Players: []models.SessionPlayer{
			encounterTestPC(1, "甲", 90, 10, "none"),
			encounterTestPC(2, "乙", 85, 10, "none"),
			encounterTestPC(3, "丁", 60, 10, "none"),
		},
		CombatState: models.JSONField[*models.CombatState]{Data: &models.CombatState{
			Active: true, Round: 1, ActorIndex: 0,
			Participants: []models.CombatParticipant{
				{Name: "甲", IsNPC: false, UserID: 1, DEX: 90},
				{Name: "乙", IsNPC: false, UserID: 2, DEX: 85},
				{Name: "食尸鬼", IsNPC: true, DEX: 70},
				{Name: "丁", IsNPC: false, UserID: 3, DEX: 60},
			},
		}},
	}
	got := BuildTurnCollection(session)
	if !got.Batched {
		t.Fatal("want Batched=true")
	}
	if len(got.UserIDs) != 2 || !containsUint(got.UserIDs, 1) || !containsUint(got.UserIDs, 2) {
		t.Errorf("UserIDs = %v, want恰好包含甲乙两人(连续PC合批,NPC打断丁)", got.UserIDs)
	}
	if containsUint(got.UserIDs, 3) {
		t.Error("丁排在NPC之后,不应进入本批次")
	}
	if !got.Order[0].InBatch || !got.Order[1].InBatch {
		t.Error("Order里甲乙的InBatch应为true")
	}
	if got.Order[2].InBatch || got.Order[3].InBatch {
		t.Error("Order里食尸鬼与丁的InBatch应为false")
	}
	if got.Label != "战斗 第1轮" {
		t.Errorf("Label = %q, want 战斗 第1轮", got.Label)
	}
}

func TestBuildTurnCollection_Combat_LeadingNPCSkipped(t *testing.T) {
	initAgentTestDB(t)
	session := models.GameSession{
		ID: 1,
		Players: []models.SessionPlayer{
			encounterTestPC(1, "甲", 70, 10, "none"),
		},
		CombatState: models.JSONField[*models.CombatState]{Data: &models.CombatState{
			Active: true, Round: 1, ActorIndex: 0,
			Participants: []models.CombatParticipant{
				{Name: "暗影生物", IsNPC: true, DEX: 90},
				{Name: "甲", IsNPC: false, UserID: 1, DEX: 70},
			},
		}},
	}
	got := BuildTurnCollection(session)
	if !got.Batched || len(got.UserIDs) != 1 || got.UserIDs[0] != 1 {
		t.Errorf("got = %+v, want批次仅含甲(前导NPC应被跳过,不阻断批次)", got)
	}
}

func TestBuildTurnCollection_Combat_DeadAndActedSkipped(t *testing.T) {
	initAgentTestDB(t)
	if err := models.DB.Create(&models.SessionNPC{SessionID: 1, Name: "食尸鬼", WoundState: "dead", IsAlive: false}).Error; err != nil {
		t.Fatalf("seed npc: %v", err)
	}
	session := models.GameSession{
		ID: 1,
		Players: []models.SessionPlayer{
			encounterTestPC(1, "甲", 90, 10, "none"),
			encounterTestPC(2, "乙", 60, 10, "none"),
		},
		CombatState: models.JSONField[*models.CombatState]{Data: &models.CombatState{
			Active: true, Round: 1, ActorIndex: 0,
			Participants: []models.CombatParticipant{
				{Name: "甲", IsNPC: false, UserID: 1, DEX: 90, HasActed: true},
				{Name: "食尸鬼", IsNPC: true, DEX: 80},
				{Name: "乙", IsNPC: false, UserID: 2, DEX: 60},
			},
		}},
	}
	got := BuildTurnCollection(session)
	if !got.Batched || len(got.UserIDs) != 1 || got.UserIDs[0] != 2 {
		t.Errorf("got = %+v, want批次仅含乙(甲已行动、食尸鬼已死亡均应跳过)", got)
	}
	if got.Order[1].Alive {
		t.Error("食尸鬼已死亡,Order里Alive应为false")
	}
}

func TestBuildTurnCollection_Combat_ScanDoesNotWrapToEarlierIndices(t *testing.T) {
	initAgentTestDB(t)
	session := models.GameSession{
		ID: 1,
		Players: []models.SessionPlayer{
			encounterTestPC(1, "甲", 80, 10, "none"),
			encounterTestPC(2, "乙", 50, 10, "none"),
		},
		CombatState: models.JSONField[*models.CombatState]{Data: &models.CombatState{
			Active: true, Round: 1, ActorIndex: 1, // 指向乙,甲在此之前但仍是HasActed=false的异常态
			Participants: []models.CombatParticipant{
				{Name: "甲", IsNPC: false, UserID: 1, DEX: 80},
				{Name: "乙", IsNPC: false, UserID: 2, DEX: 50},
			},
		}},
	}
	got := BuildTurnCollection(session)
	if !got.Batched || len(got.UserIDs) != 1 || got.UserIDs[0] != 2 {
		t.Errorf("got = %+v, want批次仅含乙(不应回绕到ActorIndex之前的甲)", got)
	}
}

func TestBuildTurnCollection_Combat_ClarifyingBatchTakesPriority(t *testing.T) {
	initAgentTestDB(t)
	session := models.GameSession{
		ID: 1,
		Players: []models.SessionPlayer{
			encounterTestPC(1, "调查员", 50, 10, "none"),
		},
		CombatState: models.JSONField[*models.CombatState]{Data: &models.CombatState{
			Active: true, Round: 1, ActorIndex: 0, // 冻结在攻击方(NPC)处
			Participants: []models.CombatParticipant{
				{Name: "暗影生物", IsNPC: true, DEX: 90},
				{Name: "调查员", IsNPC: false, UserID: 1, DEX: 50, PendingClarification: true, PendingQuestion: "闪避还是反击？"},
			},
		}},
	}
	got := BuildTurnCollection(session)
	if !got.Batched || len(got.UserIDs) != 1 || got.UserIDs[0] != 1 {
		t.Errorf("got = %+v, want批次仅含被问话的调查员(不含冻结的攻击方NPC)", got)
	}
	if !got.Order[1].Clarifying {
		t.Error("调查员的Clarifying应为true")
	}
}

func TestBuildTurnCollection_Combat_NoAlivePCFallsBackToAllPlayers(t *testing.T) {
	initAgentTestDB(t)
	session := models.GameSession{
		ID: 1,
		Players: []models.SessionPlayer{
			encounterTestPC(1, "旁观者", 60, 10, "none"), // 不在这场NPC互殴里
		},
		CombatState: models.JSONField[*models.CombatState]{Data: &models.CombatState{
			Active: true, Round: 1, ActorIndex: 0,
			Participants: []models.CombatParticipant{
				{Name: "食尸鬼甲", IsNPC: true, DEX: 80},
				{Name: "食尸鬼乙", IsNPC: true, DEX: 60},
			},
		}},
	}
	got := BuildTurnCollection(session)
	if got.Batched {
		t.Error("参战者全是NPC时Batched应为false(安全阀退回全员)")
	}
	if len(got.UserIDs) != 1 || got.UserIDs[0] != 1 {
		t.Errorf("UserIDs = %v, want [1](退回房间内全体存活玩家)", got.UserIDs)
	}
}

func TestBuildTurnCollection_Chase_ConsecutivePCsBatchTogether(t *testing.T) {
	initAgentTestDB(t)
	session := models.GameSession{
		ID: 1,
		Players: []models.SessionPlayer{
			encounterTestPC(1, "甲", 0, 10, "none"),
			encounterTestPC(2, "乙", 0, 10, "none"),
		},
		ChaseState: models.JSONField[*models.ChaseState]{Data: &models.ChaseState{
			Active: true, Round: 1, ActorIndex: 0, MinMOV: 5,
			Participants: []models.ChaseParticipant{
				{Name: "甲", IsNPC: false, UserID: 1, DEX: 90},
				{Name: "乙", IsNPC: false, UserID: 2, DEX: 85},
				{Name: "追猎者", IsNPC: true, DEX: 60},
			},
		}},
	}
	got := BuildTurnCollection(session)
	if !got.Batched || len(got.UserIDs) != 2 || !containsUint(got.UserIDs, 1) || !containsUint(got.UserIDs, 2) {
		t.Errorf("got = %+v, want批次包含甲乙两人", got)
	}
	if got.Label != "追逐 第1轮" {
		t.Errorf("Label = %q, want 追逐 第1轮", got.Label)
	}
}

func TestBuildTurnCollection_Chase_NPCInterruptsBatch(t *testing.T) {
	initAgentTestDB(t)
	session := models.GameSession{
		ID: 1,
		Players: []models.SessionPlayer{
			encounterTestPC(1, "甲", 0, 10, "none"),
			encounterTestPC(2, "乙", 0, 10, "none"),
		},
		ChaseState: models.JSONField[*models.ChaseState]{Data: &models.ChaseState{
			Active: true, Round: 1, ActorIndex: 0, MinMOV: 5,
			Participants: []models.ChaseParticipant{
				{Name: "甲", IsNPC: false, UserID: 1, DEX: 90},
				{Name: "追猎者", IsNPC: true, DEX: 80},
				{Name: "乙", IsNPC: false, UserID: 2, DEX: 60},
			},
		}},
	}
	got := BuildTurnCollection(session)
	if !got.Batched || len(got.UserIDs) != 1 || got.UserIDs[0] != 1 {
		t.Errorf("got = %+v, want批次仅含甲(追猎者打断,乙要等追猎者先裁定)", got)
	}
}

func TestBuildTurnCollection_Chase_ClarifyingBatchTakesPriority(t *testing.T) {
	initAgentTestDB(t)
	session := models.GameSession{
		ID: 1,
		Players: []models.SessionPlayer{
			encounterTestPC(1, "调查员", 0, 10, "none"),
		},
		ChaseState: models.JSONField[*models.ChaseState]{Data: &models.ChaseState{
			Active: true, Round: 1, ActorIndex: 0, MinMOV: 5,
			Participants: []models.ChaseParticipant{
				{Name: "追猎者", IsNPC: true, DEX: 90},
				{Name: "调查员", IsNPC: false, UserID: 1, DEX: 50, PendingClarification: true, PendingQuestion: "闪避还是反击？"},
			},
		}},
	}
	got := BuildTurnCollection(session)
	if !got.Batched || len(got.UserIDs) != 1 || got.UserIDs[0] != 1 {
		t.Errorf("got = %+v, want批次仅含被问话的调查员", got)
	}
}

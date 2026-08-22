// NOTE: combat_test.go 验证战斗状态机(buildCombatState/applyCombatAct)的核心分支逻辑。
package agent

import (
	"strings"
	"testing"

	"github.com/llmcoc/server/internal/models"
)

// newEncounterTestActx 构造combat/chase状态机测试用的最小ActionContext:纯内存
// Players/TempNPCs,不接触DB(buildCombatState/buildChaseState/applyCombatAct/
// applyChaseAct均只读内存状态,不在这些函数内部落库)。传入的每个PC都会自动补一条
// 占位PendingActions声明,满足声明可见性闸门——多数状态机测试关心的是顺序/结算逻
// 辑本身而非声明内容;需要验证"未声明被拒"分支的用例请用
// newEncounterTestActxNoDeclarations。
func newEncounterTestActx(players []models.SessionPlayer, npcs []models.SessionNPC) ActionContext {
	gctx := &GameContext{Session: models.GameSession{Players: players}, PendingActions: declareTestActions(players)}
	return ActionContext{GCtx: gctx, TempNPCs: &npcs}
}

// newEncounterTestActxNoDeclarations 与newEncounterTestActx相同,但不预置任何
// PendingActions,用于验证声明可见性闸门在PC本轮未提交行动时的拒绝分支。
func newEncounterTestActxNoDeclarations(players []models.SessionPlayer, npcs []models.SessionNPC) ActionContext {
	gctx := &GameContext{Session: models.GameSession{Players: players}}
	return ActionContext{GCtx: gctx, TempNPCs: &npcs}
}

func declareTestActions(players []models.SessionPlayer) []PlayerAction {
	actions := make([]PlayerAction, 0, len(players))
	for _, p := range players {
		actions = append(actions, PlayerAction{UserID: p.UserID, PlayerName: p.CharacterCard.Name, Content: "(测试声明)"})
	}
	return actions
}

func encounterTestPC(userID uint, name string, dex, hp int, wound string) models.SessionPlayer {
	return models.SessionPlayer{
		UserID: userID,
		CharacterCard: models.CharacterCard{
			Name:       name,
			WoundState: wound,
			Stats: models.JSONField[models.CharacterStats]{Data: models.CharacterStats{
				DEX: dex, HP: hp, MaxHP: hp,
			}},
		},
	}
}

func encounterTestNPC(name string, dex, hp int, wound string, alive bool) models.SessionNPC {
	return models.SessionNPC{
		Name:       name,
		WoundState: wound,
		IsAlive:    alive,
		Stats:      models.JSONField[map[string]int]{Data: map[string]int{"DEX": dex, "HP": hp, "MaxHP": hp}},
	}
}

func TestBuildCombatState_DEXOrderAndSkillTiebreak(t *testing.T) {
	npcs := []models.SessionNPC{
		encounterTestNPC("食尸鬼甲", 60, 15, "none", true),
		encounterTestNPC("食尸鬼乙", 60, 15, "none", true),
		encounterTestNPC("食尸鬼丙", 80, 15, "none", true),
	}
	actx := newEncounterTestActx(nil, npcs)
	inputs := []CombatParticipantInput{
		{Name: "食尸鬼甲", IsNPC: true, DEX: 60, CombatSkill: 30},
		{Name: "食尸鬼乙", IsNPC: true, DEX: 60, CombatSkill: 50},
		{Name: "食尸鬼丙", IsNPC: true, DEX: 80, CombatSkill: 10},
	}
	cs, err := buildCombatState(inputs, actx)
	if err != nil {
		t.Fatalf("buildCombatState error: %v", err)
	}
	want := []string{"食尸鬼丙", "食尸鬼乙", "食尸鬼甲"}
	if len(cs.Participants) != len(want) {
		t.Fatalf("participants = %d, want %d", len(cs.Participants), len(want))
	}
	for i, name := range want {
		if cs.Participants[i].Name != name {
			t.Errorf("participants[%d] = %q, want %q (order: DEX desc, tie by combat_skill desc)", i, cs.Participants[i].Name, name)
		}
	}
}

func TestBuildCombatState_StableOrderWhenDEXAndSkillEqual(t *testing.T) {
	npcs := []models.SessionNPC{
		encounterTestNPC("暴徒A", 50, 10, "none", true),
		encounterTestNPC("暴徒B", 50, 10, "none", true),
	}
	actx := newEncounterTestActx(nil, npcs)
	inputs := []CombatParticipantInput{
		{Name: "暴徒A", IsNPC: true, DEX: 50},
		{Name: "暴徒B", IsNPC: true, DEX: 50},
	}
	cs, err := buildCombatState(inputs, actx)
	if err != nil {
		t.Fatalf("buildCombatState error: %v", err)
	}
	if cs.Participants[0].Name != "暴徒A" || cs.Participants[1].Name != "暴徒B" {
		t.Errorf("DEX与combat_skill都相同时应保持输入顺序,got %s, %s", cs.Participants[0].Name, cs.Participants[1].Name)
	}
}

func TestBuildCombatState_PCDEXFromCardIgnoresModelInput(t *testing.T) {
	players := []models.SessionPlayer{encounterTestPC(7, "约翰", 70, 12, "none")}
	actx := newEncounterTestActx(players, nil)
	inputs := []CombatParticipantInput{
		{Name: "约翰", IsNPC: false, DEX: 99}, // 模型谎报DEX,必须被忽略
	}
	cs, err := buildCombatState(inputs, actx)
	if err != nil {
		t.Fatalf("buildCombatState error: %v", err)
	}
	if cs.Participants[0].DEX != 70 {
		t.Errorf("PC DEX = %d, want 70(权威来自角色卡,忽略模型输入的99)", cs.Participants[0].DEX)
	}
	if cs.Participants[0].UserID != 7 {
		t.Errorf("PC UserID = %d, want 7", cs.Participants[0].UserID)
	}
}

func TestBuildCombatState_MissingPCFails(t *testing.T) {
	players := []models.SessionPlayer{encounterTestPC(1, "约翰", 70, 12, "none")}
	actx := newEncounterTestActx(players, nil)
	inputs := []CombatParticipantInput{{Name: "不存在的角色", IsNPC: false}}
	_, err := buildCombatState(inputs, actx)
	if err == nil {
		t.Fatal("want error when PC name not found among session players")
	}
	if !strings.Contains(err.Error(), "不存在的角色") || !strings.Contains(err.Error(), "约翰") {
		t.Errorf("error = %q, want it to name the missing character and list real character names", err.Error())
	}
}

func TestApplyCombatAct_OrderEnforced(t *testing.T) {
	cs := &models.CombatState{
		Active: true, Round: 1, ActorIndex: 0,
		Participants: []models.CombatParticipant{
			{Name: "甲", DEX: 80, IsNPC: true},
			{Name: "乙", DEX: 50, IsNPC: true},
		},
	}
	actx := newEncounterTestActx(nil, nil)
	roundClosed := false
	call := ToolCall{CombatActorName: "乙", CombatAction: &CombatActionDetail{Type: "attack"}}
	result := applyCombatAct(cs, call, actx, &roundClosed)
	if !strings.Contains(result, "SYSTEM REJECT") || !strings.Contains(result, "甲") {
		t.Errorf("result = %q, want SYSTEM REJECT naming 甲 as the correct next actor", result)
	}
	if cs.Participants[0].HasActed {
		t.Error("乱序调用被拒时不应更改任何参战者的HasActed")
	}
}

func TestApplyCombatAct_RoundAdvanceResetsStateAndClosesRound(t *testing.T) {
	cs := &models.CombatState{
		Active: true, Round: 1, ActorIndex: 0,
		Participants: []models.CombatParticipant{
			{Name: "甲", DEX: 80, IsNPC: true, APDebt: 0},
			{Name: "乙", DEX: 50, IsNPC: true, APDebt: 2},
		},
	}
	actx := newEncounterTestActx(nil, nil)
	roundClosed := false

	r1 := applyCombatAct(cs, ToolCall{CombatActorName: "甲", CombatAction: &CombatActionDetail{Type: "attack", TargetName: "乙"}}, actx, &roundClosed)
	if roundClosed {
		t.Fatal("第一人行动后本轮不应关闭")
	}
	if cs.ActorIndex != 1 {
		t.Errorf("ActorIndex = %d, want 1(轮到乙)", cs.ActorIndex)
	}
	if !cs.Participants[0].HasActed {
		t.Error("甲行动后HasActed应为true")
	}
	_ = r1

	r2 := applyCombatAct(cs, ToolCall{CombatActorName: "乙", CombatAction: &CombatActionDetail{Type: "attack", TargetName: "甲"}}, actx, &roundClosed)
	if !roundClosed {
		t.Fatalf("全员行动完毕后roundClosed应置true,result=%q", r2)
	}
	if cs.Round != 2 {
		t.Errorf("Round = %d, want 2", cs.Round)
	}
	if cs.ActorIndex != 0 {
		t.Errorf("ActorIndex = %d, want 0(翻页归零)", cs.ActorIndex)
	}
	if cs.Participants[0].HasActed || cs.Participants[1].HasActed {
		t.Error("翻页后HasActed应全部清空")
	}
	if cs.Participants[1].APDebt != 1 {
		t.Errorf("乙的APDebt = %d, want 1(翻页时递减1)", cs.Participants[1].APDebt)
	}
	if cs.Participants[0].APDebt != 0 {
		t.Errorf("甲的APDebt = %d, want 0(本就是0,不应变负)", cs.Participants[0].APDebt)
	}
}

func TestApplyCombatAct_DeadParticipantSkippedInRoundCompletion(t *testing.T) {
	players := []models.SessionPlayer{
		encounterTestPC(1, "甲", 90, 12, "none"),
		encounterTestPC(2, "乙", 80, 0, "dead"),
	}
	npcs := []models.SessionNPC{encounterTestNPC("丙", 70, 10, "none", true)}
	actx := newEncounterTestActx(players, npcs)
	cs := &models.CombatState{
		Active: true, Round: 1, ActorIndex: 0,
		Participants: []models.CombatParticipant{
			{Name: "甲", DEX: 90, IsNPC: false, UserID: 1},
			{Name: "乙", DEX: 80, IsNPC: false, UserID: 2},
			{Name: "丙", DEX: 70, IsNPC: true},
		},
	}
	roundClosed := false

	applyCombatAct(cs, ToolCall{CombatActorName: "甲", CombatAction: &CombatActionDetail{Type: "attack", TargetName: "丙"}}, actx, &roundClosed)
	if cs.ActorIndex != 2 {
		t.Fatalf("ActorIndex = %d, want 2(应跳过已阵亡的乙,直接轮到丙)", cs.ActorIndex)
	}

	result := applyCombatAct(cs, ToolCall{CombatActorName: "丙", CombatAction: &CombatActionDetail{Type: "attack", TargetName: "甲"}}, actx, &roundClosed)
	if !roundClosed {
		t.Fatalf("甲丙都已行动、乙已阵亡,本轮应结算完毕,result=%q", result)
	}
	if cs.Round != 2 {
		t.Errorf("Round = %d, want 2", cs.Round)
	}
}

func TestApplyCombatAct_ClarificationPauseFreezesState(t *testing.T) {
	players := []models.SessionPlayer{encounterTestPC(3, "调查员", 50, 10, "none")}
	npcs := []models.SessionNPC{encounterTestNPC("暗影生物", 90, 20, "none", true)}
	actx := newEncounterTestActx(players, npcs)
	cs := &models.CombatState{
		Active: true, Round: 1, ActorIndex: 0,
		Participants: []models.CombatParticipant{
			{Name: "暗影生物", DEX: 90, IsNPC: true},
			{Name: "调查员", DEX: 50, IsNPC: false, UserID: 3},
		},
	}
	roundClosed := false
	call := ToolCall{
		CombatActorName: "暗影生物",
		CombatAction: &CombatActionDetail{
			Type: "dodge", TargetName: "调查员",
			NeedsClarification: true, ClarifyQuestion: "你选择闪避还是反击？",
		},
	}
	result := applyCombatAct(cs, call, actx, &roundClosed)
	if !strings.Contains(result, "暂停") {
		t.Errorf("result = %q, want message indicating the round paused for clarification", result)
	}
	target := findCombatParticipant(cs, "调查员")
	if !target.PendingClarification {
		t.Error("被攻击的调查员应置PendingClarification=true")
	}
	if target.PendingQuestion != "你选择闪避还是反击？" {
		t.Errorf("PendingQuestion = %q, want 原样保留问题文本", target.PendingQuestion)
	}
	if cs.Participants[0].HasActed {
		t.Error("待澄清暂停时攻击方的HasActed不应置位")
	}
	if cs.ActorIndex != 0 {
		t.Errorf("ActorIndex = %d, want 0(冻结在攻击方,未推进)", cs.ActorIndex)
	}
	if cs.Round != 1 {
		t.Errorf("Round = %d, want 1(不应推进轮次)", cs.Round)
	}
	if roundClosed {
		t.Error("待澄清暂停不应置位roundClosed")
	}
}

func TestApplyCombatAct_ClarificationRejectedForNPCTarget(t *testing.T) {
	npcs := []models.SessionNPC{
		encounterTestNPC("调查员的NPC同伴", 50, 10, "none", true),
		encounterTestNPC("暗影生物", 90, 20, "none", true),
	}
	actx := newEncounterTestActx(nil, npcs)
	cs := &models.CombatState{
		Active: true, Round: 1, ActorIndex: 0,
		Participants: []models.CombatParticipant{
			{Name: "暗影生物", DEX: 90, IsNPC: true},
			{Name: "调查员的NPC同伴", DEX: 50, IsNPC: true},
		},
	}
	roundClosed := false
	call := ToolCall{
		CombatActorName: "暗影生物",
		CombatAction: &CombatActionDetail{
			Type: "dodge", TargetName: "调查员的NPC同伴",
			NeedsClarification: true, ClarifyQuestion: "闪避还是反击？",
		},
	}
	result := applyCombatAct(cs, call, actx, &roundClosed)
	if !strings.Contains(result, "SYSTEM REJECT") {
		t.Errorf("result = %q, want SYSTEM REJECT(NPC反应不得用needs_clarification回避决策)", result)
	}
	target := findCombatParticipant(cs, "调查员的NPC同伴")
	if target.PendingClarification {
		t.Error("被拒绝的needs_clarification不应改变目标状态")
	}
}

func TestApplyCombatAct_ClarificationRecoveryThenAdvances(t *testing.T) {
	players := []models.SessionPlayer{encounterTestPC(3, "调查员", 50, 10, "none")}
	npcs := []models.SessionNPC{encounterTestNPC("暗影生物", 90, 20, "none", true)}
	actx := newEncounterTestActx(players, npcs)
	cs := &models.CombatState{
		Active: true, Round: 1, ActorIndex: 0,
		Participants: []models.CombatParticipant{
			{Name: "暗影生物", DEX: 90, IsNPC: true},
			{Name: "调查员", DEX: 50, IsNPC: false, UserID: 3},
		},
	}
	roundClosed1 := false
	applyCombatAct(cs, ToolCall{
		CombatActorName: "暗影生物",
		CombatAction:    &CombatActionDetail{Type: "dodge", TargetName: "调查员", NeedsClarification: true, ClarifyQuestion: "闪避还是反击？"},
	}, actx, &roundClosed1)

	// 下一会话轮(新的run,新的roundClosed指针),攻击方仍是当前行动者,这次带明确反应。
	roundClosed2 := false
	applyCombatAct(cs, ToolCall{
		CombatActorName: "暗影生物",
		CombatAction:    &CombatActionDetail{Type: "dodge", TargetName: "调查员"},
	}, actx, &roundClosed2)

	target := findCombatParticipant(cs, "调查员")
	if target.PendingClarification {
		t.Error("续接成功结算后应清空目标的PendingClarification")
	}
	if !cs.Participants[0].HasActed {
		t.Error("续接结算后攻击方HasActed应为true")
	}
	if cs.ActorIndex != 1 {
		t.Errorf("ActorIndex = %d, want 1(轮到调查员)", cs.ActorIndex)
	}
}

func TestApplyCombatAct_D2GateBlocksSecondRoundSameRun(t *testing.T) {
	cs := &models.CombatState{
		Active: true, Round: 1, ActorIndex: 0,
		Participants: []models.CombatParticipant{
			{Name: "甲", DEX: 80, IsNPC: true},
			{Name: "乙", DEX: 50, IsNPC: true},
		},
	}
	actx := newEncounterTestActx(nil, nil)
	roundClosed := false
	applyCombatAct(cs, ToolCall{CombatActorName: "甲", CombatAction: &CombatActionDetail{Type: "attack"}}, actx, &roundClosed)
	applyCombatAct(cs, ToolCall{CombatActorName: "乙", CombatAction: &CombatActionDetail{Type: "attack"}}, actx, &roundClosed)
	if !roundClosed || cs.Round != 2 {
		t.Fatalf("前置条件不满足: roundClosed=%v Round=%d", roundClosed, cs.Round)
	}

	result := applyCombatAct(cs, ToolCall{CombatActorName: "甲", CombatAction: &CombatActionDetail{Type: "attack"}}, actx, &roundClosed)
	if !strings.HasPrefix(result, "SYSTEM REJECT") || !strings.Contains(result, "结算完毕") {
		t.Errorf("result = %q, want SYSTEM REJECT要求response收尾(D2:一次run最多推进一轮)", result)
	}
	if cs.Round != 2 {
		t.Errorf("Round = %d, want 2(D2拒绝后不应继续推进)", cs.Round)
	}
	if cs.Participants[0].HasActed {
		t.Error("D2拒绝后不应更改任何参战者状态")
	}
}

func TestApplyCombatAct_DeclarationGate_NPCAlwaysAllowed(t *testing.T) {
	cs := &models.CombatState{
		Active: true, Round: 1, ActorIndex: 0,
		Participants: []models.CombatParticipant{
			{Name: "食尸鬼甲", DEX: 80, IsNPC: true},
			{Name: "食尸鬼乙", DEX: 60, IsNPC: true},
		},
	}
	// 不带任何PendingActions,NPC行动者不受声明可见性闸门约束。
	actx := newEncounterTestActxNoDeclarations(nil, nil)
	roundClosed := false
	result := applyCombatAct(cs, ToolCall{CombatActorName: "食尸鬼甲", CombatAction: &CombatActionDetail{Type: "attack"}}, actx, &roundClosed)
	if strings.Contains(result, "SYSTEM REJECT") {
		t.Errorf("result = %q, NPC行动者不应被声明可见性闸门拒绝", result)
	}
	if !cs.Participants[0].HasActed {
		t.Error("NPC行动应正常结算,HasActed应为true")
	}
}

func TestApplyCombatAct_DeclarationGate_DeclaredPCAllowed(t *testing.T) {
	players := []models.SessionPlayer{encounterTestPC(1, "甲", 80, 10, "none")}
	cs := &models.CombatState{
		Active: true, Round: 1, ActorIndex: 0,
		Participants: []models.CombatParticipant{
			{Name: "甲", DEX: 80, IsNPC: false, UserID: 1},
			{Name: "食尸鬼", DEX: 60, IsNPC: true},
		},
	}
	actx := newEncounterTestActx(players, nil) // 自动补一条甲的占位声明
	roundClosed := false
	result := applyCombatAct(cs, ToolCall{CombatActorName: "甲", CombatAction: &CombatActionDetail{Type: "attack"}}, actx, &roundClosed)
	if strings.Contains(result, "SYSTEM REJECT") {
		t.Errorf("result = %q, 有声明的PC不应被拒", result)
	}
	if !cs.Participants[0].HasActed {
		t.Error("有声明的PC行动应正常结算,HasActed应为true")
	}
}

func TestApplyCombatAct_DeclarationGate_UndeclaredPCRejected(t *testing.T) {
	players := []models.SessionPlayer{encounterTestPC(1, "甲", 80, 10, "none")}
	cs := &models.CombatState{
		Active: true, Round: 1, ActorIndex: 0,
		Participants: []models.CombatParticipant{{Name: "甲", DEX: 80, IsNPC: false, UserID: 1}},
	}
	actx := newEncounterTestActxNoDeclarations(players, nil)
	roundClosed := false
	result := applyCombatAct(cs, ToolCall{CombatActorName: "甲", CombatAction: &CombatActionDetail{Type: "attack"}}, actx, &roundClosed)
	if !strings.Contains(result, "SYSTEM REJECT") || !strings.Contains(result, "甲") {
		t.Errorf("result = %q, want SYSTEM REJECT点名甲尚未提交声明", result)
	}
	if cs.Participants[0].HasActed {
		t.Error("未声明被拒时不应更改HasActed")
	}
	if cs.ActorIndex != 0 || cs.Round != 1 || roundClosed {
		t.Error("未声明被拒时不应推进ActorIndex/Round/roundClosed")
	}
}

func TestApplyCombatAct_DeclarationGate_FrozenAttackerResumesWithoutRedeclaring(t *testing.T) {
	players := []models.SessionPlayer{
		encounterTestPC(1, "甲", 80, 10, "none"),
		encounterTestPC(2, "乙", 50, 10, "none"),
	}
	cs := &models.CombatState{
		Active: true, Round: 1, ActorIndex: 0,
		Participants: []models.CombatParticipant{
			{Name: "甲", DEX: 80, IsNPC: false, UserID: 1},
			{Name: "乙", DEX: 50, IsNPC: false, UserID: 2},
		},
	}
	// 同一个actx贯穿两次调用,模拟D4:暂停时攻击方的原始声明不会被清掉,续接时无需重交。
	actx := newEncounterTestActx(players, nil)
	roundClosed := false

	applyCombatAct(cs, ToolCall{
		CombatActorName: "甲",
		CombatAction:    &CombatActionDetail{Type: "attack", TargetName: "乙", NeedsClarification: true, ClarifyQuestion: "闪避还是反击？"},
	}, actx, &roundClosed)
	if cs.Participants[0].HasActed {
		t.Fatal("待澄清暂停时攻击方HasActed不应置位")
	}

	result := applyCombatAct(cs, ToolCall{
		CombatActorName: "甲",
		CombatAction:    &CombatActionDetail{Type: "attack", TargetName: "乙"},
	}, actx, &roundClosed)
	if strings.Contains(result, "SYSTEM REJECT") {
		t.Errorf("result = %q, 冻结的攻击方续接时应凭原声明放行,无需重交", result)
	}
	if !cs.Participants[0].HasActed {
		t.Error("续接结算后攻击方HasActed应为true")
	}
	if cs.ActorIndex != 1 {
		t.Errorf("ActorIndex = %d, want 1(轮到乙)", cs.ActorIndex)
	}
}

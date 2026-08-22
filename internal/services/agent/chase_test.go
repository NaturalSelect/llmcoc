// NOTE: chase_test.go 验证追逐状态机(buildChaseState/applyChaseAct)的核心分支逻辑。
// 共用combat_test.go里的newEncounterTestActx/encounterTestPC/encounterTestNPC。
package agent

import (
	"strings"
	"testing"

	"github.com/llmcoc/server/internal/models"
)

func TestBuildChaseState_DEXOrderAndMinMOV(t *testing.T) {
	npcs := []models.SessionNPC{
		encounterTestNPC("暴徒", 40, 10, "none", true),
	}
	actx := newEncounterTestActx(nil, npcs)
	inputs := []ChaseParticipantInput{
		{Name: "暴徒", IsNPC: true, DEX: 40, MOV: 6, Location: -2, IsPursuer: true},
		{Name: "路人", IsNPC: true, DEX: 70, MOV: 9, Location: 0, IsPursuer: false},
	}
	chs, err := buildChaseState(inputs, actx)
	if err != nil {
		t.Fatalf("buildChaseState error: %v", err)
	}
	if chs.Participants[0].Name != "路人" || chs.Participants[1].Name != "暴徒" {
		t.Errorf("顺序应按DEX降序: got %s, %s", chs.Participants[0].Name, chs.Participants[1].Name)
	}
	if chs.MinMOV != 6 {
		t.Errorf("MinMOV = %d, want 6(全员MOV最小值)", chs.MinMOV)
	}
	// "路人"是NPC但TempNPCs里没有它的卡,DEX应退回模型提供的70。
	if chs.Participants[0].DEX != 70 {
		t.Errorf("路人 DEX = %d, want 70(NPC无卡时退回模型输入)", chs.Participants[0].DEX)
	}
}

func TestBuildChaseState_PCDEXFromCardButMOVFromSpeedCheck(t *testing.T) {
	players := []models.SessionPlayer{encounterTestPC(5, "哈维", 70, 10, "none")}
	actx := newEncounterTestActx(players, nil)
	inputs := []ChaseParticipantInput{
		{Name: "哈维", IsNPC: false, DEX: 30, MOV: 9, Location: 0, IsPursuer: false},
	}
	chs, err := buildChaseState(inputs, actx)
	if err != nil {
		t.Fatalf("buildChaseState error: %v", err)
	}
	if chs.Participants[0].DEX != 70 {
		t.Errorf("DEX = %d, want 70(权威来自角色卡,忽略模型输入的30)", chs.Participants[0].DEX)
	}
	if chs.Participants[0].MOV != 9 {
		t.Errorf("MOV = %d, want 9(速度检定结果,只能来自模型输入)", chs.Participants[0].MOV)
	}
	if chs.Participants[0].UserID != 5 {
		t.Errorf("UserID = %d, want 5", chs.Participants[0].UserID)
	}
}

func TestBuildChaseState_MissingPCFails(t *testing.T) {
	players := []models.SessionPlayer{encounterTestPC(1, "哈维", 70, 10, "none")}
	actx := newEncounterTestActx(players, nil)
	inputs := []ChaseParticipantInput{{Name: "不存在的角色", IsNPC: false}}
	_, err := buildChaseState(inputs, actx)
	if err == nil {
		t.Fatal("want error when PC name not found among session players")
	}
	if !strings.Contains(err.Error(), "不存在的角色") || !strings.Contains(err.Error(), "哈维") {
		t.Errorf("error = %q, want it to name the missing character and list real character names", err.Error())
	}
}

func TestChaseActionPoints_Formula(t *testing.T) {
	cases := []struct {
		name   string
		p      models.ChaseParticipant
		minMOV int
		want   int
	}{
		{"高于最低MOV按差值加行动点", models.ChaseParticipant{MOV: 8}, 5, 4},
		{"等于最低MOV时基础1点", models.ChaseParticipant{MOV: 5}, 5, 1},
		{"扣除AP欠债", models.ChaseParticipant{MOV: 8, APDebt: 2}, 5, 2},
		{"欠债超过基础值下限为0", models.ChaseParticipant{MOV: 5, APDebt: 5}, 5, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := chaseActionPoints(tc.p, tc.minMOV); got != tc.want {
				t.Errorf("chaseActionPoints() = %d, want %d", got, tc.want)
			}
		})
	}
}

func TestApplyChaseAct_OrderEnforced(t *testing.T) {
	chs := &models.ChaseState{
		Active: true, Round: 1, ActorIndex: 0, MinMOV: 5,
		Participants: []models.ChaseParticipant{
			{Name: "甲", DEX: 80, MOV: 8},
			{Name: "乙", DEX: 50, MOV: 6},
		},
	}
	actx := newEncounterTestActx(nil, nil)
	roundClosed := false
	result := applyChaseAct(chs, ToolCall{ChaseActorName: "乙", ChaseAction: &ChaseActionDetail{Type: "move"}}, actx, &roundClosed)
	if !strings.Contains(result, "SYSTEM REJECT") || !strings.Contains(result, "甲") {
		t.Errorf("result = %q, want SYSTEM REJECT naming 甲 as the correct next actor", result)
	}
}

func TestApplyChaseAct_MoveDeltaAndRoundAdvance(t *testing.T) {
	chs := &models.ChaseState{
		Active: true, Round: 1, ActorIndex: 0, MinMOV: 5,
		Participants: []models.ChaseParticipant{
			{Name: "甲", IsNPC: true, DEX: 80, IsPursuer: true, MOV: 8, Location: -2, APDebt: 0},
			{Name: "乙", IsNPC: true, DEX: 50, IsPursuer: false, MOV: 6, Location: 0, APDebt: 3},
		},
	}
	actx := newEncounterTestActx(nil, nil)
	roundClosed := false

	applyChaseAct(chs, ToolCall{ChaseActorName: "甲", ChaseAction: &ChaseActionDetail{Type: "move", MoveDelta: 2}}, actx, &roundClosed)
	if chs.Participants[0].Location != 0 {
		t.Errorf("甲 Location = %d, want 0(-2+2)", chs.Participants[0].Location)
	}
	if roundClosed {
		t.Fatal("第一人行动后本轮不应关闭")
	}
	if chs.ActorIndex != 1 {
		t.Errorf("ActorIndex = %d, want 1(轮到乙)", chs.ActorIndex)
	}

	applyChaseAct(chs, ToolCall{ChaseActorName: "乙", ChaseAction: &ChaseActionDetail{Type: "move", MoveDelta: -1}}, actx, &roundClosed)
	if !roundClosed {
		t.Fatal("全员行动完毕后roundClosed应置true")
	}
	if chs.Round != 2 {
		t.Errorf("Round = %d, want 2", chs.Round)
	}
	if chs.Participants[1].APDebt != 2 {
		t.Errorf("乙的APDebt = %d, want 2(翻页递减1)", chs.Participants[1].APDebt)
	}
	if chs.Participants[1].Location != -1 {
		t.Errorf("乙 Location = %d, want -1", chs.Participants[1].Location)
	}
}

func TestApplyChaseAct_ObstacleCreateThenUpdateInPlace(t *testing.T) {
	chs := &models.ChaseState{
		Active: true, Round: 1, ActorIndex: 0, MinMOV: 5,
		Participants: []models.ChaseParticipant{{Name: "逃亡者", IsNPC: true, DEX: 60, MOV: 5}},
	}
	actx := newEncounterTestActx(nil, nil)

	roundClosed1 := false
	applyChaseAct(chs, ToolCall{
		ChaseActorName: "逃亡者",
		ChaseAction: &ChaseActionDetail{
			Type: "obstacle", ObstacleName: "木门", ObstacleHP: 10, ObstacleMaxHP: 10, ObstacleBetween: [2]int{2, 3},
		},
	}, actx, &roundClosed1)
	if len(chs.Obstacles) != 1 {
		t.Fatalf("Obstacles长度 = %d, want 1", len(chs.Obstacles))
	}
	if chs.Obstacles[0].Between != [2]int{2, 3} {
		t.Errorf("Between = %v, want [2 3]", chs.Obstacles[0].Between)
	}
	if chs.Obstacles[0].HP != 10 {
		t.Errorf("HP = %d, want 10", chs.Obstacles[0].HP)
	}

	roundClosed2 := false
	applyChaseAct(chs, ToolCall{
		ChaseActorName: "逃亡者",
		ChaseAction: &ChaseActionDetail{
			Type: "obstacle", ObstacleName: "木门", ObstacleHP: 4, ObstacleMaxHP: 10, ObstacleBetween: [2]int{2, 3},
		},
	}, actx, &roundClosed2)
	if len(chs.Obstacles) != 1 {
		t.Fatalf("再次对同名障碍调用应原地更新,不应新增;Obstacles长度 = %d, want 1", len(chs.Obstacles))
	}
	if chs.Obstacles[0].HP != 4 {
		t.Errorf("HP = %d, want 4(原地更新)", chs.Obstacles[0].HP)
	}
}

func TestApplyChaseAct_ConflictClarificationPause(t *testing.T) {
	players := []models.SessionPlayer{encounterTestPC(9, "调查员", 50, 10, "none")}
	npcs := []models.SessionNPC{encounterTestNPC("追猎者", 90, 15, "none", true)}
	actx := newEncounterTestActx(players, npcs)
	chs := &models.ChaseState{
		Active: true, Round: 1, ActorIndex: 0, MinMOV: 5,
		Participants: []models.ChaseParticipant{
			{Name: "追猎者", DEX: 90, IsNPC: true, MOV: 8, IsPursuer: true},
			{Name: "调查员", DEX: 50, IsNPC: false, UserID: 9, MOV: 6, IsPursuer: false},
		},
	}
	roundClosed := false
	result := applyChaseAct(chs, ToolCall{
		ChaseActorName: "追猎者",
		ChaseAction: &ChaseActionDetail{
			Type: "conflict", TargetName: "调查员",
			NeedsClarification: true, ClarifyQuestion: "闪避还是反击？",
		},
	}, actx, &roundClosed)
	if !strings.Contains(result, "暂停") {
		t.Errorf("result = %q, want 暂停提示", result)
	}
	target := findChaseParticipant(chs, "调查员")
	if !target.PendingClarification {
		t.Error("被攻击的调查员应置PendingClarification=true")
	}
	if chs.Participants[0].HasActed {
		t.Error("待澄清暂停时攻击方HasActed不应置位")
	}
	if chs.ActorIndex != 0 || chs.Round != 1 || roundClosed {
		t.Error("待澄清暂停不应推进ActorIndex/Round/roundClosed")
	}
}

func TestApplyChaseAct_ClarificationRejectedForNonConflictType(t *testing.T) {
	npcs := []models.SessionNPC{
		encounterTestNPC("追猎者", 90, 15, "none", true),
		encounterTestNPC("同伴", 50, 10, "none", true),
	}
	actx := newEncounterTestActx(nil, npcs)
	chs := &models.ChaseState{
		Active: true, Round: 1, ActorIndex: 0, MinMOV: 5,
		Participants: []models.ChaseParticipant{
			{Name: "追猎者", DEX: 90, IsNPC: true, MOV: 8},
			{Name: "同伴", DEX: 50, IsNPC: true, MOV: 6},
		},
	}
	roundClosed := false
	result := applyChaseAct(chs, ToolCall{
		ChaseActorName: "追猎者",
		ChaseAction: &ChaseActionDetail{
			Type: "hazard", TargetName: "同伴",
			NeedsClarification: true, ClarifyQuestion: "谨慎还是鲁莽？",
		},
	}, actx, &roundClosed)
	if !strings.Contains(result, "SYSTEM REJECT") {
		t.Errorf("result = %q, want SYSTEM REJECT(needs_clarification仅限conflict类型)", result)
	}
}

func TestApplyChaseAct_D2GateBlocksSecondRoundSameRun(t *testing.T) {
	chs := &models.ChaseState{
		Active: true, Round: 1, ActorIndex: 0, MinMOV: 5,
		Participants: []models.ChaseParticipant{{Name: "逃亡者", IsNPC: true, DEX: 60, MOV: 5}},
	}
	actx := newEncounterTestActx(nil, nil)
	roundClosed := false
	applyChaseAct(chs, ToolCall{ChaseActorName: "逃亡者", ChaseAction: &ChaseActionDetail{Type: "move", MoveDelta: 1}}, actx, &roundClosed)
	if !roundClosed || chs.Round != 2 {
		t.Fatalf("前置条件不满足: roundClosed=%v Round=%d", roundClosed, chs.Round)
	}

	result := applyChaseAct(chs, ToolCall{ChaseActorName: "逃亡者", ChaseAction: &ChaseActionDetail{Type: "move", MoveDelta: 1}}, actx, &roundClosed)
	if !strings.HasPrefix(result, "SYSTEM REJECT") || !strings.Contains(result, "结算完毕") {
		t.Errorf("result = %q, want SYSTEM REJECT要求response收尾(D2:一次run最多推进一轮)", result)
	}
	if chs.Round != 2 {
		t.Errorf("Round = %d, want 2(D2拒绝后不应继续推进)", chs.Round)
	}
}

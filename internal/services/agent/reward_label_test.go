package agent

import (
	"strings"
	"testing"

	"github.com/llmcoc/server/internal/models"
)

func TestRewardTypeLabel(t *testing.T) {
	tests := map[string]string{
		"tome":     "神话典籍",
		" Tome ":   "神话典籍",
		"artifact": "神话物品",
		"ARTIFACT": "神话物品",
		"relic":    "relic",
		"":         "",
	}
	for in, want := range tests {
		if got := rewardTypeLabel(in); got != want {
			t.Errorf("rewardTypeLabel(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestStripInternalTypeTags(t *testing.T) {
	tests := []struct{ name, in, want string }{
		{"开头的标记", "[tome] 次级巫师手记。精读需1-4周", "次级巫师手记。精读需1-4周"},
		{"括号描述里的标记", "《航海星历表》手记([tome] 次级巫师手记。神话+2%)", "《航海星历表》手记(次级巫师手记。神话+2%)"},
		{"大小写与空白", "古物([ Artifact ]  会发光)", "古物(会发光)"},
		{"没有标记原样返回", "手电筒(电量不足, x2)", "手电筒(电量不足, x2)"},
		{"普通方括号不动", "字条([重要] 地下室钥匙在哪)", "字条([重要] 地下室钥匙在哪)"},
		{"没有方括号", "子弹(x6)", "子弹(x6)"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := stripInternalTypeTags(tt.in); got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestManageInventoryAction_StripsInternalTypeTag(t *testing.T) {
	initAgentTestDB(t)
	players := eventTestPlayers()
	actx := eventTestActx(players)

	evs := onlyResultEvents(t, manageInventoryAction{}.Execute(ToolCall{
		CharacterName: "甲", Operate: "add",
		ItemName: "《航海星历表》手记", ItemDesc: "[tome] 次级巫师手记。神话+2%",
	}, actx))

	if len(evs) != 1 || evs[0].Op != "add" {
		t.Fatalf("events = %+v, want one add", evs)
	}
	var got models.Item
	for _, it := range players[0].CharacterCard.Inventory.Data {
		if it.Name == "《航海星历表》手记" {
			got = it
		}
	}
	if got.Name == "" {
		t.Fatalf("item not added, inventory = %v", players[0].CharacterCard.Inventory.Data)
	}
	if strings.Contains(got.Label(), "[tome]") {
		t.Errorf("inventory item leaks internal tag: %q", got.Label())
	}
	if strings.Contains(evs[0].Name+evs[0].Detail, "[tome]") {
		t.Errorf("event leaks internal tag: %+v", evs[0])
	}
}

func TestBuildKPHead_RewardHidesInternalType(t *testing.T) {
	content := models.ScenarioContent{
		Reward: &models.ScenarioReward{Name: "《航海星历表》手记", Type: "tome", MechanicsNote: "精读需1-4周，损失1d4点SAN"},
	}
	gctx := GameContext{Session: models.GameSession{Scenario: models.Scenario{
		Name:    "测试剧本",
		Content: models.JSONField[models.ScenarioContent]{Data: content},
	}}}

	scenario := buildKPHead(gctx, "system")[1].Content

	if strings.Contains(scenario, "[tome]") {
		t.Errorf("scenario prompt must not present the raw [tome] tag to the Director:\n%s", scenario)
	}
	if !strings.Contains(scenario, "《航海星历表》手记（神话典籍）") {
		t.Errorf("scenario prompt should show the localized reward type, got:\n%s", scenario)
	}
}

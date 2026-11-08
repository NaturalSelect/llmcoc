package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/llmcoc/server/internal/models"
)

func eventTestPlayers() []models.SessionPlayer {
	return []models.SessionPlayer{{
		UserID: 1,
		CharacterCard: models.CharacterCard{
			Name:      "甲",
			Stats:     models.JSONField[models.CharacterStats]{Data: models.CharacterStats{HP: 10, MaxHP: 12, SAN: 50, MaxSAN: 99, MP: 8, MaxMP: 8, POW: 40}},
			Skills:    models.JSONField[map[string]int]{Data: map[string]int{"侦查": 40}},
			Inventory: models.JSONField[[]string]{Data: []string{"手电筒", "子弹(x6)"}},
		},
	}}
}

func eventTestActx(players []models.SessionPlayer) ActionContext {
	var npcs []models.SessionNPC
	return ActionContext{
		Ctx:      context.Background(),
		GCtx:     &GameContext{Session: models.GameSession{ID: 1, TurnRound: 1, Players: players}},
		TempNPCs: &npcs,
	}
}

func onlyResultEvents(t *testing.T, res []ToolResult) []models.GameEvent {
	t.Helper()
	if len(res) != 1 {
		t.Fatalf("results = %+v, want one", res)
	}
	return res[0].Events
}

func TestCardStatEvents_ClampedHPAndWoundTransitions(t *testing.T) {
	before := eventTestPlayers()[0].CharacterCard
	after := before
	s := after.Stats.Data
	s.HP = 0
	after.Stats.Data = s
	after.WoundState = "dying"
	after.IsUnconscious = true

	evs := cardStatEvents(before, after)
	if len(evs) != 2 {
		t.Fatalf("events = %+v, want hp + wound (dying implies unconscious)", evs)
	}
	if evs[0].Field != "hp" || *evs[0].From != 10 || *evs[0].To != 0 {
		t.Errorf("hp event = %+v", evs[0])
	}
	if evs[1].Type != "wound" || evs[1].State != "dying" {
		t.Errorf("wound event = %+v", evs[1])
	}

	woke := after
	woke.IsUnconscious = false
	woke.WoundState = "major"
	evs = cardStatEvents(after, woke)
	if len(evs) != 2 || evs[0].State != "major" || evs[1].State != "conscious" {
		t.Errorf("recovery events = %+v", evs)
	}
}

func TestListDiffEvents_AddRemoveAndQuantityChange(t *testing.T) {
	before := []listEntry{{Key: "子弹", Label: "子弹(x6)"}, {Key: "手电筒", Label: "手电筒"}}
	after := []listEntry{{Key: "子弹", Label: "子弹(x5)"}, {Key: "钥匙", Label: "钥匙"}}
	evs := listDiffEvents("甲", "inventory", before, after)
	if len(evs) != 3 {
		t.Fatalf("events = %+v", evs)
	}
	if evs[0].Op != "change" || evs[0].Name != "子弹" || evs[0].Detail != "子弹(x6) → 子弹(x5)" {
		t.Errorf("change event = %+v", evs[0])
	}
	if evs[1].Op != "add" || evs[1].Name != "钥匙" {
		t.Errorf("add event = %+v", evs[1])
	}
	if evs[2].Op != "remove" || evs[2].Name != "手电筒" {
		t.Errorf("remove event = %+v", evs[2])
	}
}

func TestRollDiceEvents_HiddenCarriesNothingAndNPCNameDropped(t *testing.T) {
	players := eventTestPlayers()
	actx := eventTestActx(players)

	hidden := onlyResultEvents(t, rollDiceAction{}.Execute(ToolCall{Dice: &DiceCheck{Character: "甲", Hidden: true, What: "聆听", DiceExpr: "1d100"}}, actx))
	if len(hidden) != 1 || hidden[0] != (models.GameEvent{Type: "dice", Hidden: true}) {
		t.Errorf("hidden dice events = %+v, want only type+hidden", hidden)
	}
	npc := onlyResultEvents(t, rollDiceAction{}.Execute(ToolCall{Dice: &DiceCheck{Character: "管家", What: "潜行", DiceExpr: "1d100"}}, actx))
	if len(npc) != 1 || npc[0].Character != "" || npc[0].Skill != "潜行" || npc[0].Roll == 0 {
		t.Errorf("npc dice events = %+v, want no character name", npc)
	}
	bad := onlyResultEvents(t, rollDiceAction{}.Execute(ToolCall{Dice: &DiceCheck{Character: "甲", What: "x", DiceExpr: "abc"}}, actx))
	if len(bad) != 0 {
		t.Errorf("unsupported expression produced events: %+v", bad)
	}
}

func TestUpdateCharactersEvents_InvestigatorOnly(t *testing.T) {
	initAgentTestDB(t)
	players := eventTestPlayers()
	actx := eventTestActx(players)

	evs := onlyResultEvents(t, updateCharactersAction{}.Execute(ToolCall{Changes: []string{"HP -30(甲)", "SAN -3(甲)"}}, actx))
	var fields []string
	for _, ev := range evs {
		fields = append(fields, ev.Type+":"+ev.Field+ev.State)
	}
	got := strings.Join(fields, ",")
	if got != "stat:hp,wound:dead,stat:san" {
		t.Errorf("events = %s, want stat:hp,wound:dead,stat:san", got)
	}
	if *evs[0].To != 0 {
		t.Errorf("hp should clamp to 0, got %d", *evs[0].To)
	}

	npc := onlyResultEvents(t, updateCharactersAction{}.Execute(ToolCall{Changes: []string{"HP -3(管家)"}}, actx))
	if len(npc) != 0 {
		t.Errorf("npc update produced events: %+v", npc)
	}
}

func TestInventoryAndOtherListEvents(t *testing.T) {
	initAgentTestDB(t)
	players := eventTestPlayers()
	actx := eventTestActx(players)

	evs := onlyResultEvents(t, manageInventoryAction{}.Execute(ToolCall{CharacterName: "甲", Operate: "remove", Item: "子弹"}, actx))
	if len(evs) != 1 || evs[0].Op != "change" || evs[0].Detail != "子弹(x6) → 子弹(x5)" {
		t.Errorf("inventory decrement events = %+v", evs)
	}
	evs = onlyResultEvents(t, manageInventoryAction{}.Execute(ToolCall{CharacterName: "甲", Operate: "add", ItemName: "钥匙"}, actx))
	if len(evs) != 1 || evs[0].Op != "add" || evs[0].Name != "钥匙" || evs[0].Category != "inventory" {
		t.Errorf("inventory add events = %+v", evs)
	}
	evs = onlyResultEvents(t, manageInventoryAction{}.Execute(ToolCall{CharacterName: "管家", Operate: "add", ItemName: "钥匙"}, actx))
	if len(evs) != 0 {
		t.Errorf("unknown character produced events: %+v", evs)
	}

	evs = onlyResultEvents(t, manageSpellAction{}.Execute(ToolCall{CharacterName: "甲", Operate: "add", Spell: "联络食尸鬼"}, actx))
	if len(evs) != 1 || evs[0].Category != "spell" || evs[0].Op != "add" {
		t.Errorf("spell events = %+v", evs)
	}
	evs = onlyResultEvents(t, recordMonsterAction{}.Execute(ToolCall{CharacterName: "甲", Operate: "add", Monster: "深潜者"}, actx))
	if len(evs) != 1 || evs[0].Category != "monster" || evs[0].Name != "深潜者" {
		t.Errorf("monster events = %+v", evs)
	}
	evs = onlyResultEvents(t, manageAssetAction{}.Execute(ToolCall{CharacterName: "甲", Operate: "add", Asset: &models.Asset{Name: "旧宅", Category: "房产"}}, actx))
	if len(evs) != 1 || evs[0].Category != "asset" || evs[0].Name != "旧宅(房产)" {
		t.Errorf("asset events = %+v", evs)
	}
	evs = onlyResultEvents(t, manageRelationAction{}.Execute(ToolCall{CharacterName: "甲", Operate: "add", Relation: &models.SocialRelation{Name: "老约翰", Relationship: "线人"}}, actx))
	if len(evs) != 1 || evs[0].Category != "relation" || evs[0].Name != "老约翰(线人)" {
		t.Errorf("relation events = %+v", evs)
	}
	evs = onlyResultEvents(t, manageRelationAction{}.Execute(ToolCall{CharacterName: "甲", Operate: "remove", Relation: &models.SocialRelation{Name: "老约翰"}}, actx))
	if len(evs) != 1 || evs[0].Op != "remove" {
		t.Errorf("relation remove events = %+v", evs)
	}
}

func TestSceneAndMadnessEvents(t *testing.T) {
	initAgentTestDB(t)
	players := eventTestPlayers()
	actx := eventTestActx(players)
	advanced := false
	actx.TimeAdvancedInTurn = &advanced

	evs := onlyResultEvents(t, updateLocationAction{}.Execute(ToolCall{CharacterName: "甲", NewLocation: "地下室"}, actx))
	if len(evs) != 1 || evs[0].Type != "location" || evs[0].Location != "地下室" {
		t.Errorf("location events = %+v", evs)
	}
	if evs := onlyResultEvents(t, updateLocationAction{}.Execute(ToolCall{CharacterName: "管家", NewLocation: "地下室"}, actx)); len(evs) != 0 {
		t.Errorf("unknown character location produced events: %+v", evs)
	}
	evs = onlyResultEvents(t, updateArmorAction{}.Execute(ToolCall{CharacterName: "甲", ArmorValue: 2}, actx))
	if len(evs) != 1 || evs[0].Field != "armor" || *evs[0].From != 0 || *evs[0].To != 2 {
		t.Errorf("armor events = %+v", evs)
	}
	evs = onlyResultEvents(t, advanceTimeAction{}.Execute(ToolCall{TimeRounds: 2, TimeReason: "秘密仪式开始"}, actx))
	if len(evs) != 1 || evs[0].Type != "time" || !strings.HasPrefix(evs[0].Time, "第1天 01:00") || strings.Contains(evs[0].Time, "仪式") {
		t.Errorf("time events = %+v", evs)
	}
	evs = onlyResultEvents(t, manageMadnessAction{}.Execute(ToolCall{CharacterName: "甲", Operate: "trigger", MadnessType: "temporary"}, actx))
	if len(evs) != 1 || evs[0].Type != "madness" || evs[0].State != "temporary" || evs[0].Symptom == "" {
		t.Errorf("madness events = %+v", evs)
	}
	evs = onlyResultEvents(t, manageMadnessAction{}.Execute(ToolCall{CharacterName: "甲", Operate: "clear"}, actx))
	if len(evs) != 1 || evs[0].State != "none" {
		t.Errorf("madness clear events = %+v", evs)
	}
}

func TestEventMessagesRenderOnlyPublicDiceForLLM(t *testing.T) {
	from, to := 10, 7
	public := models.Message{Role: models.MessageRoleAssistant, Kind: models.MessageKindEvent, Meta: models.JSONField[*models.MessageMeta]{Data: &models.MessageMeta{Events: []models.GameEvent{
		{Type: "dice", Character: "甲", Skill: "侦查", Roll: 45, Level: "困难"},
		{Type: "dice", Hidden: true},
		{Type: "stat", Character: "甲", Field: "hp", From: &from, To: &to},
	}}}}
	hiddenOnly := models.Message{Role: models.MessageRoleAssistant, Kind: models.MessageKindEvent, Meta: models.JSONField[*models.MessageMeta]{Data: &models.MessageMeta{Events: []models.GameEvent{
		{Type: "dice", Hidden: true},
		{Type: "stat", Character: "甲", Field: "hp", From: &from, To: &to},
	}}}}
	log := BuildSessionChatLog([]models.Message{public, hiddenOnly})
	if log != "[骰子]: 甲 侦查鉴定: 45 难度:困难\n" {
		t.Errorf("chat log = %q", log)
	}
}

func TestLoadNPCTableLogIncludesPublicDiceEvents(t *testing.T) {
	initAgentTestDB(t)
	const sid = 640
	seedTableMsg(t, models.Message{SessionID: sid, Role: models.MessageRoleAssistant, Kind: models.MessageKindEvent, Meta: models.JSONField[*models.MessageMeta]{Data: &models.MessageMeta{Events: []models.GameEvent{{Type: "dice", Hidden: true}}}}})
	seedTableMsg(t, models.Message{SessionID: sid, Role: models.MessageRoleAssistant, Kind: models.MessageKindEvent, Meta: models.JSONField[*models.MessageMeta]{Data: &models.MessageMeta{Events: []models.GameEvent{{Type: "dice", Skill: "力量", Roll: 33}}}}})
	last := seedTableMsg(t, models.Message{SessionID: sid, Role: models.MessageRoleAssistant, Kind: models.MessageKindKP, Content: "门开了。"})

	log, seen := loadNPCTableLog(sid, 0)
	if seen != last.ID {
		t.Errorf("seen = %d, want %d", seen, last.ID)
	}
	if !strings.Contains(log, "[骰子]: 力量鉴定: 33") || strings.Count(log, "[骰子]") != 1 {
		t.Errorf("table log = %q, want only the public dice line", log)
	}
}

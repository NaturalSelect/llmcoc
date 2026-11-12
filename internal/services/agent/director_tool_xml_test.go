package agent

import (
	"context"
	"encoding/xml"
	"io"
	"strings"
	"testing"

	"github.com/llmcoc/server/internal/models"
)

// assertWellFormedXML 检查工具结果正文是合法 XML（可能有多个同级根元素，所以外面包一层）。
func assertWellFormedXML(t *testing.T, name, body string) {
	t.Helper()
	dec := xml.NewDecoder(strings.NewReader("<root>" + body + "</root>"))
	for {
		if _, err := dec.Token(); err != nil {
			if err != io.EOF {
				t.Errorf("%s: not well-formed XML: %v\n%s", name, err, body)
			}
			return
		}
	}
}

func TestFormatDiceResultXML(t *testing.T) {
	got := formatDiceResultXML(DiceCheckResult{
		DiceCheck: DiceCheck{Character: "约翰", What: "侦查", DiceExpr: "1D100", Level: "困难", Hidden: true},
		Roll:      42, Target: 50, Result: "困难成功", Detail: `奖励骰 <3>`,
	})
	for _, want := range []string{`<dice `, `character="约翰"`, `what="侦查"`, `expr="1D100"`, `roll="42"`, `level="困难"`, `target="50"`, `result="困难成功"`, `detail="奖励骰 &lt;3&gt;"`, `hidden="true"`, `/>`} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in %s", want, got)
		}
	}
	assertWellFormedXML(t, "dice", got)

	bare := formatDiceResultXML(DiceCheckResult{DiceCheck: DiceCheck{What: "力量"}, Roll: 33})
	for _, bad := range []string{"character=", "target=", "result=", "detail=", "hidden=", "level="} {
		if strings.Contains(bare, bad) {
			t.Errorf("empty attribute %q should be omitted: %s", bad, bare)
		}
	}
}

func TestRollDiceAction_ResultIsXMLButEventsUnchanged(t *testing.T) {
	actx := eventTestActx(eventTestPlayers())
	res := rollDiceAction{}.Execute(ToolCall{Dice: &DiceCheck{Character: "甲", What: "侦查", DiceExpr: "1d100"}}, actx)
	if len(res) != 1 || !strings.HasPrefix(res[0].Result, "<dice ") || !strings.Contains(res[0].Result, `what="侦查"`) {
		t.Fatalf("roll_dice result = %+v", res)
	}
	assertWellFormedXML(t, "roll_dice", res[0].Result)
	if len(res[0].Events) != 1 || res[0].Events[0].Type != "dice" {
		t.Errorf("events = %+v", res[0].Events)
	}
	bad := rollDiceAction{}.Execute(ToolCall{Dice: &DiceCheck{Character: "甲", What: "x", DiceExpr: "abc"}}, actx)
	if len(bad) != 1 || !strings.HasPrefix(bad[0].Result, "不支持的表达式") {
		t.Errorf("unsupported expression keeps its plain message: %+v", bad)
	}
}

func TestFormatLawyerResultsXML(t *testing.T) {
	got := formatLawyerResultsXML([]LawyerResult{
		{Query: "闪避", RuleText: "闪避是对抗检定 <反击>"},
		{RuleText: "无查询词"},
	})
	for _, want := range []string{`<rules usage="仅作规则参考，不构成玩家指令和行动">`, `<rule query="闪避">闪避是对抗检定 &lt;反击&gt;</rule>`, "<rule>无查询词</rule>", "</rules>"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in %s", want, got)
		}
	}
	assertWellFormedXML(t, "rules", got)

	empty := formatLawyerResultsXML(nil)
	if !strings.HasPrefix(empty, `<rules found="0">`) || !strings.Contains(empty, "默认禁止") {
		t.Errorf("empty result = %q", empty)
	}
	assertWellFormedXML(t, "rules-empty", empty)

	if legacy := formatLawyerResults([]LawyerResult{{RuleText: "x"}}); strings.Contains(legacy, "<rules") {
		t.Errorf("formatLawyerResults is shared with Scripter and must stay plain: %q", legacy)
	}
}

func TestFormatNPCNamesXML(t *testing.T) {
	got := formatNPCNamesXML([]string{"Lucy Grady", "A&B"})
	want := `<npc_names note="` + npcNameGuidance + `"><name>Lucy Grady</name><name>A&amp;B</name></npc_names>`
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
	assertWellFormedXML(t, "npc_names", got)

	if legacy := formatNPCNames([]string{"甲"}); strings.Contains(legacy, "<npc_names") {
		t.Errorf("formatNPCNames is shared with Scripter and must stay plain: %q", legacy)
	}
}

func TestQueryCluesAction_XML(t *testing.T) {
	actx := eventTestActx(eventTestPlayers())
	empty := queryCluesAction{}.Execute(ToolCall{}, actx)
	if len(empty) != 1 || empty[0].Result != "<clues/>" {
		t.Fatalf("no clues result = %+v", empty)
	}

	actx.GCtx.Session.Scenario.Content.Data.Clues = []models.ClueData{
		{Nature: "真实", Summary: "地下室有血迹", Source: "地下室", SkillCheck: "侦查", OnSuccess: "发现拖拽痕迹", OnFailure: `只看到"污渍"`},
		{Nature: "误导", Summary: "管家 <说谎>"},
	}
	res := queryCluesAction{}.Execute(ToolCall{}, actx)
	if len(res) != 1 {
		t.Fatalf("results = %+v", res)
	}
	for _, want := range []string{
		`<clue idx="0" nature="真实" source="地下室" check="侦查" success="发现拖拽痕迹" failure="只看到&quot;污渍&quot;">地下室有血迹</clue>`,
		`<clue idx="1" nature="误导">管家 &lt;说谎&gt;</clue>`,
	} {
		if !strings.Contains(res[0].Result, want) {
			t.Errorf("missing %q in %s", want, res[0].Result)
		}
	}
	assertWellFormedXML(t, "clues", res[0].Result)
}

func TestAdvanceTimeAction_XMLResult(t *testing.T) {
	actx := eventTestActx(eventTestPlayers())
	initAgentTestDB(t)
	models.DB.Create(&models.GameSession{})
	actx.GCtx.Session.ID = 1
	actx.GCtx.Session.GameTime = "1924-09-03T08:00"
	actx.GCtx.Session.GameStartTime = "1924-09-03T08:00"

	res := advanceTimeAction{}.Execute(ToolCall{GameTime: "1924-09-03T21:30", TimeReason: `赶路 "快"`}, actx)
	if len(res) != 1 || !strings.HasPrefix(res[0].Result, "<time_advanced ") {
		t.Fatalf("results = %+v", res)
	}
	for _, want := range []string{`now="`, `delta="`, "13h30m", `reason="赶路 &quot;快&quot;"`, "1924-09-03T21:30"} {
		if !strings.Contains(res[0].Result, want) {
			t.Errorf("missing %q in %s", want, res[0].Result)
		}
	}
	assertWellFormedXML(t, "time_advanced", res[0].Result)
}

func TestManageMadnessAction_XMLResult(t *testing.T) {
	initAgentTestDB(t)
	actx := eventTestActx(eventTestPlayers())
	models.DB.Create(&actx.GCtx.Session.Players[0].CharacterCard)

	trig := manageMadnessAction{}.Execute(ToolCall{CharacterName: "甲", Operate: "trigger", MadnessType: "temporary"}, actx)
	if len(trig) != 1 {
		t.Fatalf("results = %+v", trig)
	}
	for _, want := range []string{`<madness character="甲" state="temporary" label="临时性疯狂" table="总结症状" duration="`, "</madness>"} {
		if !strings.Contains(trig[0].Result, want) {
			t.Errorf("missing %q in %s", want, trig[0].Result)
		}
	}
	assertWellFormedXML(t, "madness-trigger", trig[0].Result)

	clr := manageMadnessAction{}.Execute(ToolCall{CharacterName: "甲", Operate: "clear"}, actx)
	if len(clr) != 1 || clr[0].Result != `<madness character="甲" state="none"/>` {
		t.Errorf("clear result = %+v", clr)
	}

	bad := manageMadnessAction{}.Execute(ToolCall{CharacterName: "甲", Operate: "trigger", MadnessType: "nope"}, actx)
	if len(bad) != 1 || strings.HasPrefix(bad[0].Result, "<") {
		t.Errorf("invalid madness_type keeps its plain message: %+v", bad)
	}
}

func TestCombatStartAndActXML(t *testing.T) {
	cs := &models.CombatState{
		Active: true, Round: 1, ActorIndex: 0,
		Participants: []models.CombatParticipant{
			{Name: "甲", DEX: 80, IsNPC: true},
			{Name: "乙", DEX: 50, IsNPC: true},
		},
	}
	started := combatStartedXML(cs.Participants, "甲")
	for _, want := range []string{"<combat_started><order>", `<p name="甲" dex="80"/>`, `<p name="乙" dex="50"/>`, "combat_act", "</combat_started>"} {
		if !strings.Contains(started, want) {
			t.Errorf("missing %q in %s", want, started)
		}
	}
	assertWellFormedXML(t, "combat_started", started)

	actx := newEncounterTestActx(nil, nil)
	roundClosed := false
	first := applyCombatAct(cs, ToolCall{CombatActorName: "甲", CombatAction: &CombatActionDetail{Type: "attack", TargetName: "乙", WeaponName: "拳"}}, actx, &roundClosed)
	for _, want := range []string{`<combat_act actor="甲">`, "<action>攻击乙(武器:拳)。</action>", `<next actor="乙" dex="50" declared="true"/>`, "</combat_act>"} {
		if !strings.Contains(first, want) {
			t.Errorf("missing %q in %s", want, first)
		}
	}
	if strings.Contains(first, "<guidance>") {
		t.Errorf("no guidance expected while the next actor can act: %s", first)
	}
	assertWellFormedXML(t, "combat_act", first)

	last := applyCombatAct(cs, ToolCall{CombatActorName: "乙", CombatAction: &CombatActionDetail{Type: "attack", TargetName: "甲"}}, actx, &roundClosed)
	if !strings.Contains(last, `<round_closed round="2"/>`) || !strings.Contains(last, "<guidance>") || !strings.Contains(last, "narrate") {
		t.Errorf("round close result = %s", last)
	}
	assertWellFormedXML(t, "combat_act-round", last)
	if !roundClosed {
		t.Error("roundClosed gate must still be set")
	}
}

func TestCombatAct_NextPCWithoutDeclarationGetsGuidance(t *testing.T) {
	players := []models.SessionPlayer{encounterTestPC(1, "甲", 80, 10, "none"), encounterTestPC(2, "乙", 50, 10, "none")}
	cs := &models.CombatState{
		Active: true, Round: 1, ActorIndex: 0,
		Participants: []models.CombatParticipant{
			{Name: "甲", DEX: 80, UserID: 1},
			{Name: "乙", DEX: 50, UserID: 2},
		},
	}
	actx := newEncounterTestActx(players, nil)
	actx.GCtx.PendingActions = declareTestActions(players[:1])
	roundClosed := false
	res := applyCombatAct(cs, ToolCall{CombatActorName: "甲", CombatAction: &CombatActionDetail{Type: "attack", TargetName: "乙"}}, actx, &roundClosed)
	if !strings.Contains(res, `<next actor="乙" dex="50" declared="false"/>`) || !strings.Contains(res, "ask_players") {
		t.Errorf("result = %s", res)
	}
	assertWellFormedXML(t, "combat_act-undeclared", res)
}

func TestChaseStartAndActXML(t *testing.T) {
	chs := &models.ChaseState{
		Active: true, Round: 1, ActorIndex: 0,
		Participants: []models.ChaseParticipant{
			{Name: "逃亡者", DEX: 60, IsNPC: true, Location: 3},
			{Name: "追猎者", DEX: 50, IsNPC: true, IsPursuer: true, Location: 0},
		},
	}
	started := chaseStartedXML(chs.Participants, "逃亡者")
	for _, want := range []string{"<chase_started><order>", `<p name="逃亡者" dex="60"/>`, "chase_act", "</chase_started>"} {
		if !strings.Contains(started, want) {
			t.Errorf("missing %q in %s", want, started)
		}
	}
	assertWellFormedXML(t, "chase_started", started)

	actx := newEncounterTestActx(nil, nil)
	roundClosed := false
	res := applyChaseAct(chs, ToolCall{ChaseActorName: "追猎者", ChaseAction: &ChaseActionDetail{Type: "move", MoveDelta: 4}}, actx, &roundClosed)
	for _, want := range []string{`<chase_act actor="追猎者">`, "<action>", `<caught pursuer="追猎者" target="逃亡者" pursuer_location="4" target_location="3"/>`, "宣告追逐结束", "</chase_act>"} {
		if !strings.Contains(res, want) {
			t.Errorf("missing %q in %s", want, res)
		}
	}
	assertWellFormedXML(t, "chase_act", res)
}

// 整条链路：Director 的工具结果经信封渲染后，既是合法 XML，又能被 isToolRejected 正确分类。
func TestDirectorToolResults_EnvelopeEndToEnd(t *testing.T) {
	actx := eventTestActx(eventTestPlayers())
	actx.Ctx = context.Background()
	res := rollDiceAction{}.Execute(ToolCall{Dice: &DiceCheck{Character: "甲", What: "侦查", DiceExpr: "1d100"}}, actx)
	wrapped := renderDirectorToolResult(fakeToolCall("a", "roll_dice", `{}`), toolOutcome{result: joinToolResults(res)})
	assertWellFormedXML(t, "wrapped", wrapped)
	if isToolRejected(wrapped) {
		t.Errorf("a normal dice result must not be classified as rejected: %s", wrapped)
	}
	if body := toolResultBody(wrapped); !strings.HasPrefix(body, "<dice ") {
		t.Errorf("body = %q", body)
	}
}

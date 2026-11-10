package agent

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/llmcoc/server/internal/models"
)

func newBackstoryTestHandle(prov *sequentialFakeProvider) agentHandle {
	return agentHandle{
		provider: prov,
		config:   &models.AgentConfig{Role: models.AgentRoleWriter, IsActive: true},
		enabled:  true,
	}
}

func backstoryTestCard(name, backstory string, hp int) models.CharacterCard {
	card := models.CharacterCard{Name: name, Occupation: "记者", Backstory: backstory}
	card.Stats.Data = models.CharacterStats{HP: hp, MaxHP: 10}
	return card
}

func backstoryOutput(pairs ...string) backstoryEvolutionLLMOutput {
	var out backstoryEvolutionLLMOutput
	for i := 0; i+1 < len(pairs); i += 2 {
		out.Characters = append(out.Characters, struct {
			CharacterName string `json:"character_name"`
			NewBackstory  string `json:"new_backstory"`
		}{pairs[i], pairs[i+1]})
	}
	return out
}

func TestFilterBackstories(t *testing.T) {
	old := map[string]string{"甲": "旧经历", "乙": "乙的旧经历"}
	tooLong := strings.Repeat("长", backstoryMaxRunes+1)
	exactly := strings.Repeat("长", backstoryMaxRunes)

	tests := []struct {
		name string
		out  backstoryEvolutionLLMOutput
		want map[string]string
	}{
		{"正常改写", backstoryOutput("甲", "旧经历。他在雾中见过那座灯塔。"), map[string]string{"甲": "旧经历。他在雾中见过那座灯塔。"}},
		{"首尾空白被去掉", backstoryOutput("甲", "  新经历 \n"), map[string]string{"甲": "新经历"}},
		{"内容未变不算改写", backstoryOutput("甲", "旧经历", "乙", " 乙的旧经历 "), map[string]string{}},
		{"空串被丢弃", backstoryOutput("甲", "   "), map[string]string{}},
		{"超长被丢弃", backstoryOutput("甲", tooLong), map[string]string{}},
		{"刚好到上限保留", backstoryOutput("甲", exactly), map[string]string{"甲": exactly}},
		{"名字不是存活调查员被丢弃", backstoryOutput("丙", "凭空出现的经历"), map[string]string{}},
		{"部分有效", backstoryOutput("甲", "甲的新经历", "丙", "无效", "乙", ""), map[string]string{"甲": "甲的新经历"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := filterBackstories(tt.out, old)
			if len(got) != len(tt.want) {
				t.Fatalf("got %v, want %v", got, tt.want)
			}
			for name, text := range tt.want {
				if got[name] != text {
					t.Errorf("%s = %q, want %q", name, got[name], text)
				}
			}
		})
	}
}

func TestEvolveBackstories_ReturnsRewriteForAliveOnly(t *testing.T) {
	prov := &sequentialFakeProvider{jsonResponses: []string{
		`{"characters":[{"character_name":"活人","new_backstory":"旧经历。他再也不敢夜里走过码头。"},{"character_name":"死者","new_backstory":"不该出现"}]}`,
	}}
	session := &models.GameSession{ID: 7, Players: []models.SessionPlayer{
		{CharacterCard: backstoryTestCard("活人", "旧经历。", 6)},
		{CharacterCard: backstoryTestCard("死者", "死者的经历", 0)},
	}}
	msgs := []models.Message{{Role: models.MessageRoleAssistant, Content: "众人逃出了码头仓库。"}}

	got := evolveBackstories(context.Background(), newBackstoryTestHandle(prov), session, msgs)

	if len(got) != 1 || got["活人"] != "旧经历。他再也不敢夜里走过码头。" {
		t.Fatalf("got %v, want only 活人 rewritten", got)
	}
	if len(prov.recordedMessages) != 1 {
		t.Fatalf("LLM called %d times, want exactly 1 for all investigators", len(prov.recordedMessages))
	}
	var prompt strings.Builder
	for _, m := range prov.recordedMessages[0] {
		prompt.WriteString(m.Content)
		prompt.WriteString("\n")
	}
	if !strings.Contains(prompt.String(), "活人") || !strings.Contains(prompt.String(), "众人逃出了码头仓库") {
		t.Errorf("prompt should carry the survivor and the chat log, got:\n%s", prompt.String())
	}
	if strings.Contains(prompt.String(), "死者的经历") {
		t.Error("dead investigator's backstory must not be sent to the LLM")
	}
}

func TestEvolveBackstories_NoSurvivorSkipsLLM(t *testing.T) {
	prov := &sequentialFakeProvider{}
	session := &models.GameSession{ID: 8, Players: []models.SessionPlayer{
		{CharacterCard: backstoryTestCard("死者", "经历", 0)},
	}}

	if got := evolveBackstories(context.Background(), newBackstoryTestHandle(prov), session, nil); got != nil {
		t.Fatalf("got %v, want nil", got)
	}
	if len(prov.recordedMessages) != 0 {
		t.Errorf("LLM called %d times, want 0 when nobody survived", len(prov.recordedMessages))
	}
}

func TestEvolveBackstories_InvalidJSONKeepsBackstory(t *testing.T) {
	// NOTE: RepairJSON 需要真实 Provider，测试库没有 AgentConfig，修复必然失败，整体应返回 nil。
	initEndSessionTestDB(t)
	prov := &sequentialFakeProvider{jsonResponses: []string{`这不是 JSON`}}
	session := &models.GameSession{ID: 9, Players: []models.SessionPlayer{
		{CharacterCard: backstoryTestCard("活人", "旧经历", 6)},
	}}

	if got := evolveBackstories(context.Background(), newBackstoryTestHandle(prov), session, nil); len(got) != 0 {
		t.Fatalf("got %v, want no rewrite on unparsable output", got)
	}
}

// stubBackstoryEvolver 替换结算里的背景演变调用，返回调用计数。
func stubBackstoryEvolver(t *testing.T, result map[string]string) *int {
	t.Helper()
	calls := 0
	prev := backstoryEvolver
	backstoryEvolver = func(context.Context, *models.GameSession, []models.Message) map[string]string {
		calls++
		return result
	}
	t.Cleanup(func() { backstoryEvolver = prev })
	return &calls
}

func eventsOfType(events []models.GameEvent, typ string) []models.GameEvent {
	var out []models.GameEvent
	for _, e := range events {
		if e.Type == typ {
			out = append(out, e)
		}
	}
	return out
}

func newBackstoryEndSessionFixture(t *testing.T, sessionID uint, name string) (*models.GameSession, *models.CharacterCard) {
	t.Helper()
	uid := newEndSessionUser(t, name+"_user")
	card := newEndSessionCard(t, name, models.CharacterStats{
		MaxHP: 10, HP: 10, MaxMP: 10, MP: 10, MaxSAN: 99, SAN: 50, POW: 50,
	}, nil)
	card.Backstory = "旧经历"
	if err := models.DB.Save(card).Error; err != nil {
		t.Fatalf("save card: %v", err)
	}
	return makeTestSession(sessionID, uid, *card), card
}

func TestRunEndSession_EvolveBackstory_On_WinRewritesAndEmitsEvent(t *testing.T) {
	initEndSessionTestDB(t)
	calls := stubBackstoryEvolver(t, map[string]string{"演变者": "旧经历。他从此不再相信旧神的传说。"})
	sess, card := newBackstoryEndSessionFixture(t, 60, "演变者")
	sess.EvolveBackstory = true

	events, err := RunEndSession(context.Background(), sess, nil, true, nil)
	if err != nil {
		t.Fatalf("RunEndSession: %v", err)
	}

	var updated models.CharacterCard
	models.DB.First(&updated, card.ID)
	if updated.Backstory != "旧经历。他从此不再相信旧神的传说。" {
		t.Errorf("backstory = %q, want rewritten", updated.Backstory)
	}
	got := eventsOfType(events, "backstory")
	if len(got) != 1 || got[0].Character != "演变者" || got[0].Detail != "旧经历。他从此不再相信旧神的传说。" {
		t.Errorf("backstory events = %+v, want one carrying the new text", got)
	}
	if *calls != 1 {
		t.Errorf("evolver called %d times, want 1", *calls)
	}
}

func TestRunEndSession_EvolveBackstory_Off_NeverCallsEvolver(t *testing.T) {
	initEndSessionTestDB(t)
	calls := stubBackstoryEvolver(t, map[string]string{"保守者": "不该写入"})
	sess, card := newBackstoryEndSessionFixture(t, 61, "保守者")

	events, err := RunEndSession(context.Background(), sess, nil, true, nil)
	if err != nil {
		t.Fatalf("RunEndSession: %v", err)
	}

	var updated models.CharacterCard
	models.DB.First(&updated, card.ID)
	if updated.Backstory != "旧经历" {
		t.Errorf("backstory = %q, want unchanged when the switch is off", updated.Backstory)
	}
	if len(eventsOfType(events, "backstory")) != 0 {
		t.Error("no backstory event expected when the switch is off")
	}
	if *calls != 0 {
		t.Errorf("evolver called %d times, want 0", *calls)
	}
}

func TestRunEndSession_EvolveBackstory_On_LossDoesNotEvolve(t *testing.T) {
	initEndSessionTestDB(t)
	calls := stubBackstoryEvolver(t, map[string]string{"失败者": "不该写入"})
	sess, card := newBackstoryEndSessionFixture(t, 62, "失败者")
	sess.EvolveBackstory = true

	events, err := RunEndSession(context.Background(), sess, nil, false, nil)
	if err != nil {
		t.Fatalf("RunEndSession: %v", err)
	}

	var updated models.CharacterCard
	models.DB.First(&updated, card.ID)
	if updated.Backstory != "旧经历" {
		t.Errorf("backstory = %q, want unchanged on a loss", updated.Backstory)
	}
	if len(eventsOfType(events, "backstory")) != 0 || *calls != 0 {
		t.Errorf("loss must not evolve: events=%v calls=%d", events, *calls)
	}
}

func TestRunEndSession_EvolveBackstory_On_DeadInvestigatorKeepsBackstory(t *testing.T) {
	initEndSessionTestDB(t)
	// NOTE: 即使演变结果里误带了死者，结算也不能改写她的经历。
	stubBackstoryEvolver(t, map[string]string{"殉职者": "不该写入"})
	uid := newEndSessionUser(t, "dead_user")
	card := newEndSessionCard(t, "殉职者", models.CharacterStats{
		MaxHP: 10, HP: 0, MaxMP: 10, MP: 5, MaxSAN: 99, SAN: 30, POW: 50,
	}, nil)
	card.Backstory = "旧经历"
	card.WoundState = "dead"
	if err := models.DB.Save(card).Error; err != nil {
		t.Fatalf("save card: %v", err)
	}
	sess := makeTestSession(63, uid, *card)
	sess.EvolveBackstory = true

	events, err := RunEndSession(context.Background(), sess, nil, true, nil)
	if err != nil {
		t.Fatalf("RunEndSession: %v", err)
	}

	var updated models.CharacterCard
	models.DB.First(&updated, card.ID)
	if updated.Backstory != "旧经历" {
		t.Errorf("dead investigator backstory = %q, want unchanged", updated.Backstory)
	}
	if len(eventsOfType(events, "backstory")) != 0 {
		t.Error("no backstory event expected for a dead investigator")
	}
}

func TestRunEndSession_EmitsCoinsEvent(t *testing.T) {
	initEndSessionTestDB(t)
	uid := newEndSessionUser(t, "coins_event_user")
	card := newEndSessionCard(t, "领奖者", models.CharacterStats{
		MaxHP: 10, HP: 10, MaxMP: 10, MP: 10, MaxSAN: 99, SAN: 50, POW: 50,
	}, nil)
	sess := makeTestSession(64, uid, *card)

	events, err := RunEndSession(context.Background(), sess, nil, true, []PlayerSettlement{{CharacterName: "领奖者", Score: intPtr(80)}})
	if err != nil {
		t.Fatalf("RunEndSession: %v", err)
	}

	coins := eventsOfType(events, "coins")
	if len(coins) != 1 || coins[0].Character != "领奖者" || coins[0].Amount != settlementBaseCoins+40 {
		t.Errorf("coins events = %+v, want one for 领奖者 with %d coins", coins, settlementBaseCoins+40)
	}
	if want := fmt.Sprintf("基础 %d + 评分奖励 40", settlementBaseCoins); len(coins) == 1 && coins[0].Detail != want {
		t.Errorf("coins detail = %q, want %q", coins[0].Detail, want)
	}
}

func TestRunEndSession_EmitsRestoreAndMadnessEvents(t *testing.T) {
	initEndSessionTestDB(t)
	uid := newEndSessionUser(t, "restore_event_user")
	card := newEndSessionCard(t, "负伤者", models.CharacterStats{
		MaxHP: 10, HP: 3, MaxMP: 8, MP: 1, MaxSAN: 99, SAN: 40, POW: 50,
	}, nil)
	card.MadnessState = "temporary"
	if err := models.DB.Save(card).Error; err != nil {
		t.Fatalf("save card: %v", err)
	}
	sess := makeTestSession(65, uid, *card)

	events, err := RunEndSession(context.Background(), sess, nil, false, nil)
	if err != nil {
		t.Fatalf("RunEndSession: %v", err)
	}

	fields := map[string]models.GameEvent{}
	for _, e := range eventsOfType(events, "stat") {
		fields[e.Field] = e
	}
	if e, ok := fields["hp"]; !ok || *e.From != 3 || *e.To != 10 {
		t.Errorf("hp event = %+v, want 3 -> 10", e)
	}
	if e, ok := fields["mp"]; !ok || *e.From != 1 || *e.To != 8 {
		t.Errorf("mp event = %+v, want 1 -> 8", e)
	}
	for _, f := range []string{"hp", "mp", "san"} {
		if e, ok := fields[f]; ok && e.Detail != "结算恢复" {
			t.Errorf("%s event detail = %q, want 结算恢复", f, e.Detail)
		}
	}
	if _, ok := fields["san"]; !ok {
		t.Errorf("san event missing, want a restore event with reason (SAN 40 recovers 3D6)")
	}
	madness := eventsOfType(events, "madness")
	if len(madness) != 1 || madness[0].Character != "负伤者" || madness[0].State != "none" {
		t.Errorf("madness events = %+v, want one clearing 负伤者", madness)
	}
}

func TestRunEndSession_NoMadnessEventWhenSane(t *testing.T) {
	initEndSessionTestDB(t)
	sess, _ := newBackstoryEndSessionFixture(t, 66, "清醒者")

	events, err := RunEndSession(context.Background(), sess, nil, false, nil)
	if err != nil {
		t.Fatalf("RunEndSession: %v", err)
	}
	if got := eventsOfType(events, "madness"); len(got) != 0 {
		t.Errorf("madness events = %+v, want none for a sane investigator", got)
	}
}

func TestRunEndSession_WinTrue_EmitsSkillGrowthEvent(t *testing.T) {
	initEndSessionTestDB(t)
	uid := newEndSessionUser(t, "skill_event_user")
	card := newEndSessionCard(t, "成长者", models.CharacterStats{
		MaxHP: 10, HP: 10, MaxMP: 10, MP: 10, MaxSAN: 99, SAN: 50, POW: 50,
	}, map[string]int{"侦查": 1})
	settlement := []PlayerSettlement{{CharacterName: "成长者", Score: intPtr(70), Skills: []string{"侦查"}}}

	for i := 0; i < 200; i++ {
		card.Skills.Data["侦查"] = 1
		models.DB.Save(card)
		sess := makeTestSession(uint(70+i), uid, *card)
		events, err := RunEndSession(context.Background(), sess, nil, true, settlement)
		if err != nil {
			t.Fatalf("RunEndSession: %v", err)
		}
		for _, e := range eventsOfType(events, "stat") {
			if e.Field != "skill" {
				continue
			}
			if e.Character != "成长者" || e.Skill != "侦查" || *e.From != 1 || *e.To < 2 || *e.To > 4 {
				t.Fatalf("skill event = %+v, want 成长者 侦查 1 -> 2..4", e)
			}
			if !strings.HasPrefix(e.Detail, "成长检定 骰值 ") {
				t.Fatalf("skill event detail = %q, want the growth roll recorded", e.Detail)
			}
			return
		}
	}
	t.Error("no skill growth event in 200 tries")
}

func TestRunEndSession_WinFalse_NoSkillGrowthEvent(t *testing.T) {
	initEndSessionTestDB(t)
	uid := newEndSessionUser(t, "no_skill_event_user")
	card := newEndSessionCard(t, "落败者", models.CharacterStats{
		MaxHP: 10, HP: 10, MaxMP: 10, MP: 10, MaxSAN: 99, SAN: 50, POW: 50,
	}, map[string]int{"侦查": 1})
	settlement := []PlayerSettlement{{CharacterName: "落败者", Score: intPtr(70), Skills: []string{"侦查"}}}

	for i := 0; i < 50; i++ {
		sess := makeTestSession(uint(300+i), uid, *card)
		events, err := RunEndSession(context.Background(), sess, nil, false, settlement)
		if err != nil {
			t.Fatalf("RunEndSession: %v", err)
		}
		for _, e := range eventsOfType(events, "stat") {
			if e.Field == "skill" {
				t.Fatalf("loss must not emit skill growth, got %+v", e)
			}
		}
	}
}

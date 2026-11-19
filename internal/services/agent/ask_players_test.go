// NOTE: ask_players_test.go 验证KP点名工具:名字解析、拒绝分支、批次规则与kp_floor渲染。
package agent

import (
	"strings"
	"testing"

	"github.com/llmcoc/server/internal/models"
	"github.com/llmcoc/server/internal/services/llm"
)

func askTestPlayers() []models.SessionPlayer {
	return []models.SessionPlayer{
		{UserID: 1, CharacterCard: models.CharacterCard{Name: "甲"}},
		{UserID: 2, CharacterCard: models.CharacterCard{Name: "乙"}},
		{UserID: 3, CharacterCard: models.CharacterCard{Name: "丙", WoundState: "dead"}},
	}
}

func TestAskPlayersResolvesNames(t *testing.T) {
	hasEnd := false
	var outbox []Output
	var ask *AskResult
	var combat *models.CombatState
	var chase *models.ChaseState
	actx := ActionContext{
		GCtx:   &GameContext{Session: models.GameSession{Players: askTestPlayers()}},
		HasEnd: &hasEnd,
		Outbox: &outbox,
		Ask:    &ask,
		Combat: &combat,
		Chase:  &chase,
	}

	results := askPlayersAction{}.Execute(ToolCall{Targets: []string{"乙"}, Question: "你怎么办？"}, actx)
	if len(results) != 1 || strings.HasPrefix(results[0].Result, "SYSTEM REJECT") {
		t.Fatalf("点名存活调查员应成功, got %+v", results)
	}
	if !hasEnd {
		t.Error("成功点名应置位HasEnd")
	}
	if ask == nil || len(ask.UserIDs) != 1 || ask.UserIDs[0] != 2 || ask.Names[0] != "乙" || ask.Question != "你怎么办？" {
		t.Errorf("Ask = %+v", ask)
	}
	if len(outbox) != 1 || outbox[0].Kind != OutputAsk || outbox[0].Text != "你怎么办？" || outbox[0].Ask == nil {
		t.Errorf("成功点名应产生一条ask输出, got %+v", outbox)
	}
	if outbox[0].Options != nil {
		t.Errorf("没给 options 时不应带推荐行动, got %v", outbox[0].Options)
	}
}

func TestAskPlayersCarriesNormalizedOptions(t *testing.T) {
	hasEnd := false
	var outbox []Output
	var ask *AskResult
	actx := ActionContext{
		GCtx:   &GameContext{Session: models.GameSession{Players: askTestPlayers()}},
		HasEnd: &hasEnd,
		Outbox: &outbox,
		Ask:    &ask,
	}
	askPlayersAction{}.Execute(ToolCall{
		Targets:  []string{"乙"},
		Question: "你怎么办？",
		Options:  []string{"我走进去", " ", "我走进去", "「谁在那里？」", "第三条被截断"},
	}, actx)
	if len(outbox) != 1 {
		t.Fatalf("outbox = %+v", outbox)
	}
	if got := outbox[0].Options; len(got) != 2 || got[0] != "我走进去" || got[1] != "「谁在那里？」" {
		t.Errorf("Options = %v, want 去空去重并截到2条", got)
	}
}

func TestAskPlayersRejectsMultipleTargets(t *testing.T) {
	cases := []struct {
		name    string
		targets []string
	}{
		{"两个存活角色", []string{"甲", "乙"}},
		{"两个存活角色顶序颠倒", []string{"乙", "甲"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			hasEnd := false
			var outbox []Output
			var ask *AskResult
			actx := ActionContext{
				GCtx:   &GameContext{Session: models.GameSession{Players: askTestPlayers()}},
				HasEnd: &hasEnd,
				Outbox: &outbox,
				Ask:    &ask,
			}
			results := askPlayersAction{}.Execute(ToolCall{Targets: tc.targets, Question: "各位怎么办？"}, actx)
			if len(results) != 1 || !strings.HasPrefix(results[0].Result, "SYSTEM REJECT") || !strings.Contains(results[0].Result, "一次只能点名一个人") {
				t.Fatalf("点名多人应被拒绝, got %+v", results)
			}
			if hasEnd || ask != nil || len(outbox) != 0 {
				t.Errorf("拒绝时不应产生副作用: hasEnd=%v ask=%+v outbox=%+v", hasEnd, ask, outbox)
			}
		})
	}
}

func TestAskPlayersDuplicateTargetCountsOnce(t *testing.T) {
	hasEnd := false
	var outbox []Output
	var ask *AskResult
	actx := ActionContext{
		GCtx:   &GameContext{Session: models.GameSession{Players: askTestPlayers()}},
		HasEnd: &hasEnd,
		Outbox: &outbox,
		Ask:    &ask,
	}
	results := askPlayersAction{}.Execute(ToolCall{Targets: []string{"甲", "甲"}, Question: "你怎么办？"}, actx)
	if strings.HasPrefix(results[0].Result, "SYSTEM REJECT") {
		t.Fatalf("重复的同一个人应视为一人, got %+v", results)
	}
	if ask == nil || len(ask.UserIDs) != 1 || ask.UserIDs[0] != 1 {
		t.Errorf("Ask = %+v", ask)
	}
}

func TestAskPlayersRejectsUnknownAndDead(t *testing.T) {
	cases := []struct {
		name    string
		targets []string
		want    string
	}{
		{"未知角色", []string{"不存在"}, "找不到调查员"},
		{"已死角色", []string{"丙"}, "已死亡"},
		{"部分错误整批拒绝", []string{"甲", "不存在"}, "找不到调查员"},
		{"all不再支持", []string{"all"}, "找不到调查员"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			hasEnd := false
			var outbox []Output
			var ask *AskResult
			var combat *models.CombatState
			var chase *models.ChaseState
			actx := ActionContext{
				GCtx:   &GameContext{Session: models.GameSession{Players: askTestPlayers()}},
				HasEnd: &hasEnd,
				Outbox: &outbox,
				Ask:    &ask,
				Combat: &combat,
				Chase:  &chase,
			}
			results := askPlayersAction{}.Execute(ToolCall{Targets: tc.targets, Question: "怎么办？"}, actx)
			if len(results) != 1 || !strings.HasPrefix(results[0].Result, "SYSTEM REJECT") || !strings.Contains(results[0].Result, tc.want) {
				t.Fatalf("应拒绝并包含%q, got %+v", tc.want, results)
			}
			if hasEnd || ask != nil || len(outbox) != 0 {
				t.Errorf("拒绝时不应产生副作用: hasEnd=%v ask=%+v outbox=%+v", hasEnd, ask, outbox)
			}
		})
	}
}

func TestAskPlayersRejectsEmptyQuestion(t *testing.T) {
	hasEnd := false
	var outbox []Output
	var ask *AskResult
	var combat *models.CombatState
	var chase *models.ChaseState
	actx := ActionContext{
		GCtx:   &GameContext{Session: models.GameSession{Players: askTestPlayers()}},
		HasEnd: &hasEnd,
		Outbox: &outbox,
		Ask:    &ask,
		Combat: &combat,
		Chase:  &chase,
	}
	results := askPlayersAction{}.Execute(ToolCall{Targets: []string{"甲"}, Question: "  "}, actx)
	if len(results) != 1 || !strings.HasPrefix(results[0].Result, "SYSTEM REJECT") || hasEnd {
		t.Fatalf("空问题应被拒绝, got %+v hasEnd=%v", results, hasEnd)
	}
}

func TestAskPlayersPendingClarificationMustBeTargeted(t *testing.T) {
	newCtx := func(combat *models.CombatState) (ActionContext, *bool, *[]Output, **AskResult) {
		hasEnd := false
		outbox := []Output{}
		var ask *AskResult
		var chase *models.ChaseState
		return ActionContext{
			GCtx:   &GameContext{Session: models.GameSession{Players: askTestPlayers()}},
			HasEnd: &hasEnd,
			Outbox: &outbox,
			Ask:    &ask,
			Combat: &combat,
			Chase:  &chase,
		}, &hasEnd, &outbox, &ask
	}
	pendingCombat := func() *models.CombatState {
		return &models.CombatState{Active: true, Participants: []models.CombatParticipant{
			{Name: "乙", UserID: 2, PendingClarification: true},
			{Name: "哥布林", IsNPC: true},
		}}
	}

	t.Run("点其他人被拒绝", func(t *testing.T) {
		actx, hasEnd, outbox, ask := newCtx(pendingCombat())
		results := askPlayersAction{}.Execute(ToolCall{Targets: []string{"甲"}, Question: "你怎么办？"}, actx)
		if !strings.HasPrefix(results[0].Result, "SYSTEM REJECT") || !strings.Contains(results[0].Result, "待澄清") {
			t.Fatalf("待澄清期间点其他人应被拒绝, got %+v", results)
		}
		if *hasEnd || *ask != nil || len(*outbox) != 0 {
			t.Errorf("拒绝时不应产生副作用")
		}
	})

	t.Run("点待澄清者放行", func(t *testing.T) {
		actx, _, _, ask := newCtx(pendingCombat())
		results := askPlayersAction{}.Execute(ToolCall{Targets: []string{"乙"}, Question: "闪避还是反击？"}, actx)
		if strings.HasPrefix(results[0].Result, "SYSTEM REJECT") {
			t.Fatalf("点名待澄清者应成功, got %+v", results)
		}
		if *ask == nil || len((*ask).UserIDs) != 1 || (*ask).UserIDs[0] != 2 {
			t.Errorf("Ask = %+v", *ask)
		}
	})

	t.Run("多人待澄清点其中任一位即可", func(t *testing.T) {
		combat := pendingCombat()
		combat.Participants = append(combat.Participants, models.CombatParticipant{Name: "甲", UserID: 1, PendingClarification: true})
		actx, _, _, ask := newCtx(combat)
		results := askPlayersAction{}.Execute(ToolCall{Targets: []string{"甲"}, Question: "闪避还是反击？"}, actx)
		if strings.HasPrefix(results[0].Result, "SYSTEM REJECT") {
			t.Fatalf("多人待澄清时点其中一位应成功, got %+v", results)
		}
		if *ask == nil || len((*ask).UserIDs) != 1 || (*ask).UserIDs[0] != 1 {
			t.Errorf("Ask = %+v", *ask)
		}
	})
}

func TestDirectorBatchPolicyAskPlayers(t *testing.T) {
	t.Run("ask_players可与write和状态更新同批", func(t *testing.T) {
		reject := noEncounterBatchPolicy()([]llm.ToolCall{
			policyCall("write", `{"direction":"雨夜"}`),
			policyCall("update_location", `{"character_name":"甲","new_location":"门厅"}`),
			policyCall("ask_players", `{"targets":["甲"],"question":"你怎么办？"}`),
		})
		if reject != "" {
			t.Errorf("应放行, got %s", reject)
		}
	})

	t.Run("ask_players与查询类工具同批被拒", func(t *testing.T) {
		reject := noEncounterBatchPolicy()([]llm.ToolCall{
			policyCall("query_character", `{"character_name":"甲"}`),
			policyCall("ask_players", `{"targets":["甲"],"question":"你怎么办？"}`),
		})
		if reject == "" {
			t.Error("ask_players不应与query_character同批")
		}
	})

	t.Run("空question被视为空回复", func(t *testing.T) {
		reject := noEncounterBatchPolicy()([]llm.ToolCall{
			policyCall("ask_players", `{"targets":["甲"],"question":""}`),
		})
		if !strings.Contains(reject, "empty reply") {
			t.Errorf("want empty reply reject, got %q", reject)
		}
	})
}

func TestKPFloorRendering(t *testing.T) {
	t.Run("opening不渲染intent", func(t *testing.T) {
		out := buildKPCurrentActionBlock(GameContext{KPFloor: KPFloorOpening})
		if !strings.Contains(out, "<kp_floor reason='opening'>") || strings.Contains(out, "<intent") {
			t.Errorf("got %q", out)
		}
		for _, want := range []string{"先根据玩家装备更新属性", "query_character", "update_characters", "update_armor", "描述没有写明的效果不要编造", "必须先用narrate发布一段旁白", "不需要先调用write", "[UNKNOWN]", "不替调查员说话"} {
			if !strings.Contains(out, want) {
				t.Errorf("opening floor should contain %q, got %q", want, out)
			}
		}
	})

	t.Run("非opening不要求旁白开场", func(t *testing.T) {
		out := buildKPCurrentActionBlock(GameContext{KPFloor: KPFloorHostContinue})
		if strings.Contains(out, "旁白") || strings.Contains(out, "先根据玩家装备更新属性") {
			t.Errorf("host_continue must not ask for an opening narration or equipment update: %q", out)
		}
	})

	t.Run("host_continue列出被跳过者", func(t *testing.T) {
		out := buildKPCurrentActionBlock(GameContext{KPFloor: KPFloorHostContinue, SkippedNames: []string{"乙", "丙"}})
		if !strings.Contains(out, "乙、丙") {
			t.Errorf("got %q", out)
		}
	})

	t.Run("host_continue带已提交声明时保留声明", func(t *testing.T) {
		out := buildKPCurrentActionBlock(GameContext{
			KPFloor:        KPFloorHostContinue,
			PendingActions: []PlayerAction{{UserID: 1, PlayerName: "甲", Content: "我推门"}},
			UserName:       "甲",
			UserInput:      "我推门",
		})
		if !strings.Contains(out, "<kp_floor") || !strings.Contains(out, "我推门") {
			t.Errorf("got %q", out)
		}
	})

	t.Run("回应段在有提问时先输出asked", func(t *testing.T) {
		out := buildKPCurrentActionBlock(GameContext{AskedQuestion: "你怎么办？", UserName: "甲", UserInput: "我推门"})
		askedIdx := strings.Index(out, "<asked>你怎么办？</asked>")
		intentIdx := strings.Index(out, "<intent")
		if askedIdx < 0 || intentIdx < 0 || askedIdx > intentIdx {
			t.Errorf("asked应在intent之前, got %q", out)
		}
	})

	t.Run("无KPFloor无提问时渲染不变", func(t *testing.T) {
		out := buildKPCurrentActionBlock(GameContext{UserName: "甲", UserInput: "我推门"})
		if strings.Contains(out, "kp_floor") || strings.Contains(out, "asked") || !strings.Contains(out, "<intent") {
			t.Errorf("got %q", out)
		}
	})
}

func TestWhisperAction(t *testing.T) {
	var outbox []Output
	hasEnd := false
	actx := ActionContext{
		GCtx:   &GameContext{Session: models.GameSession{Players: askTestPlayers()}},
		HasEnd: &hasEnd,
		Outbox: &outbox,
	}

	// 成功向存活调查员发密讯
	res := whisperAction{}.Execute(ToolCall{
		Action:        ToolWhisper,
		CharacterName: "乙",
		Content:       "你看到墙角闪过一道阴影。",
		CallID:        "c1",
	}, actx)
	if len(res) != 1 || strings.HasPrefix(res[0].Result, "SYSTEM REJECT") {
		t.Fatalf("whisper should succeed, got %+v", res)
	}
	if hasEnd {
		t.Error("whisper should not end floor")
	}
	if len(outbox) != 1 || outbox[0].Kind != OutputKP || outbox[0].RecipientName != "乙" || len(outbox[0].VisibleTo) != 1 || outbox[0].VisibleTo[0] != 2 {
		t.Fatalf("unexpected outbox = %+v", outbox)
	}

	// 未知角色、已死亡角色、空内容都应被拒绝
	for _, tc := range []ToolCall{
		{CharacterName: "不存在", Content: "hi"},
		{CharacterName: "丙", Content: "hi"},
		{CharacterName: "甲", Content: "   "},
	} {
		outbox = nil
		got := whisperAction{}.Execute(tc, actx)
		if len(got) != 1 || !strings.HasPrefix(got[0].Result, "SYSTEM REJECT") || len(outbox) != 0 {
			t.Errorf("call %+v should be rejected without output, got res=%+v outbox=%+v", tc, got, outbox)
		}
	}
}

func TestSecretBackgroundInjection(t *testing.T) {
	players := []models.SessionPlayer{
		{
			UserID:           1,
			SecretBackground: "你是教会派来销毁手稿的秘密探员",
			CharacterCard:    models.CharacterCard{Name: "甲", Occupation: "记者"},
		},
		{
			UserID:        2,
			CharacterCard: models.CharacterCard{Name: "乙", Occupation: "医生"},
		},
	}
	brief := buildPlayerBrief(players)
	if !strings.Contains(brief, "有秘密背景/HO") || !strings.Contains(brief, "<attention>") {
		t.Errorf("buildPlayerBrief should highlight HO presence, got:\n%s", brief)
	}
	detail := buildCharacterDetail("甲", players)
	if !strings.Contains(detail, "<secret_background>你是教会派来销毁手稿的秘密探员</secret_background>") {
		t.Errorf("buildCharacterDetail should embed <secret_background>, got:\n%s", detail)
	}
	detailNoHO := buildCharacterDetail("乙", players)
	if strings.Contains(detailNoHO, "<secret_background>") {
		t.Errorf("buildCharacterDetail should omit empty <secret_background>, got:\n%s", detailNoHO)
	}
}

func TestFallbackWriterIgnoresPrivateWhisper(t *testing.T) {
	var published []RoundOutputs
	gctx := &GameContext{
		Publish: func(ro RoundOutputs) error {
			published = append(published, ro)
			return nil
		},
	}
	fr := newFloorRun(t.Context(), gctx, &ContextManager{}, 1, nil, nil)
	fr.beatNeedsWriter = true
	fr.outbox = []Output{{
		Kind:          OutputKP,
		Text:          "只有你听见了低语。",
		VisibleTo:     []uint{2},
		RecipientName: "乙",
	}}
	if err := fr.flush(1, nil); err != nil {
		t.Fatalf("flush whisper-only: %v", err)
	}
	if len(published) != 1 || len(published[0].Items) != 1 || published[0].Items[0].Kind != OutputKP {
		t.Fatalf("whisper-only flush should not synthesize public writer, got %+v", published)
	}
	if !fr.beatNeedsWriter {
		t.Error("whisper-only flush should preserve beatNeedsWriter for subsequent public beat")
	}

	published = nil
	fr.outbox = []Output{
		{Kind: OutputKP, Text: "只有你听见了低语。", VisibleTo: []uint{2}, RecipientName: "乙"},
		{Kind: OutputKP, Text: "大厅吊灯突然熄灭，一片漆黑。"},
	}
	if err := fr.flush(2, nil); err != nil {
		t.Fatalf("flush mixed: %v", err)
	}
	if len(published) != 1 {
		t.Fatalf("expected 1 round output, got %d", len(published))
	}
	var writerFound bool
	for _, it := range published[0].Items {
		if it.Kind == OutputWriter {
			writerFound = true
			if strings.Contains(it.Text, "只有你听见了低语") || !strings.Contains(it.Text, "大厅吊灯突然熄灭") {
				t.Fatalf("fallback writer leaked whisper or missed public kp: %q", it.Text)
			}
		}
	}
	if !writerFound {
		t.Fatal("expected fallback public writer for public kp output")
	}
}


package agent

import (
	"strings"
	"testing"

	"github.com/llmcoc/server/internal/models"
)

func makeRealityCheckContext(san, maxSAN int, madness string) (*GameContext, ActionContext, *[]Output) {
	gctx := &GameContext{
		Session: models.GameSession{
			ID:       42,
			GameTime: "1924-09-03T22:00",
			Players: []models.SessionPlayer{{
				UserID: 7,
				CharacterCard: models.CharacterCard{
					Name:         "约翰",
					Occupation:   "侦探",
					MadnessState: madness,
					Stats: models.JSONField[models.CharacterStats]{Data: models.CharacterStats{
						HP: 12, MaxHP: 12, SAN: san, MaxSAN: maxSAN, POW: 60, DEX: 60, APP: 50, MOV: 8,
					}},
				},
			}},
		},
	}
	var outbox []Output
	actx := ActionContext{
		GCtx:   gctx,
		Sid:    gctx.Session.ID,
		Outbox: &outbox,
	}
	return gctx, actx, &outbox
}

func TestWhisperAction_HallucinationFlag(t *testing.T) {
	_, actx, outbox := makeRealityCheckContext(50, 70, "temporary")

	res := whisperAction{}.Execute(ToolCall{
		Action:          ToolWhisper,
		CharacterName:   "约翰",
		Content:         "你分明听见壁橱里的旧怀表在低声呼唤你的乳名。",
		IsHallucination: true,
		CallID:          "call-w1",
	}, actx)
	if len(res) != 1 || !strings.Contains(res[0].Result, "幻觉耳语") {
		t.Fatalf("expected hallucination whisper confirmation, got %+v", res)
	}
	if len(*outbox) != 1 || !(*outbox)[0].Hallucination || (*outbox)[0].RecipientName != "约翰" {
		t.Fatalf("outbox = %+v, want Hallucination=true for 约翰", *outbox)
	}

	res2 := whisperAction{}.Execute(ToolCall{
		Action:        ToolWhisper,
		CharacterName: "约翰",
		Content:       "你在桌面缝隙摸到一枚黄铜钥匙。",
	}, actx)
	if len(res2) != 1 || strings.Contains(res2[0].Result, "幻觉") {
		t.Fatalf("normal whisper should not mention hallucination, got %+v", res2)
	}
	if len(*outbox) != 2 || (*outbox)[1].Hallucination {
		t.Fatalf("second outbox item should have Hallucination=false, got %+v", (*outbox)[1])
	}
}

func TestRealityCheckAction_RejectsInvalidStates(t *testing.T) {
	for _, tc := range []struct {
		name    string
		madness string
		target  string
		wantSub string
	}{
		{"sane", "none", "约翰", "未处于临时性或不定性疯狂"},
		{"empty_state", "", "约翰", "未处于临时性或不定性疯狂"},
		{"permanent", "permanent", "约翰", "永久性疯狂"},
		{"unknown", "temporary", "玛丽", "找不到调查员"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, actx, _ := makeRealityCheckContext(50, 70, tc.madness)
			res := realityCheckAction{}.Execute(ToolCall{
				Action:        ToolRealityCheck,
				CharacterName: tc.target,
			}, actx)
			if len(res) != 1 || !strings.HasPrefix(res[0].Result, "SYSTEM REJECT:") || !strings.Contains(res[0].Result, tc.wantSub) {
				t.Fatalf("got %+v, want SYSTEM REJECT containing %q", res, tc.wantSub)
			}
		})
	}
}

func TestRealityCheckAction_PassAndFail(t *testing.T) {
	origRoll := realityCheckRoll
	t.Cleanup(func() { realityCheckRoll = origRoll })

	// 1. 恰好等于当前 SAN：判定成功，不损 SAN
	gctx, actx, _ := makeRealityCheckContext(45, 70, "temporary")
	realityCheckRoll = func() int { return 45 }

	passRes := realityCheckAction{}.Execute(ToolCall{
		Action:        ToolRealityCheck,
		CharacterName: "约翰",
		Reason:        "确认墙上的血迹是否为幻觉",
	}, actx)
	if len(passRes) != 1 || !strings.Contains(passRes[0].Result, `passed="true"`) {
		t.Fatalf("expected passed=true, got %+v", passRes)
	}
	if got := gctx.Session.Players[0].CharacterCard.Stats.Data.SAN; got != 45 {
		t.Errorf("SAN changed on pass: got %d, want 45", got)
	}
	if len(passRes[0].Events) != 1 || passRes[0].Events[0].Type != "reality_check" || passRes[0].Events[0].Result != "成功" {
		t.Errorf("pass events = %+v", passRes[0].Events)
	}

	// 2. 高于当前 SAN：判定失败，扣 1 SAN、累计 DailySanLoss、发出 reality_check + stat 事件
	realityCheckRoll = func() int { return 46 }
	failRes := realityCheckAction{}.Execute(ToolCall{
		Action:        ToolRealityCheck,
		CharacterName: "约翰",
		Reason:        "怀疑队友低声诅咒自己",
	}, actx)
	if len(failRes) != 1 || !strings.Contains(failRes[0].Result, `passed="false"`) || !strings.Contains(failRes[0].Result, "manage_madness") {
		t.Fatalf("expected passed=false with manage_madness reminder, got %+v", failRes)
	}
	card := gctx.Session.Players[0].CharacterCard
	if card.Stats.Data.SAN != 44 {
		t.Errorf("SAN after fail = %d, want 44", card.Stats.Data.SAN)
	}
	if card.DailySanLoss != 1 || card.DailySanLossDay != models.GameDayKey(gctx.Session.GameTime) {
		t.Errorf("DailySanLoss = %d (day %d), want 1 (day %d)", card.DailySanLoss, card.DailySanLossDay, models.GameDayKey(gctx.Session.GameTime))
	}
	if len(failRes[0].Events) != 2 {
		t.Fatalf("fail events len = %d, want 2: %+v", len(failRes[0].Events), failRes[0].Events)
	}
	if failRes[0].Events[0].Type != "reality_check" || failRes[0].Events[0].Result != "失败" {
		t.Errorf("event[0] = %+v", failRes[0].Events[0])
	}
	if failRes[0].Events[1].Type != "stat" || failRes[0].Events[1].Field != "san" || *failRes[0].Events[1].From != 45 || *failRes[0].Events[1].To != 44 {
		t.Errorf("event[1] = %+v", failRes[0].Events[1])
	}

	// 3. SAN 降为 0 时提醒永久性疯狂
	gctxZero, actxZero, _ := makeRealityCheckContext(1, 70, "indefinite")
	realityCheckRoll = func() int { return 88 }
	zeroRes := realityCheckAction{}.Execute(ToolCall{
		Action:        ToolRealityCheck,
		CharacterName: "约翰",
	}, actxZero)
	if len(zeroRes) != 1 || !strings.Contains(zeroRes[0].Result, `madness_type="permanent"`) {
		t.Errorf("expected permanent madness reminder at SAN=0, got %+v", zeroRes)
	}
	if gctxZero.Session.Players[0].CharacterCard.Stats.Data.SAN != 0 {
		t.Errorf("expected SAN=0, got %d", gctxZero.Session.Players[0].CharacterCard.Stats.Data.SAN)
	}
}

func TestBuildPlayerBrief_UnderlyingMadnessNotice(t *testing.T) {
	gctx, _, _ := makeRealityCheckContext(50, 70, "temporary")
	brief := buildPlayerBrief(gctx.Session.Players)
	if !strings.Contains(brief, "潜伏疯狂期") || !strings.Contains(brief, "reality_check") || !strings.Contains(brief, "is_hallucination=true") {
		t.Errorf("brief missing underlying madness guidance:\n%s", brief)
	}
}

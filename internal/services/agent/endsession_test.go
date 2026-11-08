// NOTE: 测试 RunEndSession、结算校验和 endGameAction 的 win/settlement 行为。
package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/llmcoc/server/internal/models"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// ── ToolCall.Win JSON 解析：区分缺失、null、false、true ────────────────────────

// TestToolCallWin_Missing 验证 JSON 缺失 win 字段时 Win 指针为 nil。
func TestToolCallWin_Missing(t *testing.T) {
	var call ToolCall
	if err := json.Unmarshal([]byte(`{"action":"end_game","end_summary":"test"}`), &call); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if call.Win != nil {
		t.Errorf("missing win should unmarshal to nil, got *%v", *call.Win)
	}
}

// TestToolCallWin_Null 验证 JSON 中 win=null 时 Win 指针为 nil（与缺失等价）。
func TestToolCallWin_Null(t *testing.T) {
	var call ToolCall
	if err := json.Unmarshal([]byte(`{"action":"end_game","win":null}`), &call); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if call.Win != nil {
		t.Errorf("win=null should unmarshal to nil pointer, got *%v", *call.Win)
	}
}

// TestToolCallWin_False 验证 JSON 中 win=false 时 Win 指针非 nil 且值为 false。
func TestToolCallWin_False(t *testing.T) {
	var call ToolCall
	if err := json.Unmarshal([]byte(`{"action":"end_game","win":false}`), &call); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if call.Win == nil {
		t.Fatal("win=false should unmarshal to non-nil pointer")
	}
	if *call.Win != false {
		t.Errorf("*win = %v, want false", *call.Win)
	}
}

// TestToolCallWin_True 验证 JSON 中 win=true 时 Win 指针非 nil 且值为 true。
func TestToolCallWin_True(t *testing.T) {
	var call ToolCall
	if err := json.Unmarshal([]byte(`{"action":"end_game","win":true}`), &call); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if call.Win == nil {
		t.Fatal("win=true should unmarshal to non-nil pointer")
	}
	if *call.Win != true {
		t.Errorf("*win = %v, want true", *call.Win)
	}
}

// TestToolCallSettlement_Decodes 验证 settlement 能解出评分、点评和技能，且缺失 score 时指针为 nil。
func TestToolCallSettlement_Decodes(t *testing.T) {
	var call ToolCall
	raw := `{"action":"end_game","win":true,"settlement":[
		{"character_name":"甲","score":85,"comment":"沉着","skills":["侦查","图书馆使用"]},
		{"character_name":"乙"}]}`
	if err := json.Unmarshal([]byte(raw), &call); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(call.Settlement) != 2 {
		t.Fatalf("settlement len = %d, want 2", len(call.Settlement))
	}
	a, b := call.Settlement[0], call.Settlement[1]
	if a.CharacterName != "甲" || a.Score == nil || *a.Score != 85 || a.Comment != "沉着" || len(a.Skills) != 2 {
		t.Errorf("first entry decoded wrong: %+v", a)
	}
	if b.Score != nil {
		t.Errorf("missing score should decode to nil, got *%d", *b.Score)
	}
}

// ── 结算校验 ───────────────────────────────────────────────────────────────────

func settlementPlayers(names ...string) []models.SessionPlayer {
	players := make([]models.SessionPlayer, len(names))
	for i, n := range names {
		players[i].CharacterCard.Name = n
	}
	return players
}

// TestValidateSettlement 验证结算必须恰好覆盖全部调查员，且每条都有 0-100 的评分。
func TestValidateSettlement(t *testing.T) {
	players := settlementPlayers("甲", "乙")
	tests := []struct {
		name       string
		settlement []PlayerSettlement
		wantReject string
	}{
		{"完整", []PlayerSettlement{{CharacterName: "甲", Score: intPtr(80)}, {CharacterName: "乙", Score: intPtr(0)}}, ""},
		{"边界100分", []PlayerSettlement{{CharacterName: "甲", Score: intPtr(100)}, {CharacterName: "乙", Score: intPtr(1)}}, ""},
		{"空结算", nil, "缺少调查员 甲、乙"},
		{"缺人", []PlayerSettlement{{CharacterName: "甲", Score: intPtr(80)}}, "缺少调查员 乙"},
		{"名字对不上", []PlayerSettlement{{CharacterName: "甲", Score: intPtr(80)}, {CharacterName: "丙", Score: intPtr(80)}}, `"丙" 不是本局调查员`},
		{"重复", []PlayerSettlement{{CharacterName: "甲", Score: intPtr(80)}, {CharacterName: "甲", Score: intPtr(70)}, {CharacterName: "乙", Score: intPtr(60)}}, `"甲" 重复`},
		{"缺评分", []PlayerSettlement{{CharacterName: "甲"}, {CharacterName: "乙", Score: intPtr(60)}}, `"甲" 的 score`},
		{"评分过高", []PlayerSettlement{{CharacterName: "甲", Score: intPtr(101)}, {CharacterName: "乙", Score: intPtr(60)}}, `"甲" 的 score`},
		{"评分为负", []PlayerSettlement{{CharacterName: "甲", Score: intPtr(-1)}, {CharacterName: "乙", Score: intPtr(60)}}, `"甲" 的 score`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := validateSettlement(players, tt.settlement)
			if tt.wantReject == "" {
				if got != "" {
					t.Fatalf("want pass, got %q", got)
				}
				return
			}
			if !strings.HasPrefix(got, "SYSTEM REJECT") || !strings.Contains(got, tt.wantReject) {
				t.Errorf("got %q, want SYSTEM REJECT containing %q", got, tt.wantReject)
			}
		})
	}
}

// ── endGameAction.Execute 早返回：无状态/HasEnd/消息等副作用 ───────────────────

func newEndGameActionContext(sessionID uint, players []models.SessionPlayer, hasEnd *bool, outbox *[]Output) ActionContext {
	return ActionContext{
		Ctx: context.Background(),
		GCtx: &GameContext{
			Session: models.GameSession{
				ID:      sessionID,
				Status:  models.SessionStatusPlaying,
				Players: players,
			},
		},
		Sid:    sessionID,
		HasEnd: hasEnd,
		Outbox: outbox,
	}
}

// TestEndGameAction_WinNil_NoSideEffect 验证 call.Win==nil 时：
//   - 返回包含 "win" 关键字的错误 ToolResult
//   - HasEnd 保持 false（未被设置）
//   - session.Status 不变为 ended
//   - 在返回错误之前没有任何状态修改
func TestEndGameAction_WinNil_NoSideEffect(t *testing.T) {
	hasEnd := false
	var outbox []Output
	actx := newEndGameActionContext(999, nil, &hasEnd, &outbox)
	// NOTE: Win 为 nil（缺失）
	call := ToolCall{Action: ToolEndGame, EndSummary: "测试结局"}

	results := endGameAction{}.Execute(call, actx)

	if len(results) != 1 {
		t.Fatalf("want 1 result for nil win, got %d", len(results))
	}
	if results[0].Action != ToolEndGame {
		t.Errorf("result action = %q, want %q", results[0].Action, ToolEndGame)
	}
	if !strings.Contains(results[0].Result, "win") {
		t.Errorf("error message should mention 'win', got %q", results[0].Result)
	}
	if hasEnd {
		t.Error("HasEnd must not be set when win is nil")
	}
	if actx.GCtx.Session.Status == models.SessionStatusEnded {
		t.Error("session.Status must not change to ended when win is nil")
	}
	if len(outbox) != 0 {
		t.Errorf("outbox should be empty when win is nil, got %+v", outbox)
	}
}

// TestEndGameAction_InvalidSettlement_NoSideEffect 验证结算不合格时被拒绝，且游戏不会结束。
// NOTE: 会话里预置了 Players，所以校验之前不会碰数据库。
func TestEndGameAction_InvalidSettlement_NoSideEffect(t *testing.T) {
	for _, win := range []bool{true, false} {
		hasEnd := false
		var outbox []Output
		actx := newEndGameActionContext(998, settlementPlayers("甲", "乙"), &hasEnd, &outbox)
		w := win
		call := ToolCall{
			Action:     ToolEndGame,
			Win:        &w,
			EndSummary: "测试结局",
			Settlement: []PlayerSettlement{{CharacterName: "甲", Score: intPtr(80)}},
		}

		results := endGameAction{}.Execute(call, actx)

		if len(results) != 1 || !strings.HasPrefix(results[0].Result, "SYSTEM REJECT") || !strings.Contains(results[0].Result, "乙") {
			t.Fatalf("win=%v: results = %+v, want one SYSTEM REJECT naming the missing 乙", win, results)
		}
		if hasEnd {
			t.Errorf("win=%v: HasEnd must not be set when settlement is rejected", win)
		}
		if actx.GCtx.Session.Status == models.SessionStatusEnded {
			t.Errorf("win=%v: session must not end when settlement is rejected", win)
		}
		if len(outbox) != 0 {
			t.Errorf("win=%v: outbox should stay empty, got %+v", win, outbox)
		}
	}
}

// ── RunEndSession 行为测试（内存 SQLite，无真实 LLM）─────────────────────────────

// initEndSessionTestDB 为 RunEndSession 测试建立含 User/CharacterCard/GameEvaluation
// 的内存 SQLite 库。LLM 相关表（AgentConfig 等）不迁移，使 loadSingleAgent 返回错误，
// 从而让关系提升跳过——无需真实 LLM。
func initEndSessionTestDB(t *testing.T) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("get sql db: %v", err)
	}
	sqlDB.SetMaxOpenConns(1)
	if err := db.AutoMigrate(
		&models.User{},
		&models.CharacterCard{},
		&models.GameEvaluation{},
	); err != nil {
		t.Fatalf("auto-migrate: %v", err)
	}
	prev := models.DB
	models.DB = db
	t.Cleanup(func() {
		models.DB = prev
		_ = sqlDB.Close()
	})
}

// newEndSessionCard 创建并持久化测试用角色卡，返回指针。
func newEndSessionCard(t *testing.T, name string, stats models.CharacterStats, skills map[string]int) *models.CharacterCard {
	t.Helper()
	card := &models.CharacterCard{
		Name:     name,
		IsActive: true,
		Stats:    models.JSONField[models.CharacterStats]{Data: stats},
		Skills:   models.JSONField[map[string]int]{Data: skills},
	}
	if err := models.DB.Create(card).Error; err != nil {
		t.Fatalf("newEndSessionCard %q: %v", name, err)
	}
	return card
}

// newEndSessionUser 创建并持久化测试用 User，返回 ID。
func newEndSessionUser(t *testing.T, name string) uint {
	t.Helper()
	u := models.User{
		Username:     name,
		Email:        name + "@test.com",
		PasswordHash: "hash",
		Role:         "user",
		Coins:        0,
	}
	if err := models.DB.Create(&u).Error; err != nil {
		t.Fatalf("newEndSessionUser %q: %v", name, err)
	}
	return u.ID
}

// makeTestSession 构建一个含单个 Player 的 GameSession（不写入 DB，ID 由调用方设置）。
func makeTestSession(sessionID uint, userID uint, card models.CharacterCard) *models.GameSession {
	return &models.GameSession{
		ID: sessionID,
		Players: []models.SessionPlayer{{
			UserID:        userID,
			CharacterCard: card,
		}},
	}
}

// TestRunEndSession_WinFalse_SkipsPOW 验证 win=false 时 POW 不发生增长。
// NOTE: 使用 POW=0；win=true 时 RollD100()>0 必为真，POW 必增；win=false 时跳过 POW 块。
func TestRunEndSession_WinFalse_SkipsPOW(t *testing.T) {
	initEndSessionTestDB(t)

	uid := newEndSessionUser(t, "powtest_false")
	card := newEndSessionCard(t, "POWFalse", models.CharacterStats{
		POW: 0, MaxHP: 10, HP: 10, MaxMP: 10, MP: 10, MaxSAN: 99, SAN: 50,
	}, nil)

	sess := makeTestSession(10, uid, *card)
	if err := RunEndSession(context.Background(), sess, nil, false, nil); err != nil {
		t.Fatalf("RunEndSession(win=false): %v", err)
	}

	var updated models.CharacterCard
	models.DB.First(&updated, card.ID)
	if updated.Stats.Data.POW != 0 {
		t.Errorf("win=false: POW should remain 0, got %d", updated.Stats.Data.POW)
	}
}

// TestRunEndSession_WinTrue_POWGrows 验证 win=true 时 POW=0 的角色 POW 必然增长。
// NOTE: POW=0 时 limit=0，RollD100()（1-100）必大于 0，point=5，Roll(1,5)>=1，故 POW 必增。
func TestRunEndSession_WinTrue_POWGrows(t *testing.T) {
	initEndSessionTestDB(t)

	uid := newEndSessionUser(t, "powtest_true")
	card := newEndSessionCard(t, "POWTrue", models.CharacterStats{
		POW: 0, MaxHP: 10, HP: 10, MaxMP: 10, MP: 10, MaxSAN: 99, SAN: 50,
	}, nil)

	sess := makeTestSession(11, uid, *card)
	if err := RunEndSession(context.Background(), sess, nil, true, nil); err != nil {
		t.Fatalf("RunEndSession(win=true): %v", err)
	}

	var updated models.CharacterCard
	models.DB.First(&updated, card.ID)
	if updated.Stats.Data.POW <= 0 {
		t.Errorf("win=true, POW=0: POW should have grown, got %d", updated.Stats.Data.POW)
	}
}

// TestRunEndSession_CoinsFollowSettlementScore 验证金币 = 20 基础 + 评分/2 加成，胜负都发，
// 评分越界时按 0-100 截断，没有结算(手动结束)时只发基础金币。
func TestRunEndSession_CoinsFollowSettlementScore(t *testing.T) {
	tests := []struct {
		name       string
		win        bool
		settlement []PlayerSettlement
		wantCoins  int
	}{
		{"胜利80分", true, []PlayerSettlement{{CharacterName: "CoinChar", Score: intPtr(80)}}, 60},
		{"失败也按评分发", false, []PlayerSettlement{{CharacterName: "CoinChar", Score: intPtr(80)}}, 60},
		{"满分", true, []PlayerSettlement{{CharacterName: "CoinChar", Score: intPtr(100)}}, 70},
		{"0分只有基础", true, []PlayerSettlement{{CharacterName: "CoinChar", Score: intPtr(0)}}, 20},
		{"评分过高被截断", false, []PlayerSettlement{{CharacterName: "CoinChar", Score: intPtr(500)}}, 70},
		{"没有结算只有基础", false, nil, 20},
	}
	for i, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			initEndSessionTestDB(t)
			uid := newEndSessionUser(t, "coinstest")
			card := newEndSessionCard(t, "CoinChar", models.CharacterStats{
				MaxHP: 10, HP: 10, MaxMP: 10, MP: 10, MaxSAN: 99, SAN: 50,
			}, nil)

			sess := makeTestSession(uint(100+i), uid, *card)
			if err := RunEndSession(context.Background(), sess, nil, tt.win, tt.settlement); err != nil {
				t.Fatalf("RunEndSession: %v", err)
			}

			var user models.User
			models.DB.First(&user, uid)
			if user.Coins != tt.wantCoins {
				t.Errorf("coins = %d, want %d", user.Coins, tt.wantCoins)
			}
		})
	}
}

// TestRunEndSession_PersistsEvaluation 验证有结算时写入评价记录，手动结束(无结算)时不写。
func TestRunEndSession_PersistsEvaluation(t *testing.T) {
	initEndSessionTestDB(t)

	uid := newEndSessionUser(t, "evaltest")
	card := newEndSessionCard(t, "EvalChar", models.CharacterStats{
		MaxHP: 10, HP: 10, MaxMP: 10, MP: 10, MaxSAN: 99, SAN: 50,
	}, nil)
	sess := makeTestSession(30, uid, *card)
	settlement := []PlayerSettlement{{CharacterName: "EvalChar", Score: intPtr(90), Comment: "表现出色"}}
	if err := RunEndSession(context.Background(), sess, nil, true, settlement); err != nil {
		t.Fatalf("RunEndSession: %v", err)
	}

	var eval models.GameEvaluation
	if err := models.DB.Where("session_id = ?", 30).First(&eval).Error; err != nil {
		t.Fatalf("evaluation not saved: %v", err)
	}
	got := eval.Content.Data.Players
	if len(got) != 1 || got[0].CharacterName != "EvalChar" || got[0].Score != 90 || got[0].Comment != "表现出色" ||
		got[0].BaseCoins != 20 || got[0].BonusCoins != 45 {
		t.Errorf("saved players = %+v, want EvalChar score 90 comment 表现出色 base 20 bonus 45", got)
	}

	sess = makeTestSession(31, uid, *card)
	if err := RunEndSession(context.Background(), sess, nil, false, nil); err != nil {
		t.Fatalf("RunEndSession(manual): %v", err)
	}
	var count int64
	models.DB.Model(&models.GameEvaluation{}).Where("session_id = ?", 31).Count(&count)
	if count != 0 {
		t.Errorf("manual end should not save an evaluation, got %d rows", count)
	}
}

// TestRunEndSession_WinFalse_DeadCardDeactivated 验证 win=false 时死亡角色被失活（撕卡）。
func TestRunEndSession_WinFalse_DeadCardDeactivated(t *testing.T) {
	initEndSessionTestDB(t)

	uid := newEndSessionUser(t, "deadtest")
	card := newEndSessionCard(t, "DeadChar", models.CharacterStats{
		MaxHP: 10, HP: 0, MaxMP: 10, MP: 5, MaxSAN: 99, SAN: 30, POW: 50,
	}, nil)
	card.WoundState = "dead"
	models.DB.Save(card)

	sess := makeTestSession(13, uid, *card)
	if err := RunEndSession(context.Background(), sess, nil, false, nil); err != nil {
		t.Fatalf("RunEndSession(win=false, dead): %v", err)
	}

	var updated models.CharacterCard
	models.DB.First(&updated, card.ID)
	if updated.IsActive {
		t.Error("win=false: dead card should have IsActive=false (撕卡)")
	}
}

// TestRunEndSession_WinFalse_LivingCardRestored 验证 win=false 时存活角色 HP/MP 被恢复。
func TestRunEndSession_WinFalse_LivingCardRestored(t *testing.T) {
	initEndSessionTestDB(t)

	uid := newEndSessionUser(t, "restoretest")
	card := newEndSessionCard(t, "LivingChar", models.CharacterStats{
		MaxHP: 10, HP: 3, MaxMP: 10, MP: 2, MaxSAN: 99, SAN: 50, POW: 50,
	}, nil)

	sess := makeTestSession(14, uid, *card)
	if err := RunEndSession(context.Background(), sess, nil, false, nil); err != nil {
		t.Fatalf("RunEndSession(win=false, living): %v", err)
	}

	var updated models.CharacterCard
	models.DB.First(&updated, card.ID)
	if updated.Stats.Data.HP != 10 {
		t.Errorf("win=false: living card HP = %d, want 10 (MaxHP)", updated.Stats.Data.HP)
	}
	if updated.Stats.Data.MP != 10 {
		t.Errorf("win=false: living card MP = %d, want 10 (MaxMP)", updated.Stats.Data.MP)
	}
}

// TestRunEndSession_WinFalse_SkillsUnchanged 验证 win=false 时即使 Director 报了技能，技能也不被修改。
func TestRunEndSession_WinFalse_SkillsUnchanged(t *testing.T) {
	initEndSessionTestDB(t)

	uid := newEndSessionUser(t, "skilltest")
	card := newEndSessionCard(t, "SkillChar", models.CharacterStats{
		MaxHP: 10, HP: 10, MaxMP: 10, MP: 10, MaxSAN: 99, SAN: 50, POW: 50,
	}, map[string]int{"侦查": 1, "图书馆使用": 1})

	sess := makeTestSession(15, uid, *card)
	settlement := []PlayerSettlement{{CharacterName: "SkillChar", Score: intPtr(60), Skills: []string{"侦查", "图书馆使用"}}}
	if err := RunEndSession(context.Background(), sess, nil, false, settlement); err != nil {
		t.Fatalf("RunEndSession(win=false): %v", err)
	}

	var updated models.CharacterCard
	models.DB.First(&updated, card.ID)
	if updated.Skills.Data["侦查"] != 1 {
		t.Errorf("win=false: 侦查 = %d, want 1 (no growth)", updated.Skills.Data["侦查"])
	}
	if updated.Skills.Data["图书馆使用"] != 1 {
		t.Errorf("win=false: 图书馆使用 = %d, want 1 (no growth)", updated.Skills.Data["图书馆使用"])
	}
}

// TestRunEndSession_WinTrue_AppliesReportedSkillGrowth 验证 win=true 时只有 Director 报的技能参与成长，
// 其余技能不变，且不可成长的技能和卡上没有的技能被忽略。
// NOTE: 技能值 1 时成长检定只有 1% 概率不通过，所以循环多次直到成长出现，其余断言是确定的。
func TestRunEndSession_WinTrue_AppliesReportedSkillGrowth(t *testing.T) {
	initEndSessionTestDB(t)

	uid := newEndSessionUser(t, "growthtest")
	card := newEndSessionCard(t, "GrowthChar", models.CharacterStats{
		MaxHP: 10, HP: 10, MaxMP: 10, MP: 10, MaxSAN: 99, SAN: 50, POW: 50,
	}, map[string]int{"侦查": 1, "图书馆使用": 40, "克苏鲁神话": 1, "信用评级": 1})
	settlement := []PlayerSettlement{{
		CharacterName: "GrowthChar",
		Score:         intPtr(70),
		Skills:        []string{"侦查", "克苏鲁神话", "信用评级", "不存在的技能"},
	}}

	grew := false
	for i := 0; i < 200 && !grew; i++ {
		card.Skills.Data["侦查"] = 1
		models.DB.Save(card)
		sess := makeTestSession(uint(40+i), uid, *card)
		if err := RunEndSession(context.Background(), sess, nil, true, settlement); err != nil {
			t.Fatalf("RunEndSession(win=true): %v", err)
		}
		var updated models.CharacterCard
		models.DB.First(&updated, card.ID)
		skills := updated.Skills.Data

		if v := skills["侦查"]; v > 1 {
			grew = true
			if v < 2 || v > 4 {
				t.Fatalf("侦查 = %d, want growth of 1-3 from 1", v)
			}
		}
		if skills["图书馆使用"] != 40 {
			t.Fatalf("图书馆使用 = %d, want 40 (not reported)", skills["图书馆使用"])
		}
		if skills["克苏鲁神话"] != 1 || skills["信用评级"] != 1 {
			t.Fatalf("克苏鲁神话/信用评级 must not grow, got %d/%d", skills["克苏鲁神话"], skills["信用评级"])
		}
		if _, ok := skills["不存在的技能"]; ok {
			t.Fatal("skills not on the card must not be added")
		}
	}
	if !grew {
		t.Error("侦查 never grew in 200 tries")
	}
}

// TestApplySkillGrowth_CapsAtNinetyNine 验证成长不超过 99，且已满 99 的技能不变。
func TestApplySkillGrowth_CapsAtNinetyNine(t *testing.T) {
	for i := 0; i < 300; i++ {
		card := &models.CharacterCard{Skills: models.JSONField[map[string]int]{Data: map[string]int{"高": 98, "满": 99}}}
		applySkillGrowth(card, []string{"高", "满", "高"})
		if card.Skills.Data["高"] < 98 || card.Skills.Data["高"] > 99 {
			t.Fatalf("高 = %d, want 98 or 99", card.Skills.Data["高"])
		}
		if card.Skills.Data["满"] != 99 {
			t.Fatalf("满 = %d, want 99", card.Skills.Data["满"])
		}
	}
}

// TestRunEndSession_WinTrue_NoRegression 验证 win=true 时存活角色正常结算且 HP 被恢复。
func TestRunEndSession_WinTrue_NoRegression(t *testing.T) {
	initEndSessionTestDB(t)

	uid := newEndSessionUser(t, "regrtest")
	card := newEndSessionCard(t, "RegrChar", models.CharacterStats{
		MaxHP: 10, HP: 4, MaxMP: 8, MP: 2, MaxSAN: 80, SAN: 30, POW: 50,
	}, map[string]int{"侦查": 30})

	sess := makeTestSession(16, uid, *card)
	if err := RunEndSession(context.Background(), sess, nil, true, nil); err != nil {
		t.Fatalf("RunEndSession(win=true): %v", err)
	}

	var updated models.CharacterCard
	models.DB.First(&updated, card.ID)
	if updated.Stats.Data.HP != 10 {
		t.Errorf("win=true: HP = %d, want 10", updated.Stats.Data.HP)
	}
	if updated.Stats.Data.MP != 8 {
		t.Errorf("win=true: MP = %d, want 8", updated.Stats.Data.MP)
	}
}

// ── 社交关系分层结算：tier1(会话) → tier2(人物卡) ────────────────────────────

// TestRunEndSession_RelationPromotion_ExistingUpdatesApplyWithoutAI 验证结算时：
//   - 人物卡(tier2)已有关系的本局更新/移除直接生效，不需要 AI 判断；
//   - 本局新结识的候选关系(B)在测试环境无 AgentConfig、LLM 不可用时不会写入人物卡。
func TestRunEndSession_RelationPromotion_ExistingUpdatesApplyWithoutAI(t *testing.T) {
	initEndSessionTestDB(t)

	uid := newEndSessionUser(t, "reltest")
	card := newEndSessionCard(t, "RelChar", models.CharacterStats{
		MaxHP: 10, HP: 10, MaxMP: 10, MP: 10, MaxSAN: 99, SAN: 50, POW: 50,
	}, nil)
	card.SocialRelations.Data = []models.SocialRelation{
		{Name: "A", Relationship: "线人", Note: "旧备注"},
		{Name: "C", Relationship: "仇敌", Note: ""},
	}
	if err := models.DB.Save(card).Error; err != nil {
		t.Fatalf("save card social_relations: %v", err)
	}

	sess := &models.GameSession{
		ID: 20,
		Players: []models.SessionPlayer{{
			UserID:        uid,
			CharacterCard: *card,
			SessionRelations: models.JSONField[[]models.SessionRelation]{Data: []models.SessionRelation{
				{SocialRelation: models.SocialRelation{Name: "A", Relationship: "盟友", Note: "新备注"}},   // tier2已有,本局更新
				{SocialRelation: models.SocialRelation{Name: "B", Relationship: "路人", Note: "只打过照面"}}, // tier2没有,新候选
				{SocialRelation: models.SocialRelation{Name: "C"}, Removed: true},                     // tier2已有,本局移除
			}},
		}},
	}

	if err := RunEndSession(context.Background(), sess, nil, false, nil); err != nil {
		t.Fatalf("RunEndSession(win=false): %v", err)
	}

	var updated models.CharacterCard
	models.DB.First(&updated, card.ID)
	byName := make(map[string]models.SocialRelation)
	for _, r := range updated.SocialRelations.Data {
		byName[r.Name] = r
	}
	if rel, ok := byName["A"]; !ok || rel.Relationship != "盟友" || rel.Note != "新备注" {
		t.Errorf("A should be updated to 盟友/新备注, got %+v (ok=%v)", rel, ok)
	}
	if _, ok := byName["C"]; ok {
		t.Errorf("C should have been removed by tombstone, got %+v", byName["C"])
	}
	if _, ok := byName["B"]; ok {
		t.Errorf("B is a new candidate and LLM is unavailable in this test, should not be written, got %+v", byName["B"])
	}
}

// TestRunEndSession_RelationPromotion_DeadCharacterSkipped 验证已死亡角色不参与关系合并
// （卡即将被撕，本局的 tier1 关系变更全部作废）。
func TestRunEndSession_RelationPromotion_DeadCharacterSkipped(t *testing.T) {
	initEndSessionTestDB(t)

	uid := newEndSessionUser(t, "reldeadtest")
	card := newEndSessionCard(t, "DeadRelChar", models.CharacterStats{
		MaxHP: 10, HP: 0, MaxMP: 10, MP: 5, MaxSAN: 99, SAN: 30, POW: 50,
	}, nil)
	card.WoundState = "dead"
	card.SocialRelations.Data = []models.SocialRelation{{Name: "A", Relationship: "线人"}}
	if err := models.DB.Save(card).Error; err != nil {
		t.Fatalf("save card: %v", err)
	}

	sess := &models.GameSession{
		ID: 21,
		Players: []models.SessionPlayer{{
			UserID:        uid,
			CharacterCard: *card,
			SessionRelations: models.JSONField[[]models.SessionRelation]{Data: []models.SessionRelation{
				{SocialRelation: models.SocialRelation{Name: "A"}, Removed: true},
				{SocialRelation: models.SocialRelation{Name: "B", Relationship: "路人"}},
			}},
		}},
	}

	if err := RunEndSession(context.Background(), sess, nil, false, nil); err != nil {
		t.Fatalf("RunEndSession(win=false, dead): %v", err)
	}

	var updated models.CharacterCard
	models.DB.First(&updated, card.ID)
	if len(updated.SocialRelations.Data) != 1 || updated.SocialRelations.Data[0].Name != "A" {
		t.Errorf("dead character's social_relations should stay untouched, got %+v", updated.SocialRelations.Data)
	}
	if updated.IsActive {
		t.Error("dead card should have IsActive=false")
	}
}

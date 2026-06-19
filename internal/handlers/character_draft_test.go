package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/llmcoc/server/internal/config"
	"github.com/llmcoc/server/internal/models"
	"github.com/llmcoc/server/internal/services/agent"
)

func draftRouter(userID uint) *gin.Engine {
	r := gin.New()
	auth := withAuth(userID, "tester", "user")
	r.POST("/characters/roll", auth, RollCharacterDraft)
	r.POST("/characters/finalize", auth, FinalizeCharacterDraft)
	r.GET("/characters/skill-defaults", auth, GetCharacterSkillDefaults)
	return r
}

type rollDraftResp struct {
	DraftID       uint                     `json:"draft_id"`
	Token         string                   `json:"token"`
	Stats         models.CharacterStats    `json:"stats"`
	RawRolls      models.CharacterRawRolls `json:"raw_rolls"`
	SkillDefaults map[string]int           `json:"skill_defaults"`
	SkillBudget   map[string]int           `json:"skill_budget"`
	ExpiresAt     time.Time                `json:"expires_at"`
}

func rollDraftForTest(t *testing.T, r *gin.Engine, age int) rollDraftResp {
	t.Helper()
	w := httptest.NewRecorder()
	r.ServeHTTP(w, jsonReq("POST", "/characters/roll", map[string]any{"age": age}))
	if w.Code != http.StatusOK {
		t.Fatalf("roll want 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp rollDraftResp
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode roll response: %v", err)
	}
	return resp
}

func stubGenerateCharacterNarrative(t *testing.T, fn func(context.Context, agent.GenerateCharacterReq) (*agent.GeneratedCharacter, error)) {
	t.Helper()
	prev := generateCharacterNarrative
	generateCharacterNarrative = fn
	t.Cleanup(func() { generateCharacterNarrative = prev })
}

func TestRollCharacterDraft_Success(t *testing.T) {
	initTestDB(t)
	config.Global.JWT.Secret = "test-secret"
	uid := seedUser(t, "alice", models.RoleUser, 0, 3)
	r := draftRouter(uid)

	resp := rollDraftForTest(t, r, 42)
	if resp.DraftID == 0 || resp.Token == "" {
		t.Fatalf("missing draft token: %+v", resp)
	}
	if resp.Stats.HP != resp.Stats.MaxHP || resp.Stats.MP != resp.Stats.MaxMP || resp.Stats.SAN != resp.Stats.POW {
		t.Fatalf("derived stats not initialized: %+v", resp.Stats)
	}
	if resp.SkillDefaults["母语"] != resp.Stats.EDU || resp.SkillDefaults["闪避"] != resp.Stats.DEX/2 {
		t.Fatalf("skill defaults not derived from stats")
	}
	if resp.RawRolls.Age != 42 || len(resp.RawRolls.AgeLog) == 0 {
		t.Fatalf("missing age log: %+v", resp.RawRolls)
	}
}

func TestRollCharacterDraft_InvalidAge(t *testing.T) {
	initTestDB(t)
	uid := seedUser(t, "alice", models.RoleUser, 0, 3)
	r := draftRouter(uid)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, jsonReq("POST", "/characters/roll", map[string]any{"age": 17}))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d: %s", w.Code, w.Body.String())
	}
}

func TestRollCharacterDraft_TooManyDrafts(t *testing.T) {
	initTestDB(t)
	uid := seedUser(t, "alice", models.RoleUser, 0, 3)
	r := draftRouter(uid)

	for i := 0; i < 3; i++ {
		rollDraftForTest(t, r, 25+i)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, jsonReq("POST", "/characters/roll", map[string]any{"age": 30}))
	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("want 429, got %d: %s", w.Code, w.Body.String())
	}
}

func TestFinalizeCharacterDraft_Success(t *testing.T) {
	initTestDB(t)
	config.Global.JWT.Secret = "test-secret"
	uid := seedUser(t, "alice", models.RoleUser, 0, 3)
	r := draftRouter(uid)
	roll := rollDraftForTest(t, r, 42)
	var gotAIReq agent.GenerateCharacterReq
	stubGenerateCharacterNarrative(t, func(ctx context.Context, req agent.GenerateCharacterReq) (*agent.GeneratedCharacter, error) {
		gotAIReq = req
		return &agent.GeneratedCharacter{
			Backstory:  "AI背景",
			Appearance: "AI外貌",
			Traits:     "AI性格",
			Stats:      &models.CharacterStats{STR: 999},
		}, nil
	})

	w := httptest.NewRecorder()
	r.ServeHTTP(w, jsonReq("POST", "/characters/finalize", map[string]any{
		"draft_id":          roll.DraftID,
		"token":             roll.Token,
		"name":              "调查员",
		"gender":            "男",
		"occupation":        "记者",
		"birthplace":        "上海",
		"residence":         "伦敦",
		"background_prompt": "没落家族，语气冷峻",
		"backstory":         "玩家填写的成品背景不应保存",
		"appearance":        "玩家填写的外貌不应保存",
		"traits":            "玩家填写的性格不应保存",
		"stats":             map[string]any{"str": 999},
		"skills":            map[string]any{"侦查": roll.SkillDefaults["侦查"] + 10, "母语": 1, "闪避": 1},
	}))
	if w.Code != http.StatusCreated {
		t.Fatalf("want 201, got %d: %s", w.Code, w.Body.String())
	}
	var card models.CharacterCard
	if err := json.NewDecoder(w.Body).Decode(&card); err != nil {
		t.Fatalf("decode card: %v", err)
	}
	if card.Stats.Data.STR == 999 {
		t.Fatal("finalize trusted client stats")
	}
	if card.Stats.Data.STR != roll.Stats.STR || card.Age != 42 {
		t.Fatalf("card stats/age mismatch: %+v vs %+v", card.Stats.Data, roll.Stats)
	}
	if card.Skills.Data["母语"] != roll.Stats.EDU || card.Skills.Data["闪避"] != roll.Stats.DEX/2 {
		t.Fatalf("locked skills were overridden: %+v", card.Skills.Data)
	}
	if card.Backstory != "AI背景" || card.Appearance != "AI外貌" || card.Traits != "AI性格" {
		t.Fatalf("narrative not generated by AI: backstory=%q appearance=%q traits=%q", card.Backstory, card.Appearance, card.Traits)
	}
	if gotAIReq.Name != "调查员" || gotAIReq.Gender != "男" || gotAIReq.Occupation != "记者" || gotAIReq.Background != "没落家族，语气冷峻" || gotAIReq.Age != 42 {
		t.Fatalf("AI request mismatch: %+v", gotAIReq)
	}
	if gotAIReq.Stats.STR != roll.Stats.STR || gotAIReq.Stats.INT != roll.Stats.INT {
		t.Fatalf("AI received wrong draft stats: %+v vs %+v", gotAIReq.Stats, roll.Stats)
	}
	var draft models.CharacterDraft
	models.DB.First(&draft, roll.DraftID)
	if !draft.IsUsed || draft.UsedAt == nil {
		t.Fatal("draft was not marked used")
	}
}

func TestFinalizeCharacterDraft_BackstoryFallbackPrompt(t *testing.T) {
	initTestDB(t)
	uid := seedUser(t, "alice", models.RoleUser, 0, 3)
	r := draftRouter(uid)
	roll := rollDraftForTest(t, r, 25)
	var gotPrompt string
	stubGenerateCharacterNarrative(t, func(ctx context.Context, req agent.GenerateCharacterReq) (*agent.GeneratedCharacter, error) {
		gotPrompt = req.Background
		return &agent.GeneratedCharacter{Backstory: "AI背景", Appearance: "AI外貌", Traits: "AI性格"}, nil
	})

	w := httptest.NewRecorder()
	r.ServeHTTP(w, jsonReq("POST", "/characters/finalize", map[string]any{
		"draft_id":  roll.DraftID,
		"token":     roll.Token,
		"name":      "调查员",
		"gender":    "女",
		"backstory": "旧字段作为提示词",
	}))
	if w.Code != http.StatusCreated {
		t.Fatalf("want 201, got %d: %s", w.Code, w.Body.String())
	}
	if gotPrompt != "旧字段作为提示词" {
		t.Fatalf("fallback prompt = %q", gotPrompt)
	}
}

func TestFinalizeCharacterDraft_AIFailureKeepsDraftUnused(t *testing.T) {
	initTestDB(t)
	uid := seedUser(t, "alice", models.RoleUser, 0, 3)
	r := draftRouter(uid)
	roll := rollDraftForTest(t, r, 25)
	stubGenerateCharacterNarrative(t, func(ctx context.Context, req agent.GenerateCharacterReq) (*agent.GeneratedCharacter, error) {
		return nil, errors.New("provider down")
	})

	w := httptest.NewRecorder()
	r.ServeHTTP(w, jsonReq("POST", "/characters/finalize", map[string]any{
		"draft_id":          roll.DraftID,
		"token":             roll.Token,
		"name":              "调查员",
		"gender":            "男",
		"background_prompt": "失败后可重试",
	}))
	if w.Code != http.StatusBadGateway {
		t.Fatalf("want 502, got %d: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "AI生成背景失败") {
		t.Fatalf("want AI error message, got: %s", w.Body.String())
	}
	var draft models.CharacterDraft
	models.DB.First(&draft, roll.DraftID)
	if draft.IsUsed || draft.UsedAt != nil {
		t.Fatalf("draft should remain unused after AI failure: %+v", draft)
	}
	var count int64
	models.DB.Model(&models.CharacterCard{}).Where("user_id = ?", uid).Count(&count)
	if count != 0 {
		t.Fatalf("created %d cards after AI failure", count)
	}
}

func TestFinalizeCharacterDraft_ReuseFails(t *testing.T) {
	initTestDB(t)
	uid := seedUser(t, "alice", models.RoleUser, 0, 3)
	r := draftRouter(uid)
	roll := rollDraftForTest(t, r, 25)
	stubGenerateCharacterNarrative(t, func(ctx context.Context, req agent.GenerateCharacterReq) (*agent.GeneratedCharacter, error) {
		return &agent.GeneratedCharacter{Backstory: "AI背景", Appearance: "AI外貌", Traits: "AI性格"}, nil
	})
	body := map[string]any{"draft_id": roll.DraftID, "token": roll.Token, "name": "调查员", "gender": "男"}

	w1 := httptest.NewRecorder()
	r.ServeHTTP(w1, jsonReq("POST", "/characters/finalize", body))
	if w1.Code != http.StatusCreated {
		t.Fatalf("first finalize want 201, got %d: %s", w1.Code, w1.Body.String())
	}
	w2 := httptest.NewRecorder()
	r.ServeHTTP(w2, jsonReq("POST", "/characters/finalize", body))
	if w2.Code != http.StatusBadRequest {
		t.Fatalf("reuse want 400, got %d: %s", w2.Code, w2.Body.String())
	}
}

func TestFinalizeCharacterDraft_TamperedTokenFails(t *testing.T) {
	initTestDB(t)
	uid := seedUser(t, "alice", models.RoleUser, 0, 3)
	r := draftRouter(uid)
	roll := rollDraftForTest(t, r, 25)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, jsonReq("POST", "/characters/finalize", map[string]any{
		"draft_id": roll.DraftID,
		"token":    roll.Token + "00",
		"name":     "调查员",
		"gender":   "男",
	}))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d: %s", w.Code, w.Body.String())
	}
}

func TestFinalizeCharacterDraft_ExpiredFails(t *testing.T) {
	initTestDB(t)
	uid := seedUser(t, "alice", models.RoleUser, 0, 3)
	r := draftRouter(uid)
	roll := rollDraftForTest(t, r, 25)
	models.DB.Model(&models.CharacterDraft{}).Where("id = ?", roll.DraftID).Update("expires_at", time.Now().Add(-time.Minute))

	w := httptest.NewRecorder()
	r.ServeHTTP(w, jsonReq("POST", "/characters/finalize", map[string]any{
		"draft_id": roll.DraftID,
		"token":    roll.Token,
		"name":     "调查员",
		"gender":   "男",
	}))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d: %s", w.Code, w.Body.String())
	}
}

func TestFinalizeCharacterDraft_OverBudgetFails(t *testing.T) {
	initTestDB(t)
	uid := seedUser(t, "alice", models.RoleUser, 0, 3)
	r := draftRouter(uid)
	roll := rollDraftForTest(t, r, 25)
	skills := map[string]any{}
	for _, name := range []string{"会计", "估价", "考古学", "魅惑", "攀爬", "计算机使用", "乔装", "驾驶(汽车)", "电气维修", "话术", "急救", "历史", "恐吓", "跳跃", "法律", "图书馆使用", "聆听", "锁匠", "机械维修", "医学", "博物学", "神秘学", "说服", "药学", "摄影", "心理学", "潜行", "游泳", "投掷", "追踪", "侦查", "斗殴", "手枪", "步枪/霰弹枪", "冲锋枪"} {
		skills[name] = 90
	}

	w := httptest.NewRecorder()
	r.ServeHTTP(w, jsonReq("POST", "/characters/finalize", map[string]any{
		"draft_id": roll.DraftID,
		"token":    roll.Token,
		"name":     "调查员",
		"gender":   "男",
		"skills":   skills,
	}))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "details") {
		t.Fatalf("want details in response: %s", w.Body.String())
	}
}

func TestGetCharacterSkillDefaults(t *testing.T) {
	initTestDB(t)
	uid := seedUser(t, "alice", models.RoleUser, 0, 3)
	r := draftRouter(uid)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, jsonReq("GET", "/characters/skill-defaults", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), fmt.Sprintf("%q", "侦查")) {
		t.Fatalf("missing skill defaults: %s", w.Body.String())
	}
}

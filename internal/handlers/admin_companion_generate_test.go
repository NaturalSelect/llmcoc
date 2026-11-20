package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"unicode"

	"github.com/gin-gonic/gin"
	"github.com/llmcoc/server/internal/middleware"
	"github.com/llmcoc/server/internal/models"
	"github.com/llmcoc/server/internal/services/agent"
	"github.com/llmcoc/server/internal/services/game"
)

type genEvent struct {
	name string
	data map[string]any
}

// eventLog 收集批量任务推送的事件；三张卡并发生成，所以要加锁。
type eventLog struct {
	mu     sync.Mutex
	events []genEvent
}

func (l *eventLog) emit(name string, data any) {
	raw, _ := json.Marshal(data)
	var m map[string]any
	_ = json.Unmarshal(raw, &m)
	l.mu.Lock()
	defer l.mu.Unlock()
	l.events = append(l.events, genEvent{name: name, data: m})
}

func (l *eventLog) named(name string) []genEvent {
	l.mu.Lock()
	defer l.mu.Unlock()
	var out []genEvent
	for _, e := range l.events {
		if e.name == name {
			out = append(out, e)
		}
	}
	return out
}

func stubCompanionCardName(t *testing.T, fn func(ctx context.Context, gender string, used map[string]bool) (string, error)) {
	t.Helper()
	prev := pickCompanionCardName
	pickCompanionCardName = fn
	t.Cleanup(func() { pickCompanionCardName = prev })
}

// sequentialCompanionNames 按调用顺序发放不重复的汉字姓名。
func sequentialCompanionNames() func(context.Context, string, map[string]bool) (string, error) {
	var n atomic.Int32
	suffixes := []rune("甲乙丙丁戊己庚辛壬癸")
	return func(_ context.Context, _ string, _ map[string]bool) (string, error) {
		i := int(n.Add(1)) - 1
		return "测试" + string(suffixes[i%len(suffixes)]), nil
	}
}

func fakeNarrative(_ context.Context, req agent.GenerateCharacterReq) (*agent.GeneratedCharacter, error) {
	return &agent.GeneratedCharacter{Backstory: req.Name + "的经历", Appearance: "外貌", Traits: "特质"}, nil
}

func botCards(t *testing.T, botID uint) []models.CharacterCard {
	t.Helper()
	var cards []models.CharacterCard
	if err := models.DB.Where("user_id = ?", botID).Order("id").Find(&cards).Error; err != nil {
		t.Fatalf("load bot cards: %v", err)
	}
	return cards
}

func isHanName(s string) bool {
	for _, r := range s {
		if !unicode.Is(unicode.Han, r) {
			return false
		}
	}
	return s != ""
}

func TestRunCompanionCardGeneration_CreatesCardsForBot(t *testing.T) {
	initTestDB(t)
	botID, err := models.EnsureCompanionUser()
	if err != nil {
		t.Fatalf("EnsureCompanionUser: %v", err)
	}
	stubGenerateCharacterNarrative(t, fakeNarrative)
	stubCompanionCardName(t, sequentialCompanionNames())

	log := &eventLog{}
	runCompanionCardGeneration(botID, 4, log.emit)

	cards := botCards(t, botID)
	if len(cards) != 4 {
		t.Fatalf("bot cards = %d, want 4", len(cards))
	}
	names := map[string]bool{}
	for _, c := range cards {
		if !c.IsActive || c.Backstory == "" || c.Appearance == "" || c.Traits == "" {
			t.Errorf("card %q incomplete: %+v", c.Name, c)
		}
		if !slices.Contains(game.Occupations, c.Occupation) {
			t.Errorf("occupation %q is not from the occupation list", c.Occupation)
		}
		if c.Age < companionGenMinAge || c.Age > companionGenMaxAge {
			t.Errorf("age %d out of [%d,%d]", c.Age, companionGenMinAge, companionGenMaxAge)
		}
		if c.Gender != models.GenderMale && c.Gender != models.GenderFemale {
			t.Errorf("gender %q invalid", c.Gender)
		}
		if names[c.Name] {
			t.Errorf("duplicate name %q", c.Name)
		}
		names[c.Name] = true
	}

	if got := len(log.named("card_start")); got != 4 {
		t.Errorf("card_start events = %d, want 4", got)
	}
	if got := len(log.named("card_done")); got != 4 {
		t.Errorf("card_done events = %d, want 4", got)
	}
	done := log.named("batch_done")
	if len(done) != 1 || done[0].data["total"] != float64(4) || done[0].data["succeeded"] != float64(4) || done[0].data["failed"] != float64(0) {
		t.Fatalf("batch_done = %+v, want total=4 succeeded=4 failed=0", done)
	}
	if last := log.events[len(log.events)-1]; last.name != "batch_done" {
		t.Errorf("last event = %q, want batch_done", last.name)
	}
}

func TestRunCompanionCardGeneration_OneFailureDoesNotStopOthers(t *testing.T) {
	initTestDB(t)
	botID, _ := models.EnsureCompanionUser()
	stubCompanionCardName(t, sequentialCompanionNames())
	var calls atomic.Int32
	stubGenerateCharacterNarrative(t, func(ctx context.Context, req agent.GenerateCharacterReq) (*agent.GeneratedCharacter, error) {
		if calls.Add(1) == 2 {
			return nil, errors.New("模型超时")
		}
		return fakeNarrative(ctx, req)
	})

	log := &eventLog{}
	runCompanionCardGeneration(botID, 4, log.emit)

	if got := len(botCards(t, botID)); got != 3 {
		t.Fatalf("bot cards = %d, want 3", got)
	}
	errs := log.named("card_error")
	if len(errs) != 1 {
		t.Fatalf("card_error events = %+v, want 1", errs)
	}
	if msg, _ := errs[0].data["message"].(string); !strings.Contains(msg, "AI生成失败") || !strings.Contains(msg, "模型超时") {
		t.Errorf("card_error message = %q, want the generation failure", msg)
	}
	done := log.named("batch_done")
	if len(done) != 1 || done[0].data["succeeded"] != float64(3) || done[0].data["failed"] != float64(1) {
		t.Fatalf("batch_done = %+v, want succeeded=3 failed=1", done)
	}
}

func TestRunCompanionCardGeneration_NameFailureCreatesNoCard(t *testing.T) {
	initTestDB(t)
	botID, _ := models.EnsureCompanionUser()
	stubGenerateCharacterNarrative(t, fakeNarrative)
	stubCompanionCardName(t, func(context.Context, string, map[string]bool) (string, error) {
		return "", errors.New("姓名池已用尽")
	})

	log := &eventLog{}
	runCompanionCardGeneration(botID, 2, log.emit)

	if got := len(botCards(t, botID)); got != 0 {
		t.Fatalf("bot cards = %d, want 0", got)
	}
	errs := log.named("card_error")
	if len(errs) != 2 {
		t.Fatalf("card_error events = %d, want 2", len(errs))
	}
	if msg, _ := errs[0].data["message"].(string); !strings.Contains(msg, "取名失败") {
		t.Errorf("message = %q, want 取名失败", msg)
	}
}

func TestRunCompanionCardGeneration_ExistingPoolNamesAreExcluded(t *testing.T) {
	initTestDB(t)
	botID, _ := models.EnsureCompanionUser()
	seedCard(t, botID, "陈守拙")
	stubGenerateCharacterNarrative(t, fakeNarrative)
	var seenUsed atomic.Bool
	stubCompanionCardName(t, func(_ context.Context, _ string, used map[string]bool) (string, error) {
		if used["陈守拙"] {
			seenUsed.Store(true)
		}
		return "测试甲", nil
	})

	runCompanionCardGeneration(botID, 1, (&eventLog{}).emit)

	if !seenUsed.Load() {
		t.Error("name picker did not receive the existing pool name as used")
	}
}

func TestRunCompanionCardGeneration_NamesAreUniqueAcrossConcurrentCards(t *testing.T) {
	initTestDB(t)
	botID, _ := models.EnsureCompanionUser()
	stubGenerateCharacterNarrative(t, fakeNarrative)
	// NOTE: 只发放「还没被登记」的姓名：如果并发的两张卡看不到彼此登记的姓名，就会拿到同一个。
	stubCompanionCardName(t, func(_ context.Context, _ string, used map[string]bool) (string, error) {
		for _, n := range []string{"测试甲", "测试乙", "测试丙", "测试丁", "测试戊", "测试己"} {
			if !used[n] {
				return n, nil
			}
		}
		return "", errors.New("exhausted")
	})

	log := &eventLog{}
	runCompanionCardGeneration(botID, 6, log.emit)

	seen := map[string]bool{}
	for _, c := range botCards(t, botID) {
		if seen[c.Name] {
			t.Fatalf("duplicate name %q", c.Name)
		}
		seen[c.Name] = true
	}
	if len(seen) != 6 {
		t.Fatalf("cards = %d, want 6", len(seen))
	}
}

func TestRunCompanionCardGeneration_NamesAreChineseWithoutLLM(t *testing.T) {
	initTestDB(t)
	botID, _ := models.EnsureCompanionUser()
	stubGenerateCharacterNarrative(t, fakeNarrative)

	log := &eventLog{}
	runCompanionCardGeneration(botID, 5, log.emit)

	cards := botCards(t, botID)
	if len(cards) != 5 {
		t.Fatalf("bot cards = %d, want 5", len(cards))
	}
	for _, c := range cards {
		if !isHanName(strings.ReplaceAll(c.Name, "·", "")) {
			t.Errorf("card name %q is not Chinese", c.Name)
		}
	}
}

func companionGenWSServer(t *testing.T) *httptest.Server {
	t.Helper()
	r := gin.New()
	g := r.Group("/admin", withAuth(1, "root", "admin"), middleware.AdminRequired())
	g.GET("/companion-cards/generate/ws", AdminGenerateCompanionCardsWS)
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)
	return srv
}

func TestAdminGenerateCompanionCardsWS_RejectsBadRequests(t *testing.T) {
	initTestDB(t)
	srv := companionGenWSServer(t)

	t.Run("数量超过上限", func(t *testing.T) {
		if _, err := models.EnsureCompanionUser(); err != nil {
			t.Fatal(err)
		}
		c, _ := scenarioWSDial(t, srv, "/admin/companion-cards/generate/ws", map[string]any{"count": maxCompanionGenCount + 1})
		f := wsRead(t, c)
		if f.Type != "rejected" || !strings.Contains(wsMessage(t, f), "不能超过") {
			t.Fatalf("frame = %s %s, want rejected 不能超过", f.Type, f.Data)
		}
		wsExpectClosed(t, c)
	})

	t.Run("已有任务在运行", func(t *testing.T) {
		companionGenRunning.Store(true)
		t.Cleanup(func() { companionGenRunning.Store(false) })
		c, _ := scenarioWSDial(t, srv, "/admin/companion-cards/generate/ws", map[string]any{"count": 1})
		f := wsRead(t, c)
		if f.Type != "rejected" || !strings.Contains(wsMessage(t, f), "已有批量生成任务在运行") {
			t.Fatalf("frame = %s %s, want rejected 已有批量生成任务在运行", f.Type, f.Data)
		}
		wsExpectClosed(t, c)
	})
}

func TestAdminGenerateCompanionCardsWS_NoBotUserRejected(t *testing.T) {
	initTestDB(t)
	srv := companionGenWSServer(t)

	c, _ := scenarioWSDial(t, srv, "/admin/companion-cards/generate/ws", map[string]any{"count": 1})
	f := wsRead(t, c)
	if f.Type != "rejected" || !strings.Contains(wsMessage(t, f), "AI 陪玩用户不存在") {
		t.Fatalf("frame = %s %s, want rejected 用户不存在", f.Type, f.Data)
	}
	wsExpectClosed(t, c)
}

func TestAdminGenerateCompanionCardsWS_StreamsAndReleasesTheSlot(t *testing.T) {
	initTestDB(t)
	botID, _ := models.EnsureCompanionUser()
	stubGenerateCharacterNarrative(t, fakeNarrative)
	stubCompanionCardName(t, sequentialCompanionNames())
	srv := companionGenWSServer(t)

	c, _ := scenarioWSDial(t, srv, "/admin/companion-cards/generate/ws", map[string]any{"count": 2})
	var types []string
	for f := range c.frames {
		types = append(types, f.Type)
		if f.Type == "batch_done" {
			var d struct{ Total, Succeeded, Failed int }
			if err := json.Unmarshal(f.Data, &d); err != nil || d.Total != 2 || d.Succeeded != 2 || d.Failed != 0 {
				t.Fatalf("batch_done = %s (err %v), want total=2 succeeded=2", f.Data, err)
			}
		}
	}
	if len(types) != 5 || types[len(types)-1] != "batch_done" {
		t.Fatalf("frames = %v, want 2x card_start + 2x card_done + batch_done", types)
	}
	if got := len(botCards(t, botID)); got != 2 {
		t.Fatalf("bot cards = %d, want 2", got)
	}
	if companionGenRunning.Load() {
		t.Fatal("running flag still set after the job finished")
	}
}

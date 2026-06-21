package agent

import (
	"context"
	"encoding/json"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/llmcoc/server/internal/models"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

type fakeImageProvider struct {
	fakeProvider
	base64Data string
	mimeType   string
	prompt     string
	size       string
	err        error
	called     atomic.Bool
	block      <-chan struct{}
}

func (f *fakeImageProvider) GenerateImage(ctx context.Context, prompt string, size string) (string, string, error) {
	f.called.Store(true)
	f.prompt = prompt
	f.size = size
	if f.block != nil {
		select {
		case <-f.block:
		case <-ctx.Done():
			return "", "", ctx.Err()
		}
	}
	if f.err != nil {
		return "", "", f.err
	}
	return f.base64Data, f.mimeType, nil
}

func TestResponseActionFormatsOptionsAndPayload(t *testing.T) {
	hasEnd := false
	narration := ""
	actx := ActionContext{
		Sid:         1,
		HasEnd:      &hasEnd,
		KPNarration: &narration,
	}

	responseAction{}.Execute(ToolCall{
		Reply:   "你想先检查哪一处？",
		Options: []string{"书桌", "窗台", "书桌", "书架"},
	}, actx)

	if !hasEnd {
		t.Fatal("response should end the turn")
	}
	if strings.Contains(narration, "可以回复多个编号") {
		t.Fatalf("narration should keep option hints in payload only: %s", narration)
	}
	if strings.Count(narration, "书桌") != 1 {
		t.Fatalf("payload should contain deduplicated option once: %s", narration)
	}

	start := strings.Index(narration, "<response_options>")
	end := strings.Index(narration, "</response_options>")
	if start < 0 || end < 0 || end <= start {
		t.Fatalf("missing response_options payload: %s", narration)
	}
	raw := narration[start+len("<response_options>") : end]
	var payload responseOptionsPayload
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		t.Fatalf("payload is not json: %v", err)
	}
	if len(payload.Options) != 3 {
		t.Fatalf("unexpected payload: %+v", payload)
	}
}

func TestFallbackWriterDirectionUsesVisibleKPReply(t *testing.T) {
	direction := fallbackWriterDirection("你回到阁楼。\n<response_options>{\"options\":[\"离开\"]}</response_options>\n<ack>tool</ack>")
	if !strings.Contains(direction, "你回到阁楼。") {
		t.Fatalf("direction should include player visible reply: %q", direction)
	}
	if strings.Contains(direction, "response_options") || strings.Contains(direction, "<ack>") {
		t.Fatalf("direction should strip internal tags: %q", direction)
	}
}

func TestToolCallUnmarshalPreservesImagePrompt(t *testing.T) {
	raw := `[{"action":"generate_image","image_prompt":"A foggy lighthouse at night","characters":["约翰","艾琳"]}]`
	var calls []ToolCall
	if err := json.Unmarshal([]byte(raw), &calls); err != nil {
		t.Fatalf("json.Unmarshal failed: %v", err)
	}
	if len(calls) != 1 {
		t.Fatalf("expected 1 call, got %d", len(calls))
	}
	if calls[0].Action != ToolGenerateImage || calls[0].ImagePrompt != "A foggy lighthouse at night" {
		t.Fatalf("image prompt not preserved: %+v", calls[0])
	}
	if len(calls[0].Characters) != 2 || calls[0].Characters[0] != "约翰" || calls[0].Characters[1] != "艾琳" {
		t.Fatalf("image characters not preserved: %+v", calls[0].Characters)
	}
}

func TestGenerateImageActionQueuesPromptWithoutCallingGenerator(t *testing.T) {
	provider := &fakeImageProvider{base64Data: "YWJj", mimeType: "image/jpeg"}
	var prompts []ImagePromptRequest
	actx := ActionContext{
		Ctx:  context.Background(),
		GCtx: &GameContext{},
		Handles: map[models.AgentRole]agentHandle{
			models.AgentRolePainter: {
				provider: provider,
				enabled:  true,
				config:   &models.AgentConfig{IsActive: true},
			},
		},
		PendingImages: &prompts,
	}

	results := generateImageAction{}.Execute(ToolCall{ImagePrompt: "A foggy lighthouse", Characters: []string{"约翰", " ", "约翰", "艾琳"}}, actx)
	if provider.called.Load() || provider.prompt != "" || provider.size != "" {
		t.Fatalf("generator should not be called during KP tool execution, called=%v prompt=%q size=%q", provider.called.Load(), provider.prompt, provider.size)
	}
	if len(prompts) != 1 || prompts[0] != (ImagePromptRequest{Prompt: "A foggy lighthouse"}) {
		t.Fatalf("queued prompts = %#v", prompts)
	}
	if len(results) != 1 || !strings.Contains(results[0].Result, "queued") {
		t.Fatalf("unexpected tool result: %+v", results)
	}
	if strings.Contains(results[0].Result, "YWJj") || strings.Contains(results[0].Result, "data:image") {
		t.Fatalf("tool result leaked image data: %q", results[0].Result)
	}
}

func TestGenerateImageActionDoesNotBlockOnGenerator(t *testing.T) {
	provider := &fakeImageProvider{block: make(chan struct{})}
	var prompts []ImagePromptRequest
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	actx := ActionContext{
		Ctx:  ctx,
		GCtx: &GameContext{},
		Handles: map[models.AgentRole]agentHandle{
			models.AgentRolePainter: {
				provider: provider,
				enabled:  true,
				config:   &models.AgentConfig{IsActive: true},
			},
		},
		PendingImages: &prompts,
	}

	start := time.Now()
	done := make(chan []ToolResult, 1)
	go func() {
		done <- generateImageAction{}.Execute(ToolCall{ImagePrompt: "A foggy lighthouse"}, actx)
	}()
	var results []ToolResult
	select {
	case results = <-done:
	case <-time.After(100 * time.Millisecond):
		cancel()
		t.Fatal("generate_image Execute blocked on image generator")
	}
	if elapsed := time.Since(start); elapsed > 100*time.Millisecond {
		t.Fatalf("generate_image Execute blocked for %v", elapsed)
	}
	if provider.called.Load() {
		t.Fatal("GenerateImage must not be called by Execute")
	}
	if len(results) != 1 || !strings.Contains(results[0].Result, "queued") {
		t.Fatalf("unexpected tool result: %+v", results)
	}
}

func TestGenerateImageActionUnavailableWithoutPainter(t *testing.T) {
	var prompts []ImagePromptRequest
	results := generateImageAction{}.Execute(ToolCall{ImagePrompt: "A foggy lighthouse"}, ActionContext{
		Ctx:           context.Background(),
		GCtx:          &GameContext{},
		Handles:       map[models.AgentRole]agentHandle{},
		PendingImages: &prompts,
	})
	if len(prompts) != 0 {
		t.Fatalf("image prompt should not be queued without painter: %#v", prompts)
	}
	if len(results) != 1 || !strings.Contains(results[0].Result, "unavailable") {
		t.Fatalf("unexpected tool result: %+v", results)
	}
}

func TestRunPainterSendsPromptAsProvided(t *testing.T) {
	const sessionID uint = 424301
	provider := &fakeImageProvider{base64Data: "YWJj", mimeType: "image/png"}
	writer := &fakeProvider{resp: "John is depicted as a gaunt investigator in an emerald raincoat."}
	sessionAgents.Store(sessionID, map[models.AgentRole]agentHandle{
		models.AgentRolePainter: {provider: provider, enabled: true},
		models.AgentRoleWriter:  {provider: writer, enabled: true},
	})
	t.Cleanup(func() { deleteCachedAgents(sessionID) })

	prompt := "A foggy lighthouse at night"
	dataURL, err := RunPainter(context.Background(), GameContext{Session: models.GameSession{ID: sessionID}}, ImagePromptRequest{Prompt: prompt})
	if err != nil {
		t.Fatalf("RunPainter failed: %v", err)
	}
	if dataURL != "data:image/png;base64,YWJj" {
		t.Fatalf("unexpected data URL: %q", dataURL)
	}
	if provider.prompt != prompt {
		t.Fatalf("RunPainter prompt = %q, want raw prompt %q", provider.prompt, prompt)
	}
	if provider.size != "1024x1024" {
		t.Fatalf("size=%q, want 1024x1024", provider.size)
	}
	if len(writer.messages) != 0 {
		t.Fatalf("RunPainter should not call Writer, got %d messages", len(writer.messages))
	}
}

func TestDescribeCharactersUsesWriterVisualDescription(t *testing.T) {
	writer := &fakeProvider{resp: "John is a gaunt investigator with sunken eyes behind round spectacles and a torn emerald raincoat."}
	actx := ActionContext{
		Ctx: context.Background(),
		GCtx: &GameContext{Session: models.GameSession{
			ID: 424302,
			Players: []models.SessionPlayer{{
				CharacterCard: models.CharacterCard{
					ID:         1,
					Name:       "约翰",
					Appearance: "RAW_APPEARANCE_DETAIL: sunken eyes behind round spectacles and a torn emerald raincoat.",
					Inventory:  models.JSONField[[]string]{Data: []string{"silver revolver", "field camera serial RAW-123"}},
				},
			}},
		}},
		Handles: map[models.AgentRole]agentHandle{
			models.AgentRoleWriter: {provider: writer, enabled: true},
		},
	}

	results := describeCharactersAction{}.Execute(ToolCall{Characters: []string{"  约翰  ", "不存在", "约翰"}}, actx)
	if len(results) != 1 || results[0].Action != ToolDescribeCharacters {
		t.Fatalf("unexpected result: %+v", results)
	}
	if results[0].Result != writer.resp {
		t.Fatalf("describe_characters result = %q, want writer response %q", results[0].Result, writer.resp)
	}
	if len(writer.messages) != 2 {
		t.Fatalf("writer messages = %d, want 2", len(writer.messages))
	}
	writerUserPrompt := writer.messages[1].Content
	for _, want := range []string{"约翰", "RAW_APPEARANCE_DETAIL", "field camera serial RAW-123", "silver revolver"} {
		if !strings.Contains(writerUserPrompt, want) {
			t.Fatalf("writer prompt missing raw card data %q: %q", want, writerUserPrompt)
		}
	}
}

func TestDescribeCharactersFallsBackToAppearanceOnly(t *testing.T) {
	writer := &fakeProvider{err: context.Canceled}
	actx := ActionContext{
		Ctx: context.Background(),
		GCtx: &GameContext{Session: models.GameSession{
			ID: 424303,
			Players: []models.SessionPlayer{{
				CharacterCard: models.CharacterCard{
					ID:         1,
					Name:       "约翰",
					Appearance: "brass goggles and a patched black coat.",
					Inventory:  models.JSONField[[]string]{Data: []string{"field camera serial RAW-456"}},
				},
			}},
		}},
		Handles: map[models.AgentRole]agentHandle{
			models.AgentRoleWriter: {provider: writer, enabled: true},
		},
	}

	results := describeCharactersAction{}.Execute(ToolCall{Characters: []string{"约翰"}}, actx)
	if len(results) != 1 || results[0].Action != ToolDescribeCharacters {
		t.Fatalf("unexpected result: %+v", results)
	}
	for _, want := range []string{"约翰", "brass goggles", "patched black coat"} {
		if !strings.Contains(results[0].Result, want) {
			t.Fatalf("fallback result missing %q: %q", want, results[0].Result)
		}
	}
	if strings.Contains(results[0].Result, "field camera serial RAW-456") {
		t.Fatalf("fallback result should not include inventory details: %q", results[0].Result)
	}
	if len(writer.messages) != 2 {
		t.Fatalf("writer should be attempted before fallback, got %d messages", len(writer.messages))
	}
}

func TestDescribeCharactersFallsBackWhenWriterUnavailable(t *testing.T) {
	actx := ActionContext{
		Ctx: context.Background(),
		GCtx: &GameContext{Session: models.GameSession{
			ID: 424304,
			Players: []models.SessionPlayer{{
				CharacterCard: models.CharacterCard{
					ID:         1,
					Name:       "约翰",
					Appearance: "brass goggles and a patched black coat.",
					Inventory:  models.JSONField[[]string]{Data: []string{"field camera serial RAW-456"}},
				},
			}},
		}},
		Handles: map[models.AgentRole]agentHandle{},
	}

	results := describeCharactersAction{}.Execute(ToolCall{Characters: []string{"约翰"}}, actx)
	if len(results) != 1 || !strings.Contains(results[0].Result, "brass goggles") {
		t.Fatalf("unexpected fallback result: %+v", results)
	}
}

func TestBuildCharacterDetailIncludesAssets(t *testing.T) {
	players := []models.SessionPlayer{{
		CharacterCard: models.CharacterCard{
			Name: "调查员",
			Assets: models.JSONField[[]models.Asset]{Data: []models.Asset{{
				Name:     "老宅",
				Category: "不动产",
				Note:     "祖宅",
			}}},
		},
	}}

	detail := buildCharacterDetail("调查员", players)
	if !strings.Contains(detail, `<assets><asset n="老宅" cat="不动产" note="祖宅"/></assets>`) {
		t.Fatalf("character detail should include assets: %s", detail)
	}
}

func TestManageAssetAddUpdateRemovePersists(t *testing.T) {
	initAgentActionTestDB(t)
	card := models.CharacterCard{
		Name:   "调查员",
		Stats:  models.JSONField[models.CharacterStats]{},
		Skills: models.JSONField[map[string]int]{Data: map[string]int{}},
		Assets: models.JSONField[[]models.Asset]{Data: []models.Asset{{
			Name:     "老宅",
			Category: "不动产",
			Note:     "祖宅",
		}}},
	}
	if err := models.DB.Create(&card).Error; err != nil {
		t.Fatalf("create card: %v", err)
	}
	players := []models.SessionPlayer{{CharacterCard: card}}

	if got := manageAsset(players, "调查员", "add", &models.Asset{Name: "汽车", Category: "载具", Note: "破旧"}); !strings.Contains(got, "更新资产:汽车") {
		t.Fatalf("unexpected add result: %s", got)
	}
	if got := manageAsset(players, "调查员", "update", &models.Asset{Name: "汽车", Category: "载具", Note: "已修好"}); !strings.Contains(got, "更新资产:汽车") {
		t.Fatalf("unexpected update result: %s", got)
	}
	if got := manageAsset(players, "调查员", "remove", &models.Asset{Name: "老宅"}); !strings.Contains(got, "移除资产:老宅") {
		t.Fatalf("unexpected remove result: %s", got)
	}

	var stored models.CharacterCard
	if err := models.DB.First(&stored, card.ID).Error; err != nil {
		t.Fatalf("load stored card: %v", err)
	}
	if len(stored.Assets.Data) != 1 || stored.Assets.Data[0].Name != "汽车" || stored.Assets.Data[0].Note != "已修好" {
		t.Fatalf("assets not persisted as expected: %#v", stored.Assets.Data)
	}
}

func initAgentActionTestDB(t *testing.T) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("open sql db: %v", err)
	}
	sqlDB.SetMaxOpenConns(1)
	if err := db.AutoMigrate(&models.CharacterCard{}); err != nil {
		t.Fatalf("auto-migrate: %v", err)
	}
	prev := models.DB
	models.DB = db
	t.Cleanup(func() {
		models.DB = prev
		_ = sqlDB.Close()
	})
}

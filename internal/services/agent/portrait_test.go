// NOTE: portrait_test.go 验证立绘生成：Writer 收到外貌和已装备物品、Painter 按竖版出图、
// 扩散模式先过 translator、Writer 失败时不调用 Painter、始终按全年龄渲染。禁止真实网络与真实 LLM。
package agent

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/llmcoc/server/internal/models"
	"github.com/llmcoc/server/internal/services/llm"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func portraitTestCard() *models.CharacterCard {
	return &models.CharacterCard{
		ID:         7,
		Name:       "艾琳",
		Appearance: "黑色短发，灰蓝色眼睛，身形瘦削",
		Inventory: models.JSONField[[]models.Item]{Data: []models.Item{
			{Name: "风衣", Desc: "深灰色", Count: 1, Type: models.ItemTypeEquipment, Equipped: true},
			{Name: "左轮手枪", Count: 1, Type: models.ItemTypeWeapon},
		}},
	}
}

func TestGeneratePortraitWith_WriterThenPainterPortrait(t *testing.T) {
	writer := &chatFakeProvider{chatResponses: []string{"黑色短发的瘦削女性，身穿深灰色风衣。"}}
	painter := &recordingImageProvider{}

	dataURL, err := generatePortraitWith(context.Background(),
		newWriterTestHandle(writer),
		newPainterTestHandle(models.AgentRolePainterHQ, painter, true, false),
		agentHandle{}, portraitTestCard())
	if err != nil {
		t.Fatalf("generatePortraitWith error: %v", err)
	}
	if dataURL != "data:image/png;base64,ZmFrZS1pbWFnZQ==" {
		t.Errorf("dataURL = %q", dataURL)
	}

	if len(writer.chatMessages) != 1 {
		t.Fatalf("writer calls = %d, want 1", len(writer.chatMessages))
	}
	msgs := writer.chatMessages[0]
	if !strings.Contains(msgs[1].Content, "黑色短发，灰蓝色眼睛，身形瘦削") {
		t.Errorf("writer user prompt misses appearance: %q", msgs[1].Content)
	}
	if !strings.Contains(msgs[1].Content, "风衣") {
		t.Errorf("writer user prompt misses equipped item: %q", msgs[1].Content)
	}
	if strings.Contains(msgs[1].Content, "左轮手枪") {
		t.Errorf("writer user prompt must not include unequipped items: %q", msgs[1].Content)
	}
	if !strings.Contains(msgs[0].Content, nsfwAppearancePolicyOff) {
		t.Errorf("writer system prompt must render the all-ages appearance policy")
	}
	if got := writer.chatKeys[0]; got != portraitCacheSession+":painter" {
		t.Errorf("writer cache key = %q", got)
	}

	if painter.calls != 1 {
		t.Fatalf("GenerateImage calls = %d, want 1", painter.calls)
	}
	if painter.opts.Aspect != llm.ImageAspectPortrait || painter.opts.Diffusion {
		t.Errorf("opts = %+v, want portrait aspect without diffusion", painter.opts)
	}
	for _, want := range []string{"调查员角色立绘", "黑色短发的瘦削女性，身穿深灰色风衣。", "日式漫画", "全身立绘", nsfwImagePolicyOff} {
		if !strings.Contains(painter.prompt, want) {
			t.Errorf("painter prompt misses %q: %q", want, painter.prompt)
		}
	}
}

func TestGeneratePortraitWith_DiffusionModeTranslatesWithAllAgesNegative(t *testing.T) {
	writer := &chatFakeProvider{chatResponses: []string{"黑色短发的瘦削女性，身穿深灰色风衣。"}}
	translatorProv := &chatFakeProvider{chatResponses: []string{"1girl, black hair, grey coat, full body"}}
	painter := &recordingImageProvider{}

	_, err := generatePortraitWith(context.Background(),
		newWriterTestHandle(writer),
		newPainterTestHandle(models.AgentRolePainterHQ, painter, true, true),
		newDiffusionTranslatorHandle(translatorProv), portraitTestCard())
	if err != nil {
		t.Fatalf("generatePortraitWith error: %v", err)
	}
	if painter.prompt != "1girl, black hair, grey coat, full body" {
		t.Errorf("painter prompt = %q, want translated tags", painter.prompt)
	}
	wantOpts := llm.ImageOptions{Aspect: llm.ImageAspectPortrait, Diffusion: true, NegativePrompt: allAgesNegativeTags + ", " + diffusionBaseNegativePrompt}
	if painter.opts != wantOpts {
		t.Errorf("opts = %+v, want %+v", painter.opts, wantOpts)
	}
	if len(translatorProv.chatMessages) != 1 {
		t.Fatalf("translator calls = %d, want 1", len(translatorProv.chatMessages))
	}
	translatorInput := translatorProv.chatMessages[0][1].Content
	if !strings.Contains(translatorInput, "全身") || !strings.Contains(translatorInput, "身穿深灰色风衣") {
		t.Errorf("translator input must carry the composition and the description: %q", translatorInput)
	}
}

func TestGeneratePortraitWith_DiffusionModeNeedsTranslator(t *testing.T) {
	writer := &chatFakeProvider{chatResponses: []string{"描述"}}
	painter := &recordingImageProvider{}

	_, err := generatePortraitWith(context.Background(),
		newWriterTestHandle(writer),
		newPainterTestHandle(models.AgentRolePainterHQ, painter, true, true),
		agentHandle{}, portraitTestCard())
	if err == nil || !strings.Contains(err.Error(), string(models.AgentRoleDiffusionTranslator)) {
		t.Fatalf("err = %v, want error naming diffusion_translator", err)
	}
	if painter.calls != 0 {
		t.Errorf("GenerateImage calls = %d, want 0", painter.calls)
	}
}

func TestGeneratePortraitWith_WriterFailureSkipsPainter(t *testing.T) {
	cases := map[string]*chatFakeProvider{
		"error": {chatErr: errors.New("writer down")},
		"empty": {chatResponses: []string{"   "}},
	}
	for name, writer := range cases {
		t.Run(name, func(t *testing.T) {
			painter := &recordingImageProvider{}
			_, err := generatePortraitWith(context.Background(),
				newWriterTestHandle(writer),
				newPainterTestHandle(models.AgentRolePainterHQ, painter, true, false),
				agentHandle{}, portraitTestCard())
			if err == nil {
				t.Fatal("want error when the writer fails")
			}
			if painter.calls != 0 {
				t.Errorf("GenerateImage calls = %d, want 0", painter.calls)
			}
		})
	}
}

func TestGeneratePortraitWith_PainterWithoutImageSupportSkipsWriter(t *testing.T) {
	writer := &chatFakeProvider{chatResponses: []string{"描述"}}
	_, err := generatePortraitWith(context.Background(),
		newWriterTestHandle(writer),
		newPainterTestHandle(models.AgentRolePainterHQ, &chatFakeProvider{}, true, false),
		agentHandle{}, portraitTestCard())
	if err == nil {
		t.Fatal("want error when painter provider cannot generate images")
	}
	if !strings.Contains(err.Error(), string(models.AgentRolePainterHQ)) {
		t.Errorf("err = %v, want error naming painter_hq", err)
	}
	if len(writer.chatKeys) != 0 {
		t.Errorf("writer must not be called when the painter cannot run, got %v", writer.chatKeys)
	}
}

func TestGeneratePortrait_DoesNotFallBackToPainter(t *testing.T) {
	for _, name := range []string{"missing", "disabled"} {
		t.Run(name, func(t *testing.T) {
			db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
			if err != nil {
				t.Fatalf("open db: %v", err)
			}
			sqlDB, err := db.DB()
			if err != nil {
				t.Fatalf("get sql db: %v", err)
			}
			sqlDB.SetMaxOpenConns(1)
			if err := db.AutoMigrate(&models.LLMProviderConfig{}, &models.AgentConfig{}); err != nil {
				t.Fatalf("auto-migrate: %v", err)
			}
			prev := models.DB
			models.DB = db
			t.Cleanup(func() {
				models.DB = prev
				_ = sqlDB.Close()
			})

			prov := models.LLMProviderConfig{Name: "p", Provider: "openai", BaseURL: "http://127.0.0.1:1", IsActive: true}
			if err := db.Create(&prov).Error; err != nil {
				t.Fatalf("create provider: %v", err)
			}
			// NOTE: writer 和 painter 都启用，painter_hq 缺失或被禁用，立绘仍必须失败。
			rows := []models.AgentConfig{
				{Role: models.AgentRoleWriter, ProviderConfigID: &prov.ID, ModelName: "m", IsActive: true},
				{Role: models.AgentRolePainter, ProviderConfigID: &prov.ID, ModelName: "m", IsActive: true},
			}
			if name == "disabled" {
				rows = append(rows, models.AgentConfig{Role: models.AgentRolePainterHQ, ProviderConfigID: &prov.ID, ModelName: "m", IsActive: false})
			}
			for i := range rows {
				if err := db.Create(&rows[i]).Error; err != nil {
					t.Fatalf("create %s config: %v", rows[i].Role, err)
				}
			}
			// NOTE: IsActive 带 default:true，Create 会吞掉零值 false，需要建后显式置 false。
			if name == "disabled" {
				if err := db.Model(&models.AgentConfig{}).Where("role = ?", models.AgentRolePainterHQ).Update("is_active", false).Error; err != nil {
					t.Fatalf("disable painter_hq: %v", err)
				}
			}
			_, err = GeneratePortrait(context.Background(), portraitTestCard())
			if err == nil || !strings.Contains(err.Error(), string(models.AgentRolePainterHQ)) {
				t.Fatalf("err = %v, want error naming painter_hq even though painter is enabled", err)
			}
		})
	}
}

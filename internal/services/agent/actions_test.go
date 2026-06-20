package agent

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/llmcoc/server/internal/models"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

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

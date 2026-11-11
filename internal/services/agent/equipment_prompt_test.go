package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/llmcoc/server/internal/models"
)

func equipmentTestCard() models.CharacterCard {
	return models.CharacterCard{
		Name:       "约翰",
		Appearance: "黑发青年",
		Inventory: models.JSONField[[]models.Item]{Data: []models.Item{
			{Name: "风衣", Desc: "深灰", Count: 1, Type: models.ItemTypeEquipment, Equipped: true},
			{Name: "绷带", Count: 3, Type: models.ItemTypeMisc},
			{Name: "银戒指", Count: 1, Type: models.ItemTypeAccessory, Equipped: true},
		}},
	}
}

func TestBuildImagePromptCharacterVisualUserPrompt_OnlyEquipped(t *testing.T) {
	prompt := buildImagePromptCharacterVisualUserPrompt("", []models.CharacterCard{equipmentTestCard()})

	if !strings.Contains(prompt, "装备栏：") || strings.Contains(prompt, "物品栏") {
		t.Errorf("prompt should use the equipment section, got:\n%s", prompt)
	}
	for _, want := range []string{"黑发青年", "风衣(深灰)", "银戒指"} {
		if !strings.Contains(prompt, want) {
			t.Errorf("prompt missing %q:\n%s", want, prompt)
		}
	}
	if strings.Contains(prompt, "绷带") {
		t.Errorf("unequipped item leaked into prompt:\n%s", prompt)
	}
}

func TestBuildImagePromptCharacterVisualUserPrompt_NoEquipment(t *testing.T) {
	card := equipmentTestCard()
	card.Inventory.Data = []models.Item{{Name: "绷带", Count: 3}}
	prompt := buildImagePromptCharacterVisualUserPrompt("", []models.CharacterCard{card})

	if !strings.Contains(prompt, "装备栏：\n- （无）") || strings.Contains(prompt, "绷带") {
		t.Errorf("empty equipment should render the placeholder only, got:\n%s", prompt)
	}
}

func TestFallbackCharacterVisualDescriptions_Equipment(t *testing.T) {
	got := fallbackCharacterVisualDescriptions([]models.CharacterCard{equipmentTestCard()})
	if !strings.Contains(got, "黑发青年") || !strings.Contains(got, "装备：风衣(深灰)、银戒指") || strings.Contains(got, "绷带") {
		t.Errorf("fallback = %q", got)
	}

	noGear := equipmentTestCard()
	noGear.Inventory.Data = nil
	got = fallbackCharacterVisualDescriptions([]models.CharacterCard{noGear})
	if strings.Contains(got, "装备") {
		t.Errorf("fallback without equipment should not mention it: %q", got)
	}

	noApp := equipmentTestCard()
	noApp.Appearance = ""
	got = fallbackCharacterVisualDescriptions([]models.CharacterCard{noApp})
	if !strings.Contains(got, "没有记录外貌细节") || !strings.Contains(got, "装备：风衣(深灰)、银戒指") {
		t.Errorf("fallback without appearance = %q", got)
	}
}

// TestDescribeCharactersAction_SendsEquipmentToWriter 验证外貌查询发给Writer的内容是外貌加装备栏，不含未装备的物品。
func TestDescribeCharactersAction_SendsEquipmentToWriter(t *testing.T) {
	prov := &writerFakeProvider{responses: []string{"黑发青年，身披深灰风衣"}}
	gctx := &GameContext{Session: models.GameSession{
		ID:      9303,
		Players: []models.SessionPlayer{{CharacterCard: equipmentTestCard()}},
	}}
	actx := ActionContext{
		Ctx:     context.Background(),
		GCtx:    gctx,
		Handles: map[models.AgentRole]agentHandle{models.AgentRoleWriter: newWriterTestHandle(prov)},
	}

	results := describeCharactersAction{}.Execute(ToolCall{Characters: []string{"约翰"}}, actx)
	if len(results) != 1 || prov.callCount() != 1 {
		t.Fatalf("results = %+v, calls = %d", results, prov.callCount())
	}
	msgs := prov.msgsAt(0)
	user := msgs[len(msgs)-1].Content
	if !strings.Contains(user, "风衣(深灰)") || !strings.Contains(user, "银戒指") || strings.Contains(user, "绷带") {
		t.Errorf("user prompt = %s", user)
	}
	if !strings.Contains(msgs[0].Content, "装备栏") || strings.Contains(msgs[0].Content, "物品栏不是随身物品清单") {
		t.Errorf("system prompt = %s", msgs[0].Content)
	}
}

func TestBuildWriterInstruction_IncludesEquipment(t *testing.T) {
	gctx := GameContext{Session: models.GameSession{
		ID:      1,
		Players: []models.SessionPlayer{{CharacterCard: equipmentTestCard()}},
	}}
	opening := buildWriterInstruction(WriterRequest{Direction: "继续描述"}, gctx, false)

	if !strings.Contains(opening.Content, "<eq>风衣(深灰)、银戒指</eq>") {
		t.Errorf("opening missing equipment block:\n%s", opening.Content)
	}
	if strings.Contains(opening.Content, "绷带") {
		t.Errorf("unequipped item leaked into writer opening:\n%s", opening.Content)
	}

	empty := GameContext{Session: models.GameSession{
		ID:      1,
		Players: []models.SessionPlayer{{CharacterCard: models.CharacterCard{Name: "乙"}}},
	}}
	opening = buildWriterInstruction(WriterRequest{Direction: "继续描述"}, empty, false)
	if !strings.Contains(opening.Content, "<eq></eq>") {
		t.Errorf("empty equipment should render an empty block:\n%s", opening.Content)
	}
}

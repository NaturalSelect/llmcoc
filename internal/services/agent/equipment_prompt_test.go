package agent

import (
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

func TestBuildWriterInstruction_IncludesEquipment(t *testing.T) {
	gctx := GameContext{Session: models.GameSession{
		ID:      1,
		Players: []models.SessionPlayer{{CharacterCard: equipmentTestCard()}},
	}}
	opening := buildWriterInstruction(WriterRequest{Direction: "继续描述"}, gctx, false, false)

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
	opening = buildWriterInstruction(WriterRequest{Direction: "继续描述"}, empty, false, false)
	if !strings.Contains(opening.Content, "<eq></eq>") {
		t.Errorf("empty equipment should render an empty block:\n%s", opening.Content)
	}
}

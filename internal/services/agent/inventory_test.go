package agent

import (
	"strings"
	"testing"

	"github.com/llmcoc/server/internal/models"
)

func inventoryOf(players []models.SessionPlayer) []models.Item {
	return players[0].CharacterCard.Inventory.Data
}

func TestManageInventory_AddStacksCount(t *testing.T) {
	initAgentTestDB(t)
	players := eventTestPlayers()
	actx := eventTestActx(players)

	res := manageInventoryAction{}.Execute(ToolCall{CharacterName: "甲", Operate: "add", ItemName: "子弹", ItemCount: 4}, actx)
	inv := inventoryOf(players)
	if len(inv) != 2 || inv[1].Count != 10 {
		t.Fatalf("inventory after stacking add = %+v", inv)
	}
	if len(res[0].Events) != 1 || res[0].Events[0].Op != "change" || res[0].Events[0].Detail != "子弹(x6) → 子弹(x10)" {
		t.Errorf("stack add events = %+v", res[0].Events)
	}
}

func TestManageInventory_RemoveCount(t *testing.T) {
	initAgentTestDB(t)
	players := eventTestPlayers()
	actx := eventTestActx(players)

	manageInventoryAction{}.Execute(ToolCall{CharacterName: "甲", Operate: "remove", ItemName: "子弹", ItemCount: 4}, actx)
	if inv := inventoryOf(players); len(inv) != 2 || inv[1].Count != 2 {
		t.Fatalf("partial remove = %+v", inv)
	}

	res := manageInventoryAction{}.Execute(ToolCall{CharacterName: "甲", Operate: "remove", ItemName: "子弹", ItemCount: 9}, actx)
	if inv := inventoryOf(players); len(inv) != 1 || inv[0].Name != "手电筒" {
		t.Fatalf("over remove = %+v", inv)
	}
	if !strings.Contains(res[0].Result, "已全部移除") {
		t.Errorf("over remove result = %q", res[0].Result)
	}
	if len(res[0].Events) != 1 || res[0].Events[0].Op != "remove" {
		t.Errorf("over remove events = %+v", res[0].Events)
	}
}

func TestManageInventory_RemoveAmbiguousFailsWithoutChange(t *testing.T) {
	initAgentTestDB(t)
	players := eventTestPlayers()
	players[0].CharacterCard.Inventory.Data = []models.Item{
		{Name: "手电筒", Desc: "没电", Count: 1, Type: models.ItemTypeMisc},
		{Name: "手电筒", Desc: "电量充足", Count: 1, Type: models.ItemTypeMisc},
	}
	actx := eventTestActx(players)

	res := manageInventoryAction{}.Execute(ToolCall{CharacterName: "甲", Operate: "remove", ItemName: "手电筒"}, actx)
	if len(inventoryOf(players)) != 2 {
		t.Fatalf("ambiguous remove changed inventory: %+v", inventoryOf(players))
	}
	if !strings.Contains(res[0].Result, "没电") || !strings.Contains(res[0].Result, "电量充足") || len(res[0].Events) != 0 {
		t.Errorf("ambiguous remove result = %q events = %+v", res[0].Result, res[0].Events)
	}

	manageInventoryAction{}.Execute(ToolCall{CharacterName: "甲", Operate: "remove", ItemName: "手电筒", ItemDesc: "没电"}, actx)
	if inv := inventoryOf(players); len(inv) != 1 || inv[0].Desc != "电量充足" {
		t.Fatalf("remove with desc = %+v", inv)
	}
}

func TestManageInventory_UpdateChangesDescAndType(t *testing.T) {
	initAgentTestDB(t)
	players := eventTestPlayers()
	actx := eventTestActx(players)

	res := manageInventoryAction{}.Execute(ToolCall{
		CharacterName: "甲", Operate: "update", ItemName: "手电筒", ItemDesc: "没电", ItemType: models.ItemTypeEquipment,
	}, actx)
	inv := inventoryOf(players)
	if len(inv) != 2 || inv[0].Desc != "没电" || inv[0].Type != models.ItemTypeEquipment || inv[0].Count != 1 {
		t.Fatalf("update = %+v", inv)
	}
	if len(res[0].Events) != 1 || res[0].Events[0].Op != "change" || res[0].Events[0].Name != "手电筒" ||
		res[0].Events[0].Detail != "手电筒 → 手电筒(没电)" {
		t.Errorf("update events = %+v", res[0].Events)
	}

	res = manageInventoryAction{}.Execute(ToolCall{CharacterName: "甲", Operate: "update", ItemName: "钥匙", ItemDesc: "生锈"}, actx)
	if !strings.Contains(res[0].Result, "没有") {
		t.Errorf("update missing item result = %q", res[0].Result)
	}
}

func TestManageInventory_AddWritesItemType(t *testing.T) {
	initAgentTestDB(t)
	players := eventTestPlayers()
	actx := eventTestActx(players)

	manageInventoryAction{}.Execute(ToolCall{CharacterName: "甲", Operate: "add", ItemName: "左轮手枪", ItemType: models.ItemTypeWeapon}, actx)
	inv := inventoryOf(players)
	if got := inv[len(inv)-1]; got.Name != "左轮手枪" || got.Type != models.ItemTypeWeapon {
		t.Fatalf("add with type = %+v", got)
	}

	manageInventoryAction{}.Execute(ToolCall{CharacterName: "甲", Operate: "add", ItemName: "纸条"}, actx)
	inv = inventoryOf(players)
	if got := inv[len(inv)-1]; got.Type != models.ItemTypeMisc {
		t.Fatalf("add without type = %+v", got)
	}

	res := manageInventoryAction{}.Execute(ToolCall{CharacterName: "甲", Operate: "add", ItemName: "怪东西", ItemType: "bogus"}, actx)
	if !strings.Contains(res[0].Result, "无效") || len(inventoryOf(players)) != len(inv) {
		t.Errorf("invalid type result = %q, inventory = %+v", res[0].Result, inventoryOf(players))
	}
}

func TestManageInventory_AddStoryItem(t *testing.T) {
	initAgentTestDB(t)
	players := eventTestPlayers()
	actx := eventTestActx(players)

	res := manageInventoryAction{}.Execute(ToolCall{
		CharacterName: "甲", Operate: "add", ItemName: "地下室钥匙", ItemDesc: "黄铜", ItemType: models.ItemTypeStory,
	}, actx)
	inv := inventoryOf(players)
	if got := inv[len(inv)-1]; got.Name != "地下室钥匙" || got.Type != models.ItemTypeStory {
		t.Fatalf("story add = %+v", got)
	}
	if len(res[0].Events) != 1 || res[0].Events[0].Op != "add" || res[0].Events[0].Detail != storyItemNote {
		t.Errorf("story add events = %+v", res[0].Events)
	}

	res = manageInventoryAction{}.Execute(ToolCall{
		CharacterName: "甲", Operate: "add", ItemName: "子弹", ItemType: models.ItemTypeStory,
	}, actx)
	if !strings.Contains(res[0].Result, "不能同名同描述并存") {
		t.Errorf("story add clashing with ordinary stack result = %q", res[0].Result)
	}
	if inv := inventoryOf(players); len(inv) != 3 || inv[1].Count != 6 {
		t.Errorf("clashing add changed inventory: %+v", inv)
	}

	res = manageInventoryAction{}.Execute(ToolCall{
		CharacterName: "甲", Operate: "add", ItemName: "地下室钥匙", ItemDesc: "黄铜", ItemType: models.ItemTypeMisc,
	}, actx)
	if !strings.Contains(res[0].Result, "不能同名同描述并存") {
		t.Errorf("ordinary add clashing with story stack result = %q", res[0].Result)
	}
}

func TestManageInventory_UpdateCannotCrossStoryBoundary(t *testing.T) {
	initAgentTestDB(t)
	players := eventTestPlayers()
	players[0].CharacterCard.Inventory.Data = append(players[0].CharacterCard.Inventory.Data,
		models.Item{Name: "请柬", Count: 1, Type: models.ItemTypeStory})
	actx := eventTestActx(players)

	res := manageInventoryAction{}.Execute(ToolCall{
		CharacterName: "甲", Operate: "update", ItemName: "手电筒", ItemType: models.ItemTypeStory,
	}, actx)
	if !strings.Contains(res[0].Result, "不能用 update") {
		t.Errorf("ordinary -> story result = %q", res[0].Result)
	}
	res = manageInventoryAction{}.Execute(ToolCall{
		CharacterName: "甲", Operate: "update", ItemName: "请柬", ItemType: models.ItemTypeMisc,
	}, actx)
	if !strings.Contains(res[0].Result, "不能用 update") {
		t.Errorf("story -> ordinary result = %q", res[0].Result)
	}
	for _, it := range inventoryOf(players) {
		if (it.Name == "请柬") != (it.Type == models.ItemTypeStory) {
			t.Errorf("update crossed the story boundary: %+v", inventoryOf(players))
		}
	}

	manageInventoryAction{}.Execute(ToolCall{
		CharacterName: "甲", Operate: "update", ItemName: "请柬", ItemDesc: "已盖章",
	}, actx)
	for _, it := range inventoryOf(players) {
		if it.Name == "请柬" && (it.Desc != "已盖章" || it.Type != models.ItemTypeStory) {
			t.Errorf("desc-only update lost story type: %+v", it)
		}
	}
}

func TestInventoryDiffEvents_RenamedDescIsOneChange(t *testing.T) {
	before := []models.Item{{Name: "手电筒", Count: 1}}
	after := []models.Item{{Name: "手电筒", Desc: "没电", Count: 1}}
	evs := inventoryDiffEvents("甲", before, after)
	if len(evs) != 1 || evs[0].Op != "change" || evs[0].Detail != "手电筒 → 手电筒(没电)" {
		t.Fatalf("events = %+v", evs)
	}
}

func TestWriteCompactInventory(t *testing.T) {
	var sb strings.Builder
	writeCompactInventory(&sb, []models.Item{
		{Name: "手电筒", Count: 1, Type: models.ItemTypeMisc},
		{Name: "子弹", Desc: "穿甲", Count: 6, Type: models.ItemTypeWeapon},
	})
	want := `<inv><item n="手电筒"/><item n="子弹" desc="穿甲" x="6" type="weapon"/></inv>`
	if sb.String() != want {
		t.Errorf("got %s, want %s", sb.String(), want)
	}
}

func TestWriteCompactInventory_Equipped(t *testing.T) {
	var sb strings.Builder
	writeCompactInventory(&sb, []models.Item{
		{Name: "风衣", Count: 1, Type: models.ItemTypeEquipment, Equipped: true},
		{Name: "手电筒", Count: 1, Type: models.ItemTypeMisc, Equipped: true},
	})
	want := `<inv><item n="风衣" type="equipment" eq="1"/><item n="手电筒" eq="1"/></inv>`
	if sb.String() != want {
		t.Errorf("got %s, want %s", sb.String(), want)
	}
}

func TestManageInventory_AddEquipped(t *testing.T) {
	initAgentTestDB(t)
	players := eventTestPlayers()
	actx := eventTestActx(players)
	yes := true

	res := manageInventoryAction{}.Execute(ToolCall{
		CharacterName: "甲", Operate: "add", ItemName: "风衣", ItemType: models.ItemTypeEquipment, ItemEquipped: &yes,
	}, actx)
	inv := inventoryOf(players)
	if got := inv[len(inv)-1]; got.Name != "风衣" || !got.Equipped {
		t.Fatalf("add equipped = %+v", inv)
	}
	if !strings.Contains(res[0].Result, "已装备") {
		t.Errorf("add equipped result = %q", res[0].Result)
	}

	manageInventoryAction{}.Execute(ToolCall{CharacterName: "甲", Operate: "add", ItemName: "围巾"}, actx)
	inv = inventoryOf(players)
	if got := inv[len(inv)-1]; got.Name != "围巾" || got.Equipped {
		t.Fatalf("add without item_equipped should be unequipped, got %+v", got)
	}
}

func TestManageInventory_UpdateEquippedOnly(t *testing.T) {
	initAgentTestDB(t)
	players := eventTestPlayers()
	actx := eventTestActx(players)
	yes, no := true, false

	res := manageInventoryAction{}.Execute(ToolCall{
		CharacterName: "甲", Operate: "update", ItemName: "子弹", ItemEquipped: &yes,
	}, actx)
	inv := inventoryOf(players)
	if len(inv) != 2 || !inv[1].Equipped || inv[1].Count != 6 || inv[1].Desc != "" {
		t.Fatalf("update equipped = %+v", inv)
	}
	if len(res[0].Events) != 1 || res[0].Events[0].Op != "equip" || res[0].Events[0].Name != "子弹" {
		t.Errorf("equip events = %+v", res[0].Events)
	}
	if !strings.Contains(res[0].Result, "已装备") {
		t.Errorf("equip result = %q", res[0].Result)
	}

	res = manageInventoryAction{}.Execute(ToolCall{
		CharacterName: "甲", Operate: "update", ItemName: "子弹", ItemEquipped: &no,
	}, actx)
	if inv := inventoryOf(players); inv[1].Equipped {
		t.Fatalf("unequip = %+v", inv)
	}
	if len(res[0].Events) != 1 || res[0].Events[0].Op != "unequip" {
		t.Errorf("unequip events = %+v", res[0].Events)
	}
}

func TestManageInventory_UpdateKeepsEquippedWhenOmitted(t *testing.T) {
	initAgentTestDB(t)
	players := eventTestPlayers()
	players[0].CharacterCard.Inventory.Data[0].Equipped = true
	actx := eventTestActx(players)

	manageInventoryAction{}.Execute(ToolCall{CharacterName: "甲", Operate: "update", ItemName: "手电筒", ItemDesc: "没电"}, actx)
	if inv := inventoryOf(players); !inv[0].Equipped || inv[0].Desc != "没电" {
		t.Fatalf("update without item_equipped must keep the flag, got %+v", inv)
	}
}

func TestManageInventory_UpdateNeedsSomething(t *testing.T) {
	initAgentTestDB(t)
	players := eventTestPlayers()
	actx := eventTestActx(players)

	res := manageInventoryAction{}.Execute(ToolCall{CharacterName: "甲", Operate: "update", ItemName: "手电筒"}, actx)
	if !strings.Contains(res[0].Result, "item_equipped") || len(res[0].Events) != 0 {
		t.Errorf("empty update result = %q events = %+v", res[0].Result, res[0].Events)
	}
}

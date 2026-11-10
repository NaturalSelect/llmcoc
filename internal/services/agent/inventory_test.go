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

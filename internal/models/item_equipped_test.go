package models

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestItemEquippedJSON(t *testing.T) {
	b, err := json.Marshal(Item{Name: "手电筒", Count: 1, Type: ItemTypeMisc})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(b), "equipped") {
		t.Fatalf("unequipped item should omit equipped: %s", b)
	}

	b, err = json.Marshal(Item{Name: "风衣", Count: 1, Type: ItemTypeEquipment, Equipped: true})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var back Item
	if err := json.Unmarshal(b, &back); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !back.Equipped || back.Name != "风衣" {
		t.Fatalf("round trip = %+v from %s", back, b)
	}

	var legacy []Item
	if err := json.Unmarshal([]byte(`["绷带(x3)", {"name":"左轮","count":1,"type":"weapon"}]`), &legacy); err != nil {
		t.Fatalf("unmarshal legacy: %v", err)
	}
	for _, it := range legacy {
		if it.Equipped {
			t.Fatalf("legacy item should not be equipped: %+v", it)
		}
	}
}

func TestItemLabelIgnoresEquipped(t *testing.T) {
	it := Item{Name: "风衣", Desc: "深灰", Count: 1, Equipped: true}
	if got := it.Label(); got != "风衣(深灰)" {
		t.Fatalf("Label = %q", got)
	}
}

func TestAddItemMergesEquipped(t *testing.T) {
	list := []Item{{Name: "戒指", Count: 1, Type: ItemTypeAccessory}}

	out := AddItem(list, Item{Name: "戒指", Count: 1, Equipped: true})
	if len(out) != 1 || !out[0].Equipped || out[0].Count != 2 {
		t.Fatalf("equipped add should merge as equipped: %+v", out)
	}

	out = AddItem(out, Item{Name: "戒指", Count: 1})
	if !out[0].Equipped {
		t.Fatalf("unequipped add must not clear equipped: %+v", out)
	}
}

func TestSetEquipped(t *testing.T) {
	list := []Item{
		{Name: "风衣", Count: 1, Type: ItemTypeEquipment},
		{Name: "风衣", Desc: "破旧", Count: 1, Type: ItemTypeEquipment},
	}

	out, ok := SetEquipped(list, "风衣", "破旧", true)
	if !ok || out[0].Equipped || !out[1].Equipped {
		t.Fatalf("equip = %+v, %v", out, ok)
	}
	if list[1].Equipped {
		t.Fatalf("input slice mutated: %+v", list)
	}

	out, ok = SetEquipped(out, "风衣", "破旧", false)
	if !ok || out[1].Equipped {
		t.Fatalf("unequip = %+v, %v", out, ok)
	}

	out, ok = SetEquipped(list, "手电筒", "", true)
	if ok || len(out) != 2 {
		t.Fatalf("miss = %+v, %v", out, ok)
	}
}

func TestEquippedItems(t *testing.T) {
	if got := EquippedItems(nil); len(got) != 0 {
		t.Fatalf("nil list = %+v", got)
	}
	list := []Item{
		{Name: "绷带", Count: 3},
		{Name: "风衣", Count: 1, Equipped: true},
		{Name: "左轮", Count: 1},
		{Name: "戒指", Count: 1, Equipped: true},
	}
	got := EquippedItems(list)
	if len(got) != 2 || got[0].Name != "风衣" || got[1].Name != "戒指" {
		t.Fatalf("equipped = %+v", got)
	}
}

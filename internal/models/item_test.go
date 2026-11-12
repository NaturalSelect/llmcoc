package models

import (
	"encoding/json"
	"testing"
)

func TestParseItemLabel(t *testing.T) {
	cases := []struct {
		in   string
		want Item
	}{
		{"手电筒", Item{Name: "手电筒", Count: 1, Type: ItemTypeMisc}},
		{"绷带(x3)", Item{Name: "绷带", Count: 3, Type: ItemTypeMisc}},
		{"绷带(X3)", Item{Name: "绷带", Count: 3, Type: ItemTypeMisc}},
		{"手电筒(没电)", Item{Name: "手电筒", Desc: "没电", Count: 1, Type: ItemTypeMisc}},
		{"子弹(.38 口径, x6)", Item{Name: "子弹", Desc: ".38 口径", Count: 6, Type: ItemTypeMisc}},
		{"  钥匙 ( 铜制 , x2 ) ", Item{Name: "钥匙", Desc: "铜制", Count: 2, Type: ItemTypeMisc}},
	}
	for _, c := range cases {
		if got := ParseItemLabel(c.in); got != c.want {
			t.Errorf("ParseItemLabel(%q) = %+v, want %+v", c.in, got, c.want)
		}
	}
}

func TestParseItemLabel_LabelRoundTrip(t *testing.T) {
	for _, s := range []string{"手电筒", "绷带(x3)", "手电筒(没电)", "子弹(.38 口径, x6)", "左轮手枪(.38)(保养良好)"} {
		if got := ParseItemLabel(s).Label(); got != s {
			t.Errorf("Label round trip of %q = %q", s, got)
		}
	}
}

func TestItemUnmarshalJSON(t *testing.T) {
	var items []Item
	raw := `["绷带(x3)", {"name":"左轮","desc":"满膛","count":2,"type":"weapon"}, {"name":"纸条"}, {"name":"怪书","count":0,"type":"bogus"}]`
	if err := json.Unmarshal([]byte(raw), &items); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	want := []Item{
		{Name: "绷带", Count: 3, Type: ItemTypeMisc},
		{Name: "左轮", Desc: "满膛", Count: 2, Type: ItemTypeWeapon},
		{Name: "纸条", Count: 1, Type: ItemTypeMisc},
		{Name: "怪书", Count: 1, Type: ItemTypeMisc},
	}
	if len(items) != len(want) {
		t.Fatalf("got %d items, want %d: %+v", len(items), len(want), items)
	}
	for i := range want {
		if items[i] != want[i] {
			t.Errorf("item %d = %+v, want %+v", i, items[i], want[i])
		}
	}
}

func TestJSONFieldItemsScan(t *testing.T) {
	var f JSONField[[]Item]
	if err := f.Scan(`["绷带(x3)","手电筒(没电)"]`); err != nil {
		t.Fatalf("scan legacy: %v", err)
	}
	if len(f.Data) != 2 || f.Data[0].Count != 3 || f.Data[1].Desc != "没电" {
		t.Fatalf("legacy scan = %+v", f.Data)
	}
	if err := f.Scan(`[{"name":"绷带","count":3,"type":"misc"}]`); err != nil {
		t.Fatalf("scan new: %v", err)
	}
	if len(f.Data) != 1 || f.Data[0].Name != "绷带" {
		t.Fatalf("new scan = %+v", f.Data)
	}
	if err := f.Scan(nil); err != nil || f.Data != nil {
		t.Fatalf("scan nil = %+v, %v", f.Data, err)
	}
	if err := f.Scan("null"); err != nil || f.Data != nil {
		t.Fatalf("scan null = %+v, %v", f.Data, err)
	}
}

func TestAddItem(t *testing.T) {
	list := []Item{{Name: "绷带", Count: 2, Type: ItemTypeMisc}}

	out := AddItem(list, Item{Name: "绷带", Count: 3, Type: ItemTypeEquipment})
	if len(out) != 1 || out[0].Count != 5 || out[0].Type != ItemTypeEquipment {
		t.Fatalf("stack add = %+v", out)
	}
	if list[0].Count != 2 {
		t.Fatalf("input slice mutated: %+v", list)
	}

	out = AddItem(list, Item{Name: "绷带", Desc: "染血", Count: 1})
	if len(out) != 2 || out[1].Desc != "染血" || out[1].Type != ItemTypeMisc {
		t.Fatalf("different desc should not stack: %+v", out)
	}

	out = AddItem(nil, Item{Name: "手电筒"})
	if len(out) != 1 || out[0].Count != 1 {
		t.Fatalf("add to empty = %+v", out)
	}
}

func TestTakeItem(t *testing.T) {
	list := []Item{
		{Name: "子弹", Count: 6, Type: ItemTypeMisc},
		{Name: "子弹", Desc: "穿甲", Count: 2, Type: ItemTypeMisc},
	}

	out, taken, ok := TakeItem(list, "子弹", "", 2)
	if !ok || taken.Count != 2 || len(out) != 2 || out[0].Count != 4 {
		t.Fatalf("partial take = %+v, %+v, %v", out, taken, ok)
	}
	if list[0].Count != 6 {
		t.Fatalf("input slice mutated: %+v", list)
	}

	out, taken, ok = TakeItem(list, "子弹", "", 0)
	if !ok || taken.Count != 6 || len(out) != 1 || out[0].Desc != "穿甲" {
		t.Fatalf("whole take = %+v, %+v, %v", out, taken, ok)
	}

	out, taken, ok = TakeItem(list, "子弹", "穿甲", 9)
	if !ok || taken.Count != 2 || len(out) != 1 || out[0].Desc != "" {
		t.Fatalf("over take = %+v, %+v, %v", out, taken, ok)
	}

	out, _, ok = TakeItem(list, "手电筒", "", 1)
	if ok || len(out) != 2 {
		t.Fatalf("miss = %+v, %v", out, ok)
	}
}

func TestAddItem_StoryItemNeverMergesWithOrdinary(t *testing.T) {
	list := []Item{{Name: "钥匙", Desc: "黄铜", Count: 1, Type: ItemTypeMisc}}

	out := AddItem(list, Item{Name: "钥匙", Desc: "黄铜", Count: 1, Type: ItemTypeStory})
	if len(out) != 2 || out[0].Count != 1 || out[0].Type != ItemTypeMisc || out[1].Type != ItemTypeStory {
		t.Fatalf("story add should append a separate stack: %+v", out)
	}

	out = AddItem(out, Item{Name: "钥匙", Desc: "黄铜", Count: 2, Type: ItemTypeStory})
	if len(out) != 2 || out[1].Count != 3 || out[0].Count != 1 {
		t.Fatalf("story add should stack with story: %+v", out)
	}

	out = AddItem(out, Item{Name: "钥匙", Desc: "黄铜", Count: 1, Type: ItemTypeMisc})
	if len(out) != 2 || out[0].Count != 2 || out[1].Count != 3 {
		t.Fatalf("ordinary add should stack with ordinary: %+v", out)
	}
}

func TestSplitStoryItems(t *testing.T) {
	list := []Item{
		{Name: "绷带", Count: 3, Type: ItemTypeMisc},
		{Name: "请柬", Count: 1, Type: ItemTypeStory},
		{Name: "左轮", Count: 1, Type: ItemTypeWeapon},
		{Name: "地图", Desc: "手绘", Count: 1, Type: ItemTypeStory},
	}
	kept, story := SplitStoryItems(list)
	if len(kept) != 2 || kept[0].Name != "绷带" || kept[1].Name != "左轮" {
		t.Fatalf("kept = %+v", kept)
	}
	if len(story) != 2 || story[0].Name != "请柬" || story[1].Name != "地图" {
		t.Fatalf("story = %+v", story)
	}
	if len(list) != 4 || list[1].Type != ItemTypeStory {
		t.Fatalf("input slice mutated: %+v", list)
	}

	kept, story = SplitStoryItems(nil)
	if kept == nil || len(kept) != 0 || len(story) != 0 {
		t.Fatalf("empty split = %+v, %+v", kept, story)
	}
}

func TestStoryItemTypeIsInventoryType(t *testing.T) {
	if !IsInventoryItemType(ItemTypeStory) {
		t.Fatal("story must be a valid inventory item type")
	}
	if got := NormalizeItem(Item{Name: "请柬", Type: ItemTypeStory}); got.Type != ItemTypeStory {
		t.Fatalf("normalize changed story type: %+v", got)
	}
}

func TestMergeStacks(t *testing.T) {
	out := MergeStacks([]Item{
		{Name: "绷带", Count: 3},
		{Name: "手电筒", Desc: "没电", Count: 1},
		{Name: "绷带", Count: 3},
		{Name: "  ", Count: 1},
	})
	if len(out) != 2 || out[0].Name != "绷带" || out[0].Count != 6 || out[1].Name != "手电筒" {
		t.Fatalf("merge = %+v", out)
	}
}

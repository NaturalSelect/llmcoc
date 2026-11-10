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

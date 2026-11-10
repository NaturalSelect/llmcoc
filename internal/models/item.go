// NOTE: item.go 定义人物卡物品栏与账号仓库共用的结构化物品 Item，以及堆叠（同名同描述合并数量）规则。
// 库里历史上存的是裸字符串（如 "绷带(描述, x3)"），Item.UnmarshalJSON 同时接受字符串和对象，
// 所以旧数据不论是否已被 backfillLegacyItems 改写都能读出。
package models

import (
	"bytes"
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"strings"
)

const (
	ItemTypeTome     ItemType = "tome"     // 典籍
	ItemTypeArtifact ItemType = "artifact" // 神器
	ItemTypeMisc     ItemType = "misc"     // 杂物，旧数据一律归入此类
)

// IsInventoryItemType 判断 t 是否可以作为物品栏物品的类型（卡槽、金币不是物品）。
func IsInventoryItemType(t ItemType) bool {
	switch t {
	case ItemTypeEquipment, ItemTypeWeapon, ItemTypeAccessory, ItemTypeTome, ItemTypeArtifact, ItemTypeMisc:
		return true
	}
	return false
}

type Item struct {
	Name     string   `json:"name"`
	Desc     string   `json:"desc,omitempty"`
	Count    int      `json:"count"`
	Type     ItemType `json:"type"`
	Equipped bool     `json:"equipped,omitempty"`
}

// NormalizeItem 去掉首尾空白，数量不足 1 时按 1 处理，类型缺失或不合法时归为 misc。
func NormalizeItem(it Item) Item {
	it.Name = strings.TrimSpace(it.Name)
	it.Desc = strings.TrimSpace(it.Desc)
	if it.Count < 1 {
		it.Count = 1
	}
	if !IsInventoryItemType(it.Type) {
		it.Type = ItemTypeMisc
	}
	return it
}

func (it *Item) UnmarshalJSON(b []byte) error {
	trimmed := bytes.TrimSpace(b)
	if bytes.Equal(trimmed, []byte("null")) {
		return nil
	}
	if len(trimmed) > 0 && trimmed[0] == '"' {
		var s string
		if err := json.Unmarshal(trimmed, &s); err != nil {
			return err
		}
		*it = NormalizeItem(ParseItemLabel(s))
		return nil
	}
	// NOTE: 用别名类型解码，避免再次进入本方法造成无限递归。
	type plainItem Item
	var p plainItem
	if err := json.Unmarshal(trimmed, &p); err != nil {
		return err
	}
	*it = NormalizeItem(Item(p))
	return nil
}

// ParseItemLabel 解析旧的字符串格式 "名称(描述, xN)"，描述和 xN 都可省略。
func ParseItemLabel(s string) Item {
	it := Item{Count: 1, Type: ItemTypeMisc}
	idx := strings.Index(s, "(")
	if idx < 0 {
		it.Name = strings.TrimSpace(s)
		return it
	}
	it.Name = strings.TrimSpace(s[:idx])
	inner := strings.TrimSpace(s[idx+1:])
	inner = strings.TrimSuffix(inner, ")")
	inner = strings.TrimSpace(inner)
	if ci := strings.LastIndex(inner, ","); ci >= 0 {
		if n, ok := parseItemQty(strings.TrimSpace(inner[ci+1:])); ok {
			it.Count = n
			it.Desc = strings.TrimSpace(inner[:ci])
			return it
		}
	}
	if n, ok := parseItemQty(inner); ok {
		it.Count = n
		return it
	}
	it.Desc = inner
	return it
}

func parseItemQty(s string) (int, bool) {
	if !strings.HasPrefix(s, "x") && !strings.HasPrefix(s, "X") {
		return 0, false
	}
	n, err := strconv.Atoi(s[1:])
	if err != nil || n < 1 {
		return 0, false
	}
	return n, true
}

// Label 生成玩家和 LLM 看到的文案，格式与旧字符串一致："名称(描述, xN)"。
func (it Item) Label() string {
	switch {
	case it.Desc == "" && it.Count <= 1:
		return it.Name
	case it.Desc == "":
		return fmt.Sprintf("%s(x%d)", it.Name, it.Count)
	case it.Count <= 1:
		return fmt.Sprintf("%s(%s)", it.Name, it.Desc)
	default:
		return fmt.Sprintf("%s(%s, x%d)", it.Name, it.Desc, it.Count)
	}
}

// SameStack 判断两个物品是否属于同一堆：名称和描述都相同。
func (it Item) SameStack(o Item) bool {
	return strings.TrimSpace(it.Name) == strings.TrimSpace(o.Name) &&
		strings.TrimSpace(it.Desc) == strings.TrimSpace(o.Desc)
}

// AddItem 把 it 加入物品列表：命中同一堆则累加数量，否则追加。不修改传入的切片。
func AddItem(list []Item, it Item) []Item {
	it = NormalizeItem(it)
	out := slices.Clone(list)
	for i := range out {
		if !out[i].SameStack(it) {
			continue
		}
		out[i].Count += it.Count
		if out[i].Type == ItemTypeMisc && it.Type != ItemTypeMisc {
			out[i].Type = it.Type
		}
		out[i].Equipped = out[i].Equipped || it.Equipped
		return out
	}
	return append(out, it)
}

// TakeItem 按名称和描述精确取出物品。count<=0 或不小于堆内数量时整堆取走，否则只扣减 count。
// 不修改传入的切片；未命中时返回原列表和 ok=false。
func TakeItem(list []Item, name, desc string, count int) (out []Item, taken Item, ok bool) {
	key := Item{Name: name, Desc: desc}
	for i := range list {
		if !list[i].SameStack(key) {
			continue
		}
		taken = list[i]
		out = slices.Clone(list)
		if count <= 0 || count >= list[i].Count {
			return slices.Delete(out, i, i+1), taken, true
		}
		out[i].Count -= count
		taken.Count = count
		return out, taken, true
	}
	return list, Item{}, false
}

// SetEquipped 按名称和描述精确定位一堆物品并设置装备状态，装备状态属于整堆。
// 不修改传入的切片；未命中时返回原列表和 ok=false。
func SetEquipped(list []Item, name, desc string, equipped bool) (out []Item, ok bool) {
	key := Item{Name: name, Desc: desc}
	for i := range list {
		if !list[i].SameStack(key) {
			continue
		}
		out = slices.Clone(list)
		out[i].Equipped = equipped
		return out, true
	}
	return list, false
}

// EquippedItems 返回已装备的物品，保持原顺序。
func EquippedItems(list []Item) []Item {
	var out []Item
	for _, it := range list {
		if it.Equipped {
			out = append(out, it)
		}
	}
	return out
}

// MergeStacks 合并同一堆物品并丢弃空名称条目，保持首次出现的顺序。
func MergeStacks(list []Item) []Item {
	out := make([]Item, 0, len(list))
	for _, it := range list {
		it = NormalizeItem(it)
		if it.Name == "" {
			continue
		}
		out = AddItem(out, it)
	}
	return out
}

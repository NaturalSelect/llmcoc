package models

import (
	"testing"
	"time"
)

func TestBackfillLegacyItems(t *testing.T) {
	db := openSeedTestDB(t)
	if err := db.AutoMigrate(&User{}, &CharacterCard{}); err != nil {
		t.Fatalf("auto-migrate: %v", err)
	}

	past := time.Date(2020, 1, 2, 3, 4, 5, 0, time.UTC)
	insertCard := func(name, inventory string) uint {
		t.Helper()
		var inv any
		if inventory != "" {
			inv = inventory
		}
		err := db.Exec(
			"INSERT INTO character_cards (user_id, name, stats, skills, inventory, updated_at) VALUES (1, ?, '{}', '{}', ?, ?)",
			name, inv, past,
		).Error
		if err != nil {
			t.Fatalf("insert card %s: %v", name, err)
		}
		var id uint
		db.Raw("SELECT id FROM character_cards WHERE name = ?", name).Scan(&id)
		return id
	}
	legacyID := insertCard("legacy", `["绷带(x3)","绷带(x3)","手电筒(没电)"]`)
	emptyID := insertCard("empty", `[]`)
	nullJSONID := insertCard("nulljson", `null`)
	nullID := insertCard("null", "")
	newID := insertCard("new", `[{"name":"绷带","count":2,"type":"misc"}]`)

	err := db.Exec(
		"INSERT INTO users (username, email, password_hash, warehouse) VALUES ('u', 'u@example.com', 'x', ?)",
		`["钥匙","钥匙(x2)"]`,
	).Error
	if err != nil {
		t.Fatalf("insert user: %v", err)
	}

	rawInventory := func(id uint) (s string, null bool) {
		var ns *string
		db.Raw("SELECT inventory FROM character_cards WHERE id = ?", id).Scan(&ns)
		if ns == nil {
			return "", true
		}
		return *ns, false
	}

	backfillLegacyItems()

	var card CharacterCard
	if err := db.First(&card, legacyID).Error; err != nil {
		t.Fatalf("load legacy card: %v", err)
	}
	items := card.Inventory.Data
	if len(items) != 2 || items[0].Name != "绷带" || items[0].Count != 6 || items[1].Desc != "没电" {
		t.Fatalf("legacy inventory = %+v", items)
	}
	if raw, _ := rawInventory(legacyID); len(raw) < 2 || raw[:2] != `[{` {
		t.Fatalf("legacy row not rewritten to objects: %s", raw)
	}
	if !card.UpdatedAt.Equal(past) {
		t.Fatalf("updated_at changed: %v", card.UpdatedAt)
	}

	var user User
	if err := db.First(&user).Error; err != nil {
		t.Fatalf("load user: %v", err)
	}
	if len(user.Warehouse.Data) != 1 || user.Warehouse.Data[0].Name != "钥匙" || user.Warehouse.Data[0].Count != 3 {
		t.Fatalf("warehouse = %+v", user.Warehouse.Data)
	}

	before := map[uint]string{}
	for _, id := range []uint{legacyID, emptyID, nullJSONID, newID} {
		before[id], _ = rawInventory(id)
	}
	if raw, _ := rawInventory(emptyID); raw != `[]` {
		t.Fatalf("empty row changed: %s", raw)
	}
	if raw, _ := rawInventory(nullJSONID); raw != `null` {
		t.Fatalf("null json row changed: %s", raw)
	}
	if _, isNull := rawInventory(nullID); !isNull {
		t.Fatalf("NULL row changed")
	}
	if raw, _ := rawInventory(newID); raw != `[{"name":"绷带","count":2,"type":"misc"}]` {
		t.Fatalf("new-format row changed: %s", raw)
	}

	backfillLegacyItems()
	for id, want := range before {
		if got, _ := rawInventory(id); got != want {
			t.Fatalf("second run changed row %d: %s -> %s", id, want, got)
		}
	}
}

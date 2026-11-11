// NOTE: character_portrait_test.go 守住 portrait_hash 只读列的语义：整行写入永远不能覆盖它。
package models

import "testing"

func TestPortraitHashColumnIsMigrated(t *testing.T) {
	db := openSeedTestDB(t)
	if err := db.AutoMigrate(&User{}, &CharacterCard{}); err != nil {
		t.Fatalf("auto-migrate: %v", err)
	}
	if !db.Migrator().HasColumn(&CharacterCard{}, "PortraitHash") {
		t.Fatal("character_cards.portrait_hash was not created by AutoMigrate")
	}
}

func TestPortraitHashSurvivesWholeRowWrites(t *testing.T) {
	db := openSeedTestDB(t)
	if err := db.AutoMigrate(&User{}, &CharacterCard{}); err != nil {
		t.Fatalf("auto-migrate: %v", err)
	}

	card := CharacterCard{UserID: 1, Name: "stale", PortraitHash: "ignored-on-create"}
	if err := db.Create(&card).Error; err != nil {
		t.Fatalf("create card: %v", err)
	}
	readHash := func() string {
		t.Helper()
		var got string
		if err := db.Raw("SELECT portrait_hash FROM character_cards WHERE id = ?", card.ID).Scan(&got).Error; err != nil {
			t.Fatalf("read portrait_hash: %v", err)
		}
		return got
	}
	if got := readHash(); got != "" {
		t.Fatalf("Create wrote portrait_hash = %q, want empty", got)
	}

	if err := db.Exec("UPDATE character_cards SET portrait_hash = ? WHERE id = ?", "newhash", card.ID).Error; err != nil {
		t.Fatalf("raw update: %v", err)
	}

	stale := card
	stale.Name = "renamed"
	stale.PortraitHash = "stale-value"
	if err := db.Save(&stale).Error; err != nil {
		t.Fatalf("save stale card: %v", err)
	}
	if got := readHash(); got != "newhash" {
		t.Fatalf("Save overwrote portrait_hash = %q, want newhash", got)
	}

	if err := db.Model(&CharacterCard{}).Where("id = ?", card.ID).Updates(map[string]any{"name": "again", "portrait_hash": "map-value"}).Error; err != nil {
		t.Fatalf("map update: %v", err)
	}
	if got := readHash(); got != "newhash" {
		t.Fatalf("map Updates overwrote portrait_hash = %q, want newhash", got)
	}

	var loaded CharacterCard
	if err := db.First(&loaded, card.ID).Error; err != nil {
		t.Fatalf("load card: %v", err)
	}
	if loaded.PortraitHash != "newhash" {
		t.Fatalf("loaded PortraitHash = %q, want newhash", loaded.PortraitHash)
	}
	if loaded.Name != "again" {
		t.Fatalf("loaded Name = %q, want again", loaded.Name)
	}
}

func TestSeedDefaultSiteSettingsIncludesPortraitCost(t *testing.T) {
	db := openSeedTestDB(t)
	if err := db.AutoMigrate(&SiteSetting{}); err != nil {
		t.Fatalf("auto-migrate: %v", err)
	}
	seedDefaultSiteSettings()
	var s SiteSetting
	if err := db.Where("key = ?", "regenerate_portrait_cost").First(&s).Error; err != nil {
		t.Fatalf("regenerate_portrait_cost not seeded: %v", err)
	}
	if s.Value != "100" {
		t.Fatalf("regenerate_portrait_cost = %q, want 100", s.Value)
	}
}

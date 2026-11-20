package models

import "testing"

func TestEnsureCompanionUser_CreatesOnceAndIsRepeatable(t *testing.T) {
	db := openSeedTestDB(t)
	if err := db.AutoMigrate(&User{}); err != nil {
		t.Fatalf("auto-migrate: %v", err)
	}

	if got := CompanionUserID(); got != 0 {
		t.Fatalf("CompanionUserID before seed = %d, want 0", got)
	}
	if IsCompanionUser(1) {
		t.Fatal("IsCompanionUser must be false before the bot exists")
	}

	id, err := EnsureCompanionUser()
	if err != nil || id == 0 {
		t.Fatalf("EnsureCompanionUser = %d, %v", id, err)
	}
	again, err := EnsureCompanionUser()
	if err != nil || again != id {
		t.Fatalf("second EnsureCompanionUser = %d, %v, want %d", again, err, id)
	}

	var count int64
	db.Model(&User{}).Where("role = ?", RoleBot).Count(&count)
	if count != 1 {
		t.Fatalf("bot users = %d, want 1", count)
	}
	if !IsCompanionUser(id) || IsCompanionUser(id+1) || IsCompanionUser(0) {
		t.Error("IsCompanionUser must only match the bot user")
	}

	var u User
	db.First(&u, id)
	if u.PasswordHash != companionPasswordHash {
		t.Errorf("bot password hash = %q, want the unusable marker", u.PasswordHash)
	}
}

func TestEnsureCompanionUser_AvoidsUsernameCollision(t *testing.T) {
	db := openSeedTestDB(t)
	if err := db.AutoMigrate(&User{}); err != nil {
		t.Fatalf("auto-migrate: %v", err)
	}
	if err := db.Create(&User{Username: companionUsername, Email: "taken@example.com", PasswordHash: "x", Role: RoleUser}).Error; err != nil {
		t.Fatalf("seed colliding user: %v", err)
	}

	id, err := EnsureCompanionUser()
	if err != nil || id == 0 {
		t.Fatalf("EnsureCompanionUser = %d, %v", id, err)
	}
	var u User
	db.First(&u, id)
	if u.Username == companionUsername || u.Role != RoleBot {
		t.Errorf("bot user = %+v, want a suffixed username and the bot role", u)
	}
}

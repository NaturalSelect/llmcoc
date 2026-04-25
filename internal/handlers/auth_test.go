package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/llmcoc/server/internal/config"
	"github.com/llmcoc/server/internal/models"
	"golang.org/x/crypto/bcrypt"
)

func init() {
	// Ensure a JWT secret is set for token generation in tests.
	config.Global.JWT.Secret = "test-secret"
	config.Global.JWT.ExpireHours = 1
	config.Global.Shop.InitialCardSlots = 3
}

// authRouter sets up a minimal router for auth endpoints.
func authRouter() *gin.Engine {
	r := gin.New()
	r.POST("/auth/register", Register)
	r.POST("/auth/login", Login)
	return r
}

func TestRegister_Success(t *testing.T) {
	initTestDB(t)
	r := authRouter()

	w := httptest.NewRecorder()
	r.ServeHTTP(w, jsonReq("POST", "/auth/register", map[string]any{
		"username": "alice",
		"email":    "alice@example.com",
		"password": "secret123",
	}))

	if w.Code != http.StatusCreated {
		t.Fatalf("want 201, got %d: %s", w.Code, w.Body.String())
	}
	var resp map[string]any
	json.NewDecoder(w.Body).Decode(&resp)
	if resp["token"] == nil {
		t.Error("response must include token")
	}
}

func TestRegister_DuplicateUser(t *testing.T) {
	initTestDB(t)
	seedUser(t, "bob", "user", 0, 3)

	r := authRouter()

	w := httptest.NewRecorder()
	r.ServeHTTP(w, jsonReq("POST", "/auth/register", map[string]any{
		"username": "bob",
		"email":    "bob@example.com",
		"password": "secret123",
	}))

	if w.Code != http.StatusConflict {
		t.Errorf("want 409, got %d", w.Code)
	}
}

func TestRegister_InvalidBody(t *testing.T) {
	initTestDB(t)
	r := authRouter()

	w := httptest.NewRecorder()
	r.ServeHTTP(w, jsonReq("POST", "/auth/register", map[string]any{
		"username": "x", // too short (min=3)
		"email":    "not-an-email",
		"password": "ab", // too short (min=6)
	}))

	if w.Code != http.StatusBadRequest {
		t.Errorf("want 400, got %d", w.Code)
	}
}

func TestLogin_Success(t *testing.T) {
	initTestDB(t)

	hash, _ := bcrypt.GenerateFromPassword([]byte("mypassword"), bcrypt.MinCost)
	seedUserWithHash(t, "carol", string(hash))

	r := authRouter()

	w := httptest.NewRecorder()
	r.ServeHTTP(w, jsonReq("POST", "/auth/login", map[string]any{
		"username": "carol",
		"password": "mypassword",
	}))

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp map[string]any
	json.NewDecoder(w.Body).Decode(&resp)
	if resp["token"] == nil {
		t.Error("response must include token")
	}
}

func TestLogin_WrongPassword(t *testing.T) {
	initTestDB(t)
	hash, _ := bcrypt.GenerateFromPassword([]byte("correct"), bcrypt.MinCost)
	seedUserWithHash(t, "dave", string(hash))

	r := authRouter()

	w := httptest.NewRecorder()
	r.ServeHTTP(w, jsonReq("POST", "/auth/login", map[string]any{
		"username": "dave",
		"password": "wrong",
	}))

	if w.Code != http.StatusUnauthorized {
		t.Errorf("want 401, got %d", w.Code)
	}
}

func TestLogin_UserNotFound(t *testing.T) {
	initTestDB(t)
	r := authRouter()

	w := httptest.NewRecorder()
	r.ServeHTTP(w, jsonReq("POST", "/auth/login", map[string]any{
		"username": "nobody",
		"password": "anything",
	}))

	if w.Code != http.StatusUnauthorized {
		t.Errorf("want 401, got %d", w.Code)
	}
}

func TestMe_Success(t *testing.T) {
	initTestDB(t)
	uid := seedUser(t, "eve", "user", 100, 3)

	r := gin.New()
	r.GET("/auth/me", withAuth(uid, "eve", "user"), Me)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, jsonReq("GET", "/auth/me", nil))

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", w.Code)
	}
	var resp map[string]any
	json.NewDecoder(w.Body).Decode(&resp)
	if resp["username"] != "eve" {
		t.Errorf("username = %v, want eve", resp["username"])
	}
}

func TestMe_NotFound(t *testing.T) {
	initTestDB(t)

	r := gin.New()
	r.GET("/auth/me", withAuth(9999, "ghost", "user"), Me)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, jsonReq("GET", "/auth/me", nil))

	if w.Code != http.StatusNotFound {
		t.Errorf("want 404, got %d", w.Code)
	}
}

// ── Invite code tests ────────────────────────────────────────────────────────

func TestRegister_SucceedsWithoutInviteCode_WhenSettingOff(t *testing.T) {
	initTestDB(t)
	// Setting is off by default (not seeded), so registration should work.
	r := authRouter()
	w := httptest.NewRecorder()
	r.ServeHTTP(w, jsonReq("POST", "/auth/register", map[string]any{
		"username": "nocode", "email": "nocode@test.com", "password": "secret123",
	}))
	if w.Code != http.StatusCreated {
		t.Fatalf("want 201, got %d: %s", w.Code, w.Body.String())
	}
}

func TestRegister_FailsWithoutInviteCode_WhenSettingOn(t *testing.T) {
	initTestDB(t)
	models.SetSiteSetting("require_invite_code", "true")
	r := authRouter()
	w := httptest.NewRecorder()
	r.ServeHTTP(w, jsonReq("POST", "/auth/register", map[string]any{
		"username": "nocode2", "email": "nocode2@test.com", "password": "secret123",
	}))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d: %s", w.Code, w.Body.String())
	}
}

func TestRegister_SucceedsWithValidInviteCode(t *testing.T) {
	initTestDB(t)
	models.SetSiteSetting("require_invite_code", "true")
	// Seed an unused invite code
	code := models.InviteCode{Code: "TESTCODE", CreatedBy: 0}
	models.DB.Create(&code)

	r := authRouter()
	w := httptest.NewRecorder()
	r.ServeHTTP(w, jsonReq("POST", "/auth/register", map[string]any{
		"username": "withcode", "email": "withcode@test.com", "password": "secret123",
		"invite_code": "TESTCODE",
	}))
	if w.Code != http.StatusCreated {
		t.Fatalf("want 201, got %d: %s", w.Code, w.Body.String())
	}

	// Verify code is marked as used
	var ic models.InviteCode
	models.DB.First(&ic, code.ID)
	if ic.UsedBy == nil {
		t.Error("invite code should be marked as used")
	}
	if ic.UsedAt == nil {
		t.Error("invite code should have used_at set")
	}
}

func TestRegister_FailsWithUsedInviteCode(t *testing.T) {
	initTestDB(t)
	models.SetSiteSetting("require_invite_code", "true")
	uid := seedUser(t, "existing", "user", 0, 3)
	code := models.InviteCode{Code: "USEDCODE", CreatedBy: 0, UsedBy: &uid}
	models.DB.Create(&code)

	r := authRouter()
	w := httptest.NewRecorder()
	r.ServeHTTP(w, jsonReq("POST", "/auth/register", map[string]any{
		"username": "newuser", "email": "newuser@test.com", "password": "secret123",
		"invite_code": "USEDCODE",
	}))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d: %s", w.Code, w.Body.String())
	}
}

// seedUserWithHash creates a user with a specific bcrypt hash.
func seedUserWithHash(t *testing.T, username, hash string) {
	t.Helper()
	u := models.User{
		Username:     username,
		Email:        username + "@test.com",
		PasswordHash: hash,
		Role:         "user",
		CardSlots:    3,
	}
	if err := models.DB.Create(&u).Error; err != nil {
		t.Fatalf("seedUserWithHash: %v", err)
	}
}

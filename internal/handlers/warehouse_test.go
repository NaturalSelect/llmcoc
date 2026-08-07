package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/llmcoc/server/internal/models"
)

func warehouseRouter(userID uint) *gin.Engine {
	r := gin.New()
	auth := withAuth(userID, "tester", "user")
	r.GET("/warehouse", auth, GetWarehouse)
	r.POST("/warehouse/deposit", auth, DepositToWarehouse)
	r.POST("/warehouse/withdraw", auth, WithdrawFromWarehouse)
	r.DELETE("/warehouse/items/*item", auth, DiscardWarehouseItem)
	return r
}

// setCardInventory 直接写入人物卡的物品栏，供测试构造前置状态。
func setCardInventory(t *testing.T, cardID uint, items []string) {
	t.Helper()
	if err := models.DB.Model(&models.CharacterCard{}).Where("id = ?", cardID).
		Update("inventory", models.JSONField[[]string]{Data: items}).Error; err != nil {
		t.Fatalf("setCardInventory: %v", err)
	}
}

// setUserWarehouse 直接写入账号仓库，供测试构造前置状态。
func setUserWarehouse(t *testing.T, userID uint, items []string) {
	t.Helper()
	if err := models.DB.Model(&models.User{}).Where("id = ?", userID).
		Update("warehouse", models.JSONField[[]string]{Data: items}).Error; err != nil {
		t.Fatalf("setUserWarehouse: %v", err)
	}
}

// lockCardInSession 让人物卡出现在一个指定状态的房间中，用于测试仓库操作的 session 占用锁定。
func lockCardInSession(t *testing.T, userID, cardID uint, status models.SessionStatus) {
	t.Helper()
	sid := seedScenario(t, "Lock Scenario")
	session := models.GameSession{
		Name: "Lock Room", ScenarioID: sid, Status: status, MaxPlayers: 4, CreatedBy: userID,
	}
	if err := models.DB.Create(&session).Error; err != nil {
		t.Fatalf("create session: %v", err)
	}
	if err := models.DB.Create(&models.SessionPlayer{
		SessionID: session.ID, UserID: userID, CharacterCardID: cardID,
	}).Error; err != nil {
		t.Fatalf("create session player: %v", err)
	}
}

func cardInventory(t *testing.T, cardID uint) []string {
	t.Helper()
	var card models.CharacterCard
	if err := models.DB.First(&card, cardID).Error; err != nil {
		t.Fatalf("load card: %v", err)
	}
	return card.Inventory.Data
}

func userWarehouse(t *testing.T, userID uint) []string {
	t.Helper()
	var user models.User
	if err := models.DB.First(&user, userID).Error; err != nil {
		t.Fatalf("load user: %v", err)
	}
	return user.Warehouse.Data
}

// ── GetWarehouse ─────────────────────────────────────────────────────────────

func TestGetWarehouse_EmptyReturnsArray(t *testing.T) {
	initTestDB(t)
	uid := seedUser(t, "u", "user", 0, 3)

	r := warehouseRouter(uid)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, jsonReq("GET", "/warehouse", nil))

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp struct {
		Warehouse []string `json:"warehouse"`
	}
	json.NewDecoder(w.Body).Decode(&resp)
	if resp.Warehouse == nil {
		t.Errorf("want empty array, got null")
	}
	if len(resp.Warehouse) != 0 {
		t.Errorf("want 0 items, got %d", len(resp.Warehouse))
	}
}

// ── DepositToWarehouse ───────────────────────────────────────────────────────

func TestDeposit_Success(t *testing.T) {
	initTestDB(t)
	uid := seedUser(t, "u", "user", 0, 3)
	cardID := seedCard(t, uid, "Card A")
	setCardInventory(t, cardID, []string{"绷带(x3)"})

	r := warehouseRouter(uid)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, jsonReq("POST", "/warehouse/deposit", map[string]any{
		"character_card_id": cardID,
		"item":              "绷带(x3)",
	}))

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body.String())
	}
	if inv := cardInventory(t, cardID); len(inv) != 0 {
		t.Errorf("card inventory should be empty, got %v", inv)
	}
	if wh := userWarehouse(t, uid); len(wh) != 1 || wh[0] != "绷带(x3)" {
		t.Errorf("warehouse should have 1 item, got %v", wh)
	}
}

// TestDeposit_DuplicateItemNotLost 验证角色和仓库都已有同名物品时，存入操作不会因为
// "看起来重复"而丢弃物品——搬运侧必须无条件 append，物品总量必须守恒。
func TestDeposit_DuplicateItemNotLost(t *testing.T) {
	initTestDB(t)
	uid := seedUser(t, "u", "user", 0, 3)
	cardID := seedCard(t, uid, "Card A")
	setCardInventory(t, cardID, []string{"绷带(x3)"})
	setUserWarehouse(t, uid, []string{"绷带(x3)"})

	r := warehouseRouter(uid)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, jsonReq("POST", "/warehouse/deposit", map[string]any{
		"character_card_id": cardID,
		"item":              "绷带(x3)",
	}))

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body.String())
	}
	if inv := cardInventory(t, cardID); len(inv) != 0 {
		t.Errorf("card inventory should be empty, got %v", inv)
	}
	wh := userWarehouse(t, uid)
	if len(wh) != 2 {
		t.Fatalf("warehouse should have 2 items (conservation), got %v", wh)
	}
}

// TestDeposit_RemovesOnlyOneOfDuplicates 验证角色有两条同名物品时，存入一条只扣一条。
func TestDeposit_RemovesOnlyOneOfDuplicates(t *testing.T) {
	initTestDB(t)
	uid := seedUser(t, "u", "user", 0, 3)
	cardID := seedCard(t, uid, "Card A")
	setCardInventory(t, cardID, []string{"绷带(x3)", "绷带(x3)"})

	r := warehouseRouter(uid)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, jsonReq("POST", "/warehouse/deposit", map[string]any{
		"character_card_id": cardID,
		"item":              "绷带(x3)",
	}))

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body.String())
	}
	if inv := cardInventory(t, cardID); len(inv) != 1 {
		t.Errorf("card inventory should have 1 item left, got %v", inv)
	}
	if wh := userWarehouse(t, uid); len(wh) != 1 {
		t.Errorf("warehouse should have 1 item, got %v", wh)
	}
}

func TestDeposit_ItemNotInInventory(t *testing.T) {
	initTestDB(t)
	uid := seedUser(t, "u", "user", 0, 3)
	cardID := seedCard(t, uid, "Card A")
	setCardInventory(t, cardID, []string{"手电筒"})

	r := warehouseRouter(uid)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, jsonReq("POST", "/warehouse/deposit", map[string]any{
		"character_card_id": cardID,
		"item":              "绷带(x3)",
	}))

	if w.Code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d: %s", w.Code, w.Body.String())
	}
	if inv := cardInventory(t, cardID); len(inv) != 1 || inv[0] != "手电筒" {
		t.Errorf("card inventory should be unchanged, got %v", inv)
	}
	if wh := userWarehouse(t, uid); len(wh) != 0 {
		t.Errorf("warehouse should remain empty (no partial write), got %v", wh)
	}
}

func TestDeposit_OtherUsersCard_Forbidden(t *testing.T) {
	initTestDB(t)
	uid := seedUser(t, "u", "user", 0, 3)
	other := seedUser(t, "other", "user", 0, 3)
	cardID := seedCard(t, other, "Not Yours")
	setCardInventory(t, cardID, []string{"绷带(x3)"})

	r := warehouseRouter(uid)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, jsonReq("POST", "/warehouse/deposit", map[string]any{
		"character_card_id": cardID,
		"item":              "绷带(x3)",
	}))

	if w.Code != http.StatusForbidden {
		t.Fatalf("want 403, got %d: %s", w.Code, w.Body.String())
	}
}

func TestDeposit_CardInLobbySession_Conflict(t *testing.T) {
	initTestDB(t)
	uid := seedUser(t, "u", "user", 0, 3)
	cardID := seedCard(t, uid, "Card A")
	setCardInventory(t, cardID, []string{"绷带(x3)"})
	lockCardInSession(t, uid, cardID, models.SessionStatusLobby)

	r := warehouseRouter(uid)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, jsonReq("POST", "/warehouse/deposit", map[string]any{
		"character_card_id": cardID,
		"item":              "绷带(x3)",
	}))

	if w.Code != http.StatusConflict {
		t.Fatalf("want 409, got %d: %s", w.Code, w.Body.String())
	}
}

func TestDeposit_CardInPlayingSession_Conflict(t *testing.T) {
	initTestDB(t)
	uid := seedUser(t, "u", "user", 0, 3)
	cardID := seedCard(t, uid, "Card A")
	setCardInventory(t, cardID, []string{"绷带(x3)"})
	lockCardInSession(t, uid, cardID, models.SessionStatusPlaying)

	r := warehouseRouter(uid)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, jsonReq("POST", "/warehouse/deposit", map[string]any{
		"character_card_id": cardID,
		"item":              "绷带(x3)",
	}))

	if w.Code != http.StatusConflict {
		t.Fatalf("want 409, got %d: %s", w.Code, w.Body.String())
	}
}

func TestDeposit_CardInEndedSession_Success(t *testing.T) {
	initTestDB(t)
	uid := seedUser(t, "u", "user", 0, 3)
	cardID := seedCard(t, uid, "Card A")
	setCardInventory(t, cardID, []string{"绷带(x3)"})
	lockCardInSession(t, uid, cardID, models.SessionStatusEnded)

	r := warehouseRouter(uid)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, jsonReq("POST", "/warehouse/deposit", map[string]any{
		"character_card_id": cardID,
		"item":              "绷带(x3)",
	}))

	if w.Code != http.StatusOK {
		t.Fatalf("want 200 (ended session should not lock), got %d: %s", w.Code, w.Body.String())
	}
}

// ── WithdrawFromWarehouse ────────────────────────────────────────────────────

func TestWithdraw_Success(t *testing.T) {
	initTestDB(t)
	uid := seedUser(t, "u", "user", 0, 3)
	cardID := seedCard(t, uid, "Card A")
	setUserWarehouse(t, uid, []string{"左轮手枪(.38, x1)"})

	r := warehouseRouter(uid)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, jsonReq("POST", "/warehouse/withdraw", map[string]any{
		"character_card_id": cardID,
		"item":              "左轮手枪(.38, x1)",
	}))

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body.String())
	}
	if wh := userWarehouse(t, uid); len(wh) != 0 {
		t.Errorf("warehouse should be empty, got %v", wh)
	}
	if inv := cardInventory(t, cardID); len(inv) != 1 || inv[0] != "左轮手枪(.38, x1)" {
		t.Errorf("card inventory should have the item, got %v", inv)
	}
}

func TestWithdraw_ItemNotInWarehouse(t *testing.T) {
	initTestDB(t)
	uid := seedUser(t, "u", "user", 0, 3)
	cardID := seedCard(t, uid, "Card A")

	r := warehouseRouter(uid)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, jsonReq("POST", "/warehouse/withdraw", map[string]any{
		"character_card_id": cardID,
		"item":              "左轮手枪(.38, x1)",
	}))

	if w.Code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d: %s", w.Code, w.Body.String())
	}
}

func TestWithdraw_CardInSession_Conflict(t *testing.T) {
	initTestDB(t)
	uid := seedUser(t, "u", "user", 0, 3)
	cardID := seedCard(t, uid, "Card A")
	setUserWarehouse(t, uid, []string{"左轮手枪(.38, x1)"})
	lockCardInSession(t, uid, cardID, models.SessionStatusPlaying)

	r := warehouseRouter(uid)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, jsonReq("POST", "/warehouse/withdraw", map[string]any{
		"character_card_id": cardID,
		"item":              "左轮手枪(.38, x1)",
	}))

	if w.Code != http.StatusConflict {
		t.Fatalf("want 409, got %d: %s", w.Code, w.Body.String())
	}
	if wh := userWarehouse(t, uid); len(wh) != 1 {
		t.Errorf("warehouse should be unchanged, got %v", wh)
	}
}

// ── DiscardWarehouseItem ─────────────────────────────────────────────────────

func TestDiscardWarehouseItem_Success(t *testing.T) {
	initTestDB(t)
	uid := seedUser(t, "u", "user", 0, 3)
	setUserWarehouse(t, uid, []string{"绷带(x3)", "手电筒"})

	r := warehouseRouter(uid)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, jsonReq("DELETE", "/warehouse/items/"+"绷带(x3)", nil))

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body.String())
	}
	wh := userWarehouse(t, uid)
	if len(wh) != 1 || wh[0] != "手电筒" {
		t.Errorf("warehouse should only have 手电筒 left, got %v", wh)
	}
}

// TestDiscardWarehouseItem_AllCardsInSession_StillWorks 验证即使账号下所有人物卡都在游戏中，
// 仓库内直接丢弃也不受影响（丢弃不涉及人物卡，不做 session 占用校验）。
func TestDiscardWarehouseItem_AllCardsInSession_StillWorks(t *testing.T) {
	initTestDB(t)
	uid := seedUser(t, "u", "user", 0, 3)
	cardID := seedCard(t, uid, "Card A")
	setUserWarehouse(t, uid, []string{"绷带(x3)"})
	lockCardInSession(t, uid, cardID, models.SessionStatusPlaying)

	r := warehouseRouter(uid)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, jsonReq("DELETE", "/warehouse/items/"+"绷带(x3)", nil))

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body.String())
	}
}

// ── ListCharacters in_session flag ───────────────────────────────────────────

func TestListCharacters_InSessionFlag(t *testing.T) {
	initTestDB(t)
	uid := seedUser(t, "u", "user", 0, 3)
	freeCard := seedCard(t, uid, "Free Card")
	lockedCard := seedCard(t, uid, "Locked Card")
	lockCardInSession(t, uid, lockedCard, models.SessionStatusLobby)

	r := gin.New()
	r.GET("/characters", withAuth(uid, "u", "user"), ListCharacters)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, jsonReq("GET", "/characters", nil))

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp PaginatedResponse[models.CharacterCard]
	json.NewDecoder(w.Body).Decode(&resp)
	flags := map[uint]bool{}
	for _, c := range resp.Items {
		flags[c.ID] = c.InSession
	}
	if flags[freeCard] {
		t.Errorf("free card should have in_session=false")
	}
	if !flags[lockedCard] {
		t.Errorf("locked card should have in_session=true")
	}
}

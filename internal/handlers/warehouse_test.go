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
func setCardInventory(t *testing.T, cardID uint, items []models.Item) {
	t.Helper()
	if err := models.DB.Model(&models.CharacterCard{}).Where("id = ?", cardID).
		Update("inventory", models.JSONField[[]models.Item]{Data: items}).Error; err != nil {
		t.Fatalf("setCardInventory: %v", err)
	}
}

// setUserWarehouse 直接写入账号仓库，供测试构造前置状态。
func setUserWarehouse(t *testing.T, userID uint, items []models.Item) {
	t.Helper()
	if err := models.DB.Model(&models.User{}).Where("id = ?", userID).
		Update("warehouse", models.JSONField[[]models.Item]{Data: items}).Error; err != nil {
		t.Fatalf("setUserWarehouse: %v", err)
	}
}

// stack 构造一个测试用物品堆。
func stack(name, desc string, count int) models.Item {
	return models.Item{Name: name, Desc: desc, Count: count, Type: models.ItemTypeMisc}
}

// rawSetColumn 绕过 JSONField，直接把原始 JSON 文本写进列，用来模拟升级前的旧字符串数据。
func rawSetColumn(t *testing.T, table string, id uint, column, raw string) {
	t.Helper()
	if err := models.DB.Exec("UPDATE "+table+" SET "+column+" = ? WHERE id = ?", raw, id).Error; err != nil {
		t.Fatalf("rawSetColumn: %v", err)
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

func cardInventory(t *testing.T, cardID uint) []models.Item {
	t.Helper()
	var card models.CharacterCard
	if err := models.DB.First(&card, cardID).Error; err != nil {
		t.Fatalf("load card: %v", err)
	}
	return card.Inventory.Data
}

func userWarehouse(t *testing.T, userID uint) []models.Item {
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
		Warehouse []models.Item `json:"warehouse"`
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
	setCardInventory(t, cardID, []models.Item{stack("绷带", "", 3)})

	r := warehouseRouter(uid)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, jsonReq("POST", "/warehouse/deposit", map[string]any{
		"character_card_id": cardID,
		"item":              map[string]any{"name": "绷带"},
	}))

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body.String())
	}
	if inv := cardInventory(t, cardID); len(inv) != 0 {
		t.Errorf("card inventory should be empty, got %v", inv)
	}
	if wh := userWarehouse(t, uid); len(wh) != 1 || wh[0].Name != "绷带" || wh[0].Count != 3 {
		t.Errorf("warehouse should have 绷带 x3, got %v", wh)
	}
}

// TestDeposit_RejectsStoryItem 验证剧情物品不能存入仓库，人物卡和仓库都保持原样。
func TestDeposit_RejectsStoryItem(t *testing.T) {
	initTestDB(t)
	uid := seedUser(t, "u", "user", 0, 3)
	cardID := seedCard(t, uid, "Card A")
	setCardInventory(t, cardID, []models.Item{{Name: "请柬", Count: 1, Type: models.ItemTypeStory}})

	r := warehouseRouter(uid)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, jsonReq("POST", "/warehouse/deposit", map[string]any{
		"character_card_id": cardID,
		"item":              map[string]any{"name": "请柬"},
	}))

	if w.Code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d: %s", w.Code, w.Body.String())
	}
	if inv := cardInventory(t, cardID); len(inv) != 1 || inv[0].Type != models.ItemTypeStory {
		t.Errorf("card inventory should be untouched, got %+v", inv)
	}
	if wh := userWarehouse(t, uid); len(wh) != 0 {
		t.Errorf("warehouse should stay empty, got %+v", wh)
	}
}

// TestDeposit_ClearsEquipped 验证装备中的物品存入仓库后不再带装备状态。
func TestDeposit_ClearsEquipped(t *testing.T) {
	initTestDB(t)
	uid := seedUser(t, "u", "user", 0, 3)
	cardID := seedCard(t, uid, "Card A")
	worn := stack("风衣", "", 1)
	worn.Equipped = true
	setCardInventory(t, cardID, []models.Item{worn})

	r := warehouseRouter(uid)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, jsonReq("POST", "/warehouse/deposit", map[string]any{
		"character_card_id": cardID,
		"item":              map[string]any{"name": "风衣"},
	}))
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body.String())
	}
	if wh := userWarehouse(t, uid); len(wh) != 1 || wh[0].Equipped {
		t.Errorf("warehouse item should not be equipped, got %+v", wh)
	}
}

// TestDeposit_MergesIntoExistingStack 验证角色和仓库都已有同名同描述物品时，存入后数量合并，总量守恒。
func TestDeposit_MergesIntoExistingStack(t *testing.T) {
	initTestDB(t)
	uid := seedUser(t, "u", "user", 0, 3)
	cardID := seedCard(t, uid, "Card A")
	setCardInventory(t, cardID, []models.Item{stack("绷带", "", 3)})
	setUserWarehouse(t, uid, []models.Item{stack("绷带", "", 3)})

	r := warehouseRouter(uid)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, jsonReq("POST", "/warehouse/deposit", map[string]any{
		"character_card_id": cardID,
		"item":              map[string]any{"name": "绷带"},
	}))

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body.String())
	}
	if inv := cardInventory(t, cardID); len(inv) != 0 {
		t.Errorf("card inventory should be empty, got %v", inv)
	}
	wh := userWarehouse(t, uid)
	if len(wh) != 1 || wh[0].Count != 6 {
		t.Fatalf("warehouse should merge into one stack of 6, got %v", wh)
	}
}

// TestDeposit_PartialCount 验证带 count 时只转移该数量，剩余留在人物卡。
func TestDeposit_PartialCount(t *testing.T) {
	initTestDB(t)
	uid := seedUser(t, "u", "user", 0, 3)
	cardID := seedCard(t, uid, "Card A")
	setCardInventory(t, cardID, []models.Item{stack("绷带", "", 3)})

	r := warehouseRouter(uid)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, jsonReq("POST", "/warehouse/deposit", map[string]any{
		"character_card_id": cardID,
		"item":              map[string]any{"name": "绷带"},
		"count":             1,
	}))

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body.String())
	}
	if inv := cardInventory(t, cardID); len(inv) != 1 || inv[0].Count != 2 {
		t.Errorf("card inventory should keep 2, got %v", inv)
	}
	if wh := userWarehouse(t, uid); len(wh) != 1 || wh[0].Count != 1 {
		t.Errorf("warehouse should have 1, got %v", wh)
	}
}

// TestDeposit_DifferentDescDoesNotMerge 验证同名但描述不同的物品不会被合并，也不会误取。
func TestDeposit_DifferentDescDoesNotMerge(t *testing.T) {
	initTestDB(t)
	uid := seedUser(t, "u", "user", 0, 3)
	cardID := seedCard(t, uid, "Card A")
	setCardInventory(t, cardID, []models.Item{stack("手电筒", "没电", 1), stack("手电筒", "电量充足", 1)})
	setUserWarehouse(t, uid, []models.Item{stack("手电筒", "没电", 1)})

	r := warehouseRouter(uid)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, jsonReq("POST", "/warehouse/deposit", map[string]any{
		"character_card_id": cardID,
		"item":              map[string]any{"name": "手电筒", "desc": "电量充足"},
	}))

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body.String())
	}
	if inv := cardInventory(t, cardID); len(inv) != 1 || inv[0].Desc != "没电" {
		t.Errorf("card inventory should keep the dead flashlight, got %v", inv)
	}
	if wh := userWarehouse(t, uid); len(wh) != 2 {
		t.Errorf("warehouse should have two stacks, got %v", wh)
	}
}

// TestDeposit_LegacyStringData 验证升级前写入的旧字符串数据在读取和存入时仍然可用。
func TestDeposit_LegacyStringData(t *testing.T) {
	initTestDB(t)
	uid := seedUser(t, "u", "user", 0, 3)
	cardID := seedCard(t, uid, "Card A")
	rawSetColumn(t, "character_cards", cardID, "inventory", `["绷带(x3)","手电筒(没电)"]`)
	rawSetColumn(t, "users", uid, "warehouse", `["绷带(x2)"]`)

	r := warehouseRouter(uid)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, jsonReq("POST", "/warehouse/deposit", map[string]any{
		"character_card_id": cardID,
		"item":              map[string]any{"name": "绷带"},
	}))

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body.String())
	}
	if inv := cardInventory(t, cardID); len(inv) != 1 || inv[0].Name != "手电筒" || inv[0].Desc != "没电" {
		t.Errorf("card inventory = %v", inv)
	}
	if wh := userWarehouse(t, uid); len(wh) != 1 || wh[0].Name != "绷带" || wh[0].Count != 5 {
		t.Errorf("warehouse = %v", wh)
	}

	w = httptest.NewRecorder()
	r.ServeHTTP(w, jsonReq("GET", "/warehouse", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("get warehouse: %d %s", w.Code, w.Body.String())
	}
}

func TestDeposit_ItemNotInInventory(t *testing.T) {
	initTestDB(t)
	uid := seedUser(t, "u", "user", 0, 3)
	cardID := seedCard(t, uid, "Card A")
	setCardInventory(t, cardID, []models.Item{stack("手电筒", "", 1)})

	r := warehouseRouter(uid)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, jsonReq("POST", "/warehouse/deposit", map[string]any{
		"character_card_id": cardID,
		"item":              map[string]any{"name": "绷带"},
	}))

	if w.Code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d: %s", w.Code, w.Body.String())
	}
	if inv := cardInventory(t, cardID); len(inv) != 1 || inv[0].Name != "手电筒" {
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
	setCardInventory(t, cardID, []models.Item{stack("绷带", "", 3)})

	r := warehouseRouter(uid)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, jsonReq("POST", "/warehouse/deposit", map[string]any{
		"character_card_id": cardID,
		"item":              map[string]any{"name": "绷带"},
	}))

	if w.Code != http.StatusForbidden {
		t.Fatalf("want 403, got %d: %s", w.Code, w.Body.String())
	}
}

func TestDeposit_CardInLobbySession_Conflict(t *testing.T) {
	initTestDB(t)
	uid := seedUser(t, "u", "user", 0, 3)
	cardID := seedCard(t, uid, "Card A")
	setCardInventory(t, cardID, []models.Item{stack("绷带", "", 3)})
	lockCardInSession(t, uid, cardID, models.SessionStatusLobby)

	r := warehouseRouter(uid)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, jsonReq("POST", "/warehouse/deposit", map[string]any{
		"character_card_id": cardID,
		"item":              map[string]any{"name": "绷带"},
	}))

	if w.Code != http.StatusConflict {
		t.Fatalf("want 409, got %d: %s", w.Code, w.Body.String())
	}
}

func TestDeposit_CardInPlayingSession_Conflict(t *testing.T) {
	initTestDB(t)
	uid := seedUser(t, "u", "user", 0, 3)
	cardID := seedCard(t, uid, "Card A")
	setCardInventory(t, cardID, []models.Item{stack("绷带", "", 3)})
	lockCardInSession(t, uid, cardID, models.SessionStatusPlaying)

	r := warehouseRouter(uid)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, jsonReq("POST", "/warehouse/deposit", map[string]any{
		"character_card_id": cardID,
		"item":              map[string]any{"name": "绷带"},
	}))

	if w.Code != http.StatusConflict {
		t.Fatalf("want 409, got %d: %s", w.Code, w.Body.String())
	}
}

func TestDeposit_CardInEndedSession_Success(t *testing.T) {
	initTestDB(t)
	uid := seedUser(t, "u", "user", 0, 3)
	cardID := seedCard(t, uid, "Card A")
	setCardInventory(t, cardID, []models.Item{stack("绷带", "", 3)})
	lockCardInSession(t, uid, cardID, models.SessionStatusEnded)

	r := warehouseRouter(uid)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, jsonReq("POST", "/warehouse/deposit", map[string]any{
		"character_card_id": cardID,
		"item":              map[string]any{"name": "绷带"},
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
	setUserWarehouse(t, uid, []models.Item{stack("左轮手枪", ".38", 1)})

	r := warehouseRouter(uid)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, jsonReq("POST", "/warehouse/withdraw", map[string]any{
		"character_card_id": cardID,
		"item":              map[string]any{"name": "左轮手枪", "desc": ".38"},
	}))

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body.String())
	}
	if wh := userWarehouse(t, uid); len(wh) != 0 {
		t.Errorf("warehouse should be empty, got %v", wh)
	}
	if inv := cardInventory(t, cardID); len(inv) != 1 || inv[0].Name != "左轮手枪" || inv[0].Desc != ".38" {
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
		"item":              map[string]any{"name": "左轮手枪", "desc": ".38"},
	}))

	if w.Code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d: %s", w.Code, w.Body.String())
	}
}

func TestWithdraw_CardInSession_Conflict(t *testing.T) {
	initTestDB(t)
	uid := seedUser(t, "u", "user", 0, 3)
	cardID := seedCard(t, uid, "Card A")
	setUserWarehouse(t, uid, []models.Item{stack("左轮手枪", ".38", 1)})
	lockCardInSession(t, uid, cardID, models.SessionStatusPlaying)

	r := warehouseRouter(uid)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, jsonReq("POST", "/warehouse/withdraw", map[string]any{
		"character_card_id": cardID,
		"item":              map[string]any{"name": "左轮手枪", "desc": ".38"},
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
	setUserWarehouse(t, uid, []models.Item{stack("绷带", "", 3), stack("手电筒", "", 1)})

	r := warehouseRouter(uid)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, jsonReq("DELETE", "/warehouse/items/绷带", nil))

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body.String())
	}
	wh := userWarehouse(t, uid)
	if len(wh) != 1 || wh[0].Name != "手电筒" {
		t.Errorf("warehouse should only have 手电筒 left, got %v", wh)
	}
}

// TestDiscardWarehouseItem_PartialByQuery 验证 desc 和 count 通过 query 传入，只扣减指定数量。
func TestDiscardWarehouseItem_PartialByQuery(t *testing.T) {
	initTestDB(t)
	uid := seedUser(t, "u", "user", 0, 3)
	setUserWarehouse(t, uid, []models.Item{stack("子弹", "", 6), stack("子弹", "穿甲", 2)})

	r := warehouseRouter(uid)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, jsonReq("DELETE", "/warehouse/items/子弹?desc=%E7%A9%BF%E7%94%B2&count=1", nil))

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body.String())
	}
	wh := userWarehouse(t, uid)
	if len(wh) != 2 || wh[0].Count != 6 || wh[1].Count != 1 {
		t.Errorf("warehouse = %v", wh)
	}

	w = httptest.NewRecorder()
	r.ServeHTTP(w, jsonReq("DELETE", "/warehouse/items/子弹?count=abc", nil))
	if w.Code != http.StatusBadRequest {
		t.Errorf("invalid count want 400, got %d", w.Code)
	}
}

// TestDiscardWarehouseItem_AllCardsInSession_StillWorks 验证即使账号下所有人物卡都在游戏中，
// 仓库内直接丢弃也不受影响（丢弃不涉及人物卡，不做 session 占用校验）。
func TestDiscardWarehouseItem_AllCardsInSession_StillWorks(t *testing.T) {
	initTestDB(t)
	uid := seedUser(t, "u", "user", 0, 3)
	cardID := seedCard(t, uid, "Card A")
	setUserWarehouse(t, uid, []models.Item{stack("绷带", "", 3)})
	lockCardInSession(t, uid, cardID, models.SessionStatusPlaying)

	r := warehouseRouter(uid)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, jsonReq("DELETE", "/warehouse/items/绷带", nil))

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

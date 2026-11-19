package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/llmcoc/server/internal/models"
)

// auctionRouter 用 X-Test-User 头区分调用者，方便在同一个测试里让多个玩家互相交易。
func auctionRouter() *gin.Engine {
	r := gin.New()
	auth := func(c *gin.Context) {
		uid, _ := strconv.ParseUint(c.GetHeader("X-Test-User"), 10, 64)
		c.Set("user_id", uint(uid))
		c.Set("username", "tester")
		c.Set("role", "user")
		c.Next()
	}
	r.GET("/auction/listings", auth, ListAuctionListings)
	r.GET("/auction/mine", auth, ListMyAuctionListings)
	r.POST("/auction/listings", auth, CreateAuctionListing)
	r.POST("/auction/listings/:id/buy", auth, BuyAuctionListing)
	r.DELETE("/auction/listings/:id", auth, CancelAuctionListing)
	return r
}

func auctionDo(r *gin.Engine, uid uint, method, path string, body any) *httptest.ResponseRecorder {
	req := jsonReq(method, path, body)
	req.Header.Set("X-Test-User", strconv.FormatUint(uint64(uid), 10))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func seedListing(t *testing.T, sellerID uint, name string, itemType models.ItemType, count, price int) uint {
	t.Helper()
	l := models.AuctionListing{
		SellerID: sellerID, ItemName: name, ItemType: itemType, Count: count, Price: price, Status: models.AuctionStatusActive,
	}
	if err := models.DB.Create(&l).Error; err != nil {
		t.Fatalf("seedListing %q: %v", name, err)
	}
	return l.ID
}

func loadListing(t *testing.T, id uint) models.AuctionListing {
	t.Helper()
	var l models.AuctionListing
	if err := models.DB.First(&l, id).Error; err != nil {
		t.Fatalf("load listing: %v", err)
	}
	return l
}

func listingNames(resp PaginatedResponse[auctionListingResponse]) []string {
	names := make([]string, 0, len(resp.Items))
	for _, l := range resp.Items {
		names = append(names, l.ItemName)
	}
	return names
}

func decodeListings(t *testing.T, w *httptest.ResponseRecorder) PaginatedResponse[auctionListingResponse] {
	t.Helper()
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp PaginatedResponse[auctionListingResponse]
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return resp
}

func TestCreateAuctionListing_MovesItemOutOfWarehouse(t *testing.T) {
	initTestDB(t)
	seller := seedUser(t, "seller", "user", 0, 3)
	setUserWarehouse(t, seller, []models.Item{stack("绷带", "染血", 3), stack("地图", "", 1)})

	w := auctionDo(auctionRouter(), seller, "POST", "/auction/listings", map[string]any{
		"item": map[string]any{"name": "绷带", "desc": "染血"}, "count": 2, "price": 50,
	})
	if w.Code != http.StatusCreated {
		t.Fatalf("want 201, got %d: %s", w.Code, w.Body.String())
	}

	wh := userWarehouse(t, seller)
	if len(wh) != 2 || wh[0].Name != "绷带" || wh[0].Count != 1 {
		t.Errorf("warehouse after listing = %+v, want 绷带 x1 and 地图", wh)
	}
	var l models.AuctionListing
	if err := models.DB.Where("seller_id = ?", seller).First(&l).Error; err != nil {
		t.Fatalf("listing not stored: %v", err)
	}
	if l.ItemName != "绷带" || l.ItemDesc != "染血" || l.Count != 2 || l.Price != 50 || l.Status != models.AuctionStatusActive {
		t.Errorf("listing = %+v", l)
	}
}

func TestCreateAuctionListing_WholeStackRemovesIt(t *testing.T) {
	initTestDB(t)
	seller := seedUser(t, "seller", "user", 0, 3)
	setUserWarehouse(t, seller, []models.Item{stack("绷带", "", 3)})

	w := auctionDo(auctionRouter(), seller, "POST", "/auction/listings", map[string]any{
		"item": map[string]any{"name": "绷带"}, "count": 3, "price": 10,
	})
	if w.Code != http.StatusCreated {
		t.Fatalf("want 201, got %d: %s", w.Code, w.Body.String())
	}
	if wh := userWarehouse(t, seller); len(wh) != 0 {
		t.Errorf("warehouse should be empty, got %+v", wh)
	}
}

func TestCreateAuctionListing_Rejections(t *testing.T) {
	initTestDB(t)
	seller := seedUser(t, "seller", "user", 0, 3)
	setUserWarehouse(t, seller, []models.Item{stack("绷带", "", 2)})
	r := auctionRouter()

	cases := []struct {
		name string
		body map[string]any
	}{
		{"物品不在仓库", map[string]any{"item": map[string]any{"name": "手枪"}, "count": 1, "price": 10}},
		{"数量超过库存", map[string]any{"item": map[string]any{"name": "绷带"}, "count": 3, "price": 10}},
		{"数量为零", map[string]any{"item": map[string]any{"name": "绷带"}, "count": 0, "price": 10}},
		{"价格为零", map[string]any{"item": map[string]any{"name": "绷带"}, "count": 1, "price": 0}},
		{"价格为负", map[string]any{"item": map[string]any{"name": "绷带"}, "count": 1, "price": -5}},
		{"物品名为空", map[string]any{"item": map[string]any{"name": "  "}, "count": 1, "price": 10}},
	}
	for _, tc := range cases {
		if w := auctionDo(r, seller, "POST", "/auction/listings", tc.body); w.Code != http.StatusBadRequest {
			t.Errorf("%s: want 400, got %d: %s", tc.name, w.Code, w.Body.String())
		}
	}

	if wh := userWarehouse(t, seller); len(wh) != 1 || wh[0].Count != 2 {
		t.Errorf("rejected listings must not touch the warehouse, got %+v", wh)
	}
	var n int64
	models.DB.Model(&models.AuctionListing{}).Count(&n)
	if n != 0 {
		t.Errorf("rejected listings must not be stored, got %d", n)
	}
}

func TestListAuctionListings_OnlyActiveWithSellerName(t *testing.T) {
	initTestDB(t)
	seller := seedUser(t, "老王", "user", 0, 3)
	viewer := seedUser(t, "viewer", "user", 0, 3)
	seedListing(t, seller, "绷带", models.ItemTypeMisc, 1, 10)
	sold := seedListing(t, seller, "手枪", models.ItemTypeWeapon, 1, 99)
	models.DB.Model(&models.AuctionListing{}).Where("id = ?", sold).Update("status", models.AuctionStatusSold)

	resp := decodeListings(t, auctionDo(auctionRouter(), viewer, "GET", "/auction/listings", nil))
	if resp.Total != 1 || len(resp.Items) != 1 || resp.Items[0].ItemName != "绷带" {
		t.Fatalf("want only the active listing, got %v", listingNames(resp))
	}
	if resp.Items[0].SellerName != "老王" || resp.Items[0].SellerID != seller {
		t.Errorf("seller = %q/%d", resp.Items[0].SellerName, resp.Items[0].SellerID)
	}
}

func TestListAuctionListings_SearchEscapesWildcards(t *testing.T) {
	initTestDB(t)
	seller := seedUser(t, "seller", "user", 0, 3)
	viewer := seedUser(t, "viewer", "user", 0, 3)
	seedListing(t, seller, "绷带", models.ItemTypeMisc, 1, 10)
	seedListing(t, seller, "100%纯银", models.ItemTypeMisc, 1, 10)
	seedListing(t, seller, "a_b", models.ItemTypeMisc, 1, 10)
	seedListing(t, seller, "axb", models.ItemTypeMisc, 1, 10)
	r := auctionRouter()

	cases := []struct {
		q    string
		want []string
	}{
		{"%25", []string{"100%纯银"}},
		{"_", []string{"a_b"}},
		{"绷", []string{"绷带"}},
		{"%E4%B8%8D%E5%AD%98%E5%9C%A8", []string{}},
	}
	for _, tc := range cases {
		got := listingNames(decodeListings(t, auctionDo(r, viewer, "GET", "/auction/listings?q="+tc.q, nil)))
		if fmt.Sprint(got) != fmt.Sprint(tc.want) {
			t.Errorf("q=%s: got %v, want %v", tc.q, got, tc.want)
		}
	}
}

func TestListAuctionListings_SortFilterAndPaginate(t *testing.T) {
	initTestDB(t)
	seller := seedUser(t, "seller", "user", 0, 3)
	viewer := seedUser(t, "viewer", "user", 0, 3)
	seedListing(t, seller, "贵的", models.ItemTypeArtifact, 1, 300)
	seedListing(t, seller, "便宜的", models.ItemTypeMisc, 1, 5)
	seedListing(t, seller, "中等的", models.ItemTypeMisc, 1, 50)
	r := auctionRouter()

	asc := listingNames(decodeListings(t, auctionDo(r, viewer, "GET", "/auction/listings?sort=price_asc", nil)))
	if fmt.Sprint(asc) != "[便宜的 中等的 贵的]" {
		t.Errorf("price_asc = %v", asc)
	}
	desc := listingNames(decodeListings(t, auctionDo(r, viewer, "GET", "/auction/listings?sort=price_desc", nil)))
	if fmt.Sprint(desc) != "[贵的 中等的 便宜的]" {
		t.Errorf("price_desc = %v", desc)
	}
	newest := listingNames(decodeListings(t, auctionDo(r, viewer, "GET", "/auction/listings", nil)))
	if fmt.Sprint(newest) != "[中等的 便宜的 贵的]" {
		t.Errorf("newest = %v", newest)
	}
	misc := decodeListings(t, auctionDo(r, viewer, "GET", "/auction/listings?type=misc&sort=price_asc", nil))
	if fmt.Sprint(listingNames(misc)) != "[便宜的 中等的]" || misc.Total != 2 {
		t.Errorf("type=misc = %v total=%d", listingNames(misc), misc.Total)
	}
	page2 := decodeListings(t, auctionDo(r, viewer, "GET", "/auction/listings?sort=price_asc&page=2&page_size=2", nil))
	if fmt.Sprint(listingNames(page2)) != "[贵的]" || page2.Total != 3 || page2.TotalPages != 2 {
		t.Errorf("page2 = %v total=%d pages=%d", listingNames(page2), page2.Total, page2.TotalPages)
	}

	for _, bad := range []string{"?sort=random", "?type=card_slot", "?page=0"} {
		if w := auctionDo(r, viewer, "GET", "/auction/listings"+bad, nil); w.Code != http.StatusBadRequest {
			t.Errorf("%s: want 400, got %d", bad, w.Code)
		}
	}
}

func TestListMyAuctionListings_OnlyOwnWithStatusFilter(t *testing.T) {
	initTestDB(t)
	me := seedUser(t, "me", "user", 0, 3)
	other := seedUser(t, "other", "user", 0, 3)
	seedListing(t, me, "在售", models.ItemTypeMisc, 1, 10)
	done := seedListing(t, me, "已售", models.ItemTypeMisc, 1, 10)
	models.DB.Model(&models.AuctionListing{}).Where("id = ?", done).Update("status", models.AuctionStatusSold)
	seedListing(t, other, "别人的", models.ItemTypeMisc, 1, 10)
	r := auctionRouter()

	all := decodeListings(t, auctionDo(r, me, "GET", "/auction/mine", nil))
	if all.Total != 2 {
		t.Errorf("all mine = %v", listingNames(all))
	}
	active := decodeListings(t, auctionDo(r, me, "GET", "/auction/mine?status=active", nil))
	if fmt.Sprint(listingNames(active)) != "[在售]" {
		t.Errorf("active mine = %v", listingNames(active))
	}
	if w := auctionDo(r, me, "GET", "/auction/mine?status=bogus", nil); w.Code != http.StatusBadRequest {
		t.Errorf("bogus status: want 400, got %d", w.Code)
	}
}

func TestBuyAuctionListing_Success(t *testing.T) {
	initTestDB(t)
	seller := seedUser(t, "seller", "user", 10, 3)
	buyer := seedUser(t, "buyer", "user", 200, 3)
	setUserWarehouse(t, buyer, []models.Item{stack("绷带", "染血", 1)})
	id := seedListing(t, seller, "绷带", models.ItemTypeMisc, 2, 50)
	models.DB.Model(&models.AuctionListing{}).Where("id = ?", id).Update("item_desc", "染血")

	w := auctionDo(auctionRouter(), buyer, "POST", fmt.Sprintf("/auction/listings/%d/buy", id), nil)
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp struct {
		Coins     int           `json:"coins"`
		Warehouse []models.Item `json:"warehouse"`
	}
	json.NewDecoder(w.Body).Decode(&resp)
	if resp.Coins != 150 || len(resp.Warehouse) != 1 || resp.Warehouse[0].Count != 3 {
		t.Errorf("response = %+v", resp)
	}

	if got := userCoins(t, buyer); got != 150 {
		t.Errorf("buyer coins = %d, want 150", got)
	}
	if got := userCoins(t, seller); got != 60 {
		t.Errorf("seller coins = %d, want 60", got)
	}
	if wh := userWarehouse(t, buyer); len(wh) != 1 || wh[0].Count != 3 {
		t.Errorf("buyer warehouse = %+v, want 绷带 x3 stacked", wh)
	}
	l := loadListing(t, id)
	if l.Status != models.AuctionStatusSold || l.BuyerID == nil || *l.BuyerID != buyer || l.ClosedAt == nil {
		t.Errorf("listing after sale = %+v", l)
	}
}

func TestBuyAuctionListing_InsufficientCoins(t *testing.T) {
	initTestDB(t)
	seller := seedUser(t, "seller", "user", 10, 3)
	buyer := seedUser(t, "buyer", "user", 20, 3)
	id := seedListing(t, seller, "绷带", models.ItemTypeMisc, 1, 50)

	w := auctionDo(auctionRouter(), buyer, "POST", fmt.Sprintf("/auction/listings/%d/buy", id), nil)
	if w.Code != http.StatusPaymentRequired {
		t.Fatalf("want 402, got %d: %s", w.Code, w.Body.String())
	}
	var resp map[string]any
	json.NewDecoder(w.Body).Decode(&resp)
	if resp["need"] != float64(50) || resp["current"] != float64(20) {
		t.Errorf("response = %v, want need=50 current=20", resp)
	}

	if l := loadListing(t, id); l.Status != models.AuctionStatusActive || l.BuyerID != nil {
		t.Errorf("failed purchase must leave the listing active, got %+v", l)
	}
	if userCoins(t, buyer) != 20 || userCoins(t, seller) != 10 {
		t.Errorf("coins changed: buyer=%d seller=%d", userCoins(t, buyer), userCoins(t, seller))
	}
	if wh := userWarehouse(t, buyer); len(wh) != 0 {
		t.Errorf("buyer warehouse = %+v, want empty", wh)
	}
}

func TestBuyAuctionListing_OwnListingRejected(t *testing.T) {
	initTestDB(t)
	seller := seedUser(t, "seller", "user", 500, 3)
	id := seedListing(t, seller, "绷带", models.ItemTypeMisc, 1, 50)

	w := auctionDo(auctionRouter(), seller, "POST", fmt.Sprintf("/auction/listings/%d/buy", id), nil)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d: %s", w.Code, w.Body.String())
	}
	if loadListing(t, id).Status != models.AuctionStatusActive || userCoins(t, seller) != 500 {
		t.Error("buying own listing must change nothing")
	}
}

func TestBuyAuctionListing_MissingAndAlreadySold(t *testing.T) {
	initTestDB(t)
	seller := seedUser(t, "seller", "user", 0, 3)
	first := seedUser(t, "first", "user", 100, 3)
	second := seedUser(t, "second", "user", 100, 3)
	id := seedListing(t, seller, "绷带", models.ItemTypeMisc, 1, 40)
	r := auctionRouter()

	if w := auctionDo(r, first, "POST", "/auction/listings/9999/buy", nil); w.Code != http.StatusNotFound {
		t.Errorf("missing listing: want 404, got %d", w.Code)
	}
	if w := auctionDo(r, first, "POST", "/auction/listings/abc/buy", nil); w.Code != http.StatusBadRequest {
		t.Errorf("bad id: want 400, got %d", w.Code)
	}
	if w := auctionDo(r, first, "POST", fmt.Sprintf("/auction/listings/%d/buy", id), nil); w.Code != http.StatusOK {
		t.Fatalf("first buy: want 200, got %d: %s", w.Code, w.Body.String())
	}
	if w := auctionDo(r, second, "POST", fmt.Sprintf("/auction/listings/%d/buy", id), nil); w.Code != http.StatusConflict {
		t.Errorf("second buy: want 409, got %d", w.Code)
	}
	if userCoins(t, second) != 100 || len(userWarehouse(t, second)) != 0 {
		t.Error("the second buyer must not be charged or receive the item")
	}
	if userCoins(t, seller) != 40 {
		t.Errorf("seller coins = %d, want 40 (paid once)", userCoins(t, seller))
	}
}

func TestBuyAuctionListing_ConcurrentBuyersSellOnce(t *testing.T) {
	initTestDB(t)
	seller := seedUser(t, "seller", "user", 0, 3)
	id := seedListing(t, seller, "绷带", models.ItemTypeMisc, 1, 40)
	r := auctionRouter()

	const buyers = 8
	ids := make([]uint, buyers)
	for i := range ids {
		ids[i] = seedUser(t, fmt.Sprintf("buyer%d", i), "user", 100, 3)
	}

	codes := make([]int, buyers)
	var wg sync.WaitGroup
	for i, uid := range ids {
		wg.Add(1)
		go func() {
			defer wg.Done()
			codes[i] = auctionDo(r, uid, "POST", fmt.Sprintf("/auction/listings/%d/buy", id), nil).Code
		}()
	}
	wg.Wait()

	ok, conflict := 0, 0
	for _, code := range codes {
		switch code {
		case http.StatusOK:
			ok++
		case http.StatusConflict:
			conflict++
		}
	}
	if ok != 1 || conflict != buyers-1 {
		t.Fatalf("want 1 success and %d conflicts, got codes %v", buyers-1, codes)
	}
	if got := userCoins(t, seller); got != 40 {
		t.Errorf("seller coins = %d, want 40", got)
	}
	charged := 0
	for _, uid := range ids {
		if userCoins(t, uid) != 100 {
			charged++
		}
	}
	if charged != 1 {
		t.Errorf("%d buyers were charged, want exactly 1", charged)
	}
}

func TestCancelAuctionListing_ReturnsItemToWarehouse(t *testing.T) {
	initTestDB(t)
	seller := seedUser(t, "seller", "user", 0, 3)
	setUserWarehouse(t, seller, []models.Item{stack("绷带", "", 1)})
	id := seedListing(t, seller, "绷带", models.ItemTypeMisc, 2, 50)
	r := auctionRouter()

	w := auctionDo(r, seller, "DELETE", fmt.Sprintf("/auction/listings/%d", id), nil)
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body.String())
	}
	if wh := userWarehouse(t, seller); len(wh) != 1 || wh[0].Count != 3 {
		t.Errorf("warehouse = %+v, want 绷带 x3", wh)
	}
	if l := loadListing(t, id); l.Status != models.AuctionStatusCancelled || l.ClosedAt == nil {
		t.Errorf("listing = %+v", l)
	}

	if w := auctionDo(r, seller, "DELETE", fmt.Sprintf("/auction/listings/%d", id), nil); w.Code != http.StatusConflict {
		t.Errorf("cancel twice: want 409, got %d", w.Code)
	}
	if wh := userWarehouse(t, seller); wh[0].Count != 3 {
		t.Errorf("cancelling twice must not return the item twice, got %+v", wh)
	}
	buyer := seedUser(t, "buyer", "user", 100, 3)
	if w := auctionDo(r, buyer, "POST", fmt.Sprintf("/auction/listings/%d/buy", id), nil); w.Code != http.StatusConflict {
		t.Errorf("buying a cancelled listing: want 409, got %d", w.Code)
	}
}

func TestCancelAuctionListing_OnlySellerWhileActive(t *testing.T) {
	initTestDB(t)
	seller := seedUser(t, "seller", "user", 0, 3)
	other := seedUser(t, "other", "user", 0, 3)
	buyer := seedUser(t, "buyer", "user", 100, 3)
	id := seedListing(t, seller, "绷带", models.ItemTypeMisc, 1, 50)
	r := auctionRouter()

	if w := auctionDo(r, other, "DELETE", fmt.Sprintf("/auction/listings/%d", id), nil); w.Code != http.StatusNotFound {
		t.Errorf("other user cancelling: want 404, got %d", w.Code)
	}
	if loadListing(t, id).Status != models.AuctionStatusActive {
		t.Error("another user must not be able to cancel the listing")
	}

	if w := auctionDo(r, buyer, "POST", fmt.Sprintf("/auction/listings/%d/buy", id), nil); w.Code != http.StatusOK {
		t.Fatalf("buy: want 200, got %d", w.Code)
	}
	if w := auctionDo(r, seller, "DELETE", fmt.Sprintf("/auction/listings/%d", id), nil); w.Code != http.StatusConflict {
		t.Errorf("cancelling a sold listing: want 409, got %d", w.Code)
	}
	if wh := userWarehouse(t, seller); len(wh) != 0 {
		t.Errorf("a sold item must not come back to the seller, got %+v", wh)
	}
}

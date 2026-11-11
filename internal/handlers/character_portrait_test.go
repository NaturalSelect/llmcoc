package handlers

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/llmcoc/server/internal/models"
	"github.com/llmcoc/server/internal/services/imagestore"
)

const (
	portraitDataURLA = "data:image/png;base64,YWJj"
	portraitDataURLB = "data:image/png;base64,ZGVm"
)

func stubGeneratePortrait(t *testing.T, fn func(ctx context.Context, card *models.CharacterCard) (string, error)) {
	t.Helper()
	prev := generatePortrait
	generatePortrait = fn
	t.Cleanup(func() { generatePortrait = prev })
}

func fixedPortrait(dataURL string, calls *int) func(context.Context, *models.CharacterCard) (string, error) {
	return func(context.Context, *models.CharacterCard) (string, error) {
		if calls != nil {
			*calls++
		}
		return dataURL, nil
	}
}

func portraitRouter(userID uint, role string) *gin.Engine {
	r := gin.New()
	r.Use(withAuth(userID, "tester", role))
	r.POST("/api/characters/:id/portrait", NewCharacterHandlers().GenerateCharacterPortrait)
	r.GET("/api/characters/:id", GetCharacter)
	return r
}

func postPortrait(r *gin.Engine, cardID uint) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	r.ServeHTTP(w, jsonReq(http.MethodPost, "/api/characters/"+strconv.Itoa(int(cardID))+"/portrait", nil))
	return w
}

func decodeBody(t *testing.T, w *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body %q: %v", w.Body.String(), err)
	}
	return body
}

func cardPortraitHash(t *testing.T, cardID uint) string {
	t.Helper()
	var hash string
	if err := models.DB.Raw("SELECT portrait_hash FROM character_cards WHERE id = ?", cardID).Scan(&hash).Error; err != nil {
		t.Fatalf("read portrait_hash: %v", err)
	}
	return hash
}

func userCoins(t *testing.T, userID uint) int {
	t.Helper()
	var u models.User
	if err := models.DB.First(&u, userID).Error; err != nil {
		t.Fatalf("load user: %v", err)
	}
	return u.Coins
}

func portraitFileExists(t *testing.T, hash string) bool {
	t.Helper()
	stored, err := imagestore.PortraitStore().Resolve(hash)
	if errors.Is(err, imagestore.ErrNotFound) {
		return false
	}
	if err != nil {
		t.Fatalf("resolve portrait %s: %v", hash, err)
	}
	_, statErr := os.Stat(stored.Path)
	return statErr == nil
}

func TestGenerateCharacterPortrait_OwnerPaysAndStoresPortrait(t *testing.T) {
	initTestDB(t)
	t.Cleanup(imagestore.SetPortraitDir(t.TempDir()))
	uid := seedUser(t, "owner", models.RoleUser, 500, 3)
	cardID := seedCard(t, uid, "艾琳")
	calls := 0
	stubGeneratePortrait(t, fixedPortrait(portraitDataURLA, &calls))

	w := postPortrait(portraitRouter(uid, "user"), cardID)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	body := decodeBody(t, w)
	hash, _ := body["portrait_hash"].(string)
	if hash == "" {
		t.Fatalf("response misses portrait_hash: %v", body)
	}
	if coins, _ := body["coins"].(float64); int(coins) != 400 {
		t.Errorf("response coins = %v, want 400", body["coins"])
	}
	if calls != 1 {
		t.Errorf("generate calls = %d, want 1", calls)
	}
	if got := userCoins(t, uid); got != 400 {
		t.Errorf("db coins = %d, want 400", got)
	}
	if got := cardPortraitHash(t, cardID); got != hash {
		t.Errorf("stored portrait_hash = %q, want %q", got, hash)
	}
	if !portraitFileExists(t, hash) {
		t.Error("portrait file was not written to the portrait dir")
	}

	get := httptest.NewRecorder()
	portraitRouter(uid, "user").ServeHTTP(get, jsonReq(http.MethodGet, "/api/characters/"+strconv.Itoa(int(cardID)), nil))
	if got, _ := decodeBody(t, get)["portrait_hash"].(string); got != hash {
		t.Errorf("GetCharacter portrait_hash = %q, want %q", got, hash)
	}
}

func TestGenerateCharacterPortrait_InsufficientCoinsSkipsGeneration(t *testing.T) {
	initTestDB(t)
	t.Cleanup(imagestore.SetPortraitDir(t.TempDir()))
	uid := seedUser(t, "poor", models.RoleUser, 99, 3)
	cardID := seedCard(t, uid, "艾琳")
	calls := 0
	stubGeneratePortrait(t, fixedPortrait(portraitDataURLA, &calls))

	w := postPortrait(portraitRouter(uid, "user"), cardID)
	if w.Code != http.StatusPaymentRequired {
		t.Fatalf("status = %d, want 402", w.Code)
	}
	if calls != 0 {
		t.Errorf("generate calls = %d, want 0", calls)
	}
	body := decodeBody(t, w)
	if need, _ := body["need"].(float64); int(need) != 100 {
		t.Errorf("need = %v, want 100", body["need"])
	}
	if got := userCoins(t, uid); got != 99 {
		t.Errorf("coins = %d, want unchanged 99", got)
	}
}

func TestGenerateCharacterPortrait_OtherUserForbidden(t *testing.T) {
	initTestDB(t)
	t.Cleanup(imagestore.SetPortraitDir(t.TempDir()))
	owner := seedUser(t, "owner", models.RoleUser, 500, 3)
	other := seedUser(t, "other", models.RoleUser, 500, 3)
	cardID := seedCard(t, owner, "艾琳")
	calls := 0
	stubGeneratePortrait(t, fixedPortrait(portraitDataURLA, &calls))

	if w := postPortrait(portraitRouter(other, "user"), cardID); w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", w.Code)
	}
	if calls != 0 {
		t.Errorf("generate calls = %d, want 0", calls)
	}
}

func TestGenerateCharacterPortrait_MissingCardNotFound(t *testing.T) {
	initTestDB(t)
	t.Cleanup(imagestore.SetPortraitDir(t.TempDir()))
	uid := seedUser(t, "owner", models.RoleUser, 500, 3)
	stubGeneratePortrait(t, fixedPortrait(portraitDataURLA, nil))

	if w := postPortrait(portraitRouter(uid, "user"), 9999); w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", w.Code)
	}
}

func TestGenerateCharacterPortrait_AdminForOthersIsFree(t *testing.T) {
	initTestDB(t)
	t.Cleanup(imagestore.SetPortraitDir(t.TempDir()))
	owner := seedUser(t, "owner", models.RoleUser, 500, 3)
	admin := seedUser(t, "admin", models.RoleAdmin, 0, 3)
	cardID := seedCard(t, owner, "艾琳")
	stubGeneratePortrait(t, fixedPortrait(portraitDataURLA, nil))

	w := postPortrait(portraitRouter(admin, "admin"), cardID)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	body := decodeBody(t, w)
	if _, has := body["coins"]; has {
		t.Errorf("admin response must not carry coins: %v", body)
	}
	if got := userCoins(t, owner); got != 500 {
		t.Errorf("owner coins = %d, want unchanged 500", got)
	}
	if got := userCoins(t, admin); got != 0 {
		t.Errorf("admin coins = %d, want unchanged 0", got)
	}
	if cardPortraitHash(t, cardID) == "" {
		t.Error("portrait_hash was not written")
	}
}

func TestGenerateCharacterPortrait_DeadCardRejected(t *testing.T) {
	initTestDB(t)
	t.Cleanup(imagestore.SetPortraitDir(t.TempDir()))
	uid := seedUser(t, "owner", models.RoleUser, 500, 3)
	cardID := seedCard(t, uid, "艾琳")
	if err := models.DB.Model(&models.CharacterCard{}).Where("id = ?", cardID).Update("is_active", false).Error; err != nil {
		t.Fatalf("mark dead: %v", err)
	}
	calls := 0
	stubGeneratePortrait(t, fixedPortrait(portraitDataURLA, &calls))

	if w := postPortrait(portraitRouter(uid, "user"), cardID); w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
	if calls != 0 {
		t.Errorf("generate calls = %d, want 0", calls)
	}
	if got := userCoins(t, uid); got != 500 {
		t.Errorf("coins = %d, want unchanged 500", got)
	}
}

func TestGenerateCharacterPortrait_GenerationFailureDoesNotCharge(t *testing.T) {
	initTestDB(t)
	t.Cleanup(imagestore.SetPortraitDir(t.TempDir()))
	uid := seedUser(t, "owner", models.RoleUser, 500, 3)
	cardID := seedCard(t, uid, "艾琳")
	stubGeneratePortrait(t, func(context.Context, *models.CharacterCard) (string, error) {
		return "", errors.New("painter down")
	})

	if w := postPortrait(portraitRouter(uid, "user"), cardID); w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", w.Code)
	}
	if got := userCoins(t, uid); got != 500 {
		t.Errorf("coins = %d, want unchanged 500", got)
	}
	if got := cardPortraitHash(t, cardID); got != "" {
		t.Errorf("portrait_hash = %q, want empty", got)
	}
}

func TestGenerateCharacterPortrait_ConcurrentRequestGets409(t *testing.T) {
	initTestDB(t)
	t.Cleanup(imagestore.SetPortraitDir(t.TempDir()))
	uid := seedUser(t, "owner", models.RoleUser, 500, 3)
	cardID := seedCard(t, uid, "艾琳")
	started := make(chan struct{})
	release := make(chan struct{})
	stubGeneratePortrait(t, func(context.Context, *models.CharacterCard) (string, error) {
		close(started)
		<-release
		return portraitDataURLA, nil
	})

	r := portraitRouter(uid, "user")
	first := make(chan *httptest.ResponseRecorder, 1)
	go func() { first <- postPortrait(r, cardID) }()
	<-started

	if w := postPortrait(r, cardID); w.Code != http.StatusConflict {
		t.Fatalf("second request status = %d, want 409", w.Code)
	}
	close(release)
	if w := <-first; w.Code != http.StatusOK {
		t.Fatalf("first request status = %d, body = %s", w.Code, w.Body.String())
	}
	if got := userCoins(t, uid); got != 400 {
		t.Errorf("coins = %d, want charged exactly once (400)", got)
	}
	if _, loaded := portraitJobs.Load(cardID); loaded {
		t.Error("job marker must be released after the request finishes")
	}
}

func TestGenerateCharacterPortrait_RegenerateRemovesOldFile(t *testing.T) {
	initTestDB(t)
	t.Cleanup(imagestore.SetPortraitDir(t.TempDir()))
	uid := seedUser(t, "owner", models.RoleUser, 500, 3)
	cardID := seedCard(t, uid, "艾琳")
	r := portraitRouter(uid, "user")

	stubGeneratePortrait(t, fixedPortrait(portraitDataURLA, nil))
	first := decodeBody(t, postPortrait(r, cardID))["portrait_hash"].(string)

	stubGeneratePortrait(t, fixedPortrait(portraitDataURLB, nil))
	w := postPortrait(r, cardID)
	if w.Code != http.StatusOK {
		t.Fatalf("regenerate status = %d, body = %s", w.Code, w.Body.String())
	}
	second := decodeBody(t, w)["portrait_hash"].(string)

	if first == second {
		t.Fatal("test needs two different images")
	}
	if got := cardPortraitHash(t, cardID); got != second {
		t.Errorf("portrait_hash = %q, want %q", got, second)
	}
	if portraitFileExists(t, first) {
		t.Error("old portrait file should be removed once nothing references it")
	}
	if !portraitFileExists(t, second) {
		t.Error("new portrait file is missing")
	}
	if got := userCoins(t, uid); got != 300 {
		t.Errorf("coins = %d, want 300 after two paid generations", got)
	}
}

func TestGenerateCharacterPortrait_KeepsOldFileStillUsedByAnotherCard(t *testing.T) {
	initTestDB(t)
	t.Cleanup(imagestore.SetPortraitDir(t.TempDir()))
	uid := seedUser(t, "owner", models.RoleUser, 500, 3)
	cardA := seedCard(t, uid, "艾琳")
	cardB := seedCard(t, uid, "托马斯")
	r := portraitRouter(uid, "user")

	stubGeneratePortrait(t, fixedPortrait(portraitDataURLA, nil))
	shared := decodeBody(t, postPortrait(r, cardA))["portrait_hash"].(string)
	if err := models.DB.Exec("UPDATE character_cards SET portrait_hash = ? WHERE id = ?", shared, cardB).Error; err != nil {
		t.Fatalf("share portrait: %v", err)
	}

	stubGeneratePortrait(t, fixedPortrait(portraitDataURLB, nil))
	if w := postPortrait(r, cardA); w.Code != http.StatusOK {
		t.Fatalf("regenerate status = %d, body = %s", w.Code, w.Body.String())
	}
	if !portraitFileExists(t, shared) {
		t.Error("shared portrait file must survive while another card still references it")
	}
}

func TestGenerateCharacterPortrait_CoinsSpentDuringGenerationRollsBack(t *testing.T) {
	initTestDB(t)
	t.Cleanup(imagestore.SetPortraitDir(t.TempDir()))
	uid := seedUser(t, "owner", models.RoleUser, 150, 3)
	cardID := seedCard(t, uid, "艾琳")
	stubGeneratePortrait(t, func(context.Context, *models.CharacterCard) (string, error) {
		if err := models.DB.Model(&models.User{}).Where("id = ?", uid).Update("coins", 10).Error; err != nil {
			t.Errorf("spend coins: %v", err)
		}
		return portraitDataURLA, nil
	})

	w := postPortrait(portraitRouter(uid, "user"), cardID)
	if w.Code != http.StatusPaymentRequired {
		t.Fatalf("status = %d, want 402, body = %s", w.Code, w.Body.String())
	}
	if got := userCoins(t, uid); got != 10 {
		t.Errorf("coins = %d, want 10 (no overdraft)", got)
	}
	if got := cardPortraitHash(t, cardID); got != "" {
		t.Errorf("portrait_hash = %q, want unchanged empty", got)
	}
	// NOTE: 内容 "abc" 不是可解码图片，压缩会保留原字节，所以文件哈希就是它的 SHA-256。
	sum := sha256.Sum256([]byte("abc"))
	if portraitFileExists(t, hex.EncodeToString(sum[:])) {
		t.Error("the freshly saved portrait must be removed when the charge fails")
	}
}

func TestServePortrait(t *testing.T) {
	t.Cleanup(imagestore.SetPortraitDir(t.TempDir()))
	t.Cleanup(imagestore.SetDefaultDir(t.TempDir()))
	ref, err := imagestore.PortraitStore().SaveDataURL(portraitDataURLA)
	if err != nil {
		t.Fatalf("save portrait: %v", err)
	}
	r := gin.New()
	r.GET("/api/portraits/:hash", ServePortrait)
	r.GET("/api/images/:hash", ServeImage)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/portraits/"+ref.Hash, nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if got := w.Header().Get("Content-Type"); got != "image/png" {
		t.Errorf("Content-Type = %q", got)
	}
	if got := w.Header().Get("Cache-Control"); got != "public, max-age=31536000, immutable" {
		t.Errorf("Cache-Control = %q", got)
	}
	if w.Body.String() != "abc" {
		t.Errorf("body = %q", w.Body.String())
	}

	bad := httptest.NewRecorder()
	r.ServeHTTP(bad, httptest.NewRequest(http.MethodGet, "/api/portraits/not-a-hash", nil))
	if bad.Code != http.StatusNotFound {
		t.Errorf("invalid hash status = %d, want 404", bad.Code)
	}

	cross := httptest.NewRecorder()
	r.ServeHTTP(cross, httptest.NewRequest(http.MethodGet, "/api/images/"+ref.Hash, nil))
	if cross.Code != http.StatusNotFound {
		t.Errorf("portrait must not be served from the scene image route, status = %d", cross.Code)
	}
}

func TestGetShopCostsIncludesPortraitCost(t *testing.T) {
	initTestDB(t)
	r := gin.New()
	r.GET("/api/shop/costs", GetShopCosts)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/shop/costs", nil))
	if got, _ := decodeBody(t, w)["regenerate_portrait_cost"].(float64); int(got) != 100 {
		t.Errorf("regenerate_portrait_cost = %v, want default 100", got)
	}
}

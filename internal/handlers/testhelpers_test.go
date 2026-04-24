package handlers

import (
	"bytes"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/llmcoc/server/internal/models"
)

func init() {
	gin.SetMode(gin.TestMode)
}

// jsonReq creates an HTTP request with a JSON body.
func jsonReq(method, path string, body any) *http.Request {
	var buf []byte
	if body != nil {
		buf, _ = json.Marshal(body)
	}
	req, _ := http.NewRequest(method, path, bytes.NewReader(buf))
	req.Header.Set("Content-Type", "application/json")
	return req
}

// withAuth returns a middleware that injects user_id / username / role into gin context.
func withAuth(userID uint, username, role string) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Set("user_id", userID)
		c.Set("username", username)
		c.Set("role", role)
		c.Next()
	}
}

// seedUser inserts a User record and returns its ID.
func seedUser(t *testing.T, username string, role models.Role, coins, cardSlots int) uint {
	t.Helper()
	u := models.User{
		Username:     username,
		Email:        username + "@test.com",
		PasswordHash: "placeholder",
		Role:         role,
		Coins:        coins,
		CardSlots:    cardSlots,
	}
	if err := models.DB.Create(&u).Error; err != nil {
		t.Fatalf("seedUser %q: %v", username, err)
	}
	return u.ID
}

// seedScenario inserts a minimal active Scenario and returns its ID.
func seedScenario(t *testing.T, name string) uint {
	t.Helper()
	s := models.Scenario{
		Name:     name,
		IsActive: true,
		Content:  models.JSONField[models.ScenarioContent]{Data: models.ScenarioContent{}},
	}
	if err := models.DB.Create(&s).Error; err != nil {
		t.Fatalf("seedScenario %q: %v", name, err)
	}
	return s.ID
}

// seedProvider inserts an LLMProviderConfig and returns its ID.
func seedProvider(t *testing.T, name string) uint {
	t.Helper()
	p := models.LLMProviderConfig{
		Name:     name,
		Provider: "openai",
		APIKey:   "test-key",
		IsActive: true,
	}
	if err := models.DB.Create(&p).Error; err != nil {
		t.Fatalf("seedProvider %q: %v", name, err)
	}
	return p.ID
}

// seedCard inserts a CharacterCard for the given userID and returns its ID.
func seedCard(t *testing.T, userID uint, name string) uint {
	t.Helper()
	card := models.CharacterCard{
		UserID:   userID,
		Name:     name,
		IsActive: true,
		Stats:    models.JSONField[models.CharacterStats]{},
		Skills:   models.JSONField[map[string]int]{Data: map[string]int{}},
	}
	if err := models.DB.Create(&card).Error; err != nil {
		t.Fatalf("seedCard %q: %v", name, err)
	}
	return card.ID
}

// seedShopItem inserts a ShopItem and returns its ID.
func seedShopItem(t *testing.T, name string, price int, itemType models.ItemType, value int) uint {
	t.Helper()
	item := models.ShopItem{
		Name:     name,
		ItemType: itemType,
		Price:    price,
		Value:    value,
		IsActive: true,
	}
	if err := models.DB.Create(&item).Error; err != nil {
		t.Fatalf("seedShopItem %q: %v", name, err)
	}
	return item.ID
}

package handlers

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/llmcoc/server/internal/models"
	"github.com/llmcoc/server/internal/services/game"
	"github.com/llmcoc/server/internal/services/llm"
)

// CharacterLLMFactory lets tests inject a fake LLM provider.
type CharacterLLMFactory interface {
	LoadProvider(role models.AgentRole) (llm.Provider, error)
}

type defaultCharacterLLMFactory struct{}

func (defaultCharacterLLMFactory) LoadProvider(role models.AgentRole) (llm.Provider, error) {
	return llm.LoadProviderFromDB(role)
}

// DefaultCharacterLLMFactory is the production implementation.
var DefaultCharacterLLMFactory CharacterLLMFactory = defaultCharacterLLMFactory{}

// CharacterHandlers holds handlers that depend on an LLM factory.
type CharacterHandlers struct {
	LLMFactory CharacterLLMFactory
}

// NewCharacterHandlers creates a CharacterHandlers with the given factory.
func NewCharacterHandlers(f CharacterLLMFactory) *CharacterHandlers {
	return &CharacterHandlers{LLMFactory: f}
}

type CreateCharacterReq struct {
	Name       string                 `json:"name" binding:"required,max=100"`
	Age        int                    `json:"age"`
	Gender     string                 `json:"gender"`
	Occupation string                 `json:"occupation"`
	Birthplace string                 `json:"birthplace"`
	Residence  string                 `json:"residence"`
	Backstory  string                 `json:"backstory"`
	Appearance string                 `json:"appearance"`
	Traits     string                 `json:"traits"`
	Stats      *models.CharacterStats `json:"stats"`
	Skills     map[string]int         `json:"skills"`
}

type GenerateCharacterReq struct {
	Name       string `json:"name"`
	Age        int    `json:"age"`
	Gender     string `json:"gender"`
	Occupation string `json:"occupation"`
	Background string `json:"background"`
	Era        string `json:"era"`
}

func ListCharacters(c *gin.Context) {
	userID := c.GetUint("user_id")
	var cards []models.CharacterCard
	models.DB.Where("user_id = ? AND is_active = ?", userID, true).
		Order("created_at DESC").
		Find(&cards)
	c.JSON(http.StatusOK, cards)
}

func GetCharacter(c *gin.Context) {
	userID := c.GetUint("user_id")
	id, _ := strconv.ParseUint(c.Param("id"), 10, 64)

	var card models.CharacterCard
	if err := models.DB.First(&card, id).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "人物卡不存在"})
		return
	}
	if card.UserID != userID {
		c.JSON(http.StatusForbidden, gin.H{"error": "无权访问此人物卡"})
		return
	}
	c.JSON(http.StatusOK, card)
}

func CreateCharacter(c *gin.Context) {
	userID := c.GetUint("user_id")

	// Check card slot limit
	var user models.User
	models.DB.First(&user, userID)

	var cardCount int64
	models.DB.Model(&models.CharacterCard{}).Where("user_id = ? AND is_active = ?", userID, true).Count(&cardCount)
	if int(cardCount) >= user.CardSlots {
		c.JSON(http.StatusForbidden, gin.H{
			"error":     "人物卡槽位已满",
			"current":   cardCount,
			"max_slots": user.CardSlots,
		})
		return
	}

	var req CreateCharacterReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if req.Gender != models.GenderMale && req.Gender != models.GenderFemale {
		c.JSON(http.StatusBadRequest, gin.H{"error": "无效的性别"})
		return
	}

	// Use provided stats or generate
	stats := models.CharacterStats{}
	if req.Stats != nil {
		stats = *req.Stats
	} else {
		stats = game.GenerateStats()
	}

	// Default skills with EDU/DEX adjustments
	skills := game.DefaultSkills()
	skills["母语"] = stats.EDU * 1 // EDU×5 but stored as actual value
	skills["闪避"] = stats.DEX / 2
	// Merge provided skills
	for k, v := range req.Skills {
		skills[k] = v
	}

	card := models.CharacterCard{
		UserID:     userID,
		Name:       req.Name,
		Age:        req.Age,
		Gender:     req.Gender,
		Occupation: req.Occupation,
		Birthplace: req.Birthplace,
		Residence:  req.Residence,
		Backstory:  req.Backstory,
		Appearance: req.Appearance,
		Traits:     req.Traits,
		Stats:      models.JSONField[models.CharacterStats]{Data: stats},
		Skills:     models.JSONField[map[string]int]{Data: skills},
		IsActive:   true,
	}

	if err := models.DB.Create(&card).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "创建人物卡失败"})
		return
	}
	c.JSON(http.StatusCreated, card)
}

func (h *CharacterHandlers) GenerateCharacter(c *gin.Context) {
	userID := c.GetUint("user_id")

	// Check slot limit
	var user models.User
	models.DB.First(&user, userID)
	var cardCount int64
	models.DB.Model(&models.CharacterCard{}).Where("user_id = ? AND is_active = ?", userID, true).Count(&cardCount)
	if int(cardCount) >= user.CardSlots {
		c.JSON(http.StatusForbidden, gin.H{
			"error":     "人物卡槽位已满",
			"current":   cardCount,
			"max_slots": user.CardSlots,
		})
		return
	}

	var req GenerateCharacterReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if req.Gender != models.GenderMale && req.Gender != models.GenderFemale {
		c.JSON(http.StatusBadRequest, gin.H{"error": "无效的性别"})
		return
	}

	// Generate base stats
	stats := game.GenerateStats()
	skills := game.DefaultSkills()
	skills["母语"] = stats.EDU
	skills["闪避"] = stats.DEX / 2

	// Ask LLM to fill out backstory, name, traits
	provider, err := h.LLMFactory.LoadProvider(models.AgentRoleWriter)
	var generated *llm.GeneratedCharacter
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "加载LLM提供者失败: " + err.Error()})
		return
	}
	generated, err = provider.GenerateCharacter(c.Request.Context(), llm.GenerateCharacterReq{
		Occupation: req.Occupation,
		Background: req.Background,
		Era:        req.Era,
		Stats:      stats,
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "AI生成失败: " + err.Error()})
		return
	}

	card := models.CharacterCard{
		UserID:     userID,
		Name:       req.Name,
		Age:        req.Age,
		Gender:     req.Gender,
		Occupation: req.Occupation,
		Backstory:  generated.Backstory,
		Appearance: generated.Appearance,
		Traits:     generated.Traits,
		Stats:      models.JSONField[models.CharacterStats]{Data: stats},
		Skills:     models.JSONField[map[string]int]{Data: skills},
		IsActive:   true,
	}

	if err := models.DB.Create(&card).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "创建人物卡失败"})
		return
	}
	c.JSON(http.StatusCreated, card)
}

func UpdateCharacter(c *gin.Context) {
	userID := c.GetUint("user_id")
	id, _ := strconv.ParseUint(c.Param("id"), 10, 64)

	var card models.CharacterCard
	if err := models.DB.First(&card, id).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "人物卡不存在"})
		return
	}
	if card.UserID != userID {
		c.JSON(http.StatusForbidden, gin.H{"error": "无权修改此人物卡"})
		return
	}

	var req CreateCharacterReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	card.Name = req.Name
	card.Age = req.Age
	card.Gender = req.Gender
	card.Occupation = req.Occupation
	card.Birthplace = req.Birthplace
	card.Residence = req.Residence
	card.Backstory = req.Backstory
	card.Appearance = req.Appearance
	card.Traits = req.Traits
	if req.Stats != nil {
		card.Stats = models.JSONField[models.CharacterStats]{Data: *req.Stats}
	}
	if req.Skills != nil {
		card.Skills = models.JSONField[map[string]int]{Data: req.Skills}
	}

	models.DB.Save(&card)
	c.JSON(http.StatusOK, card)
}

func DeleteCharacter(c *gin.Context) {
	userID := c.GetUint("user_id")
	id, _ := strconv.ParseUint(c.Param("id"), 10, 64)

	var card models.CharacterCard
	if err := models.DB.First(&card, id).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "人物卡不存在"})
		return
	}
	if card.UserID != userID {
		c.JSON(http.StatusForbidden, gin.H{"error": "无权删除此人物卡"})
		return
	}

	// Soft delete: set is_active = false
	models.DB.Model(&card).Update("is_active", false)
	c.JSON(http.StatusOK, gin.H{"message": "人物卡已删除"})
}

type manageInventoryReq struct {
	Item string `json:"item" binding:"required"`
}

func GetCharacterInventory(c *gin.Context) {
	userID := c.GetUint("user_id")
	id, _ := strconv.ParseUint(c.Param("id"), 10, 64)

	var card models.CharacterCard
	if err := models.DB.First(&card, id).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "人物卡不存在"})
		return
	}
	if card.UserID != userID {
		c.JSON(http.StatusForbidden, gin.H{"error": "无权访问此人物卡"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"character_id": card.ID,
		"inventory":    card.Inventory.Data,
	})
}

func AddCharacterInventoryItem(c *gin.Context) {
	userID := c.GetUint("user_id")
	id, _ := strconv.ParseUint(c.Param("id"), 10, 64)

	var card models.CharacterCard
	if err := models.DB.First(&card, id).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "人物卡不存在"})
		return
	}
	if card.UserID != userID {
		c.JSON(http.StatusForbidden, gin.H{"error": "无权修改此人物卡"})
		return
	}

	var req manageInventoryReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	item := strings.TrimSpace(req.Item)
	if item == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "物品名不能为空"})
		return
	}

	list := card.Inventory.Data
	for _, v := range list {
		if v == item {
			c.JSON(http.StatusOK, card)
			return
		}
	}
	list = append(list, item)
	card.Inventory = models.JSONField[[]string]{Data: list}

	if err := models.DB.Save(&card).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "保存物品栏失败"})
		return
	}
	c.JSON(http.StatusOK, card)
}

func RemoveCharacterInventoryItem(c *gin.Context) {
	userID := c.GetUint("user_id")
	id, _ := strconv.ParseUint(c.Param("id"), 10, 64)
	item := strings.TrimSpace(c.Param("item"))
	if item == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "物品名不能为空"})
		return
	}

	var card models.CharacterCard
	if err := models.DB.First(&card, id).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "人物卡不存在"})
		return
	}
	if card.UserID != userID {
		c.JSON(http.StatusForbidden, gin.H{"error": "无权修改此人物卡"})
		return
	}

	list := card.Inventory.Data
	out := make([]string, 0, len(list))
	for _, v := range list {
		if v != item {
			out = append(out, v)
		}
	}
	card.Inventory = models.JSONField[[]string]{Data: out}

	if err := models.DB.Save(&card).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "保存物品栏失败"})
		return
	}
	c.JSON(http.StatusOK, card)
}

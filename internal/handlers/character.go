package handlers

import (
	"log"
	"net/http"
	"net/url"
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

// NOTE: GenerateCharacter uses LLMs to flesh out a character's backstory, traits,
// and adjusts base skills/stats according to their chosen occupation and background.
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
		Name:       req.Name,
		Occupation: req.Occupation,
		Background: req.Background,
		Era:        req.Era,
		Gender:     req.Gender,
		Stats:      stats,
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "AI生成失败: " + err.Error()})
		return
	}

	// Apply LLM-adjusted stats if valid (total unchanged, all values multiples of 5 within range)
	if s := generated.Stats; s != nil {
		if applyAdjustedStats(&stats, s) {
			// Recalculate derived values
			skills["母语"] = stats.EDU
			skills["闪避"] = stats.DEX / 2
		}
	}

	provider, err = h.LLMFactory.LoadProvider(models.AgentRoleDirector)
	if err != nil {
		log.Printf("[character] failed to load LLM provider for skill adjustment: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "加载LLM提供者失败: " + err.Error()})
		return
	}
	// Second LLM call: adjust skill levels based on occupation and background
	adjustedSkills, skillErr := provider.AdjustSkills(c.Request.Context(), llm.AdjustSkillsReq{
		Name:       req.Name,
		Occupation: req.Occupation,
		Background: req.Background,
		Era:        req.Era,
		Stats:      stats,
		BaseSkills: skills,
	})
	if skillErr != nil {
		log.Printf("[character] AdjustSkills failed (using base skills): %v", skillErr)
	} else {
		applyAdjustedSkills(skills, adjustedSkills, stats)
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
	rawItem, _ := url.PathUnescape(c.Param("item"))
	item := strings.TrimSpace(rawItem)
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

// applyAdjustedStats validates and applies LLM-returned stat adjustments.
// Rules:
//   - Group A (STR/CON/DEX/APP/POW) total must equal original Group A total.
//   - Group B (SIZ/INT/EDU) total must equal original Group B total.
//   - Every value must be a multiple of 5.
//   - STR/CON/DEX/APP/POW: 15–90; SIZ/INT/EDU: 40–90.
//
// Returns true and mutates base if valid, false otherwise (base unchanged).
func applyAdjustedStats(base *models.CharacterStats, adj *models.CharacterStats) bool {
	// Group totals must be preserved.
	origA := base.STR + base.CON + base.DEX + base.APP + base.POW
	adjA := adj.STR + adj.CON + adj.DEX + adj.APP + adj.POW
	origB := base.SIZ + base.INT + base.EDU
	adjB := adj.SIZ + adj.INT + adj.EDU
	if origA != adjA || origB != adjB {
		return false
	}

	// Validate individual ranges and multiples of 5.
	type check struct{ v, lo, hi int }
	checks := []check{
		{adj.STR, 15, 90}, {adj.CON, 15, 90}, {adj.DEX, 15, 90},
		{adj.APP, 15, 90}, {adj.POW, 15, 90},
		{adj.SIZ, 40, 90}, {adj.INT, 40, 90}, {adj.EDU, 40, 90},
	}
	for _, ck := range checks {
		if ck.v%5 != 0 || ck.v < ck.lo || ck.v > ck.hi {
			return false
		}
	}

	// Apply core attributes; recalculate derived values.
	base.STR = adj.STR
	base.CON = adj.CON
	base.SIZ = adj.SIZ
	base.DEX = adj.DEX
	base.APP = adj.APP
	base.INT = adj.INT
	base.POW = adj.POW
	base.EDU = adj.EDU

	hp := (base.CON + base.SIZ) / 10
	base.HP, base.MaxHP = hp, hp
	mp := base.POW / 5
	base.MP, base.MaxMP = mp, mp
	base.SAN = base.POW
	base.MaxSAN = 99

	// MOV
	if base.STR > base.SIZ && base.DEX > base.SIZ {
		base.MOV = 9
	} else if base.STR < base.SIZ && base.DEX < base.SIZ {
		base.MOV = 7
	} else {
		base.MOV = 8
	}

	// Build & DB
	combined := base.STR + base.SIZ
	switch {
	case combined <= 64:
		base.Build, base.DB = -2, "-2"
	case combined <= 84:
		base.Build, base.DB = -1, "-1"
	case combined <= 124:
		base.Build, base.DB = 0, "0"
	case combined <= 164:
		base.Build, base.DB = 1, "1D4"
	case combined <= 204:
		base.Build, base.DB = 2, "1D6"
	case combined <= 284:
		base.Build, base.DB = 3, "2D6"
	default:
		base.Build, base.DB = 4, "2D6+1D6"
	}
	return true
}

// applyAdjustedSkills validates and applies LLM-returned skill adjustments.
// Only skills already present in base are updated; values are clamped to [1, 90].
// 母语 and 闪避 are derived from stats and are not overridden.
func applyAdjustedSkills(base map[string]int, adjusted map[string]int, stats models.CharacterStats) {
	for k, v := range adjusted {
		if k == "母语" || k == "闪避" {
			continue
		}
		if _, exists := base[k]; !exists {
			continue // don't add unknown skills
		}
		if v < 1 {
			v = 1
		}
		if v > 90 {
			v = 90
		}
		base[k] = v
	}
	// always keep derived skills correct
	base["母语"] = stats.EDU
	base["闪避"] = stats.DEX / 2
}

func RecoverCharacterSAN(c *gin.Context) {
	userID := c.GetUint("user_id")
	id, _ := strconv.ParseUint(c.Param("id"), 10, 64)

	var card models.CharacterCard
	if err := models.DB.First(&card, id).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "人物卡不存在"})
		return
	}
	if card.UserID != userID {
		c.JSON(http.StatusForbidden, gin.H{"error": "无权恢复此人物卡"})
		return
	}

	card.Stats.Data.MaxSAN = 99
	card.Stats.Data.SAN = card.Stats.Data.MaxSAN
	models.DB.Save(&card)
	c.JSON(http.StatusOK, card)
}

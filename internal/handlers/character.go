package handlers

import (
	"context"
	"log"
	"math/rand"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/llmcoc/server/internal/models"
	"github.com/llmcoc/server/internal/services/agent"
	"github.com/llmcoc/server/internal/services/game"
)

// CharacterHandlers holds handlers for character-related routes.
type CharacterHandlers struct{}

// NewCharacterHandlers creates a CharacterHandlers.
func NewCharacterHandlers() *CharacterHandlers {
	return &CharacterHandlers{}
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
	Assets     []models.Asset         `json:"assets"`
}

type GenerateCharacterReq struct {
	Name       string         `json:"name"`
	Age        int            `json:"age"`
	Gender     string         `json:"gender"`
	Occupation string         `json:"occupation"`
	Background string         `json:"background"`
	Era        string         `json:"era"`
	Assets     []models.Asset `json:"assets"`
}

func ListCharacters(c *gin.Context) {
	userID := c.GetUint("user_id")
	var cards []models.CharacterCard
	models.DB.Where("user_id = ? AND is_active = ?", userID, true).
		Order("created_at DESC").
		Find(&cards)
	for i := range cards {
		hotFixChar(&cards[i])
	}
	c.JSON(http.StatusOK, cards)
}

func hotFixChar(card *models.CharacterCard) {
	needUpdate := false
	if card.Skills.Data == nil {
		card.Skills.Data = map[string]int{}
		needUpdate = true
	}
	for skill := range card.Skills.Data {
		if !game.IsValidSkill(skill) {
			delete(card.Skills.Data, skill)
			needUpdate = true
		}
	}
	if card.Race == "" {
		card.Race = "人类"
		needUpdate = true
	}
	before := card.Stats.Data
	game.ApplyDerivedStats(&card.Stats.Data, card.Age, false)
	if before.MaxHP != card.Stats.Data.MaxHP || before.MaxMP != card.Stats.Data.MaxMP || before.MaxSAN != card.Stats.Data.MaxSAN ||
		before.MOV != card.Stats.Data.MOV || before.Build != card.Stats.Data.Build || before.DB != card.Stats.Data.DB ||
		before.HP != card.Stats.Data.HP || before.MP != card.Stats.Data.MP || before.SAN != card.Stats.Data.SAN {
		needUpdate = true
	}
	trimed := strings.TrimSpace(card.Name)
	if trimed != card.Name {
		card.Name = trimed
		needUpdate = true
	}
	if card.Age < 15 {
		card.Age = 15
		needUpdate = true
	}
	if card.Age > 90 {
		card.Age = 90
		needUpdate = true
	}
	before = card.Stats.Data
	game.ApplyDerivedStats(&card.Stats.Data, card.Age, false)
	if before.MaxHP != card.Stats.Data.MaxHP || before.MaxMP != card.Stats.Data.MaxMP || before.MaxSAN != card.Stats.Data.MaxSAN ||
		before.MOV != card.Stats.Data.MOV || before.Build != card.Stats.Data.Build || before.DB != card.Stats.Data.DB ||
		before.HP != card.Stats.Data.HP || before.MP != card.Stats.Data.MP || before.SAN != card.Stats.Data.SAN {
		needUpdate = true
	}
	if card.Skills.Data["母语"] != card.Stats.Data.EDU {
		card.Skills.Data["母语"] = card.Stats.Data.EDU
		needUpdate = true
	}
	if card.Skills.Data["闪避"] != card.Stats.Data.DEX/2 {
		card.Skills.Data["闪避"] = card.Stats.Data.DEX / 2
		needUpdate = true
	}
	if needUpdate {
		models.DB.Save(card)
	}
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
		// is admin?
		var user models.User
		models.DB.First(&user, userID)
		if user.Role != "admin" {
			c.JSON(http.StatusForbidden, gin.H{"error": "无权访问此人物卡"})
			return
		}
	}
	hotFixChar(&card)
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
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "姓名不能为空"})
		return
	}

	if req.Age < 15 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "年龄必须至少为15岁"})
		return
	}
	if req.Age > 90 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "年龄不能超过90岁"})
		return
	}
	if err := game.RejectClientStats(req.Stats); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	stats, _, err := game.GenerateStatsForAge(req.Age)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	skills, spent, err := game.NormalizeSkills(req.Skills, stats)
	if err != nil {
		payload := gin.H{"error": "技能分配不符合规则", "spent": spent, "budget": game.SkillPointBudget(stats)}
		if ve, ok := err.(game.SkillValidationError); ok {
			payload["details"] = ve.Details
		} else {
			payload["details"] = []string{err.Error()}
		}
		c.JSON(http.StatusBadRequest, payload)
		return
	}

	card := models.CharacterCard{
		UserID:     userID,
		Name:       req.Name,
		Age:        req.Age,
		Race:       "人类",
		Gender:     req.Gender,
		Occupation: req.Occupation,
		Birthplace: req.Birthplace,
		Residence:  req.Residence,
		Backstory:  req.Backstory,
		Appearance: req.Appearance,
		Traits:     req.Traits,
		Stats:      models.JSONField[models.CharacterStats]{Data: stats},
		Skills:     models.JSONField[map[string]int]{Data: skills},
		Assets:     models.JSONField[[]models.Asset]{Data: req.Assets},
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
		log.Printf("Bad request data: %v", err)
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	req.Name = strings.TrimSpace(req.Name)

	if req.Gender != models.GenderMale && req.Gender != models.GenderFemale {
		log.Printf("Invalid gender: %s", req.Gender)
		c.JSON(http.StatusBadRequest, gin.H{"error": "无效的性别"})
		return
	}
	if req.Name == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "姓名不能为空"})
		return
	}
	if req.Age < 15 {
		req.Age = 15
	}
	if req.Age > 90 {
		req.Age = 90
	}

	// Generate base stats
	stats, _, err := game.GenerateStatsForAge(req.Age)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	skills := game.DefaultSkills()
	skills["母语"] = stats.EDU
	skills["闪避"] = stats.DEX / 2

	// Ask LLM to fill out backstory, name, traits
	generated, err := agent.GenerateCharacter(context.Background(), agent.GenerateCharacterReq{
		Name:       req.Name,
		Occupation: req.Occupation,
		Background: req.Background,
		Era:        req.Era,
		Gender:     req.Gender,
		Age:        req.Age,
		Stats:      stats,
	})
	if err != nil {
		log.Printf("GenerateCharacter LLM error: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "AI生成失败: " + err.Error()})
		return
	}

	if s := generated.Stats; s != nil {
		if applyAdjustedStats(&stats, s, req.Age) {
			skills["母语"] = stats.EDU
			skills["闪避"] = stats.DEX / 2
		}
	}

	// Second LLM call: adjust skill levels based on occupation and background
	adjustedSkills, skillErr := agent.AdjustSkills(context.Background(), agent.AdjustSkillsReq{
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
		Race:       "人类",
		Age:        req.Age,
		Gender:     req.Gender,
		Occupation: req.Occupation,
		Backstory:  generated.Backstory,
		Appearance: generated.Appearance,
		Traits:     generated.Traits,
		Stats:      models.JSONField[models.CharacterStats]{Data: stats},
		Skills:     models.JSONField[map[string]int]{Data: skills},
		Assets:     models.JSONField[[]models.Asset]{Data: req.Assets},
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
	isAdmin := false
	if card.UserID != userID {
		var user models.User
		models.DB.First(&user, userID)
		if user.Role != "admin" {
			c.JSON(http.StatusForbidden, gin.H{"error": "无权修改此人物卡"})
			return
		}
		isAdmin = true
	} else {
		var user models.User
		if err := models.DB.First(&user, userID).Error; err == nil && user.Role == models.RoleAdmin {
			isAdmin = true
		}
	}

	var req CreateCharacterReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if strings.TrimSpace(req.Name) != "" {
		card.Name = strings.TrimSpace(req.Name)
	}
	if req.Age != 0 {
		if req.Age < 15 || req.Age > 90 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "年龄必须在15-90之间"})
			return
		}
		card.Age = req.Age
	}
	if req.Gender != "" {
		if req.Gender != models.GenderMale && req.Gender != models.GenderFemale {
			c.JSON(http.StatusBadRequest, gin.H{"error": "无效的性别"})
			return
		}
		card.Gender = req.Gender
	}
	card.Occupation = req.Occupation
	card.Birthplace = req.Birthplace
	card.Residence = req.Residence
	card.Backstory = req.Backstory
	card.Appearance = req.Appearance
	card.Traits = req.Traits
	if req.Stats != nil {
		if !isAdmin {
			c.JSON(http.StatusBadRequest, gin.H{"error": "不能直接提交属性，请使用规则车卡流程"})
			return
		}
		stats := *req.Stats
		if err := game.ValidateManualStats(stats); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "属性不符合规则", "details": []string{err.Error()}})
			return
		}
		game.ApplyDerivedStats(&stats, card.Age, true)
		card.Stats = models.JSONField[models.CharacterStats]{Data: stats}
	} else {
		stats := card.Stats.Data
		game.ApplyDerivedStats(&stats, card.Age, false)
		card.Stats = models.JSONField[models.CharacterStats]{Data: stats}
	}
	if req.Skills != nil {
		skills, spent, err := game.NormalizeSkills(req.Skills, card.Stats.Data)
		if err != nil {
			payload := gin.H{"error": "技能分配不符合规则", "spent": spent, "budget": game.SkillPointBudget(card.Stats.Data)}
			if ve, ok := err.(game.SkillValidationError); ok {
				payload["details"] = ve.Details
			} else {
				payload["details"] = []string{err.Error()}
			}
			c.JSON(http.StatusBadRequest, payload)
			return
		}
		card.Skills = models.JSONField[map[string]int]{Data: skills}
	} else {
		if skills, _, err := game.NormalizeSkills(card.Skills.Data, card.Stats.Data); err == nil {
			card.Skills = models.JSONField[map[string]int]{Data: skills}
		} else {
			if card.Skills.Data == nil {
				card.Skills.Data = map[string]int{}
			}
			for skill := range card.Skills.Data {
				if !game.IsValidSkill(skill) {
					delete(card.Skills.Data, skill)
				}
			}
			card.Skills.Data["母语"] = card.Stats.Data.EDU
			card.Skills.Data["闪避"] = card.Stats.Data.DEX / 2
		}
	}
	if req.Assets != nil {
		card.Assets = models.JSONField[[]models.Asset]{Data: req.Assets}
	}
	if err := models.DB.Save(&card).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "保存人物卡失败"})
		return
	}
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
		var user models.User
		models.DB.First(&user, userID)
		if user.Role != "admin" {
			c.JSON(http.StatusForbidden, gin.H{"error": "无权删除此人物卡"})
			return
		}
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
		var user models.User
		models.DB.First(&user, userID)
		if user.Role != "admin" {
			c.JSON(http.StatusForbidden, gin.H{"error": "无权访问此人物卡"})
			return
		}
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
		var user models.User
		models.DB.First(&user, userID)
		if user.Role != "admin" {
			c.JSON(http.StatusForbidden, gin.H{"error": "无权修改此人物卡"})
			return
		}
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
	if card.UserID != userID {
		log.Printf("[admin] add_inventory admin_id=%d target_user=%d card_id=%d item=%q", userID, card.UserID, card.ID, item)
	}
	c.JSON(http.StatusOK, card)
}

func RemoveCharacterInventoryItem(c *gin.Context) {
	userID := c.GetUint("user_id")
	id, _ := strconv.ParseUint(c.Param("id"), 10, 64)
	item := strings.TrimSpace(strings.TrimPrefix(c.Param("item"), "/"))
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
		// is admin?
		var user models.User
		models.DB.First(&user, userID)
		if user.Role != "admin" {
			c.JSON(http.StatusForbidden, gin.H{"error": "无权修改此人物卡"})
			return
		}
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

// NOTE: RemoveCharacterSocialRelation removes a social relation by name from a character card.
// NOTE: Only the card owner or admin can perform this action.
func RemoveCharacterSocialRelation(c *gin.Context) {
	userID := c.GetUint("user_id")
	id, _ := strconv.ParseUint(c.Param("id"), 10, 64)

	// URL decode the name parameter because Chinese characters may be encoded
	name, err := url.QueryUnescape(c.Param("name"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "社交关系名称解码失败"})
		return
	}
	name = strings.TrimSpace(name)
	if name == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "社交关系名称不能为空"})
		return
	}

	var card models.CharacterCard
	if err := models.DB.First(&card, id).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "人物卡不存在"})
		return
	}
	if card.UserID != userID {
		var user models.User
		models.DB.First(&user, userID)
		if user.Role != "admin" {
			c.JSON(http.StatusForbidden, gin.H{"error": "无权修改此人物卡"})
			return
		}
	}

	list := card.SocialRelations.Data
	out := make([]models.SocialRelation, 0, len(list))
	for _, rel := range list {
		if rel.Name != name {
			out = append(out, rel)
		}
	}
	card.SocialRelations = models.JSONField[[]models.SocialRelation]{Data: out}

	if err := models.DB.Save(&card).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "保存社交关系失败"})
		return
	}
	c.JSON(http.StatusOK, card)
}

// NOTE: RemoveCharacterAsset removes an asset by name from a character card.
// NOTE: Only the card owner or admin can perform this action.
func RemoveCharacterAsset(c *gin.Context) {
	userID := c.GetUint("user_id")
	id, _ := strconv.ParseUint(c.Param("id"), 10, 64)

	// URL decode the name parameter because Chinese characters may be encoded
	name, err := url.QueryUnescape(c.Param("name"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "资产名称解码失败"})
		return
	}
	name = strings.TrimSpace(name)
	if name == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "资产名称不能为空"})
		return
	}

	var card models.CharacterCard
	if err := models.DB.First(&card, id).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "人物卡不存在"})
		return
	}
	if card.UserID != userID {
		var user models.User
		models.DB.First(&user, userID)
		if user.Role != "admin" {
			c.JSON(http.StatusForbidden, gin.H{"error": "无权修改此人物卡"})
			return
		}
	}

	list := card.Assets.Data
	out := make([]models.Asset, 0, len(list))
	for _, a := range list {
		if a.Name != name {
			out = append(out, a)
		}
	}
	card.Assets = models.JSONField[[]models.Asset]{Data: out}

	if err := models.DB.Save(&card).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "保存资产失败"})
		return
	}
	c.JSON(http.StatusOK, card)
}

// NOTE: applyAdjustedStats validates and applies LLM-returned stat adjustments.
// NOTE: It preserves group totals, keeps base attributes human-range, and recalculates derived values.
func applyAdjustedStats(base *models.CharacterStats, adj *models.CharacterStats, age int) bool {
	origA := base.STR + base.CON + base.DEX + base.APP + base.POW
	adjA := adj.STR + adj.CON + adj.DEX + adj.APP + adj.POW
	origB := base.SIZ + base.INT + base.EDU
	adjB := adj.SIZ + adj.INT + adj.EDU
	if origA != adjA || origB != adjB {
		return false
	}

	type check struct {
		v  int
		lo int
		hi int
	}
	checks := []check{
		{adj.STR, 1, 99}, {adj.CON, 1, 99}, {adj.DEX, 1, 99},
		{adj.APP, 1, 99}, {adj.POW, 1, 99},
		{adj.SIZ, 1, 99}, {adj.INT, 1, 99}, {adj.EDU, 1, 99},
	}
	for _, ck := range checks {
		if ck.v%5 != 0 || ck.v < ck.lo || ck.v > ck.hi {
			return false
		}
	}

	base.STR = adj.STR
	base.CON = adj.CON
	base.SIZ = adj.SIZ
	base.DEX = adj.DEX
	base.APP = adj.APP
	base.INT = adj.INT
	base.POW = adj.POW
	base.EDU = adj.EDU

	game.ApplyDerivedStats(base, age, true)
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

// NOTE: RegenerateAppearance 通过 SiteSetting 读取费率，扣除金币后重新生成外貌
func (h *CharacterHandlers) RegenerateAppearance(c *gin.Context) {
	cost := siteSettingInt("regenerate_appearance_cost", 100)
	userID := c.GetUint("user_id")
	id, _ := strconv.ParseUint(c.Param("id"), 10, 64)

	var card models.CharacterCard
	if err := models.DB.First(&card, id).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "人物卡不存在"})
		return
	}
	if card.UserID != userID {
		var u models.User
		models.DB.First(&u, userID)
		if u.Role != "admin" {
			c.JSON(http.StatusForbidden, gin.H{"error": "无权修改此人物卡"})
			return
		}
	}

	isOwner := card.UserID == userID
	var user models.User
	if isOwner {
		models.DB.First(&user, userID)
		if user.Coins < cost {
			c.JSON(http.StatusPaymentRequired, gin.H{
				"error":   "金币不足",
				"need":    cost,
				"current": user.Coins,
			})
			return
		}
	}

	appearance, err := agent.RegenerateAppearance(c.Request.Context(), &card)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "AI生成失败: " + err.Error()})
		return
	}

	tx := models.DB.Begin()
	defer func() {
		if r := recover(); r != nil {
			tx.Rollback()
		}
	}()

	if isOwner {
		if err := tx.Model(&user).Update("coins", user.Coins-cost).Error; err != nil {
			tx.Rollback()
			c.JSON(http.StatusInternalServerError, gin.H{"error": "扣除金币失败"})
			return
		}
	}

	card.Appearance = appearance
	if err := tx.Save(&card).Error; err != nil {
		tx.Rollback()
		c.JSON(http.StatusInternalServerError, gin.H{"error": "保存外貌失败"})
		return
	}

	if err := tx.Commit().Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "提交事务失败"})
		return
	}

	resp := gin.H{"appearance": appearance}
	if isOwner {
		models.DB.First(&user, userID)
		resp["coins"] = user.Coins
	}
	c.JSON(http.StatusOK, resp)
}

// NOTE: RegenerateBackstory 通过 SiteSetting 读取费率，扣除金币后重新生成个人经历
func (h *CharacterHandlers) RegenerateBackstory(c *gin.Context) {
	cost := siteSettingInt("regenerate_backstory_cost", 100)
	userID := c.GetUint("user_id")
	id, _ := strconv.ParseUint(c.Param("id"), 10, 64)

	var card models.CharacterCard
	if err := models.DB.First(&card, id).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "人物卡不存在"})
		return
	}
	if card.UserID != userID {
		var u models.User
		models.DB.First(&u, userID)
		if u.Role != "admin" {
			c.JSON(http.StatusForbidden, gin.H{"error": "无权修改此人物卡"})
			return
		}
	}

	isOwner := card.UserID == userID
	var user models.User
	if isOwner {
		models.DB.First(&user, userID)
		if user.Coins < cost {
			c.JSON(http.StatusPaymentRequired, gin.H{
				"error":   "金币不足",
				"need":    cost,
				"current": user.Coins,
			})
			return
		}
	}

	backstory, err := agent.RegenerateBackstory(c.Request.Context(), &card)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "AI生成失败: " + err.Error()})
		return
	}

	tx := models.DB.Begin()
	defer func() {
		if r := recover(); r != nil {
			tx.Rollback()
		}
	}()

	if isOwner {
		if err := tx.Model(&user).Update("coins", user.Coins-cost).Error; err != nil {
			tx.Rollback()
			c.JSON(http.StatusInternalServerError, gin.H{"error": "扣除金币失败"})
			return
		}
	}

	card.Backstory = backstory
	if err := tx.Save(&card).Error; err != nil {
		tx.Rollback()
		c.JSON(http.StatusInternalServerError, gin.H{"error": "保存个人经历失败"})
		return
	}

	if err := tx.Commit().Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "提交事务失败"})
		return
	}

	resp := gin.H{"backstory": backstory}
	if isOwner {
		models.DB.First(&user, userID)
		resp["coins"] = user.Coins
	}
	c.JSON(http.StatusOK, resp)
}

// NOTE: RegenerateTraits 通过 SiteSetting 读取费率，扣除金币后重新生成性格特征
func (h *CharacterHandlers) RegenerateTraits(c *gin.Context) {
	cost := siteSettingInt("regenerate_traits_cost", 100)
	userID := c.GetUint("user_id")
	id, _ := strconv.ParseUint(c.Param("id"), 10, 64)

	var card models.CharacterCard
	if err := models.DB.First(&card, id).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "人物卡不存在"})
		return
	}
	if card.UserID != userID {
		var u models.User
		models.DB.First(&u, userID)
		if u.Role != "admin" {
			c.JSON(http.StatusForbidden, gin.H{"error": "无权修改此人物卡"})
			return
		}
	}

	isOwner := card.UserID == userID
	var user models.User
	if isOwner {
		models.DB.First(&user, userID)
		if user.Coins < cost {
			c.JSON(http.StatusPaymentRequired, gin.H{
				"error":   "金币不足",
				"need":    cost,
				"current": user.Coins,
			})
			return
		}
	}

	traits, err := agent.RegenerateTraits(c.Request.Context(), &card)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "AI生成失败: " + err.Error()})
		return
	}

	tx := models.DB.Begin()
	defer func() {
		if r := recover(); r != nil {
			tx.Rollback()
		}
	}()

	if isOwner {
		if err := tx.Model(&user).Update("coins", user.Coins-cost).Error; err != nil {
			tx.Rollback()
			c.JSON(http.StatusInternalServerError, gin.H{"error": "扣除金币失败"})
			return
		}
	}

	card.Traits = traits
	if err := tx.Save(&card).Error; err != nil {
		tx.Rollback()
		c.JSON(http.StatusInternalServerError, gin.H{"error": "保存性格特征失败"})
		return
	}

	if err := tx.Commit().Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "提交事务失败"})
		return
	}

	resp := gin.H{"traits": traits}
	if isOwner {
		models.DB.First(&user, userID)
		resp["coins"] = user.Coins
	}
	c.JSON(http.StatusOK, resp)
}

// reviveCostFor calculates the next revive cost based on how many times the user has already revived.
func reviveCostFor(reviveCount int) int {
	return (reviveCount + 1) * siteSettingInt("revive_base_cost", 2000)
}

// ListDeadCharacters returns dead (is_active=false, is_deleted=false) character cards belonging to the user.
func ListDeadCharacters(c *gin.Context) {
	userID := c.GetUint("user_id")
	var cards []models.CharacterCard
	models.DB.Where("user_id = ? AND is_active = ? AND is_deleted = ?", userID, false, false).
		Order("updated_at DESC").
		Find(&cards)
	c.JSON(http.StatusOK, cards)
}

// ReviveCharacter spends coins (escalating by 2000 each use) to revive a dead (is_active=false) character card.
// The revived character loses a random half of their inventory and forgets a random half of their spells.
func ReviveCharacter(c *gin.Context) {
	userID := c.GetUint("user_id")
	id, _ := strconv.ParseUint(c.Param("id"), 10, 64)

	var card models.CharacterCard
	if err := models.DB.Where("id = ? AND is_deleted = ?", id, false).First(&card).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "人物卡不存在"})
		return
	}
	if card.UserID != userID {
		var u models.User
		models.DB.First(&u, userID)
		if u.Role != "admin" {
			c.JSON(http.StatusNotFound, gin.H{"error": "人物卡不存在"})
			return
		}
	}
	if card.IsActive {
		c.JSON(http.StatusBadRequest, gin.H{"error": "该调查员尚未死亡，无需复活"})
		return
	}

	isOwner := card.UserID == userID
	var user models.User
	var cost int
	if isOwner {
		models.DB.First(&user, userID)
		cost = reviveCostFor(user.ReviveCount)
		if user.Coins < cost {
			c.JSON(http.StatusPaymentRequired, gin.H{
				"error":   "金币不足",
				"need":    cost,
				"current": user.Coins,
			})
			return
		}
	}

	tx := models.DB.Begin()
	defer func() {
		if r := recover(); r != nil {
			tx.Rollback()
		}
	}()

	if isOwner {
		if err := tx.Model(&user).Updates(map[string]any{
			"coins":        user.Coins - cost,
			"revive_count": user.ReviveCount + 1,
		}).Error; err != nil {
			tx.Rollback()
			c.JSON(http.StatusInternalServerError, gin.H{"error": "扣除金币失败"})
			return
		}
	}

	// Randomly lose half inventory
	inv := card.Inventory.Data
	if len(inv) > 0 {
		rand.Shuffle(len(inv), func(i, j int) { inv[i], inv[j] = inv[j], inv[i] })
		inv = inv[:(len(inv)+1)/2]
	}
	// Randomly forget half spells
	spells := card.Spells.Data
	if len(spells) > 0 {
		rand.Shuffle(len(spells), func(i, j int) { spells[i], spells[j] = spells[j], spells[i] })
		spells = spells[:(len(spells)+1)/2]
	}

	reviveHP := card.Stats.Data.MaxHP / 2
	if reviveHP < 1 {
		reviveHP = 1
	}
	card.Stats.Data.HP = reviveHP
	card.WoundState = "none"
	card.IsUnconscious = false
	card.IsActive = true
	card.Inventory = models.JSONField[[]string]{Data: inv}
	card.Spells = models.JSONField[[]string]{Data: spells}

	if err := tx.Save(&card).Error; err != nil {
		tx.Rollback()
		c.JSON(http.StatusInternalServerError, gin.H{"error": "复活角色失败"})
		return
	}

	if err := tx.Commit().Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "提交事务失败"})
		return
	}

	resp := gin.H{
		"message":        "复活成功",
		"character_card": card,
	}
	if isOwner {
		models.DB.First(&user, userID)
		resp["coins"] = user.Coins
		resp["revive_count"] = user.ReviveCount
		log.Printf("[revive] user_id=%d card_id=%d cost=%d coins_left=%d inv_kept=%d spells_kept=%d",
			userID, card.ID, cost, user.Coins, len(inv), len(spells))
	}
	c.JSON(http.StatusOK, resp)
}

// DeleteDeadCharacter soft-deletes a dead (is_active=false) character card by setting is_deleted=true.
func DeleteDeadCharacter(c *gin.Context) {
	userID := c.GetUint("user_id")
	id, _ := strconv.ParseUint(c.Param("id"), 10, 64)

	var card models.CharacterCard
	if err := models.DB.Where("id = ?", id).First(&card).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "人物卡不存在"})
		return
	}
	if card.UserID != userID {
		var u models.User
		models.DB.First(&u, userID)
		if u.Role != "admin" {
			c.JSON(http.StatusNotFound, gin.H{"error": "人物卡不存在"})
			return
		}
	}
	if card.IsActive {
		c.JSON(http.StatusBadRequest, gin.H{"error": "只能删除已阵亡的调查员"})
		return
	}

	if err := models.DB.Model(&card).Update("is_deleted", true).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "删除失败"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "调查员已彻底消逝"})
}

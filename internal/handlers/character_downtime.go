package handlers

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/llmcoc/server/internal/models"
	"github.com/llmcoc/server/internal/services/game"
	"gorm.io/gorm"
)

var (
	errInsufficientCoins = errors.New("insufficient coins")
	errCardLocked        = errors.New("card locked in session")
)

// SanatoriumHeal 幕间休整：花费金币入住阿卡姆疗养院，恢复 1D6+2 点 SAN 并清除临时性/不定性疯狂状态。
// NOTE: 必须在非对局状态下由人物卡归属者本人执行；永久性疯狂无法通过疗养院治愈。
func SanatoriumHeal(c *gin.Context) {
	userID := c.GetUint("user_id")
	id, _ := strconv.ParseUint(c.Param("id"), 10, 64)

	var card models.CharacterCard
	if err := models.DB.First(&card, id).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "人物卡不存在"})
		return
	}
	if card.UserID != userID {
		c.JSON(http.StatusForbidden, gin.H{"error": "只能为自己的人物卡安排疗养"})
		return
	}
	if !card.IsActive || card.IsDeleted || card.WoundState == "dead" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "阵亡或停用的调查员无法入住疗养院"})
		return
	}
	if isCardLockedInSession(models.DB, card.ID) {
		c.JSON(http.StatusConflict, gin.H{"error": "该人物卡正在房间游戏中，请结团后再进行幕间休整"})
		return
	}
	if card.MadnessState == "permanent" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "永久性疯狂无法在疗养院治愈"})
		return
	}

	hasRecoverableMadness := card.MadnessState == "temporary" || card.MadnessState == "indefinite"
	if card.Stats.Data.SAN >= card.Stats.Data.MaxSAN && !hasRecoverableMadness {
		c.JSON(http.StatusBadRequest, gin.H{"error": "调查员当前理智充沛，无需疗养"})
		return
	}

	cost := siteSettingInt("sanatorium_cost", 50)
	if cost < 0 {
		cost = 0
	}

	rollVal, _ := game.Roll(1, 6)
	healRoll := rollVal + 2
	var healed int
	var clearedMadness bool

	err := models.DB.Transaction(func(tx *gorm.DB) error {
		if isCardLockedInSession(tx, card.ID) {
			return errCardLocked
		}
		if err := tx.First(&card, card.ID).Error; err != nil {
			return err
		}
		if cost > 0 {
			res := tx.Model(&models.User{}).
				Where("id = ? AND coins >= ?", userID, cost).
				Update("coins", gorm.Expr("coins - ?", cost))
			if res.Error != nil {
				return res.Error
			}
			if res.RowsAffected == 0 {
				return errInsufficientCoins
			}
		}

		stats := card.Stats.Data
		before := stats.SAN
		if stats.MaxSAN > 0 && stats.SAN < stats.MaxSAN {
			stats.SAN += healRoll
			if stats.SAN > stats.MaxSAN {
				stats.SAN = stats.MaxSAN
			}
		}
		healed = stats.SAN - before
		card.Stats = models.JSONField[models.CharacterStats]{Data: stats}

		if card.MadnessState == "temporary" || card.MadnessState == "indefinite" {
			card.MadnessState = "none"
			card.MadnessSymptom = ""
			card.MadnessDuration = 0
			clearedMadness = true
		}

		return tx.Save(&card).Error
	})
	if err != nil {
		switch {
		case errors.Is(err, errCardLocked):
			c.JSON(http.StatusConflict, gin.H{"error": "该人物卡正在房间游戏中，请结团后再进行幕间休整"})
		case errors.Is(err, errInsufficientCoins):
			var u models.User
			models.DB.Select("coins").First(&u, userID)
			c.JSON(http.StatusPaymentRequired, gin.H{
				"error":   fmt.Sprintf("金币不足，入住疗养院需要 %d 金币", cost),
				"need":    cost,
				"current": u.Coins,
			})
		default:
			c.JSON(http.StatusInternalServerError, gin.H{"error": "疗养院结算失败"})
		}
		return
	}

	var user models.User
	models.DB.Select("coins").First(&user, userID)

	msg := fmt.Sprintf("疗养完成，理智恢复 %d 点（当前 %d/%d）", healed, card.Stats.Data.SAN, card.Stats.Data.MaxSAN)
	if clearedMadness {
		msg += "，疯狂症状已平复"
	}
	c.JSON(http.StatusOK, gin.H{
		"message":         msg,
		"card":            card,
		"healed":          healed,
		"cleared_madness": clearedMadness,
		"coins_spent":     cost,
		"coins_left":      user.Coins,
	})
}

type studyTomeReq struct {
	TomeName string `json:"tome_name" binding:"required"`
}

// StudyTome 幕间研读：在非对局状态下研读人物卡物品栏中 type=tome 的神话典籍。
// 按 COC7 规则做 INT 检定：损失 1D4 点 SAN（保底留 1 点避免幕间直接永久疯狂），
// 成功获得 1D3 点克苏鲁神话技能、失败获得 1 点，并同步更新技能表与 MaxSAN=99-CM。
func StudyTome(c *gin.Context) {
	userID := c.GetUint("user_id")
	id, _ := strconv.ParseUint(c.Param("id"), 10, 64)

	var req studyTomeReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	tomeName := strings.TrimSpace(req.TomeName)
	if tomeName == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请指定要研读的典籍名称"})
		return
	}

	var card models.CharacterCard
	if err := models.DB.First(&card, id).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "人物卡不存在"})
		return
	}
	if card.UserID != userID {
		c.JSON(http.StatusForbidden, gin.H{"error": "只能为自己的人物卡研读典籍"})
		return
	}
	if !card.IsActive || card.IsDeleted || card.WoundState == "dead" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "阵亡或停用的调查员无法研读典籍"})
		return
	}
	if isCardLockedInSession(models.DB, card.ID) {
		c.JSON(http.StatusConflict, gin.H{"error": "该人物卡正在房间游戏中，请结团后再研读典籍"})
		return
	}

	foundTome := false
	for _, it := range card.Inventory.Data {
		if it.Type == models.ItemTypeTome && strings.TrimSpace(it.Name) == tomeName {
			foundTome = true
			break
		}
	}
	if !foundTome {
		c.JSON(http.StatusNotFound, gin.H{"error": "物品栏中未找到该神话典籍"})
		return
	}
	if card.Stats.Data.SAN <= 4 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "当前理智过低，贸然研读神话典籍风险过高"})
		return
	}

	intRoll, _ := game.Roll(1, 100)
	intTarget := card.Stats.Data.INT
	intSuccess := intRoll <= intTarget

	sanLoss, _ := game.Roll(1, 4)
	mythosGain := 1
	if intSuccess {
		mythosGain, _ = game.Roll(1, 3)
	}

	var actualSanLost int
	var actualMythosGained int
	err := models.DB.Transaction(func(tx *gorm.DB) error {
		if isCardLockedInSession(tx, card.ID) {
			return errCardLocked
		}
		if err := tx.First(&card, card.ID).Error; err != nil {
			return err
		}
		stats := card.Stats.Data
		if stats.SAN <= 4 {
			return errors.New("san too low")
		}
		if stats.SAN-sanLoss < 1 {
			actualSanLost = stats.SAN - 1
		} else {
			actualSanLost = sanLoss
		}
		stats.SAN -= actualSanLost

		beforeCM := card.CthulhuMythosSkill
		if card.Skills.Data != nil && card.Skills.Data["克苏鲁神话"] > beforeCM {
			beforeCM = card.Skills.Data["克苏鲁神话"]
		}
		newCM := beforeCM + mythosGain
		if newCM > 99 {
			newCM = 99
		}
		actualMythosGained = newCM - beforeCM
		card.CthulhuMythosSkill = newCM
		if card.Skills.Data == nil {
			card.Skills.Data = make(map[string]int)
		}
		card.Skills.Data["克苏鲁神话"] = newCM

		newMaxSAN := 99 - newCM
		if newMaxSAN < 1 {
			newMaxSAN = 1
		}
		stats.MaxSAN = newMaxSAN
		if stats.SAN > newMaxSAN {
			stats.SAN = newMaxSAN
		}
		card.Stats = models.JSONField[models.CharacterStats]{Data: stats}
		return tx.Save(&card).Error
	})
	if err != nil {
		if errors.Is(err, errCardLocked) {
			c.JSON(http.StatusConflict, gin.H{"error": "该人物卡正在房间游戏中，请结团后再研读典籍"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "典籍研读结算失败"})
		return
	}

	outcome := "失败"
	if intSuccess {
		outcome = "成功"
	}
	msg := fmt.Sprintf("研读《%s》：智力检定 %d/%d（%s），理智 -%d，克苏鲁神话 +%d", tomeName, intRoll, intTarget, outcome, actualSanLost, actualMythosGained)
	c.JSON(http.StatusOK, gin.H{
		"message":       msg,
		"card":          card,
		"tome_name":     tomeName,
		"int_roll":      intRoll,
		"int_target":    intTarget,
		"int_success":   intSuccess,
		"san_lost":      actualSanLost,
		"mythos_gained": actualMythosGained,
	})
}

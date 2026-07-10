package handlers

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/llmcoc/server/internal/config"
	"github.com/llmcoc/server/internal/models"
	"github.com/llmcoc/server/internal/services/agent"
	"github.com/llmcoc/server/internal/services/game"
	"gorm.io/gorm"
)

const characterDraftTTL = 24 * time.Hour

func getMaxActiveCharacterDrafts() int {
	v := models.GetSiteSetting("max_character_drafts", "3")
	n, err := strconv.Atoi(v)
	if err != nil || n <= 0 {
		return 3
	}
	return n
}

var generateCharacterNarrative = agent.GenerateCharacter

type rollCharacterReq struct {
	Age int `json:"age"`
}

type finalizeCharacterReq struct {
	DraftID      uint           `json:"draft_id"`
	Token        string         `json:"token"`
	Name         string         `json:"name"`
	Gender       string         `json:"gender"`
	Occupation   string         `json:"occupation"`
	Birthplace   string         `json:"birthplace"`
	Residence    string         `json:"residence"`
	CreationHint string         `json:"creation_hint"`
	Backstory    string         `json:"backstory"`
	Appearance   string         `json:"appearance"`
	Traits       string         `json:"traits"`
	Skills       map[string]int `json:"skills"`
	Assets       []models.Asset `json:"assets"`
}

type finalizedDraftData struct {
	draft  models.CharacterDraft
	stats  models.CharacterStats
	age    int
	skills map[string]int
}

func RollCharacterDraft(c *gin.Context) {
	userID := c.GetUint("user_id")
	var req rollCharacterReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if req.Age < game.MinManualCharacterAge || req.Age > game.MaxManualCharacterAge {
		c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("年龄必须在%d-%d之间", game.MinManualCharacterAge, game.MaxManualCharacterAge)})
		return
	}

	now := time.Now().UTC()
	var activeCount int64
	if err := models.DB.Model(&models.CharacterDraft{}).
		Where("user_id = ? AND is_used = ? AND expires_at > ?", userID, false, now).
		Count(&activeCount).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "检查草稿失败"})
		return
	}
	if activeCount >= int64(getMaxActiveCharacterDrafts()) {
		c.JSON(http.StatusTooManyRequests, gin.H{"error": "未过期车卡草稿过多，请稍后再试或完成已有草稿"})
		return
	}

	stats, rawRolls, err := game.GenerateStatsForAge(req.Age)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	draft := models.CharacterDraft{
		UserID:    userID,
		Stats:     models.JSONField[models.CharacterStats]{Data: stats},
		RawRolls:  models.JSONField[models.CharacterRawRolls]{Data: rawRolls},
		ExpiresAt: now.Add(characterDraftTTL),
	}
	if err := models.DB.Create(&draft).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "创建车卡草稿失败"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"draft_id":       draft.ID,
		"token":          signCharacterDraft(draft),
		"stats":          stats,
		"raw_rolls":      rawRolls,
		"skill_defaults": game.SkillDefaults(stats),
		"skill_budget":   game.SkillPointBudget(stats),
		"expires_at":     draft.ExpiresAt,
	})
}

func FinalizeCharacterDraft(c *gin.Context) {
	userID := c.GetUint("user_id")
	var req finalizeCharacterReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	req.Gender = strings.TrimSpace(req.Gender)
	req.Occupation = strings.TrimSpace(req.Occupation)
	req.Birthplace = strings.TrimSpace(req.Birthplace)
	req.Residence = strings.TrimSpace(req.Residence)
	req.CreationHint = strings.TrimSpace(req.CreationHint)
	if req.CreationHint == "" {
		req.CreationHint = strings.TrimSpace(req.Backstory)
	}
	if req.DraftID == 0 || strings.TrimSpace(req.Token) == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "缺少车卡草稿凭证"})
		return
	}
	if req.Name == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "姓名不能为空"})
		return
	}
	if req.Gender != models.GenderMale && req.Gender != models.GenderFemale {
		c.JSON(http.StatusBadRequest, gin.H{"error": "无效的性别"})
		return
	}

	prepared, err := prepareCharacterDraftFinalize(models.DB, userID, req)
	if err != nil {
		if fe, ok := err.(draftFinalizeError); ok {
			c.JSON(fe.status, fe.payload)
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "校验车卡草稿失败"})
		return
	}

	generated, err := generateCharacterNarrative(context.Background(), agent.GenerateCharacterReq{
		Name:       req.Name,
		Occupation: req.Occupation,
		Background: req.CreationHint,
		Gender:     req.Gender,
		Age:        prepared.age,
		Stats:      prepared.stats,
	})
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": "AI生成背景失败: " + err.Error()})
		return
	}
	if generated == nil || strings.TrimSpace(generated.Backstory) == "" || strings.TrimSpace(generated.Appearance) == "" || strings.TrimSpace(generated.Traits) == "" {
		c.JSON(http.StatusBadGateway, gin.H{"error": "AI生成背景失败: 返回内容不完整"})
		return
	}
	backstory := strings.TrimSpace(generated.Backstory)
	appearance := strings.TrimSpace(generated.Appearance)
	traits := strings.TrimSpace(generated.Traits)

	var created models.CharacterCard
	err = models.DB.Transaction(func(tx *gorm.DB) error {
		txPrepared, err := prepareCharacterDraftFinalize(tx, userID, req)
		if err != nil {
			return err
		}

		usedAt := time.Now().UTC()
		res := tx.Model(&models.CharacterDraft{}).
			Where("id = ? AND user_id = ? AND is_used = ?", txPrepared.draft.ID, userID, false).
			Updates(map[string]any{"is_used": true, "used_at": &usedAt})
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected != 1 {
			return draftFinalizeError{status: http.StatusBadRequest, payload: gin.H{"error": "车卡草稿已使用"}}
		}

		created = models.CharacterCard{
			UserID:     userID,
			Name:       req.Name,
			Age:        txPrepared.age,
			Race:       "人类",
			Gender:     req.Gender,
			Occupation: req.Occupation,
			Birthplace: req.Birthplace,
			Residence:  req.Residence,
			Backstory:  backstory,
			Appearance: appearance,
			Traits:     traits,
			Stats:      models.JSONField[models.CharacterStats]{Data: txPrepared.stats},
			Skills:     models.JSONField[map[string]int]{Data: txPrepared.skills},
			Assets:     models.JSONField[[]models.Asset]{Data: req.Assets},
			IsActive:   true,
		}
		if err := tx.Create(&created).Error; err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		if fe, ok := err.(draftFinalizeError); ok {
			c.JSON(fe.status, fe.payload)
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "创建人物卡失败"})
		return
	}
	c.JSON(http.StatusCreated, created)
}

func prepareCharacterDraftFinalize(db *gorm.DB, userID uint, req finalizeCharacterReq) (finalizedDraftData, error) {
	var draft models.CharacterDraft
	if err := db.Where("id = ? AND user_id = ? AND is_used = ?", req.DraftID, userID, false).
		First(&draft).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			var existing models.CharacterDraft
			if findErr := db.Where("id = ? AND user_id = ?", req.DraftID, userID).First(&existing).Error; findErr == nil && existing.IsUsed {
				return finalizedDraftData{}, draftFinalizeError{status: http.StatusBadRequest, payload: gin.H{"error": "车卡草稿已使用"}}
			}
			return finalizedDraftData{}, draftFinalizeError{status: http.StatusNotFound, payload: gin.H{"error": "车卡草稿不存在"}}
		}
		return finalizedDraftData{}, err
	}
	if time.Now().UTC().After(draft.ExpiresAt) {
		return finalizedDraftData{}, draftFinalizeError{status: http.StatusBadRequest, payload: gin.H{"error": "车卡草稿已过期，请重新投掷"}}
	}
	if !verifyCharacterDraftToken(draft, req.Token) {
		return finalizedDraftData{}, draftFinalizeError{status: http.StatusBadRequest, payload: gin.H{"error": "车卡草稿凭证无效，请重新投掷"}}
	}

	stats := draft.Stats.Data
	age := draft.RawRolls.Data.Age
	game.ApplyDerivedStats(&stats, age, true)
	skills, spent, err := game.NormalizeSkills(req.Skills, stats)
	if err != nil {
		payload := gin.H{"error": "技能分配不符合规则", "spent": spent, "budget": game.SkillPointBudget(stats)}
		if ve, ok := err.(game.SkillValidationError); ok {
			payload["details"] = ve.Details
		} else {
			payload["details"] = []string{err.Error()}
		}
		return finalizedDraftData{}, draftFinalizeError{status: http.StatusBadRequest, payload: payload}
	}

	var user models.User
	if err := db.First(&user, userID).Error; err != nil {
		return finalizedDraftData{}, err
	}
	var cardCount int64
	if err := db.Model(&models.CharacterCard{}).
		Where("user_id = ? AND is_active = ?", userID, true).
		Count(&cardCount).Error; err != nil {
		return finalizedDraftData{}, err
	}
	if int(cardCount) >= user.CardSlots {
		return finalizedDraftData{}, draftFinalizeError{status: http.StatusForbidden, payload: gin.H{
			"error":     "人物卡槽位已满",
			"current":   cardCount,
			"max_slots": user.CardSlots,
		}}
	}

	return finalizedDraftData{draft: draft, stats: stats, age: age, skills: skills}, nil
}

func GetCharacterSkillDefaults(c *gin.Context) {
	defaults := game.DefaultSkills()
	names := make([]string, 0, len(defaults))
	for name := range defaults {
		names = append(names, name)
	}
	sort.Strings(names)
	c.JSON(http.StatusOK, gin.H{
		"skill_defaults": defaults,
		"skill_names":    names,
		"locked_skills":  []string{"母语", "闪避"},
		"blocked_skills": []string{"克苏鲁神话"},
		"max_skill":      90,
	})
}

type draftFinalizeError struct {
	status  int
	payload gin.H
}

func (e draftFinalizeError) Error() string {
	if msg, ok := e.payload["error"].(string); ok {
		return msg
	}
	return "车卡草稿处理失败"
}

func signCharacterDraft(draft models.CharacterDraft) string {
	mac := hmac.New(sha256.New, []byte(characterDraftSecret()))
	mac.Write([]byte(characterDraftSignPayload(draft)))
	return hex.EncodeToString(mac.Sum(nil))
}

func verifyCharacterDraftToken(draft models.CharacterDraft, token string) bool {
	expected := signCharacterDraft(draft)
	provided, err := hex.DecodeString(strings.TrimSpace(token))
	if err != nil {
		return false
	}
	expectedBytes, err := hex.DecodeString(expected)
	if err != nil {
		return false
	}
	return hmac.Equal(expectedBytes, provided)
}

func characterDraftSignPayload(draft models.CharacterDraft) string {
	statsJSON, _ := json.Marshal(draft.Stats.Data)
	return fmt.Sprintf("%d|%d|%d|%s", draft.ID, draft.UserID, draft.ExpiresAt.UTC().Unix(), string(statsJSON))
}

func characterDraftSecret() string {
	secret := config.Global.JWT.Secret
	if config.Global.JWT.Secret != "" {
		return "character-draft:" + secret
	}
	return "character-draft:change-me-in-production"
}

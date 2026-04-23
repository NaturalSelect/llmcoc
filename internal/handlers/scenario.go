package handlers

import (
	"net/http"
	"os"
	"path/filepath"
	"strconv"

	"encoding/json"

	"github.com/gin-gonic/gin"
	"github.com/llmcoc/server/internal/models"
)

func ListScenarios(c *gin.Context) {
	var scenarios []models.Scenario
	models.DB.Where("is_active = ?", true).
		Select("id, name, description, author, tags, min_players, max_players, difficulty, is_active, created_at, updated_at").
		Order("created_at DESC").
		Find(&scenarios)
	c.JSON(http.StatusOK, scenarios)
}

func GetScenario(c *gin.Context) {
	id, _ := strconv.ParseUint(c.Param("id"), 10, 64)
	var scenario models.Scenario
	if err := models.DB.First(&scenario, id).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "剧本不存在"})
		return
	}
	c.JSON(http.StatusOK, scenario)
}

type CreateScenarioReq struct {
	Name        string                   `json:"name" binding:"required,max=200"`
	Description string                   `json:"description"`
	Author      string                   `json:"author"`
	Tags        string                   `json:"tags"`
	MinPlayers  int                      `json:"min_players"`
	MaxPlayers  int                      `json:"max_players"`
	Difficulty  string                   `json:"difficulty"`
	Content     models.ScenarioContent   `json:"content" binding:"required"`
}

func CreateScenario(c *gin.Context) {
	var req CreateScenarioReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if req.MinPlayers == 0 {
		req.MinPlayers = 1
	}
	if req.MaxPlayers == 0 {
		req.MaxPlayers = 4
	}
	if req.Difficulty == "" {
		req.Difficulty = "normal"
	}

	scenario := models.Scenario{
		Name:        req.Name,
		Description: req.Description,
		Author:      req.Author,
		Tags:        req.Tags,
		MinPlayers:  req.MinPlayers,
		MaxPlayers:  req.MaxPlayers,
		Difficulty:  req.Difficulty,
		IsActive:    true,
		Content:     models.JSONField[models.ScenarioContent]{Data: req.Content},
	}
	if err := models.DB.Create(&scenario).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "创建剧本失败"})
		return
	}
	c.JSON(http.StatusCreated, scenario)
}

// SeedScenarios loads scenario JSON files from the scenarios directory
func SeedScenarios(dir string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			continue
		}

		var scenarioFile struct {
			Name        string                 `json:"name"`
			Description string                 `json:"description"`
			Author      string                 `json:"author"`
			Tags        string                 `json:"tags"`
			MinPlayers  int                    `json:"min_players"`
			MaxPlayers  int                    `json:"max_players"`
			Difficulty  string                 `json:"difficulty"`
			Content     models.ScenarioContent `json:"content"`
		}
		if err := json.Unmarshal(data, &scenarioFile); err != nil {
			continue
		}

		// Skip if already exists
		var count int64
		models.DB.Model(&models.Scenario{}).Where("name = ?", scenarioFile.Name).Count(&count)
		if count > 0 {
			continue
		}

		scenario := models.Scenario{
			Name:        scenarioFile.Name,
			Description: scenarioFile.Description,
			Author:      scenarioFile.Author,
			Tags:        scenarioFile.Tags,
			MinPlayers:  scenarioFile.MinPlayers,
			MaxPlayers:  scenarioFile.MaxPlayers,
			Difficulty:  scenarioFile.Difficulty,
			IsActive:    true,
			Content:     models.JSONField[models.ScenarioContent]{Data: scenarioFile.Content},
		}
		models.DB.Create(&scenario)
	}
}

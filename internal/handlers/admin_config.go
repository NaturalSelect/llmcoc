package handlers

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/llmcoc/server/internal/models"
)

// ── view type ─────────────────────────────────────────────────────────────────

type providerView struct {
	ID        uint   `json:"id"`
	Name      string `json:"name"`
	Provider  string `json:"provider"`
	BaseURL   string `json:"base_url"`
	APIKeySet bool   `json:"api_key_set"`
	IsActive  bool   `json:"is_active"`
}

func toProviderView(p models.LLMProviderConfig) providerView {
	return providerView{
		ID:        p.ID,
		Name:      p.Name,
		Provider:  p.Provider,
		BaseURL:   p.BaseURL,
		APIKeySet: p.APIKey != "",
		IsActive:  p.IsActive,
	}
}

// ── LLM Provider handlers ─────────────────────────────────────────────────────

func AdminListProviders(c *gin.Context) {
	var providers []models.LLMProviderConfig
	models.DB.Order("id ASC").Find(&providers)
	views := make([]providerView, len(providers))
	for i, p := range providers {
		views[i] = toProviderView(p)
	}
	c.JSON(http.StatusOK, views)
}

func AdminCreateProvider(c *gin.Context) {
	var req struct {
		Name     string `json:"name" binding:"required,max=100"`
		Provider string `json:"provider" binding:"required,oneof=openai custom"`
		BaseURL  string `json:"base_url"`
		APIKey   string `json:"api_key"`
		IsActive *bool  `json:"is_active"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	isActive := true
	if req.IsActive != nil {
		isActive = *req.IsActive
	}

	p := models.LLMProviderConfig{
		Name:     req.Name,
		Provider: req.Provider,
		BaseURL:  req.BaseURL,
		APIKey:   req.APIKey,
		IsActive: isActive,
	}
	if err := models.DB.Create(&p).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "创建失败：" + err.Error()})
		return
	}
	c.JSON(http.StatusCreated, toProviderView(p))
}

func AdminUpdateProvider(c *gin.Context) {
	id, _ := strconv.ParseUint(c.Param("id"), 10, 64)
	var p models.LLMProviderConfig
	if err := models.DB.First(&p, id).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "提供商不存在"})
		return
	}

	var req struct {
		Name     string `json:"name"`
		Provider string `json:"provider"`
		BaseURL  string `json:"base_url"`
		APIKey   string `json:"api_key"`
		IsActive *bool  `json:"is_active"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	updates := map[string]interface{}{
		"base_url": req.BaseURL,
	}
	if req.Name != "" {
		updates["name"] = req.Name
	}
	if req.Provider != "" {
		updates["provider"] = req.Provider
	}
	// Only update API key if a real value (not masked placeholder) is provided
	if req.APIKey != "" && req.APIKey != "*****" {
		updates["api_key"] = req.APIKey
	}
	if req.IsActive != nil {
		updates["is_active"] = *req.IsActive
	}

	if err := models.DB.Model(&p).Updates(updates).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "更新失败"})
		return
	}
	models.DB.First(&p, id)
	c.JSON(http.StatusOK, toProviderView(p))
}

func AdminDeleteProvider(c *gin.Context) {
	id, _ := strconv.ParseUint(c.Param("id"), 10, 64)

	var count int64
	models.DB.Model(&models.AgentConfig{}).Where("provider_config_id = ?", id).Count(&count)
	if count > 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "该提供商正在被 Agent 使用，请先解除绑定"})
		return
	}
	if err := models.DB.Delete(&models.LLMProviderConfig{}, id).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "删除失败"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "删除成功"})
}

// ── Agent Config handlers ──────────────────────────────────────────────────────

func AdminListAgents(c *gin.Context) {
	var agents []models.AgentConfig
	models.DB.Preload("ProviderConfig").Order("id ASC").Find(&agents)
	// Mask API key in embedded ProviderConfig
	for i := range agents {
		if agents[i].ProviderConfig != nil {
			agents[i].ProviderConfig.APIKey = ""
		}
	}
	c.JSON(http.StatusOK, agents)
}

func AdminUpdateAgent(c *gin.Context) {
	role := c.Param("role")
	validRoles := map[string]bool{"director": true, "judger": true, "scripter": true, "writer": true}
	if !validRoles[role] {
		c.JSON(http.StatusBadRequest, gin.H{"error": "无效的 Agent 角色"})
		return
	}

	var req struct {
		ProviderConfigID *uint   `json:"provider_config_id"`
		ModelName        string  `json:"model_name"`
		MaxTokens        int     `json:"max_tokens"`
		Temperature      float32 `json:"temperature"`
		SystemPrompt     string  `json:"system_prompt"`
		IsActive         *bool   `json:"is_active"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Validate provider reference
	if req.ProviderConfigID != nil {
		var prov models.LLMProviderConfig
		if err := models.DB.First(&prov, *req.ProviderConfigID).Error; err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "提供商不存在"})
			return
		}
	}

	updates := map[string]interface{}{
		"provider_config_id": req.ProviderConfigID,
		"model_name":         req.ModelName,
		"max_tokens":         req.MaxTokens,
		"temperature":        req.Temperature,
		"system_prompt":      req.SystemPrompt,
	}
	if req.IsActive != nil {
		updates["is_active"] = *req.IsActive
	}

	var agentCfg models.AgentConfig
	result := models.DB.Where("role = ?", role).First(&agentCfg)
	if result.Error != nil {
		// Create
		isActive := true
		if req.IsActive != nil {
			isActive = *req.IsActive
		}
		agentCfg = models.AgentConfig{
			Role:             models.AgentRole(role),
			ProviderConfigID: req.ProviderConfigID,
			ModelName:        req.ModelName,
			MaxTokens:        req.MaxTokens,
			Temperature:      req.Temperature,
			SystemPrompt:     req.SystemPrompt,
			IsActive:         isActive,
		}
		models.DB.Create(&agentCfg)
	} else {
		models.DB.Model(&agentCfg).Updates(updates)
	}

	models.DB.Preload("ProviderConfig").First(&agentCfg, agentCfg.ID)
	if agentCfg.ProviderConfig != nil {
		agentCfg.ProviderConfig.APIKey = ""
	}
	c.JSON(http.StatusOK, agentCfg)
}

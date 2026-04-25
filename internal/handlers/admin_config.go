package handlers

import (
	"context"
	"log"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/llmcoc/server/internal/models"
	"github.com/llmcoc/server/internal/services/llm"
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
	log.Printf("[admin_config] create_provider name=%q provider=%s", req.Name, req.Provider)
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
	log.Printf("[admin_config] create_provider ok id=%d name=%q", p.ID, p.Name)
	c.JSON(http.StatusCreated, toProviderView(p))
}

func AdminUpdateProvider(c *gin.Context) {
	id, _ := strconv.ParseUint(c.Param("id"), 10, 64)
	log.Printf("[admin_config] update_provider id=%d", id)
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
	log.Printf("[admin_config] update_provider ok id=%d name=%q", p.ID, p.Name)
	c.JSON(http.StatusOK, toProviderView(p))
}

func AdminDeleteProvider(c *gin.Context) {
	id, _ := strconv.ParseUint(c.Param("id"), 10, 64)
	log.Printf("[admin_config] delete_provider id=%d", id)

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
	log.Printf("[admin_config] delete_provider ok id=%d", id)
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
	log.Printf("[admin_config] update_agent role=%s", role)
	validRoles := map[string]bool{
		"director": true, "writer": true, "lawyer": true, "npc": true, "evaluator": true, "growth": true,
		"scripter": true, "architect": true, "lore_researcher": true, "encounter_designer": true, "qa_guard": true,
		"parser": true,
	}
	if !validRoles[role] {
		c.JSON(http.StatusBadRequest, gin.H{"error": "无效的 Agent 角色"})
		return
	}

	// Use a raw map so that provider_config_id can be ""/null/number from JS.
	var raw map[string]any
	if err := c.ShouldBindJSON(&raw); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Coerce provider_config_id: JS sends "" when unset, numeric string when select is string-typed.
	var providerConfigID *uint
	if v, ok := raw["provider_config_id"]; ok && v != nil {
		switch n := v.(type) {
		case float64:
			if n != 0 {
				id := uint(n)
				providerConfigID = &id
			}
		case string:
			// empty string → nil; numeric string → parse as uint
			if n != "" {
				if parsed, err := strconv.ParseUint(n, 10, 64); err == nil && parsed != 0 {
					uid := uint(parsed)
					providerConfigID = &uid
				}
			}
		}
	}

	modelName, _ := raw["model_name"].(string)
	maxTokens := int(toFloat(raw["max_tokens"]))
	temperature := float32(toFloat(raw["temperature"]))
	systemPrompt, _ := raw["system_prompt"].(string)
	var isActive *bool
	if v, ok := raw["is_active"]; ok {
		if b, ok := v.(bool); ok {
			isActive = &b
		}
	}

	updates := map[string]interface{}{
		"provider_config_id": providerConfigID,
		"model_name":         modelName,
		"max_tokens":         maxTokens,
		"temperature":        temperature,
		"system_prompt":      systemPrompt,
	}
	if isActive != nil {
		updates["is_active"] = *isActive
	}

	var agentCfg models.AgentConfig
	result := models.DB.Where("role = ?", role).First(&agentCfg)
	if result.Error != nil {
		// Create
		active := true
		if isActive != nil {
			active = *isActive
		}
		agentCfg = models.AgentConfig{
			Role:             models.AgentRole(role),
			ProviderConfigID: providerConfigID,
			ModelName:        modelName,
			MaxTokens:        maxTokens,
			Temperature:      temperature,
			SystemPrompt:     systemPrompt,
			IsActive:         active,
		}
		models.DB.Create(&agentCfg)
	} else {
		models.DB.Model(&agentCfg).Updates(updates)
	}

	models.DB.Preload("ProviderConfig").First(&agentCfg, agentCfg.ID)
	if agentCfg.ProviderConfig != nil {
		agentCfg.ProviderConfig.APIKey = ""
	}
	log.Printf("[admin_config] update_agent ok role=%s provider_config_id=%v model=%s", role, providerConfigID, modelName)
	c.JSON(http.StatusOK, agentCfg)
}

// toFloat safely coerces a JSON-decoded any value to float64.
func toFloat(v any) float64 {
	if n, ok := v.(float64); ok {
		return n
	}
	return 0
}

// ProviderFactory abstracts llm.Provider construction so ping can be tested without real HTTP calls.
type ProviderFactory interface {
	NewProvider(cfg *models.LLMProviderConfig, modelName string, maxTokens int, temperature float32) llm.Provider
}

// defaultProviderFactory is the production factory that calls llm.NewProviderFromConfig.
type defaultProviderFactory struct{}

func (defaultProviderFactory) NewProvider(cfg *models.LLMProviderConfig, modelName string, maxTokens int, temperature float32) llm.Provider {
	return llm.NewProviderFromConfig(cfg, modelName, maxTokens, temperature)
}

// DefaultProviderFactory is the singleton used by production handlers.
var DefaultProviderFactory ProviderFactory = defaultProviderFactory{}

// AdminPingProvider sends a minimal Chat request to verify LLM connectivity.
// POST /admin/config/providers/:id/ping
// Body: {"model_name": "gpt-4o-mini"}  (model_name is optional)
// The factory parameter lets tests inject a mock; pass nil to use DefaultProviderFactory.
func AdminPingProvider(c *gin.Context) {
	adminPingProviderWithFactory(c, DefaultProviderFactory)
}

func adminPingProviderWithFactory(c *gin.Context, factory ProviderFactory) {
	id, _ := strconv.ParseUint(c.Param("id"), 10, 64)
	log.Printf("[admin_config] ping_provider id=%d", id)
	var p models.LLMProviderConfig
	if err := models.DB.First(&p, id).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "提供商不存在"})
		return
	}

	var req struct {
		ModelName string `json:"model_name"`
	}
	_ = c.ShouldBindJSON(&req)
	if req.ModelName == "" {
		req.ModelName = "gpt-4o-mini"
	}

	provider := factory.NewProvider(&p, req.ModelName, 16, 0.1)
	ctx, cancel := context.WithTimeout(c.Request.Context(), 15*time.Second)
	defer cancel()

	start := time.Now()
	_, err := provider.Chat(ctx, []llm.ChatMessage{{Role: "user", Content: "Reply with: pong"}})
	latencyMs := time.Since(start).Milliseconds()

	if err != nil {
		log.Printf("[admin_config] ping_provider id=%d error: %v", id, err)
		c.JSON(http.StatusBadGateway, gin.H{"ok": false, "error": err.Error()})
		return
	}
	log.Printf("[admin_config] ping_provider ok id=%d latency_ms=%d", id, latencyMs)
	c.JSON(http.StatusOK, gin.H{"ok": true, "latency_ms": latencyMs})
}

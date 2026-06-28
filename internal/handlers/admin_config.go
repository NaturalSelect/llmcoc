// NOTE: Package handlers implements the HTTP request handlers for the application's REST API.
package handlers

import (
	"context"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/llmcoc/server/internal/models"
	"github.com/llmcoc/server/internal/services/agent"
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

// NOTE: AdminListProviders handles GET /admin/config/providers.
// Returns a list of configured LLM providers, hiding sensitive API keys.
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
		c.JSON(http.StatusInternalServerError, gin.H{"error": "创建失败:" + err.Error()})
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
		c.JSON(http.StatusBadRequest, gin.H{"error": "该提供商正在被 Agent 使用,请先解除绑定"})
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
		"anti_cheat": true, "parser": true, "painter": true,
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
	thinkingLevel, _ := raw["thinking_level"].(string)
	var isActive *bool
	if v, ok := raw["is_active"]; ok {
		if b, ok := v.(bool); ok {
			isActive = &b
		}
	}
	// NOTE: 解析 disable_temperature 开关,用于不支持 temperature 参数的模型
	disableTemperature := false
	if v, ok := raw["disable_temperature"]; ok {
		if b, ok := v.(bool); ok {
			disableTemperature = b
		}
	}

	updates := map[string]interface{}{
		"provider_config_id":  providerConfigID,
		"model_name":          modelName,
		"max_tokens":          maxTokens,
		"temperature":         temperature,
		"disable_temperature": disableTemperature,
		"thinking_level":      thinkingLevel,
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
			Role:               models.AgentRole(role),
			ProviderConfigID:   providerConfigID,
			ModelName:          modelName,
			MaxTokens:          maxTokens,
			Temperature:        temperature,
			DisableTemperature: disableTemperature,
			ThinkingLevel:      thinkingLevel,
			IsActive:           active,
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
	agent.ClearAllCachedAgents()
}

// toFloat safely coerces a JSON-decoded any value to float64.
func toFloat(v any) float64 {
	if n, ok := v.(float64); ok {
		return n
	}
	return 0
}

// NOTE: ProviderFactory 抽象 llm.Provider 构造，方便 ping 测试注入替身。
type ProviderFactory interface {
	NewProvider(cfg *models.LLMProviderConfig, modelName string, maxTokens int, temperature float32, disableTemperature bool, reasoningEffort string) llm.Provider
}

// NOTE: defaultProviderFactory 是生产环境使用的 Provider 工厂。
type defaultProviderFactory struct{}

func (defaultProviderFactory) NewProvider(cfg *models.LLMProviderConfig, modelName string, maxTokens int, temperature float32, disableTemperature bool, reasoningEffort string) llm.Provider {
	return llm.NewProviderFromConfig(cfg, modelName, maxTokens, temperature, disableTemperature, reasoningEffort)
}

// NOTE: DefaultProviderFactory 是生产 handler 使用的单例工厂。
var DefaultProviderFactory ProviderFactory = defaultProviderFactory{}

// NOTE: AdminPingProvider 根据请求模式测试 Provider 文本或图片模型连通性。
// NOTE: POST /admin/config/providers/:id/ping
// NOTE: Body: {"model_name":"gpt-4o-mini","mode":"chat|image","role":"painter"}。
func AdminPingProvider(c *gin.Context) {
	adminPingProviderWithFactory(c, DefaultProviderFactory)
}

type pingProviderRequest struct {
	ModelName string `json:"model_name"`
	Mode      string `json:"mode"`
	Role      string `json:"role"`
}

func adminPingProviderWithFactory(c *gin.Context, factory ProviderFactory) {
	if factory == nil {
		factory = DefaultProviderFactory
	}
	id, _ := strconv.ParseUint(c.Param("id"), 10, 64)
	log.Printf("[admin_config] ping_provider id=%d", id)
	var p models.LLMProviderConfig
	if err := models.DB.First(&p, id).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "提供商不存在"})
		return
	}

	var req pingProviderRequest
	_ = c.ShouldBindJSON(&req)
	req.ModelName = strings.TrimSpace(req.ModelName)
	req.Mode = strings.ToLower(strings.TrimSpace(req.Mode))
	req.Role = strings.ToLower(strings.TrimSpace(req.Role))
	if req.Mode == "image" || req.Role == string(models.AgentRolePainter) {
		adminPingImageProvider(c, factory, &p, req.ModelName)
		return
	}
	if req.ModelName == "" {
		req.ModelName = "gpt-5.4-nano"
	}

	provider := factory.NewProvider(&p, req.ModelName, 16, 0.1, false, "")
	ctx, cancel := context.WithTimeout(c.Request.Context(), 15*time.Second)
	defer cancel()

	start := time.Now()
	_, err := provider.Chat(ctx, []llm.ChatMessage{{Role: "user", Content: "Reply with: pong"}})
	latencyMs := time.Since(start).Milliseconds()

	if err != nil {
		log.Printf("[admin_config] ping_provider id=%d error: %v", id, err)
		c.JSON(http.StatusBadGateway, gin.H{"ok": false, "mode": "chat", "error": err.Error()})
		return
	}
	log.Printf("[admin_config] ping_provider ok id=%d latency_ms=%d", id, latencyMs)
	c.JSON(http.StatusOK, gin.H{"ok": true, "mode": "chat", "latency_ms": latencyMs})
}

func adminPingImageProvider(c *gin.Context, factory ProviderFactory, p *models.LLMProviderConfig, modelName string) {
	if modelName == "" {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "mode": "image", "error": "请先填写模型名称"})
		return
	}

	provider := factory.NewProvider(p, modelName, 0, 0, false, "none")
	generator, ok := provider.(llm.ImageGenerator)
	if !ok {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "mode": "image", "error": "当前 Provider 不支持图片生成接口，无法测试 Painter 图片模型"})
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 30*time.Second)
	defer cancel()

	start := time.Now()
	base64Data, _, err := generator.GenerateImage(ctx, "A simple black and white test icon", "1024x1024")
	latencyMs := time.Since(start).Milliseconds()
	if err != nil {
		log.Printf("[admin_config] ping_provider image id=%d error: %v", p.ID, err)
		c.JSON(http.StatusBadGateway, gin.H{"ok": false, "mode": "image", "error": "图片生成测试失败: " + err.Error()})
		return
	}
	if strings.TrimSpace(base64Data) == "" {
		log.Printf("[admin_config] ping_provider image id=%d empty image data", p.ID)
		c.JSON(http.StatusBadGateway, gin.H{"ok": false, "mode": "image", "error": "图片生成测试未返回图片数据"})
		return
	}
	log.Printf("[admin_config] ping_provider image ok id=%d latency_ms=%d", p.ID, latencyMs)
	c.JSON(http.StatusOK, gin.H{"ok": true, "mode": "image", "latency_ms": latencyMs})
}

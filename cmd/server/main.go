package main

import (
	"embed"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
	"github.com/llmcoc/server/internal/config"
	"github.com/llmcoc/server/internal/handlers"
	"github.com/llmcoc/server/internal/middleware"
	"github.com/llmcoc/server/internal/models"
	"github.com/llmcoc/server/internal/services/agent"
	"github.com/llmcoc/server/internal/services/rulebook"
)

//go:embed web
var webFS embed.FS

func main() {
	// Load config
	cfgPath := os.Getenv("CONFIG_PATH")
	if cfgPath == "" {
		cfgPath = "config.yaml"
	}
	if err := config.Load(cfgPath); err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

	// Init DB
	if err := models.InitDB(); err != nil {
		log.Fatalf("Failed to init database: %v", err)
	}

	// Seed scenarios from scenarios/ directory
	handlers.SeedScenarios("scenarios")

	// Seed default shop items
	handlers.SeedShopItems()

	// Load COC rulebook for the Lawyer agent.
	rbPath := os.Getenv("RULEBOOK_PATH")
	if rbPath == "" {
		rbPath = "COC_kp.md"
	}
	if idx, err := rulebook.Load(rbPath); err != nil {
		log.Printf("Warning: failed to load rulebook (%s): %v — Lawyer agent will have no rule data", rbPath, err)
	} else {
		rulebook.GlobalIndex = idx
		log.Printf("Rulebook loaded: %d sections from %s", len(idx), rbPath)
	}

	// Create Gin engine
	if os.Getenv("GIN_MODE") == "" {
		gin.SetMode(gin.ReleaseMode)
	}
	r := gin.New()
	r.Use(gin.Logger(), gin.Recovery())

	// CORS
	r.Use(cors.New(cors.Config{
		AllowOrigins:     []string{"*"},
		AllowMethods:     []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"},
		AllowHeaders:     []string{"Origin", "Content-Type", "Authorization"},
		ExposeHeaders:    []string{"Content-Length"},
		AllowCredentials: false,
	}))

	// ─── API routes ───────────────────────────────────────────────────────────
	api := r.Group("/api")

	// Auth (public)
	auth := api.Group("/auth")
	{
		auth.POST("/register", handlers.Register)
		auth.POST("/login", handlers.Login)
		auth.GET("/me", middleware.AuthRequired(), handlers.Me)
		auth.GET("/settings/public", handlers.PublicSettings)
	}

	// Characters (authenticated)
	chh := handlers.NewCharacterHandlers(handlers.DefaultCharacterLLMFactory)
	chars := api.Group("/characters", middleware.AuthRequired())
	{
		chars.GET("", handlers.ListCharacters)
		chars.POST("", handlers.CreateCharacter)
		chars.POST("/generate", chh.GenerateCharacter)
		chars.GET("/:id", handlers.GetCharacter)
		chars.PUT("/:id", handlers.UpdateCharacter)
		chars.DELETE("/:id", handlers.DeleteCharacter)
		chars.GET("/:id/inventory", handlers.GetCharacterInventory)
		chars.POST("/:id/inventory", handlers.AddCharacterInventoryItem)
		chars.DELETE("/:id/inventory/:item", handlers.RemoveCharacterInventoryItem)
	}

	// Scenarios
	scenarios := api.Group("/scenarios", middleware.AuthRequired())
	{
		scenarios.GET("", handlers.ListScenarios)
		scenarios.GET("/:id/module", handlers.GetScenarioModule)
		scenarios.GET("/:id", handlers.GetScenario)
		scenarios.GET("/template", handlers.DownloadScenarioTemplate)
		scenarios.POST("", middleware.AdminRequired(), handlers.CreateScenario)
		scenarios.POST("/generate", middleware.AdminRequired(), handlers.GenerateScenarioByAgents)
		scenarios.POST("/upload", middleware.AdminRequired(), handlers.UploadScenario)
		scenarios.DELETE("/:id", middleware.AdminRequired(), handlers.DeleteScenario)
	}

	// Sessions
	sh := handlers.NewSessionHandlers(agent.DefaultRunner{})
	sessions := api.Group("/sessions", middleware.AuthRequired())
	{
		sessions.GET("", handlers.ListSessions)
		sessions.POST("", handlers.CreateSession)
		sessions.GET("/:id", handlers.GetSession)
		sessions.POST("/:id/join", handlers.JoinSession)
		sessions.POST("/:id/leave", handlers.LeaveSession)
		sessions.POST("/:id/start", handlers.StartSession)
		sessions.POST("/:id/end", handlers.EndSession)
		sessions.GET("/:id/messages", handlers.GetMessages)
		sessions.POST("/:id/chat", sh.ChatStream)
	}

	// Shop
	shop := api.Group("/shop", middleware.AuthRequired())
	{
		shop.GET("/items", handlers.ListShopItems)
		shop.POST("/purchase", handlers.PurchaseItem)
		shop.GET("/transactions", handlers.GetMyTransactions)
	}

	// Admin
	admin := api.Group("/admin", middleware.AuthRequired(), middleware.AdminRequired())
	{
		admin.GET("/users", handlers.AdminListUsers)
		admin.POST("/recharge", handlers.AdminRechargeCoins)
		admin.PUT("/users/:id/role", handlers.AdminSetRole)
		admin.GET("/recharges", handlers.AdminGetRechargeHistory)
		admin.POST("/shop/items", handlers.AdminCreateShopItem)
		// LLM provider config
		admin.GET("/config/providers", handlers.AdminListProviders)
		admin.POST("/config/providers", handlers.AdminCreateProvider)
		admin.PUT("/config/providers/:id", handlers.AdminUpdateProvider)
		admin.DELETE("/config/providers/:id", handlers.AdminDeleteProvider)
		admin.POST("/config/providers/:id/ping", handlers.AdminPingProvider)
		// Agent config
		admin.GET("/config/agents", handlers.AdminListAgents)
		admin.PUT("/config/agents/:role", handlers.AdminUpdateAgent)
		// Site settings & invite codes
		admin.GET("/config/settings", handlers.AdminGetSiteSettings)
		admin.PUT("/config/settings/:key", handlers.AdminUpdateSiteSetting)
		admin.GET("/invite-codes", handlers.AdminListInviteCodes)
		admin.POST("/invite-codes", handlers.AdminCreateInviteCodes)
		admin.DELETE("/invite-codes/:id", handlers.AdminDeleteInviteCode)
	}

	// ─── Frontend (embedded) ─────────────────────────────────────────────────
	sub, err := fs.Sub(webFS, "web")
	if err != nil {
		log.Fatalf("Failed to sub web FS: %v", err)
	}
	r.NoRoute(func(c *gin.Context) {
		// Serve index.html for SPA routes, static files otherwise
		path := c.Request.URL.Path
		if path == "/" || path == "" {
			data, _ := fs.ReadFile(sub, "index.html")
			c.Data(http.StatusOK, "text/html; charset=utf-8", data)
			return
		}
		// Try to serve static file
		file, err := sub.Open(path[1:]) // strip leading /
		if err != nil {
			// SPA fallback
			data, _ := fs.ReadFile(sub, "index.html")
			c.Data(http.StatusOK, "text/html; charset=utf-8", data)
			return
		}
		file.Close()
		c.FileFromFS(path[1:], http.FS(sub))
	})

	addr := fmt.Sprintf("%s:%d", config.Global.Server.Host, config.Global.Server.Port)
	log.Printf("🎲 LLM-COC server starting on http://%s", addr)
	srv := &http.Server{
		Addr:         addr,
		Handler:      r,
		ReadTimeout:  5 * time.Minute,  // header + body read
		WriteTimeout: 30 * time.Minute, // long AI generation won't be cut off
		IdleTimeout:  90 * time.Second,
	}
	if err := srv.ListenAndServe(); err != nil {
		log.Fatalf("Server failed: %v", err)
	}
}

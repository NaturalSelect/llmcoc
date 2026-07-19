// NOTE: Package main is the entry point for the LLM-COC server application.
// It initializes the database, configures the Gin router, sets up all API
// endpoints, and embeds the frontend web assets.
package main

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/gin-contrib/cors"
	"github.com/gin-contrib/gzip"
	"github.com/gin-gonic/gin"
	"github.com/llmcoc/server/internal/config"
	"github.com/llmcoc/server/internal/handlers"
	"github.com/llmcoc/server/internal/middleware"
	"github.com/llmcoc/server/internal/models"
	"github.com/llmcoc/server/internal/services/agent"
	"github.com/llmcoc/server/internal/services/imagestore"
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

	// Load fixed COC reference documents for the Lawyer agent.
	rbPath := os.Getenv("RULEBOOK_PATH")
	if rbPath == "" {
		rbPath = "COC_kp.md"
	}
	spellPath := os.Getenv("SPELLBOOK_PATH")
	if spellPath == "" {
		spellPath = "COC_spell.md"
	}
	monsterPath := os.Getenv("MONSTERBOOK_PATH")
	if monsterPath == "" {
		monsterPath = "COC_monster.md"
	}

	lawyerCacheEnabled := false
	lawyerCacheHashes := agent.LawyerCacheHashes{}
	if hash, err := rulebook.FileHash(rbPath); err != nil {
		log.Printf("Warning: failed to hash rulebook (%s): %v — Lawyer cache persistence disabled", rbPath, err)
	} else {
		rulebook.GlobalHash = hash
		lawyerCacheHashes.RulebookHash = hash
	}
	if hash, err := rulebook.FileHash(spellPath); err != nil {
		log.Printf("Warning: failed to hash spellbook (%s): %v — Lawyer cache persistence disabled", spellPath, err)
	} else {
		lawyerCacheHashes.SpellbookHash = hash
	}
	if hash, err := rulebook.FileHash(monsterPath); err != nil {
		log.Printf("Warning: failed to hash monsterbook (%s): %v — Lawyer cache persistence disabled", monsterPath, err)
	} else {
		lawyerCacheHashes.MonsterbookHash = hash
	}
	if err := rulebook.Load(rbPath); err != nil {
		log.Printf("Warning: failed to load rulebook (%s): %v — Lawyer agent will have no rule data", rbPath, err)
	} else {
		log.Printf("Rulebook loaded from %s", rbPath)
	}
	if err := rulebook.LoadSpellBook(spellPath); err != nil {
		log.Printf("Warning: failed to load spellbook (%s): %v — Lawyer spell lookup unavailable", spellPath, err)
	} else {
		log.Printf("Spellbook loaded from %s", spellPath)
	}
	if err := rulebook.LoadMonsterBook(monsterPath); err != nil {
		log.Printf("Warning: failed to load monsterbook (%s): %v — Lawyer monster lookup unavailable", monsterPath, err)
	} else {
		log.Printf("Monsterbook loaded from %s", monsterPath)
	}
	if lawyerCacheHashes.RulebookHash != "" && lawyerCacheHashes.SpellbookHash != "" && lawyerCacheHashes.MonsterbookHash != "" {
		lawyerCacheEnabled = true
		agent.LoadLawyerCache(lawyerCacheHashes)
		agent.LoadLawyerCacheStats()
	}
	var stopStatsPersistence func()
	var saveLawyerCacheOnce sync.Once
	saveLawyerCache := func() {
		if !lawyerCacheEnabled {
			return
		}
		saveLawyerCacheOnce.Do(func() {
			log.Printf("Saving Lawyer cache to disk...")
			agent.SaveLawyerCache(lawyerCacheHashes)
		})
	}
	if lawyerCacheEnabled {
		stopStatsPersistence = agent.StartLawyerCacheStatsPersistence(30 * time.Second)
	}
	defer func() {
		if stopStatsPersistence != nil {
			stopStatsPersistence()
		}
		saveLawyerCache()
	}()
	cleanupCtx, stopImageCleanup := context.WithCancel(context.Background())
	defer stopImageCleanup()
	imagestore.StartCleanup(cleanupCtx, imagestore.DefaultStore(), 14*24*time.Hour, 24*time.Hour)

	// Create Gin engine
	if os.Getenv("GIN_MODE") == "" {
		gin.SetMode(gin.ReleaseMode)
	}
	r := gin.New()
	r.Use(gin.LoggerWithFormatter(func(param gin.LogFormatterParams) string {
		// WARN-level behavior: only keep client/server error request logs.
		if param.StatusCode < http.StatusBadRequest {
			return ""
		}
		return fmt.Sprintf("[GIN-WARN] %s | %3d | %13v | %15s | %-7s %s\n",
			param.TimeStamp.Format("2006/01/02 - 15:04:05"),
			param.StatusCode,
			param.Latency,
			param.ClientIP,
			param.Method,
			param.Path,
		)
	}), gin.Recovery())

	// CORS
	r.Use(cors.New(cors.Config{
		AllowOrigins:     []string{"*"},
		AllowMethods:     []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"},
		AllowHeaders:     []string{"Origin", "Content-Type", "Authorization"},
		ExposeHeaders:    []string{"Content-Length"},
		AllowCredentials: false,
	}))

	// Gzip 压缩前端静态资源与常规 API 响应。
	// NOTE: 必须排除 SSE 流式接口（聊天、模组生成），否则 gzip 缓冲会破坏逐段推流。
	// gin-contrib/gzip 本身也会在请求 Accept 含 text/event-stream 时跳过，但前端 fetch 未显式带该头，
	// 故这里用路径正则显式排除，双保险。
	r.Use(gzip.Gzip(gzip.DefaultCompression, gzip.WithExcludedPathsRegexs([]string{
		`^/api/sessions/[^/]+/chat$`,
		`^/api/scenarios/generate$`,
		`^/api/scenarios/compile-story$`,
	})))

	// ─── API routes ───────────────────────────────────────────────────────────
	api := r.Group("/api")
	api.GET("/images/:hash", handlers.ServeImage)

	// Auth (public)
	auth := api.Group("/auth")
	{
		auth.POST("/register", handlers.Register)
		auth.POST("/login", handlers.Login)
		auth.GET("/me", middleware.AuthRequired(), handlers.Me)
		auth.GET("/settings/public", handlers.PublicSettings)
	}

	// Characters (authenticated)
	chh := handlers.NewCharacterHandlers()
	chars := api.Group("/characters", middleware.AuthRequired(), middleware.BanCheck())
	{
		chars.GET("", handlers.ListCharacters)
		chars.GET("/dead", handlers.ListDeadCharacters)
		chars.GET("/skill-defaults", handlers.GetCharacterSkillDefaults)
		chars.POST("/roll", handlers.RollCharacterDraft)
		chars.POST("/finalize", handlers.FinalizeCharacterDraft)
		chars.POST("", handlers.CreateCharacter)
		chars.POST("/generate", chh.GenerateCharacter)
		chars.GET("/:id", handlers.GetCharacter)
		chars.PUT("/:id", handlers.UpdateCharacter)
		chars.DELETE("/:id", handlers.DeleteCharacter)
		chars.POST("/:id/revive", handlers.ReviveCharacter)
		chars.POST("/:id/regenerate-appearance", chh.RegenerateAppearance)
		chars.POST("/:id/regenerate-backstory", chh.RegenerateBackstory)
		chars.POST("/:id/regenerate-traits", chh.RegenerateTraits)
		chars.DELETE("/:id/dead", handlers.DeleteDeadCharacter)
		chars.GET("/:id/inventory", handlers.GetCharacterInventory)
		chars.POST("/:id/inventory", handlers.AddCharacterInventoryItem)
		chars.DELETE("/:id/inventory/*item", handlers.RemoveCharacterInventoryItem)
		chars.DELETE("/:id/social-relations/:name", handlers.RemoveCharacterSocialRelation)
		chars.DELETE("/:id/assets/:name", handlers.RemoveCharacterAsset)
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
		scenarios.POST("/compile-story", middleware.AdminRequired(), handlers.CompileStoryByUpload)
		scenarios.POST("/upload", middleware.AdminRequired(), handlers.UploadScenario)
		scenarios.DELETE("/:id", middleware.AdminRequired(), handlers.DeleteScenario)
	}

	// Sessions
	sh := handlers.NewSessionHandlers(agent.DefaultRunner{})
	sessions := api.Group("/sessions", middleware.AuthRequired(), middleware.BanCheck())
	{
		sessions.GET("", handlers.ListSessions)
		sessions.GET("/my-history", handlers.ListMyHistorySessions)
		sessions.GET("/my-favorites", handlers.ListMyFavoriteSessions)
		sessions.POST("", handlers.CreateSession)
		sessions.GET("/:id", handlers.GetSession)
		sessions.POST("/:id/join", handlers.JoinSession)
		sessions.POST("/:id/leave", handlers.LeaveSession)
		sessions.POST("/:id/start", handlers.StartSession)
		sessions.POST("/:id/end", handlers.EndSession)
		sessions.POST("/:id/revive", middleware.AdminRequired(), handlers.ReviveSession)
		sessions.POST("/:id/favorite", handlers.FavoriteSession)
		sessions.DELETE("/:id/favorite", handlers.UnfavoriteSession)
		sessions.GET("/:id/messages", handlers.GetMessages)
		sessions.GET("/:id/chat-status", sh.GetChatStatus)
		sessions.POST("/:id/chat", sh.ChatStream)
	}

	// Shop
	shop := api.Group("/shop", middleware.AuthRequired(), middleware.BanCheck())
	{
		shop.GET("/items", handlers.ListShopItems)
		shop.POST("/purchase", handlers.PurchaseItem)
		shop.GET("/transactions", handlers.GetMyTransactions)
	}
	// NOTE: 费率公开接口，无需鉴权，供前端页面初始化时读取
	api.GET("/shop/costs", handlers.GetShopCosts)

	// Admin
	admin := api.Group("/admin", middleware.AuthRequired(), middleware.AdminRequired())
	{
		admin.GET("/users", handlers.AdminListUsers)
		admin.GET("/users/:id/characters", handlers.AdminListUserCharacters)
		admin.GET("/scenarios", handlers.AdminListScenarios)
		admin.GET("/scenarios/:id/generation-log", handlers.AdminGetScenarioGenerationLog)
		admin.POST("/recharge", handlers.AdminRechargeCoins)
		admin.PUT("/users/:id/role", handlers.AdminSetRole)
		admin.GET("/recharges", handlers.AdminGetRechargeHistory)
		admin.POST("/shop/items", handlers.AdminCreateShopItem)
		admin.DELETE("/shop/items/:id", handlers.AdminDeleteShopItem)
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
		// Lawyer cache management
		admin.GET("/cache/stats", handlers.AdminGetCacheStats)
		admin.DELETE("/cache", handlers.AdminClearCache)
		admin.GET("/cache/keys", handlers.AdminListCacheKeys)
		admin.GET("/cache/entry", handlers.AdminGetCacheEntry)
		admin.DELETE("/cache/entry", handlers.AdminDeleteCacheEntry)
		// Ban management
		admin.PUT("/users/:id/ban", handlers.AdminBanUser)
		admin.PUT("/users/:id/unban", handlers.AdminUnbanUser)
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
			// index.html 是 SPA 入口，禁止长缓存，避免用户长期停留在旧版本
			c.Header("Cache-Control", "no-cache")
			data, _ := fs.ReadFile(sub, "index.html")
			c.Data(http.StatusOK, "text/html; charset=utf-8", data)
			return
		}
		// Try to serve static file
		file, err := sub.Open(path[1:]) // strip leading /
		if err != nil {
			// SPA fallback
			c.Header("Cache-Control", "no-cache")
			data, _ := fs.ReadFile(sub, "index.html")
			c.Data(http.StatusOK, "text/html; charset=utf-8", data)
			return
		}
		file.Close()
		// 静态资源（JS/CSS 等）短期缓存，减少更新后用户看到旧版本的时间窗口
		c.Header("Cache-Control", "public, max-age=3600")
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
	signal.Ignore(syscall.SIGPIPE)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT, syscall.SIGQUIT, syscall.SIGHUP)
	defer stop()

	errCh := make(chan error, 1)
	go func() {
		errCh <- srv.ListenAndServe()
	}()

	select {
	case <-ctx.Done():
		log.Printf("Shutdown signal received")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := srv.Shutdown(shutdownCtx); err != nil {
			log.Printf("Server shutdown failed: %v", err)
		}
		if stopStatsPersistence != nil {
			stopStatsPersistence()
		}
		saveLawyerCache()
		if err := <-errCh; err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("Server failed: %v", err)
		}
	case err := <-errCh:
		if stopStatsPersistence != nil {
			stopStatsPersistence()
		}
		saveLawyerCache()
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("Server failed: %v", err)
		}
	}
}

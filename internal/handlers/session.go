package handlers

import (
	"fmt"
	"log"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/llmcoc/server/internal/models"
	"github.com/llmcoc/server/internal/services/agent"
	"github.com/llmcoc/server/internal/services/llm"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
)

// SessionHandlers holds injectable dependencies for session-related handlers.
type SessionHandlers struct {
	Runner agent.AgentRunner
}

// NewSessionHandlers returns a SessionHandlers wired to the given runner.
func NewSessionHandlers(r agent.AgentRunner) *SessionHandlers {
	return &SessionHandlers{Runner: r}
}

type CreateSessionReq struct {
	Name       string `json:"name" binding:"required,max=200"`
	ScenarioID uint   `json:"scenario_id" binding:"required"`
	MaxPlayers int    `json:"max_players"`
	Password   string `json:"password"`
}

type JoinSessionReq struct {
	CharacterCardID uint   `json:"character_card_id" binding:"required"`
	Password        string `json:"password"`
}

func ListSessions(c *gin.Context) {
	var sessions []models.GameSession
	models.DB.
		Preload("Scenario").
		Preload("Creator").
		Preload("Players.User").
		Preload("Players.CharacterCard").
		Where("status IN ?", []string{"lobby", "playing"}).
		Order("created_at DESC").
		Find(&sessions)
	c.JSON(http.StatusOK, sessions)
}

func GetSession(c *gin.Context) {
	id, _ := strconv.ParseUint(c.Param("id"), 10, 64)
	var session models.GameSession
	if err := models.DB.
		Preload("Scenario").
		Preload("Creator").
		Preload("Players.User").
		Preload("Players.CharacterCard").
		First(&session, id).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "房间不存在"})
		return
	}
	c.JSON(http.StatusOK, session)
}

func CreateSession(c *gin.Context) {
	userID := c.GetUint("user_id")
	var req CreateSessionReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Verify scenario exists
	var scenario models.Scenario
	if err := models.DB.First(&scenario, req.ScenarioID).Error; err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "剧本不存在"})
		return
	}

	if req.MaxPlayers == 0 {
		req.MaxPlayers = scenario.MaxPlayers
	}

	var pwHash string
	hasPassword := req.Password != ""
	if hasPassword {
		h, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.MinCost)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "密码加密失败"})
			return
		}
		pwHash = string(h)
	}

	session := models.GameSession{
		Name:        req.Name,
		ScenarioID:  req.ScenarioID,
		Status:      models.SessionStatusLobby,
		MaxPlayers:  req.MaxPlayers,
		Password:    pwHash,
		HasPassword: hasPassword,
		CreatedBy:   userID,
	}

	if err := models.DB.Create(&session).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "创建房间失败"})
		return
	}

	// Inject initial system message
	systemMsg := models.Message{
		SessionID: session.ID,
		Role:      models.MessageRoleSystem,
		Content:   fmt.Sprintf("房间「%s」已创建，等待玩家加入。剧本：%s", session.Name, scenario.Name),
		Username:  "系统",
	}
	models.DB.Create(&systemMsg)

	models.DB.Preload("Scenario").Preload("Creator").First(&session, session.ID)
	c.JSON(http.StatusCreated, session)
}

func JoinSession(c *gin.Context) {
	userID := c.GetUint("user_id")
	sessionID, _ := strconv.ParseUint(c.Param("id"), 10, 64)

	var session models.GameSession
	if err := models.DB.Preload("Players").First(&session, sessionID).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "房间不存在"})
		return
	}
	if session.Status != models.SessionStatusLobby {
		c.JSON(http.StatusBadRequest, gin.H{"error": "房间已开始或已结束"})
		return
	}
	if len(session.Players) >= session.MaxPlayers {
		c.JSON(http.StatusBadRequest, gin.H{"error": "房间已满"})
		return
	}

	// Check already joined
	for _, p := range session.Players {
		if p.UserID == userID {
			c.JSON(http.StatusBadRequest, gin.H{"error": "你已在此房间中"})
			return
		}
	}

	var req JoinSessionReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Password check
	if session.HasPassword {
		if err := bcrypt.CompareHashAndPassword([]byte(session.Password), []byte(req.Password)); err != nil {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "房间密码错误"})
			return
		}
	}

	// Verify character belongs to user
	var card models.CharacterCard
	if err := models.DB.First(&card, req.CharacterCardID).Error; err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "人物卡不存在"})
		return
	}
	if card.UserID != userID {
		c.JSON(http.StatusForbidden, gin.H{"error": "无权使用此人物卡"})
		return
	}

	// Lock check: a character card may only participate in one active session at a time.
	// Query whether this card already appears in any non-ended session.
	var lockedCount int64
	models.DB.Model(&models.SessionPlayer{}).
		Joins("JOIN game_sessions ON game_sessions.id = session_players.session_id").
		Where("session_players.character_card_id = ? AND game_sessions.status != ?",
			req.CharacterCardID, models.SessionStatusEnded).
		Count(&lockedCount)
	if lockedCount > 0 {
		c.JSON(http.StatusConflict, gin.H{"error": "该人物卡正在另一场游戏中使用，副本结束后才能再次使用"})
		return
	}

	player := models.SessionPlayer{
		SessionID:       uint(sessionID),
		UserID:          userID,
		CharacterCardID: req.CharacterCardID,
		JoinedAt:        time.Now(),
	}
	if err := models.DB.Create(&player).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "加入房间失败"})
		return
	}

	// Announce join
	username := c.GetString("username")
	joinMsg := models.Message{
		SessionID: uint(sessionID),
		Role:      models.MessageRoleSystem,
		Content:   fmt.Sprintf("「%s」以调查员「%s」的身份加入了房间。", username, card.Name),
		Username:  "系统",
	}
	models.DB.Create(&joinMsg)

	c.JSON(http.StatusOK, gin.H{"message": "加入成功"})
}

func StartSession(c *gin.Context) {
	userID := c.GetUint("user_id")
	sessionID, _ := strconv.ParseUint(c.Param("id"), 10, 64)

	var session models.GameSession
	if err := models.DB.
		Preload("Scenario").
		Preload("Players.User").
		Preload("Players.CharacterCard").
		First(&session, sessionID).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "房间不存在"})
		return
	}
	if session.CreatedBy != userID {
		c.JSON(http.StatusForbidden, gin.H{"error": "只有房主可以开始游戏"})
		return
	}
	if session.Status != models.SessionStatusLobby {
		c.JSON(http.StatusBadRequest, gin.H{"error": "房间状态不允许开始"})
		return
	}
	if len(session.Players) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "至少需要一名玩家"})
		return
	}

	models.DB.Model(&session).Update("status", models.SessionStatusPlaying)

	// Insert KP system prompt as system message
	systemPrompt := llm.BuildKPSystemPrompt(&session.Scenario, session.Players)
	sysMsg := models.Message{
		SessionID: session.ID,
		Role:      models.MessageRoleSystem,
		Content:   systemPrompt,
		Username:  "系统",
	}
	models.DB.Create(&sysMsg)

	// KP intro message
	intro := session.Scenario.Content.Data.Intro
	if intro == "" {
		intro = "游戏开始。KP将为你们展开这段旅程……"
	}
	introMsg := models.Message{
		SessionID: session.ID,
		Role:      models.MessageRoleAssistant,
		Content:   intro,
		Username:  "KP",
	}
	models.DB.Create(&introMsg)

	c.JSON(http.StatusOK, gin.H{"message": "游戏已开始"})
}

func GetMessages(c *gin.Context) {
	sessionID, _ := strconv.ParseUint(c.Param("id"), 10, 64)
	userID := c.GetUint("user_id")

	// Verify user is in session
	if !isInSession(userID, uint(sessionID)) {
		c.JSON(http.StatusForbidden, gin.H{"error": "你不在此房间中"})
		return
	}

	limitStr := c.DefaultQuery("limit", "100")
	limit, _ := strconv.Atoi(limitStr)
	if limit > 200 {
		limit = 200
	}

	var messages []models.Message
	models.DB.Where("session_id = ? AND role != ?", sessionID, models.MessageRoleSystem).
		Order("created_at ASC").
		Limit(limit).
		Find(&messages)
	c.JSON(http.StatusOK, messages)
}

// ChatStream handles SSE streaming for game chat using the multi-agent pipeline.
//
// Multi-player turn flow:
//  1. Each player submits their action; it is saved to DB and recorded in SessionTurnAction.
//  2. If not all players have acted yet, the handler sends a "waiting" SSE event and returns.
//     The player's frontend then polls /messages to pick up the KP response when it arrives.
//  3. Once the last player submits, all pending actions are collected and the agent pipeline
//     runs once, producing a single KP response for the entire round.
func (h *SessionHandlers) ChatStream(c *gin.Context) {
	sessionID, _ := strconv.ParseUint(c.Param("id"), 10, 64)
	userID := c.GetUint("user_id")
	username := c.GetString("username")

	var session models.GameSession
	if err := models.DB.
		Preload("Scenario").
		Preload("Players.User").
		Preload("Players.CharacterCard").
		First(&session, sessionID).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "房间不存在"})
		return
	}
	if session.Status != models.SessionStatusPlaying {
		c.JSON(http.StatusBadRequest, gin.H{"error": "游戏尚未开始"})
		return
	}
	if !isInSession(userID, uint(sessionID)) {
		c.JSON(http.StatusForbidden, gin.H{"error": "你不在此房间中"})
		return
	}

	content := c.PostForm("content")
	if content == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "消息内容不能为空"})
		return
	}
	if len(content) > 2000 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "消息过长（最多2000字）"})
		return
	}

	log.Printf("[chat] session=%d user=%q content_len=%d round=%d",
		sessionID, username, len([]rune(content)), session.TurnRound)

	// Save user message to DB.
	userMsg := models.Message{
		SessionID: uint(sessionID),
		UserID:    &userID,
		Role:      models.MessageRoleUser,
		Content:   content,
		Username:  username,
	}
	models.DB.Create(&userMsg)

	// Set SSE headers before any response path (including the "waiting" path).
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Header("X-Accel-Buffering", "no")

	// ── Multi-player turn-collection ────────────────────────────────────────
	playerCount := len(session.Players)

	// Determine whether the sender is a tracked player (vs. creator-only / spectator).
	isTrackedPlayer := false
	for _, p := range session.Players {
		if p.UserID == userID {
			isTrackedPlayer = true
			break
		}
	}

	var pendingActions []agent.PlayerAction

	if playerCount > 1 && isTrackedPlayer {
		// Use a DB transaction so that record + count is atomic, preventing the
		// race where two simultaneous last-submitters both try to run the agent.
		var isLastToSubmit bool
		_ = models.DB.Transaction(func(tx *gorm.DB) error {
			// Idempotent: only insert if this player has not yet acted this round.
			var existing models.SessionTurnAction
			if tx.Where("session_id = ? AND round = ? AND user_id = ?",
				session.ID, session.TurnRound, userID).First(&existing).Error != nil {
				tx.Create(&models.SessionTurnAction{
					SessionID:     session.ID,
					Round:         session.TurnRound,
					UserID:        userID,
					Username:      username,
					ActionSummary: chatTruncate(content, 500),
				})
			}
			var submitted int64
			tx.Model(&models.SessionTurnAction{}).
				Where("session_id = ? AND round = ?", session.ID, session.TurnRound).
				Count(&submitted)
			isLastToSubmit = submitted >= int64(playerCount)
			return nil
		})

		if !isLastToSubmit {
			// Tell the player how many are still pending and let them poll.
			var submitted int64
			models.DB.Model(&models.SessionTurnAction{}).
				Where("session_id = ? AND round = ?", session.ID, session.TurnRound).
				Count(&submitted)
			pending := int64(playerCount) - submitted
			log.Printf("[chat] session=%d user=%q waiting pending=%d/%d",
				sessionID, username, pending, playerCount)
			c.SSEvent("waiting", fmt.Sprintf(`{"pending":%d,"total":%d}`, pending, playerCount))
			c.Writer.Flush()
			c.SSEvent("done", "")
			c.Writer.Flush()
			return
		}

		// Last to submit: load all actions for the KP prompt.
		var turnActions []models.SessionTurnAction
		models.DB.Where("session_id = ? AND round = ?", session.ID, session.TurnRound).
			Order("created_at ASC").
			Find(&turnActions)
		for _, ta := range turnActions {
			pendingActions = append(pendingActions, agent.PlayerAction{
				PlayerName: ta.Username,
				Content:    ta.ActionSummary,
			})
		}
	} else {
		// Single-player or creator/spectator: record action (idempotent) and run immediately.
		var existing models.SessionTurnAction
		if models.DB.Where("session_id = ? AND round = ? AND user_id = ?",
			session.ID, session.TurnRound, userID).First(&existing).Error != nil {
			models.DB.Create(&models.SessionTurnAction{
				SessionID:     session.ID,
				Round:         session.TurnRound,
				UserID:        userID,
				Username:      username,
				ActionSummary: chatTruncate(content, 500),
			})
		}
	}

	// ── Load recent history for agent context ─────────────────────────────────
	var recentMsgs []models.Message
	models.DB.Where("session_id = ? AND role != ?", sessionID, models.MessageRoleSystem).
		Order("created_at DESC").
		Limit(15).
		Find(&recentMsgs)
	// Reverse to chronological order.
	for i, j := 0, len(recentMsgs)-1; i < j; i, j = i+1, j-1 {
		recentMsgs[i], recentMsgs[j] = recentMsgs[j], recentMsgs[i]
	}

	gctx := agent.GameContext{
		Session:        session,
		History:        recentMsgs,
		UserInput:      content,
		UserName:       username,
		PendingActions: pendingActions,
	}

	// ── Run agent pipeline ────────────────────────────────────────────────────
	log.Printf("[chat] session=%d user=%q pipeline start round=%d", sessionID, username, session.TurnRound)
	pipelineStart := time.Now()
	resultCh := h.Runner.RunAsync(c.Request.Context(), gctx)

	// Send periodic thinking events while pipeline runs.
	ticker := time.NewTicker(600 * time.Millisecond)
	var writerStream <-chan string
loop:
	for {
		select {
		case res := <-resultCh:
			ticker.Stop()
			if res.Err != nil {
				log.Printf("[chat] session=%d user=%q pipeline error (%.0fms): %v",
					sessionID, username, float64(time.Since(pipelineStart).Milliseconds()), res.Err)
				c.SSEvent("error", res.Err.Error())
				c.Writer.Flush()
				return
			}
			writerStream = res.Stream
			break loop
		case <-ticker.C:
			c.SSEvent("thinking", "")
			c.Writer.Flush()
		case <-c.Request.Context().Done():
			ticker.Stop()
			return
		}
	}

	// Stream Writer output token by token.
	var fullReply string
	for token := range writerStream {
		fullReply += token
		c.SSEvent("token", token)
		c.Writer.Flush()
	}

	// Persist the full KP reply so polling players can retrieve it.
	if fullReply != "" {
		assistantMsg := models.Message{
			SessionID: uint(sessionID),
			Role:      models.MessageRoleAssistant,
			Content:   fullReply,
			Username:  "KP",
		}
		models.DB.Create(&assistantMsg)
	}

	log.Printf("[chat] session=%d user=%q done tokens=%d elapsed=%.0fms",
		sessionID, username, len([]rune(fullReply)), float64(time.Since(pipelineStart).Milliseconds()))
	c.SSEvent("done", "")
	c.Writer.Flush()
}

// chatTruncate truncates s to at most maxLen runes, appending "…" when trimmed.
func chatTruncate(s string, maxLen int) string {
	runes := []rune(s)
	if len(runes) > maxLen {
		return string(runes[:maxLen]) + "…"
	}
	return s
}

func isInSession(userID, sessionID uint) bool {
	var count int64
	models.DB.Model(&models.SessionPlayer{}).
		Where("session_id = ? AND user_id = ?", sessionID, userID).
		Count(&count)
	// Also allow session creator
	if count == 0 {
		var session models.GameSession
		models.DB.Select("created_by").First(&session, sessionID)
		if session.CreatedBy == userID {
			return true
		}
	}
	return count > 0
}

func EndSession(c *gin.Context) {
	userID := c.GetUint("user_id")
	sessionID, _ := strconv.ParseUint(c.Param("id"), 10, 64)

	var session models.GameSession
	if err := models.DB.
		Preload("Scenario").
		Preload("Players.User").
		Preload("Players.CharacterCard").
		First(&session, sessionID).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "房间不存在"})
		return
	}
	if session.CreatedBy != userID {
		role := c.GetString("role")
		if role != "admin" {
			c.JSON(http.StatusForbidden, gin.H{"error": "只有房主或管理员可以结束游戏"})
			return
		}
	}
	if session.Status == models.SessionStatusEnded {
		c.JSON(http.StatusBadRequest, gin.H{"error": "游戏已结束"})
		return
	}

	models.DB.Model(&session).Update("status", models.SessionStatusEnded)

	// Load recent messages as context for evaluator and growth agents
	var messages []models.Message
	models.DB.Where("session_id = ? AND role != ?", sessionID, models.MessageRoleSystem).
		Order("created_at ASC").
		Limit(150).
		Find(&messages)

	ctx := c.Request.Context()

	// ── Evaluator: score players and suggest rewards ──────────────────────────
	evalResult, err := agent.RunEvaluator(ctx, &session, messages)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "评价失败"})
		return
	}

	// ── Growth: determine skill improvements ─────────────────────────────────
	growthResult, _ := agent.RunGrowth(ctx, &session, messages)

	// Build lookup maps for fast access
	evalByChar := make(map[string]agent.PlayerEvaluation, len(evalResult.Players))
	for _, pe := range evalResult.Players {
		evalByChar[pe.CharacterName] = pe
	}
	growthByChar := make(map[string]agent.CharacterGrowth, len(growthResult.Characters))
	for _, cg := range growthResult.Characters {
		growthByChar[cg.CharacterName] = cg
	}

	// ── DB transaction: coins + skills + evaluation record ────────────────────
	txErr := models.DB.Transaction(func(tx *gorm.DB) error {
		// Distribute coins and apply skill growth for each player
		for i := range session.Players {
			player := &session.Players[i]
			card := &player.CharacterCard

			// Award coins
			if pe, ok := evalByChar[card.Name]; ok {
				award := pe.BaseCoins + pe.BonusCoins
				if award > 0 {
					if err := tx.Model(&models.User{}).
						Where("id = ?", player.UserID).
						Update("coins", gorm.Expr("coins + ?", award)).Error; err != nil {
						return err
					}
				}
			} else {
				// Fallback: award base coins even without an evaluation entry
				if err := tx.Model(&models.User{}).
					Where("id = ?", player.UserID).
					Update("coins", gorm.Expr("coins + ?", 20)).Error; err != nil {
					return err
				}
			}

			// Apply skill growth (capped at 99)
			if cg, ok := growthByChar[card.Name]; ok && len(cg.SkillChanges) > 0 {
				skills := card.Skills.Data
				if skills == nil {
					skills = make(map[string]int)
				}
				for _, sc := range cg.SkillChanges {
					current := skills[sc.Skill]
					newVal := current + sc.Delta
					if newVal > 99 {
						newVal = 99
					}
					skills[sc.Skill] = newVal
				}
				card.Skills.Data = skills
				if err := tx.Save(card).Error; err != nil {
					return err
				}
			}
		}

		// Persist evaluation record (upsert by session_id)
		evalContent := models.EvaluationContent{
			Summary: evalResult.Summary,
		}
		for _, pe := range evalResult.Players {
			evalContent.Players = append(evalContent.Players, models.PlayerEvalContent{
				CharacterName: pe.CharacterName,
				Comment:       pe.Comment,
				Score:         pe.Score,
				BaseCoins:     pe.BaseCoins,
				BonusCoins:    pe.BonusCoins,
			})
		}
		gameEval := models.GameEvaluation{
			SessionID: uint(sessionID),
		}
		gameEval.Content.Data = evalContent
		return tx.
			Where(models.GameEvaluation{SessionID: uint(sessionID)}).
			Assign(models.GameEvaluation{Content: gameEval.Content}).
			FirstOrCreate(&gameEval).Error
	})

	if txErr != nil {
		// Session is already ended; log the error but still return success
		c.JSON(http.StatusOK, gin.H{
			"message":    "游戏已结束（奖励结算失败，请联系管理员）",
			"evaluation": evalResult,
			"growth":     growthResult,
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"message":    "游戏已结束",
		"evaluation": evalResult,
		"growth":     growthResult,
	})
}

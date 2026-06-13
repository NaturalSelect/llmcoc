package handlers

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"sync"
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

// ListMyHistorySessions returns the last 20 ended sessions the current user participated in.
func ListMyHistorySessions(c *gin.Context) {
	userID := c.GetUint("user_id")
	var sessions []models.GameSession
	models.DB.
		Preload("Scenario").
		Preload("Creator").
		Preload("Players.User").
		Preload("Players.CharacterCard").
		Joins("JOIN session_players ON session_players.session_id = game_sessions.id").
		Where("session_players.user_id = ? AND game_sessions.status = ?", userID, models.SessionStatusEnded).
		Order("game_sessions.updated_at DESC").
		Limit(20).
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
	if req.MaxPlayers > 4 {
		req.MaxPlayers = 4
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
		Content:   fmt.Sprintf("房间「%s」已创建,等待玩家加入。剧本:%s", session.Name, scenario.Name),
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
	hotFixChar(&card)

	// Lock check: a character card may only participate in one active session at a time.
	// Query whether this card already appears in any non-ended session.
	var lockedCount int64
	models.DB.Model(&models.SessionPlayer{}).
		Joins("JOIN game_sessions ON game_sessions.id = session_players.session_id").
		Where("session_players.character_card_id = ? AND game_sessions.status != ?",
			req.CharacterCardID, models.SessionStatusEnded).
		Count(&lockedCount)
	if lockedCount > 0 {
		c.JSON(http.StatusConflict, gin.H{"error": "该人物卡正在另一场游戏中使用,副本结束后才能再次使用"})
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

func LeaveSession(c *gin.Context) {
	userID := c.GetUint("user_id")
	sessionID, _ := strconv.ParseUint(c.Param("id"), 10, 64)

	var session models.GameSession
	if err := models.DB.First(&session, sessionID).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "房间不存在"})
		return
	}
	if session.Status != models.SessionStatusLobby {
		c.JSON(http.StatusBadRequest, gin.H{"error": "游戏已开始或已结束,无法退出房间"})
		return
	}

	var player models.SessionPlayer
	if err := models.DB.
		Preload("CharacterCard").
		Where("session_id = ? AND user_id = ?", sessionID, userID).
		First(&player).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			c.JSON(http.StatusBadRequest, gin.H{"error": "你不在此房间中"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "查询房间成员失败"})
		return
	}
	username := c.GetString("username")
	deletedSession := false
	if err := models.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Delete(&player).Error; err != nil {
			return err
		}

		var remain int64
		if err := tx.Model(&models.SessionPlayer{}).
			Where("session_id = ?", sessionID).
			Count(&remain).Error; err != nil {
			return err
		}

		if remain == 0 {
			if err := tx.Where("session_id = ?", sessionID).Delete(&models.Message{}).Error; err != nil {
				return err
			}
			if err := tx.Delete(&models.GameSession{}, uint(sessionID)).Error; err != nil {
				return err
			}
			deletedSession = true
			return nil
		}

		leaveMsg := models.Message{
			SessionID: uint(sessionID),
			Role:      models.MessageRoleSystem,
			Content:   fmt.Sprintf("「%s」退出了房间。", username),
			Username:  "系统",
		}
		return tx.Create(&leaveMsg).Error
	}); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "退出房间失败"})
		return
	}

	if deletedSession {
		c.JSON(http.StatusOK, gin.H{"message": "退出房间成功,房间无人已自动解散"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "退出房间成功"})
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
	intro := session.Scenario.Content.Data.Setting + "\nKP:" + session.Scenario.Content.Data.Intro
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

func ReviveSession(c *gin.Context) {
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
	if c.GetString("role") != "admin" {
		c.JSON(http.StatusForbidden, gin.H{"error": "只有管理员可以复活房间"})
		return
	}
	if session.Status != models.SessionStatusEnded {
		c.JSON(http.StatusBadRequest, gin.H{"error": "只有已结束的房间可以复活"})
		return
	}
	if len(session.Players) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "至少需要一名玩家"})
		return
	}

	username := c.GetString("username")
	if username == "" {
		var user models.User
		if err := models.DB.Select("username").First(&user, userID).Error; err == nil {
			username = user.Username
		}
	}
	if username == "" {
		username = "管理员"
	}

	if err := models.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&session).Update("status", models.SessionStatusPlaying).Error; err != nil {
			return err
		}
		msg := models.Message{
			SessionID: session.ID,
			Role:      models.MessageRoleSystem,
			Content:   fmt.Sprintf("管理员「%s」复活了房间，游戏继续。", username),
			Username:  "系统",
		}
		return tx.Create(&msg).Error
	}); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "复活房间失败"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "房间已复活"})
}

func GetMessages(c *gin.Context) {
	sessionID, _ := strconv.ParseUint(c.Param("id"), 10, 64)
	userID := c.GetUint("user_id")

	var session models.GameSession
	if err := models.DB.Preload("Players").First(&session, sessionID).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "房间不存在"})
		return
	}

	isAdmin := false
	var user models.User
	if err := models.DB.First(&user, userID).Error; err == nil {
		isAdmin = user.Role == models.RoleAdmin
	}

	if session.HasPassword {
		contain := false
		for _, pl := range session.Players {
			if pl.UserID == userID {
				contain = true
				break
			}
		}
		if !contain && !isAdmin {
			var messages []models.Message
			c.JSON(http.StatusOK, messages)
			return
		}
	}

	var messages []models.Message
	models.DB.Where("session_id = ? AND (role != ? OR content LIKE ?)", sessionID, models.MessageRoleSystem, "管理员「%」复活了房间，游戏继续。").
		Order("created_at ASC").
		Find(&messages)
	c.JSON(http.StatusOK, messages)
}

var sessionMutex = sync.Map{}

func getSessionLock(sessionID uint) *sync.Mutex {
	val, _ := sessionMutex.LoadOrStore(sessionID, &sync.Mutex{})
	return val.(*sync.Mutex)
}

func removeSessionLock(sessionID uint) {
	sessionMutex.Delete(sessionID)
}

func activeTurnPlayerIDs(players []models.SessionPlayer) map[uint]bool {
	ids := make(map[uint]bool, len(players))
	for _, p := range players {
		if p.CharacterCard.WoundState == "dead" {
			continue
		}
		ids[p.UserID] = true
	}
	return ids
}

func mapKeys(m map[uint]bool) []uint {
	keys := make([]uint, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}

func lastKPReplyTime(sessionID uint) (time.Time, bool) {
	var lastKP models.Message
	if err := models.DB.Where("session_id = ? AND role = ?", sessionID, models.MessageRoleAssistant).
		Order("created_at DESC").
		First(&lastKP).Error; err != nil {
		return time.Time{}, false
	}
	return lastKP.CreatedAt, true
}

func countSubmittedTurnPlayers(db *gorm.DB, sessionID uint, round int, activePlayerIDs map[uint]bool, cutoff time.Time, hasCutoff bool) int64 {
	ids := mapKeys(activePlayerIDs)
	if len(ids) == 0 {
		return 0
	}
	query := db.Model(&models.SessionTurnAction{}).
		Select("user_id").
		Where("session_id = ? AND round = ? AND user_id IN ?", sessionID, round, ids)
	if hasCutoff {
		query = query.Where("created_at > ?", cutoff)
	}
	var rows []struct{ UserID uint }
	query.Group("user_id").Find(&rows)
	return int64(len(rows))
}

func loadLatestTurnActions(sessionID uint, round int, activePlayerIDs map[uint]bool, cutoff time.Time, hasCutoff bool) []models.SessionTurnAction {
	ids := mapKeys(activePlayerIDs)
	turnActions := make([]models.SessionTurnAction, 0, len(ids))
	for _, id := range ids {
		var ta models.SessionTurnAction
		query := models.DB.Where("session_id = ? AND round = ? AND user_id = ?", sessionID, round, id)
		if hasCutoff {
			query = query.Where("created_at > ?", cutoff)
		}
		if err := query.Order("created_at DESC, id DESC").
			First(&ta).Error; err == nil {
			turnActions = append(turnActions, ta)
		}
	}
	return turnActions
}

// ChatStream handles SSE streaming for game chat using the multi-agent pipeline.
//
// NOTE: This is the core gameplay loop endpoint. It handles receiving player actions,
// coordinating multi-player turns, invoking the agent pipeline, and streaming
// the Keeper's narrative responses back to the clients via Server-Sent Events.
//
// Multi-player turn flow:
//  1. Each non-dead investigator submits their action; it is saved to DB and recorded in SessionTurnAction.
//  2. Dead investigators do not block the round. If revived later, they are counted again from the next round.
//  3. If not all active investigators have acted yet, the handler sends a "waiting" SSE event and returns.
//     The player's frontend then polls /messages to pick up the KP response when it arrives.
//  4. Once the last active investigator submits, all pending actions are collected and the agent pipeline
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

	var user models.User
	models.DB.First(&user, userID)

	// Use character card name as the display name; fall back to account username.
	playerDisplayName := username
	for _, p := range session.Players {
		if p.UserID == userID && p.CharacterCard.Name != "" {
			playerDisplayName = p.CharacterCard.Name
			break
		}
	}
	if session.Status != models.SessionStatusPlaying {
		c.JSON(http.StatusBadRequest, gin.H{"error": "游戏尚未开始"})
		return
	}

	// Spectator check: only creators or joined players can speak.
	isPlayer := false
	for _, p := range session.Players {
		if p.UserID == userID {
			isPlayer = true
			break
		}
	}
	isCreator := session.CreatedBy == userID

	if !isPlayer && !isCreator {
		c.JSON(http.StatusForbidden, gin.H{"error": "观战模式下无法发言"})
		return
	}

	content := c.PostForm("content")
	if content == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "消息内容不能为空"})
		return
	}
	if len(content) > 2000 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "消息过长(最多2000字)"})
		return
	}

	lock := getSessionLock(uint(sessionID))
	lock.Lock()
	defer lock.Unlock()

	if err := models.DB.
		Preload("Scenario").
		Preload("Players.User").
		Preload("Players.CharacterCard").
		First(&session, sessionID).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "房间不存在"})
		return
	}

	log.Printf("[chat] session=%d user=%q content_len=%d round=%d",
		sessionID, username, len([]rune(content)), session.TurnRound)

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
	var turnActions []models.SessionTurnAction

	activePlayerIDs := activeTurnPlayerIDs(session.Players)
	activePlayerCount := len(activePlayerIDs)
	isActiveTurnPlayer := activePlayerIDs[userID]
	actionCutoff, hasActionCutoff := lastKPReplyTime(session.ID)
	if playerCount > 1 {
		if activePlayerCount > 0 && (!isTrackedPlayer || !isActiveTurnPlayer) {
			log.Printf("[chat] session=%d user=%q rejected dead/non-player input while active players remain", sessionID, username)
			c.SSEvent("error", "当前仍有存活调查员，只有非死亡玩家可以提交本轮行动")
			c.Writer.Flush()
			c.SSEvent("done", "")
			c.Writer.Flush()
			return
		}
		if activePlayerCount == 0 && !isTrackedPlayer {
			log.Printf("[chat] session=%d user=%q rejected non-player input after party wipe", sessionID, username)
			c.SSEvent("error", "所有调查员均已死亡时，只有房间内玩家可以推进剧情")
			c.Writer.Flush()
			c.SSEvent("done", "")
			c.Writer.Flush()
			return
		}
	}

	if playerCount > 1 && isTrackedPlayer && isActiveTurnPlayer && activePlayerCount > 1 {
		// Use a DB transaction so that record + count is atomic, preventing the
		// race where two simultaneous last-submitters both try to run the agent.
		var isLastToSubmit bool
		err := models.DB.Transaction(func(tx *gorm.DB) error {
			// Same-round resubmission should overwrite the player's pending action,
			// so only the latest action is persisted with the KP reply.
			var existing models.SessionTurnAction
			query := tx.Where("session_id = ? AND round = ? AND user_id = ?", session.ID, session.TurnRound, userID)
			if hasActionCutoff {
				query = query.Where("created_at > ?", actionCutoff)
			}
			err := query.First(&existing).Error
			if err != nil {
				tx.Create(&models.SessionTurnAction{
					SessionID:     session.ID,
					Round:         session.TurnRound,
					UserID:        userID,
					Username:      playerDisplayName,
					ActionSummary: content,
				})
			} else {
				// Update so the latest action content is used if the player resubmits
				// before the round advances (e.g. agent returned without calling write).
				tx.Model(&existing).Updates(map[string]any{
					"username":       playerDisplayName,
					"action_summary": content,
				})
			}
			submitted := countSubmittedTurnPlayers(tx, session.ID, session.TurnRound, activePlayerIDs, actionCutoff, hasActionCutoff)
			isLastToSubmit = submitted >= int64(activePlayerCount)
			return nil
		})
		if err != nil {
			log.Printf("[chat] session=%d user=%q transaction error: %v", sessionID, username, err)
			return
		}

		if !isLastToSubmit {
			// Tell the player how many are still pending and let them poll.
			submitted := countSubmittedTurnPlayers(models.DB, session.ID, session.TurnRound, activePlayerIDs, actionCutoff, hasActionCutoff)
			pending := int64(activePlayerCount) - submitted
			log.Printf("[chat] session=%d user=%q waiting pending=%d/%d",
				sessionID, username, pending, activePlayerCount)
			c.SSEvent("waiting", fmt.Sprintf(`{"pending":%d,"total":%d}`, pending, activePlayerCount))
			c.Writer.Flush()
			c.SSEvent("done", "")
			c.Writer.Flush()
			return
		}

		// Last to submit: load exactly one latest action per active actor for the KP prompt.
		turnActions = loadLatestTurnActions(session.ID, session.TurnRound, activePlayerIDs, actionCutoff, hasActionCutoff)
		if len(turnActions) < activePlayerCount {
			pending := activePlayerCount - len(turnActions)
			log.Printf("[chat] session=%d user=%q waiting after load pending=%d/%d", sessionID, username, pending, activePlayerCount)
			c.SSEvent("waiting", fmt.Sprintf(`{"pending":%d,"total":%d}`, pending, activePlayerCount))
			c.Writer.Flush()
			c.SSEvent("done", "")
			c.Writer.Flush()
			return
		}
		for _, ta := range turnActions {
			var user models.User
			models.DB.First(&user, ta.UserID)
			pendingActions = append(pendingActions, agent.PlayerAction{
				IsAdmin:    user.Role == models.RoleAdmin,
				PlayerName: ta.Username,
				Content:    ta.ActionSummary,
			})
		}
	} else {
		// Single-player or creator/spectator: keep only the latest action for this round.
		var existing models.SessionTurnAction
		err := models.DB.Where("session_id = ? AND round = ? AND user_id = ?",
			session.ID, session.TurnRound, userID).First(&existing).Error
		if err != nil {
			models.DB.Create(&models.SessionTurnAction{
				SessionID:     session.ID,
				Round:         session.TurnRound,
				UserID:        userID,
				Username:      playerDisplayName,
				ActionSummary: content,
			})
		} else {
			models.DB.Model(&existing).Updates(map[string]any{
				"username":       playerDisplayName,
				"action_summary": content,
			})
		}
	}

	// ── Load recent history for agent context ─────────────────────────────────
	var recentMsgs []models.Message
	models.DB.Where("session_id = ? AND role != ?", sessionID, models.MessageRoleSystem).
		Order("created_at DESC").
		Find(&recentMsgs)
	// Reverse to chronological order.
	for i, j := 0, len(recentMsgs)-1; i < j; i, j = i+1, j-1 {
		recentMsgs[i], recentMsgs[j] = recentMsgs[j], recentMsgs[i]
	}

	gctx := agent.GameContext{
		Session:        session,
		History:        recentMsgs,
		UserInput:      content,
		UserName:       playerDisplayName,
		UserInputAdmin: user.Role == models.RoleAdmin,
		PendingActions: pendingActions,
	}

	// ── Run agent pipeline ────────────────────────────────────────────────────
	log.Printf("[chat] session=%d user=%q pipeline start round=%d", sessionID, username, session.TurnRound)
	pipelineStart := time.Now()

	// Run the synchronous agent pipeline in a goroutine so we can send
	// "thinking" heartbeats while it executes.
	type runResult struct {
		output agent.RunOutput
		err    error
	}
	resultCh := make(chan runResult, 1)
	go func() {
		// NOTE: should running in background be safe since the pipeline should respect context cancellation
		out, err := h.Runner.Run(context.Background(), gctx)
		resultCh <- runResult{output: out, err: err}
	}()

	// Send periodic thinking events while pipeline runs.
	ticker := time.NewTicker(600 * time.Millisecond)
	var output agent.RunOutput
loop:
	for {
		select {
		case res := <-resultCh:
			ticker.Stop()
			if res.err != nil {
				log.Printf("[chat] session=%d user=%q pipeline error (%.0fms): %v",
					sessionID, username, float64(time.Since(pipelineStart).Milliseconds()), res.err)
				c.SSEvent("error", res.err.Error())
				c.Writer.Flush()
				return
			}
			output = res.output
			break loop
		case <-ticker.C:
			c.SSEvent("thinking", "")
			c.Writer.Flush()
		case <-c.Request.Context().Done():
			ticker.Stop()
			// Client disconnected while pipeline is running (e.g. device was blocked
			// behind sessionLock and timed out). The pipeline uses context.Background()
			// so it will still complete; wait for the result and persist it so the
			// message is visible on refresh / next poll.
			res := <-resultCh
			if res.err == nil {
				saveChatMessages(sessionID, userID, playerDisplayName, content, turnActions, res.output)
			}
			return
		}
	}

	// Emit Writer narrative as "token" events (large text on frontend).
	sseChunk := func(eventType, text string) {
		runes := []rune(text)
		for i := 0; i < len(runes); {
			end := i + 4
			if end > len(runes) {
				end = len(runes)
			}
			c.SSEvent(eventType, string(runes[i:end]))
			c.Writer.Flush()
			i = end
		}
	}
	sseChunk("token", output.WriterText)

	// Emit KP narration as "narration" events (small text on frontend).
	if output.KPReply != "" {
		sseChunk("narration", output.KPReply)
	}

	// Persist the full KP reply (writer + narration) so polling players can retrieve it.
	saveChatMessages(sessionID, userID, playerDisplayName, content, turnActions, output)

	fullReply := output.WriterText
	if output.KPReply != "" {
		narration := output.KPReply
		if !strings.HasPrefix(narration, "KP:") {
			narration = "KP:" + narration
		}
		if fullReply != "" {
			fullReply += "\n\n"
		}
		fullReply += narration
	}

	log.Printf("[chat] session=%d user=%q done tokens=%d elapsed=%.0fms",
		sessionID, username, len([]rune(fullReply)), float64(time.Since(pipelineStart).Milliseconds()))
	c.SSEvent("done", "")
	c.Writer.Flush()
}

// saveChatMessages persists the user message(s) and KP reply to the database.
// It is called both on the normal completion path and when the client has
// disconnected mid-stream (so that messages are visible on refresh/poll).
func saveChatMessages(sessionID uint64, userID uint, playerDisplayName, content string,
	turnActions []models.SessionTurnAction, output agent.RunOutput) {
	fullReply := output.WriterText
	// NOTE: only save response to KP history
	if output.KPReply != "" {
		narration := output.KPReply
		if !strings.HasPrefix(narration, "KP:") {
			narration = "KP:" + narration
		}
		if fullReply != "" {
			fullReply += "\n\n"
		}
		fullReply += narration
	}
	if fullReply == "" {
		return
	}
	log.Printf("[chat] session=%d user=%q saving messages content_len=%d reply_len=%d turn_actions=%d",
		sessionID, playerDisplayName, len([]rune(content)), len([]rune(fullReply)), len(turnActions))
	if len(turnActions) > 0 {
		for _, ta := range turnActions {
			uid := ta.UserID
			models.DB.Create(&models.Message{
				SessionID: uint(sessionID),
				UserID:    &uid,
				Role:      models.MessageRoleUser,
				Content:   ta.ActionSummary,
				Username:  ta.Username,
			})
		}
	} else {
		uid := userID
		models.DB.Create(&models.Message{
			SessionID: uint(sessionID),
			UserID:    &uid,
			Role:      models.MessageRoleUser,
			Content:   content,
			Username:  playerDisplayName,
		})
	}
	models.DB.Create(&models.Message{
		SessionID: uint(sessionID),
		Role:      models.MessageRoleAssistant,
		Content:   fullReply,
		Username:  "KP",
	})
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

	// Deduct 200 coins from every player in the session.
	const endSessionCost = 200
	var brokePlayers []string
	for i := range session.Players {
		p := &session.Players[i]
		if p.User.Coins < endSessionCost {
			brokePlayers = append(brokePlayers, p.User.Username)
		}
	}
	if len(brokePlayers) > 0 {
		c.JSON(http.StatusPaymentRequired, gin.H{
			"error":        "金币不足，结束游戏每人需要消耗200金币",
			"insufficient": brokePlayers,
		})
		return
	}
	for i := range session.Players {
		p := &session.Players[i]
		newCoins := p.User.Coins - endSessionCost
		if err := models.DB.Model(&p.User).Update("coins", newCoins).Error; err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "扣费失败: " + p.User.Username})
			return
		}
	}

	models.DB.Model(&session).Update("status", models.SessionStatusEnded)

	// Load recent messages as context for evaluator and growth agents
	var messages []models.Message
	models.DB.Where("session_id = ? AND role != ?", sessionID, models.MessageRoleSystem).
		Order("created_at ASC").
		Limit(150).
		Find(&messages)

	result, txErr := agent.RunEndSession(context.Background(), &session, messages)

	removeSessionLock(session.ID)

	if txErr != nil {
		c.JSON(http.StatusOK, gin.H{
			"message":    "游戏已结束(奖励结算失败,请联系管理员)",
			"evaluation": result.Evaluation,
			"growth":     result.Growth,
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"message":    "游戏已结束",
		"evaluation": result.Evaluation,
		"growth":     result.Growth,
	})
}

// ListMyFavoriteSessions returns the user's favorite sessions
func ListMyFavoriteSessions(c *gin.Context) {
	userID := c.GetUint("user_id")
	var sessions []models.GameSession
	models.DB.
		Joins("JOIN session_favorites ON session_favorites.session_id = game_sessions.id").
		Preload("Scenario").
		Preload("Creator").
		Preload("Players.User").
		Preload("Players.CharacterCard").
		Where("session_favorites.user_id = ? AND game_sessions.status = ?", userID, models.SessionStatusEnded).
		Order("session_favorites.created_at DESC").
		Find(&sessions)
	c.JSON(http.StatusOK, sessions)
}

// FavoriteSession adds a session to the current user's favorites
func FavoriteSession(c *gin.Context) {
	userID := c.GetUint("user_id")
	sessionID, _ := strconv.ParseUint(c.Param("id"), 10, 64)

	var session models.GameSession
	if err := models.DB.First(&session, sessionID).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "房间不存在"})
		return
	}

	// Check if user is a participant in this session
	var player models.SessionPlayer
	if err := models.DB.
		Where("session_id = ? AND user_id = ?", sessionID, userID).
		First(&player).Error; err != nil {
		c.JSON(http.StatusForbidden, gin.H{"error": "只有房间参与者可以收藏此房间"})
		return
	}

	// Create or update favorite entry
	favorite := models.SessionFavorite{
		UserID:    userID,
		SessionID: uint(sessionID),
	}
	result := models.DB.FirstOrCreate(&favorite, models.SessionFavorite{
		UserID:    userID,
		SessionID: uint(sessionID),
	})
	if result.Error != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "收藏失败"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "已收藏"})
}

// UnfavoriteSession removes a session from the current user's favorites
func UnfavoriteSession(c *gin.Context) {
	userID := c.GetUint("user_id")
	sessionID, _ := strconv.ParseUint(c.Param("id"), 10, 64)

	var session models.GameSession
	if err := models.DB.First(&session, sessionID).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "房间不存在"})
		return
	}

	// Check if user is a participant in this session
	var player models.SessionPlayer
	if err := models.DB.
		Where("session_id = ? AND user_id = ?", sessionID, userID).
		First(&player).Error; err != nil {
		c.JSON(http.StatusForbidden, gin.H{"error": "只有房间参与者可以管理此房间"})
		return
	}

	// Delete favorite entry
	if err := models.DB.Delete(&models.SessionFavorite{}, "user_id = ? AND session_id = ?", userID, sessionID).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "取消收藏失败"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "已取消收藏"})
}

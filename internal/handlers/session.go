package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/llmcoc/server/internal/models"
	"github.com/llmcoc/server/internal/services/agent"
	"github.com/llmcoc/server/internal/services/imagestore"
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

type messageResponse struct {
	ID        uint               `json:"id"`
	SessionID uint               `json:"session_id"`
	UserID    *uint              `json:"user_id"`
	Role      models.MessageRole `json:"role"`
	Content   string             `json:"content"`
	Username  string             `json:"username"`
	CreatedAt time.Time          `json:"created_at"`
	Images    []string           `json:"images"`
}

func newMessageResponse(msg models.Message) messageResponse {
	images := extractImageSources(msg.Content)
	if images == nil {
		images = []string{}
	}
	return messageResponse{
		ID:        msg.ID,
		SessionID: msg.SessionID,
		UserID:    msg.UserID,
		Role:      msg.Role,
		Content:   stripInternalImageTags(msg.Content),
		Username:  msg.Username,
		CreatedAt: msg.CreatedAt,
		Images:    images,
	}
}

func newMessageResponses(messages []models.Message) []messageResponse {
	responses := make([]messageResponse, 0, len(messages))
	for _, msg := range messages {
		responses = append(responses, newMessageResponse(msg))
	}
	return responses
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
	// NOTE: 仅活跃且未死亡且HP>0的人物卡允许加入游戏，死亡卡由商城/复活流程处理
	if !checkCardCanJoinSession(&card) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "该人物卡已死亡或无法继续冒险"})
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
			messages := []messageResponse{}
			c.JSON(http.StatusOK, messages)
			return
		}
	}

	var messages []models.Message
	models.DB.Where("session_id = ? AND (role != ? OR content LIKE ?)", sessionID, models.MessageRoleSystem, "管理员「%」复活了房间，游戏继续。").
		Order("created_at ASC").
		Find(&messages)
	c.JSON(http.StatusOK, newMessageResponses(messages))
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

// waitingPayload 是"等待其他玩家"SSE 事件的 JSON 结构。
// submitted_names/pending_names 按房间 Players 顺序排列，前端可直接展示 badges。
type waitingPayload struct {
	Pending        int      `json:"pending"`
	Total          int      `json:"total"`
	SubmittedNames []string `json:"submitted_names"`
	PendingNames   []string `json:"pending_names"`
}

// sessionPlayerDisplayName 返回玩家的显示名：优先角色名（trim 后非空），回退用户名。
// 与前端 currentPlayerDisplayName() 和后端 playerDisplayName 逻辑保持一致。
func sessionPlayerDisplayName(p models.SessionPlayer) string {
	if name := strings.TrimSpace(p.CharacterCard.Name); name != "" {
		return name
	}
	if name := strings.TrimSpace(p.User.Username); name != "" {
		return name
	}
	return "玩家"
}

// buildWaitingSSEPayload 查询本轮已提交行动的玩家集合，按房间 Players 顺序
// 生成已提交/待提交姓名列表。若查询失败则返回 error，由调用方决定是否降级。
func buildWaitingSSEPayload(db *gorm.DB, session models.GameSession, activePlayerIDs map[uint]bool, cutoff time.Time, hasCutoff bool) (waitingPayload, error) {
	ids := mapKeys(activePlayerIDs)
	total := len(ids)
	if total == 0 {
		return waitingPayload{SubmittedNames: []string{}, PendingNames: []string{}}, nil
	}

	q := db.Model(&models.SessionTurnAction{}).
		Select("user_id").
		Where("session_id = ? AND round = ? AND user_id IN ?", session.ID, session.TurnRound, ids)
	if hasCutoff {
		q = q.Where("created_at > ?", cutoff)
	}
	var rows []struct{ UserID uint }
	if err := q.Group("user_id").Find(&rows).Error; err != nil {
		return waitingPayload{}, err
	}

	submittedSet := make(map[uint]bool, len(rows))
	for _, r := range rows {
		submittedSet[r.UserID] = true
	}

	submitted := len(rows)
	pending := total - submitted
	if pending < 0 {
		pending = 0
	}

	// NOTE: 按房间 Players 顺序遍历，保证姓名列表顺序稳定
	submittedNames := make([]string, 0, submitted)
	pendingNames := make([]string, 0, pending)
	for _, p := range session.Players {
		if !activePlayerIDs[p.UserID] {
			continue
		}
		name := sessionPlayerDisplayName(p)
		if submittedSet[p.UserID] {
			submittedNames = append(submittedNames, name)
		} else {
			pendingNames = append(pendingNames, name)
		}
	}

	return waitingPayload{
		Pending:        pending,
		Total:          total,
		SubmittedNames: submittedNames,
		PendingNames:   pendingNames,
	}, nil
}

// sendWaitingSSE 序列化 waitingPayload 并发送 "waiting" SSE 事件。
// 使用 encoding/json 确保特殊字符正确转义，序列化失败时降级为最小安全 JSON。
func sendWaitingSSE(c *gin.Context, payload waitingPayload) {
	data, err := json.Marshal(payload)
	if err != nil {
		// NOTE: 极端异常：降级为最小安全 JSON，保持 pending/total 兼容旧客户端
		data = []byte(fmt.Sprintf(`{"pending":%d,"total":%d,"submitted_names":[],"pending_names":[]}`,
			payload.Pending, payload.Total))
	}
	c.SSEvent("waiting", string(data))
	c.Writer.Flush()
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
	lockReleased := false
	defer func() {
		if !lockReleased {
			lock.Unlock()
		}
	}()

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
			// NOTE: 构建含已提交/待提交姓名的等待载荷，查询失败时降级为计数格式
			wPayload, wErr := buildWaitingSSEPayload(models.DB, session, activePlayerIDs, actionCutoff, hasActionCutoff)
			if wErr != nil {
				log.Printf("[chat] session=%d user=%q waiting payload error: %v", sessionID, username, wErr)
				submitted := countSubmittedTurnPlayers(models.DB, session.ID, session.TurnRound, activePlayerIDs, actionCutoff, hasActionCutoff)
				wPayload = waitingPayload{
					Pending:        activePlayerCount - int(submitted),
					Total:          activePlayerCount,
					SubmittedNames: []string{},
					PendingNames:   []string{},
				}
			}
			log.Printf("[chat] session=%d user=%q waiting pending=%d/%d",
				sessionID, username, wPayload.Pending, wPayload.Total)
			sendWaitingSSE(c, wPayload)
			c.SSEvent("done", "")
			c.Writer.Flush()
			return
		}

		// Last to submit: load exactly one latest action per active actor for the KP prompt.
		turnActions = loadLatestTurnActions(session.ID, session.TurnRound, activePlayerIDs, actionCutoff, hasActionCutoff)
		if len(turnActions) < activePlayerCount {
			// NOTE: 构建含已提交/待提交姓名的等待载荷，查询失败时降级为计数格式
			wPayload, wErr := buildWaitingSSEPayload(models.DB, session, activePlayerIDs, actionCutoff, hasActionCutoff)
			if wErr != nil {
				log.Printf("[chat] session=%d user=%q waiting payload error: %v", sessionID, username, wErr)
				wPayload = waitingPayload{
					Pending:        activePlayerCount - len(turnActions),
					Total:          activePlayerCount,
					SubmittedNames: []string{},
					PendingNames:   []string{},
				}
			}
			log.Printf("[chat] session=%d user=%q waiting after load pending=%d/%d", sessionID, username, wPayload.Pending, wPayload.Total)
			sendWaitingSSE(c, wPayload)
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
	stripMessageImageDataURLTags(recentMsgs)
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
	sendProgress := func(text string) {
		text = strings.TrimSpace(text)
		if text == "" {
			return
		}
		c.SSEvent("progress", text)
		c.Writer.Flush()
	}
	sendProgress("已收到行动,正在整理本轮信息")

	// Run the synchronous agent pipeline in a goroutine so we can send
	// "thinking" heartbeats while it executes.
	type runResult struct {
		output agent.RunOutput
		err    error
	}
	resultCh := make(chan runResult, 1)
	progressCh := make(chan string, 256)
	gctx.Progress = func(text string) {
		text = strings.TrimSpace(text)
		if text == "" {
			return
		}
		select {
		case progressCh <- text:
		default:
		}
	}
	go func() {
		// NOTE: 后台运行由流水线内部检查context取消,断线后仍可完成主流程落库。
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
			// 排空 progressCh 中剩余的消息，避免丢失
		drainLoop:
			for {
				select {
				case progress := <-progressCh:
					sendProgress(progress)
				default:
					break drainLoop
				}
			}
			break loop
		case progress := <-progressCh:
			sendProgress(progress)
		case <-ticker.C:
			c.SSEvent("thinking", "")
			c.Writer.Flush()
		case <-c.Request.Context().Done():
			ticker.Stop()
			// 客户端断开后仍等待KP主流程结束,保证刷新/轮询能看到主流程结果。
			res := <-resultCh
			if res.err == nil {
				assistantMsg, err := saveChatMessages(sessionID, userID, playerDisplayName, content, turnActions, res.output)
				if err != nil {
					log.Printf("[chat] session=%d user=%q save after disconnect error: %v", sessionID, username, err)
				} else if assistantMsg != nil {
					// NOTE: 消息已保存，后续 painter/writer 为后台 goroutine 不需要 lock，提前释放。
					lock.Unlock()
					lockReleased = true
					h.startWriterJob(assistantMsg.ID, gctx, res.output, nil)
					if len(res.output.ImagePrompts) > 0 {
						h.startPainterJob(assistantMsg.ID, gctx, res.output.ImagePrompts[0], nil)
					}
				}
			}
			return
		}
	}

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

	assistantMsg, err := saveChatMessages(sessionID, userID, playerDisplayName, content, turnActions, output)
	if err != nil {
		log.Printf("[chat] session=%d user=%q save error: %v", sessionID, username, err)
		c.SSEvent("error", "保存消息失败")
		c.Writer.Flush()
		return
	}
	sendProgress("KP主流程已保存")

	// KP是游戏主流程,先推给前端并解除输入锁。
	if output.KPReply != "" {
		sseChunk("narration", output.KPReply)
	}
	if assistantMsg != nil {
		c.SSEvent("kp_done", gin.H{
			"message_id":  assistantMsg.ID,
			"created_at":  assistantMsg.CreatedAt,
			"has_writer":  output.WriterText != "" || output.WriterDirection != "",
			"writer_done": output.WriterText != "" && output.WriterDirection == "",
		})
	} else {
		c.SSEvent("kp_done", gin.H{})
	}
	c.Writer.Flush()

	// NOTE: kp_done 已发送，前端已解锁输入。后续 painter/writer 为后台 goroutine，
	// 不需要 session lock 保护（消息行级乐观锁已保护并发更新），立即释放 lock。
	lock.Unlock()
	lockReleased = true

	var writerCh <-chan writerJobResult
	if assistantMsg != nil {
		writerClientDone := make(chan struct{})
		defer close(writerClientDone)
		writerCh = h.startWriterJob(assistantMsg.ID, gctx, output, writerClientDone)
	}
	var painterCh <-chan painterJobResult
	if len(output.ImagePrompts) > 0 {
		imageRequest := output.ImagePrompts[0]
		log.Printf("[chat] session=%d user=%q painter queued prompt_len=%d prompt=%q", sessionID, username, len([]rune(imageRequest.Prompt)), chatTruncate(imageRequest.Prompt, 200))
		painterClientDone := make(chan struct{})
		defer close(painterClientDone)
		assistantMessageID := uint(0)
		if assistantMsg != nil {
			assistantMessageID = assistantMsg.ID
		}
		painterCh = h.startPainterJob(assistantMessageID, gctx, imageRequest, painterClientDone)
	}

	streamedWriter := output.WriterText
	if streamedWriter != "" {
		sendProgress("叙事正文生成中")
		sseChunk("token", streamedWriter)
	}
	if writerCh != nil || painterCh != nil {
		if writerCh != nil {
			sendProgress("叙事正文生成中")
		} else {
			sendProgress("场景图像生成中")
		}
		for writerCh != nil || painterCh != nil {
			select {
			case wr, ok := <-writerCh:
				if !ok {
					writerCh = nil
					continue
				}
				if wr.token != "" {
					streamedWriter += wr.token
					c.SSEvent("token", wr.token)
					c.Writer.Flush()
				}
				if !wr.done {
					continue
				}
				if wr.text != "" {
					streamedWriter = wr.text
				}
				if wr.err != nil {
					log.Printf("[chat] session=%d user=%q writer async error: %v", sessionID, username, wr.err)
				}
				writerCh = nil
			case pr, ok := <-painterCh:
				if !ok {
					painterCh = nil
					continue
				}
				if pr.err != nil {
					log.Printf("[chat] session=%d user=%q painter async error: %v", sessionID, username, pr.err)
					painterCh = nil
					continue
				}
				if pr.dataURL != "" {
					c.SSEvent("image", pr.dataURL)
					c.Writer.Flush()
				}
				painterCh = nil
			case <-c.Request.Context().Done():
				return
			}
		}
	}

	fullReply := buildAssistantContent(streamedWriter, output.KPReply)
	log.Printf("[chat] session=%d user=%q done tokens=%d elapsed=%.0fms",
		sessionID, username, len([]rune(fullReply)), float64(time.Since(pipelineStart).Milliseconds()))
	c.SSEvent("done", "")
	c.Writer.Flush()
}

type writerJobResult struct {
	token string
	text  string
	done  bool
	err   error
}

type painterJobResult struct {
	dataURL string
	err     error
}

const writerPendingTag = "<writer_pending>true</writer_pending>"

const imageDataURLStartTag = "<image_data_url>"
const imageDataURLTagOpenPrefix = "<image_data_url"
const imageDataURLEndTag = "</image_data_url>"
const imageRefTagName = "image_ref"

var imageRefTagPattern = regexp.MustCompile(`(?is)<image_ref\b[^>]*(?:/>|>\s*</image_ref>)`)
var imageRefAttrPattern = regexp.MustCompile(`(?is)\b(hash|mime)\s*=\s*(?:"([^"]*)"|'([^']*)')`)

const assistantMessageUpdateRetries = 3

const painterJobTimeout = 600 * time.Second

func (h *SessionHandlers) startWriterJob(messageID uint, gctx agent.GameContext, output agent.RunOutput, clientDone <-chan struct{}) <-chan writerJobResult {
	direction := strings.TrimSpace(output.WriterDirection)
	if messageID == 0 || direction == "" || strings.TrimSpace(output.WriterText) != "" {
		return nil
	}
	var ch chan writerJobResult
	if clientDone != nil {
		ch = make(chan writerJobResult, 128)
	}
	go func() {
		if ch != nil {
			defer close(ch)
		}
		send := func(result writerJobResult) {
			if ch == nil {
				return
			}
			select {
			case ch <- result:
			case <-clientDone:
			}
		}
		text, err := h.Runner.RunWriterStream(context.Background(), gctx, direction, func(token string) {
			if token == "" {
				return
			}
			send(writerJobResult{token: token})
		})
		// Writer结束时即使没有生成正文,也要重写消息以清除刷新恢复用的pending标记。
		dbErr := updateAssistantMessageWriter(messageID, output.KPReply, text)
		if err == nil {
			err = dbErr
		} else if dbErr != nil {
			log.Printf("[chat] writer message update after stream error failed: %v", dbErr)
		}
		send(writerJobResult{text: text, done: true, err: err})
	}()
	return ch
}

func (h *SessionHandlers) startPainterJob(messageID uint, gctx agent.GameContext, request agent.ImagePromptRequest, clientDone <-chan struct{}) <-chan painterJobResult {
	request.Prompt = strings.TrimSpace(request.Prompt)
	prompt := request.Prompt
	if prompt == "" {
		return nil
	}
	var ch chan painterJobResult
	if clientDone != nil {
		ch = make(chan painterJobResult, 1)
	}
	go func() {
		if ch != nil {
			defer close(ch)
		}
		ctx, cancel := context.WithTimeout(context.Background(), painterJobTimeout)
		defer cancel()
		start := time.Now()
		log.Printf("[chat] session=%d painter async start prompt_len=%d prompt=%q", gctx.Session.ID, len([]rune(prompt)), chatTruncate(prompt, 200))
		if clientDone != nil && messageID == 0 {
			go func() {
				select {
				case <-clientDone:
					cancel()
				case <-ctx.Done():
				}
			}()
		}
		dataURL, err := h.Runner.RunPainter(ctx, gctx, request)
		if err == nil {
			dataURL = strings.TrimSpace(dataURL)
			if dataURL == "" || !strings.HasPrefix(dataURL, "data:image/") {
				err = fmt.Errorf("painter returned invalid image data")
			}
		}
		if err == nil && messageID != 0 {
			if dbErr := appendAssistantMessageImage(messageID, dataURL); dbErr != nil {
				err = fmt.Errorf("persist painter image: %w", dbErr)
			}
		}
		if err != nil {
			log.Printf("[chat] session=%d painter async finished error elapsed=%.0fms err=%v", gctx.Session.ID, float64(time.Since(start).Microseconds())/1000, err)
		} else {
			log.Printf("[chat] session=%d painter async finished success elapsed=%.0fms", gctx.Session.ID, float64(time.Since(start).Microseconds())/1000)
		}
		result := painterJobResult{dataURL: dataURL, err: err}
		if ch == nil {
			return
		}
		select {
		case ch <- result:
		case <-clientDone:
		}
	}()
	return ch
}

func updateAssistantMessageWriter(messageID uint, kpReply, writerText string) error {
	baseContent := buildAssistantContent(writerText, kpReply)
	for attempt := 0; attempt < assistantMessageUpdateRetries; attempt++ {
		var msg models.Message
		if err := models.DB.Select("id", "content").First(&msg, messageID).Error; err != nil {
			return err
		}
		content := appendInternalImageTags(baseContent, extractImageRefs(msg.Content), extractImageDataURLs(msg.Content))
		if strings.TrimSpace(content) == "" {
			return nil
		}
		if content == msg.Content {
			return nil
		}
		res := models.DB.Model(&models.Message{}).
			Where("id = ? AND content = ?", messageID, msg.Content).
			Update("content", content)
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected > 0 {
			return nil
		}
	}
	return fmt.Errorf("assistant message %d content changed while updating writer", messageID)
}

func appendAssistantMessageImage(messageID uint, dataURL string) error {
	dataURL = strings.TrimSpace(dataURL)
	if messageID == 0 {
		return nil
	}
	ref, err := imagestore.DefaultStore().SaveDataURL(dataURL)
	if err != nil {
		return err
	}
	imageRef := storedImageRef{Hash: ref.Hash, MIME: ref.MIME}
	for attempt := 0; attempt < assistantMessageUpdateRetries; attempt++ {
		var msg models.Message
		if err := models.DB.Select("id", "content").First(&msg, messageID).Error; err != nil {
			return err
		}
		if imageRefExists(msg.Content, imageRef.Hash) {
			return nil
		}
		content := appendInternalImageTags(msg.Content, []storedImageRef{imageRef}, nil)
		if content == msg.Content {
			return nil
		}
		res := models.DB.Model(&models.Message{}).
			Where("id = ? AND content = ?", messageID, msg.Content).
			Update("content", content)
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected > 0 {
			return nil
		}
	}
	return fmt.Errorf("assistant message %d content changed while appending image", messageID)
}

func isValidImageDataURL(dataURL string) bool {
	return strings.HasPrefix(strings.TrimSpace(dataURL), "data:image/")
}

type storedImageRef struct {
	Hash string
	MIME string
}

func imageRefExists(content, hash string) bool {
	hash = strings.ToLower(strings.TrimSpace(hash))
	if !imagestore.ValidHash(hash) {
		return false
	}
	for _, ref := range extractImageRefs(content) {
		if strings.EqualFold(ref.Hash, hash) {
			return true
		}
	}
	return false
}

func extractImageSources(content string) []string {
	var images []string
	seen := make(map[string]bool)
	for _, ref := range extractImageRefs(content) {
		url := imagestore.URL(ref.Hash)
		if !seen[url] {
			images = append(images, url)
			seen[url] = true
		}
	}
	for _, dataURL := range extractImageDataURLs(content) {
		if !seen[dataURL] {
			images = append(images, dataURL)
			seen[dataURL] = true
		}
	}
	return images
}

func extractImageRefs(content string) []storedImageRef {
	var refs []storedImageRef
	seen := make(map[string]bool)
	for _, tag := range imageRefTagPattern.FindAllString(content, -1) {
		attrs := parseImageRefAttrs(tag)
		hash := strings.ToLower(strings.TrimSpace(attrs["hash"]))
		if !imagestore.ValidHash(hash) || seen[hash] {
			continue
		}
		mime := strings.TrimSpace(attrs["mime"])
		if normalized, _, ok := imagestore.NormalizeMIME(mime); ok {
			mime = normalized
		} else if mime != "" {
			continue
		}
		refs = append(refs, storedImageRef{Hash: hash, MIME: mime})
		seen[hash] = true
	}
	return refs
}

func parseImageRefAttrs(tag string) map[string]string {
	attrs := make(map[string]string)
	for _, match := range imageRefAttrPattern.FindAllStringSubmatch(tag, -1) {
		if len(match) < 4 {
			continue
		}
		value := match[2]
		if value == "" {
			value = match[3]
		}
		attrs[strings.ToLower(match[1])] = value
	}
	return attrs
}

func extractImageDataURLs(content string) []string {
	var urls []string
	rest := content
	for {
		start := strings.Index(rest, imageDataURLTagOpenPrefix)
		if start < 0 {
			return urls
		}
		tagEnd := strings.Index(rest[start:], ">")
		if tagEnd < 0 {
			return urls
		}
		afterStart := rest[start+tagEnd+1:]
		end := strings.Index(afterStart, imageDataURLEndTag)
		if end < 0 {
			return urls
		}
		dataURL := strings.TrimSpace(afterStart[:end])
		if isValidImageDataURL(dataURL) {
			urls = append(urls, dataURL)
		}
		rest = afterStart[end+len(imageDataURLEndTag):]
	}
}

func stripInternalImageTags(content string) string {
	content = stripImageDataURLTags(content)
	if strings.Contains(strings.ToLower(content), "<"+imageRefTagName) {
		content = imageRefTagPattern.ReplaceAllString(content, "")
	}
	return strings.TrimSpace(content)
}

func stripImageDataURLTags(content string) string {
	if !strings.Contains(content, imageDataURLTagOpenPrefix) {
		return strings.TrimSpace(content)
	}
	var b strings.Builder
	rest := content
	for {
		start := strings.Index(rest, imageDataURLTagOpenPrefix)
		if start < 0 {
			b.WriteString(rest)
			break
		}
		b.WriteString(rest[:start])
		tagEnd := strings.Index(rest[start:], ">")
		if tagEnd < 0 {
			break
		}
		afterStart := rest[start+tagEnd+1:]
		end := strings.Index(afterStart, imageDataURLEndTag)
		if end < 0 {
			break
		}
		rest = afterStart[end+len(imageDataURLEndTag):]
	}
	return strings.TrimSpace(b.String())
}

func stripMessageImageDataURLTags(messages []models.Message) {
	for i := range messages {
		messages[i].Content = stripInternalImageTags(messages[i].Content)
	}
}

func appendInternalImageTags(content string, refs []storedImageRef, dataURLs []string) string {
	content = strings.TrimSpace(content)
	seenRefs := make(map[string]bool)
	for _, ref := range extractImageRefs(content) {
		seenRefs[strings.ToLower(ref.Hash)] = true
	}
	for _, ref := range refs {
		hash := strings.ToLower(strings.TrimSpace(ref.Hash))
		if !imagestore.ValidHash(hash) || seenRefs[hash] {
			continue
		}
		mime, _, ok := imagestore.NormalizeMIME(ref.MIME)
		if !ok {
			if stored, err := imagestore.DefaultStore().Resolve(hash); err == nil {
				mime = stored.MIME
				ok = true
			}
		}
		if content != "" {
			content += "\n"
		}
		if ok {
			content += fmt.Sprintf(`<image_ref hash="%s" mime="%s"/>`, hash, mime)
		} else {
			content += fmt.Sprintf(`<image_ref hash="%s"/>`, hash)
		}
		seenRefs[hash] = true
	}
	seen := make(map[string]bool)
	for _, dataURL := range extractImageDataURLs(content) {
		seen[dataURL] = true
	}
	for _, dataURL := range dataURLs {
		dataURL = strings.TrimSpace(dataURL)
		if !isValidImageDataURL(dataURL) || seen[dataURL] {
			continue
		}
		if ref, err := imagestore.DefaultStore().SaveDataURL(dataURL); err == nil {
			legacyRef := storedImageRef{Hash: ref.Hash, MIME: ref.MIME}
			if !seenRefs[legacyRef.Hash] {
				if content != "" {
					content += "\n"
				}
				content += fmt.Sprintf(`<image_ref hash="%s" mime="%s"/>`, legacyRef.Hash, legacyRef.MIME)
				seenRefs[legacyRef.Hash] = true
			}
			seen[dataURL] = true
			continue
		}
		if content != "" {
			content += "\n"
		}
		content += imageDataURLStartTag + dataURL + imageDataURLEndTag
		seen[dataURL] = true
	}
	return content
}

func buildAssistantContent(writerText, kpReply string) string {
	fullReply := strings.TrimSpace(writerText)
	if strings.TrimSpace(kpReply) == "" {
		return fullReply
	}
	narration := strings.TrimSpace(kpReply)
	narration = strings.TrimPrefix(narration, "KP:")
	narration = strings.TrimPrefix(narration, "KP：")
	narration = "KP:" + strings.TrimSpace(narration)
	if fullReply != "" {
		fullReply += "\n\n"
	}
	return fullReply + narration
}

func appendWriterPendingMarker(content string, pending bool) string {
	if !pending || strings.TrimSpace(content) == "" {
		return content
	}
	return strings.TrimSpace(content) + "\n" + writerPendingTag
}

// saveChatMessages 保存玩家消息和KP主流程回复,并返回可被Writer后续补写的消息。
func saveChatMessages(sessionID uint64, userID uint, playerDisplayName, content string,
	turnActions []models.SessionTurnAction, output agent.RunOutput) (*models.Message, error) {
	fullReply := buildAssistantContent(output.WriterText, output.KPReply)
	if fullReply == "" {
		return nil, nil
	}
	fullReply = appendWriterPendingMarker(fullReply,
		strings.TrimSpace(output.WriterDirection) != "" && strings.TrimSpace(output.WriterText) == "")
	log.Printf("[chat] session=%d user=%q saving messages content_len=%d reply_len=%d turn_actions=%d",
		sessionID, playerDisplayName, len([]rune(content)), len([]rune(fullReply)), len(turnActions))
	var assistantMsg models.Message
	err := models.DB.Transaction(func(tx *gorm.DB) error {
		if len(turnActions) > 0 {
			for _, ta := range turnActions {
				uid := ta.UserID
				if err := tx.Create(&models.Message{
					SessionID: uint(sessionID),
					UserID:    &uid,
					Role:      models.MessageRoleUser,
					Content:   ta.ActionSummary,
					Username:  ta.Username,
				}).Error; err != nil {
					return err
				}
			}
		} else {
			uid := userID
			if err := tx.Create(&models.Message{
				SessionID: uint(sessionID),
				UserID:    &uid,
				Role:      models.MessageRoleUser,
				Content:   content,
				Username:  playerDisplayName,
			}).Error; err != nil {
				return err
			}
		}
		assistantMsg = models.Message{
			SessionID: uint(sessionID),
			Role:      models.MessageRoleAssistant,
			Content:   fullReply,
			Username:  "KP",
		}
		return tx.Create(&assistantMsg).Error
	})
	if err != nil {
		return nil, err
	}
	return &assistantMsg, nil
}

// chatTruncate truncates s to at most maxLen runes, appending "…" when trimmed.
func chatTruncate(s string, maxLen int) string {
	runes := []rune(s)
	if len(runes) > maxLen {
		return string(runes[:maxLen]) + "…"
	}
	return s
}

// NOTE: checkCardCanJoinSession 校验人物卡是否满足加入游戏的生存条件：
// is_active=true 且 wound_state!="dead" 且 HP>0
func checkCardCanJoinSession(card *models.CharacterCard) bool {
	return card.IsActive && card.WoundState != "dead" && card.Stats.Data.HP > 0
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

	// NOTE: 结束游戏每人扣费，费率通过 SiteSetting 可配
	endSessionCost := siteSettingInt("end_session_cost", 200)
	var brokePlayers []string
	for i := range session.Players {
		p := &session.Players[i]
		if p.User.Coins < endSessionCost {
			brokePlayers = append(brokePlayers, p.User.Username)
		}
	}
	if len(brokePlayers) > 0 {
		c.JSON(http.StatusPaymentRequired, gin.H{
			"error":        fmt.Sprintf("金币不足，结束游戏每人需要消耗%d金币", endSessionCost),
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
	stripMessageImageDataURLTags(messages)

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

package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/llmcoc/server/internal/logging"
	"github.com/llmcoc/server/internal/models"
	"github.com/llmcoc/server/internal/services/agent"
	"github.com/llmcoc/server/internal/services/imagestore"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
)

var chatLog = logging.For("chat")

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
	EnableNSFW bool   `json:"enable_nsfw"`
}

type JoinSessionReq struct {
	CharacterCardID uint   `json:"character_card_id" binding:"required"`
	Password        string `json:"password"`
}

type messageResponse struct {
	ID                uint               `json:"id"`
	SessionID         uint               `json:"session_id"`
	UserID            *uint              `json:"user_id"`
	Role              models.MessageRole `json:"role"`
	Content           string             `json:"content"`
	Username          string             `json:"username"`
	CreatedAt         time.Time          `json:"created_at"`
	Images            []string           `json:"images"`
	DirectorElapsedMs *int64             `json:"director_elapsed_ms"`
	DirectorSteps     *int               `json:"director_steps"`
}

func newMessageResponse(msg models.Message) messageResponse {
	images := extractImageSources(msg.Content)
	if images == nil {
		images = []string{}
	}
	return messageResponse{
		ID:                msg.ID,
		SessionID:         msg.SessionID,
		UserID:            msg.UserID,
		Role:              msg.Role,
		Content:           stripInternalImageTags(msg.Content),
		Username:          msg.Username,
		CreatedAt:         msg.CreatedAt,
		Images:            images,
		DirectorElapsedMs: msg.DirectorElapsedMs,
		DirectorSteps:     msg.DirectorSteps,
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
	page, pageSize, ok := parseAdminPagination(c)
	if !ok {
		return
	}

	var total int64
	if err := models.DB.Model(&models.GameSession{}).
		Where("status IN ?", []string{"lobby", "playing"}).
		Count(&total).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "查询活跃房间总数失败"})
		return
	}

	sessions := make([]models.GameSession, 0)
	if err := models.DB.
		Preload("Scenario").
		Preload("Creator").
		Preload("Players.User").
		Preload("Players.CharacterCard").
		Where("status IN ?", []string{"lobby", "playing"}).
		Order("created_at DESC").
		Limit(pageSize).
		Offset((page - 1) * pageSize).
		Find(&sessions).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "查询活跃房间列表失败"})
		return
	}

	c.JSON(http.StatusOK, newPaginatedResponse(sessions, page, pageSize, total))
}

// ListMyHistorySessions returns the ended sessions the current user participated in, paginated.
func ListMyHistorySessions(c *gin.Context) {
	page, pageSize, ok := parseAdminPagination(c)
	if !ok {
		return
	}
	userID := c.GetUint("user_id")

	var total int64
	if err := models.DB.Model(&models.GameSession{}).
		Joins("JOIN session_players ON session_players.session_id = game_sessions.id").
		Where("session_players.user_id = ? AND game_sessions.status = ?", userID, models.SessionStatusEnded).
		Count(&total).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "查询历史房间总数失败"})
		return
	}

	sessions := make([]models.GameSession, 0)
	if err := models.DB.
		Preload("Scenario").
		Preload("Creator").
		Preload("Players.User").
		Preload("Players.CharacterCard").
		Joins("JOIN session_players ON session_players.session_id = game_sessions.id").
		Where("session_players.user_id = ? AND game_sessions.status = ?", userID, models.SessionStatusEnded).
		Order("game_sessions.updated_at DESC").
		Limit(pageSize).
		Offset((page - 1) * pageSize).
		Find(&sessions).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "查询历史房间列表失败"})
		return
	}

	c.JSON(http.StatusOK, newPaginatedResponse(sessions, page, pageSize, total))
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

	// NOTE: 全局 NSFW 开关禁用时忽略客户端传入的 enable_nsfw，防止绕过前端隐藏的选项直接调 API。
	if models.GetSiteSetting("allow_nsfw", "true") != "true" {
		req.EnableNSFW = false
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
		EnableNSFW:  req.EnableNSFW,
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
	if isCardLockedInSession(models.DB, req.CharacterCardID) {
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
		Order("created_at ASC, id ASC").
		Find(&messages)
	c.JSON(http.StatusOK, newMessageResponses(messages))
}

var sessionMutex = sync.Map{}
var sessionProcessing = sync.Map{}

type sessionProcessingState struct {
	StartedAt time.Time
}

func getSessionLock(sessionID uint) *sync.Mutex {
	val, _ := sessionMutex.LoadOrStore(sessionID, &sync.Mutex{})
	return val.(*sync.Mutex)
}

func removeSessionLock(sessionID uint) {
	sessionMutex.Delete(sessionID)
	sessionProcessing.Delete(sessionID)
}

func beginSessionProcessing(sessionID uint) bool {
	_, loaded := sessionProcessing.LoadOrStore(sessionID, sessionProcessingState{StartedAt: time.Now().UTC()})
	return !loaded
}

func finishSessionProcessing(sessionID uint) {
	sessionProcessing.Delete(sessionID)
}

func getSessionProcessing(sessionID uint) (sessionProcessingState, bool) {
	value, ok := sessionProcessing.Load(sessionID)
	if !ok {
		return sessionProcessingState{}, false
	}
	state, ok := value.(sessionProcessingState)
	return state, ok
}

// activeTurnPlayerIDs 返回房间内全体存活玩家(角色卡WoundState!=dead)的UserID集合，
// 用于判断"发言者本人是否存活"——与agent.BuildTurnCollection的批次收集集合是两回
// 事：遭遇激活时批次可能只含存活玩家的子集，但死亡玩家永远不该被允许发言，这条判
// 断必须独立于批次存在。
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

func lastKPReplyTime(sessionID uint) (time.Time, bool) {
	var lastKP models.Message
	if err := models.DB.Where("session_id = ? AND role = ?", sessionID, models.MessageRoleAssistant).
		Order("created_at DESC").
		First(&lastKP).Error; err != nil {
		return time.Time{}, false
	}
	return lastKP.CreatedAt, true
}

// countSubmittedTurnPlayers 统计collectUserIDs里已有多少人本轮提交过SessionTurnAction。
// collectUserIDs 来自 agent.BuildTurnCollection：非遭遇场景是全体存活玩家，遭遇激活
// 时是当前DEX批次，二者均已是[]uint，无需再靠map/mapKeys桥接。
func countSubmittedTurnPlayers(db *gorm.DB, sessionID uint, round int, collectUserIDs []uint) int64 {
	if len(collectUserIDs) == 0 {
		return 0
	}
	var rows []struct{ UserID uint }
	db.Model(&models.SessionTurnAction{}).
		Select("user_id").
		Where("session_id = ? AND round = ? AND user_id IN ?", sessionID, round, collectUserIDs).
		Group("user_id").Find(&rows)
	return int64(len(rows))
}

// turnActionsCoverBatch 校验turnActions是否覆盖了batchUserIDs里的每一个人。会话锁已
// 保证同一房间的ChatStream调用串行执行，这里只是"最后一人提交"之后的防御性复核。
func turnActionsCoverBatch(turnActions []models.SessionTurnAction, batchUserIDs []uint) bool {
	present := make(map[uint]bool, len(turnActions))
	for _, ta := range turnActions {
		present[ta.UserID] = true
	}
	for _, id := range batchUserIDs {
		if !present[id] {
			return false
		}
	}
	return true
}

// loadLatestTurnActions 按 players 的房间顺序(而非DB返回序)加载每个玩家本轮最新一条
// 行动记录，不按"当前批次"过滤——D4精确清理会跨run保留尚未被消费的声明(被冻结的攻
// 击方、还没轮到的参战者)，这里必须把它们也读出来喂给Director，否则combat_act/
// chase_act的声明可见性闸门会看不到玩家已经提交过的内容。
func loadLatestTurnActions(sessionID uint, round int, players []models.SessionPlayer) []models.SessionTurnAction {
	turnActions := make([]models.SessionTurnAction, 0, len(players))
	for _, p := range players {
		var ta models.SessionTurnAction
		if err := models.DB.Where("session_id = ? AND round = ? AND user_id = ?", sessionID, round, p.UserID).
			Order("created_at DESC, id DESC").
			First(&ta).Error; err == nil {
			turnActions = append(turnActions, ta)
		}
	}
	return turnActions
}

// filterTurnActionsSince 只保留cutoff之后新建/更新的记录，供saveChatMessages把"跨run
// 保留、之前已经落过聊天记录"的旧声明排除在外，避免同一条声明被重复写入transcript。
func filterTurnActionsSince(actions []models.SessionTurnAction, cutoff time.Time) []models.SessionTurnAction {
	filtered := make([]models.SessionTurnAction, 0, len(actions))
	for _, a := range actions {
		if a.CreatedAt.After(cutoff) {
			filtered = append(filtered, a)
		}
	}
	return filtered
}

// waitingPayload 是"等待其他玩家"SSE 事件/chat-status 的 JSON 结构。
// submitted_names/pending_names 按房间 Players 顺序排列，前端可直接展示 badges。
// batched=true 时 batch_user_ids 是遭遇激活时当前DEX批次的玩家UserID列表；
// encounter_order 覆盖遭遇全部参与者(含NPC、已行动、未行动)，供前端渲染完整队列；
// 无激活遭遇时 batched=false、encounter_label/encounter_order 为零值。
type waitingPayload struct {
	Pending        int                    `json:"pending"`
	Total          int                    `json:"total"`
	SubmittedNames []string               `json:"submitted_names"`
	PendingNames   []string               `json:"pending_names"`
	Batched        bool                   `json:"batched"`
	BatchUserIDs   []uint                 `json:"batch_user_ids"`
	EncounterLabel string                 `json:"encounter_label"`
	EncounterOrder []agent.EncounterActor `json:"encounter_order"`
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

// buildWaitingSSEPayload 查询collect.UserIDs里已提交行动的玩家集合，按房间 Players
// 顺序生成已提交/待提交姓名列表，并透传批次与DEX序信息供前端渲染完整收集队列。
// 若查询失败则返回 error，由调用方决定是否降级。
func buildWaitingSSEPayload(db *gorm.DB, session models.GameSession, collect agent.TurnCollection) (waitingPayload, error) {
	ids := collect.UserIDs
	total := len(ids)
	if total == 0 {
		return waitingPayload{
			SubmittedNames: []string{}, PendingNames: []string{},
			Batched: collect.Batched, BatchUserIDs: collect.UserIDs,
			EncounterLabel: collect.Label, EncounterOrder: collect.Order,
		}, nil
	}

	var rows []struct{ UserID uint }
	if err := db.Model(&models.SessionTurnAction{}).
		Select("user_id").
		Where("session_id = ? AND round = ? AND user_id IN ?", session.ID, session.TurnRound, ids).
		Group("user_id").Find(&rows).Error; err != nil {
		return waitingPayload{}, err
	}

	submittedSet := make(map[uint]bool, len(rows))
	for _, r := range rows {
		submittedSet[r.UserID] = true
	}
	idSet := make(map[uint]bool, len(ids))
	for _, id := range ids {
		idSet[id] = true
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
		if !idSet[p.UserID] {
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
		Batched:        collect.Batched,
		BatchUserIDs:   collect.UserIDs,
		EncounterLabel: collect.Label,
		EncounterOrder: collect.Order,
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

// turnCollectionIncludesUser 判断userID是否在collect.UserIDs(本次应收集的批次/全员
// 集合)里。
func turnCollectionIncludesUser(collect agent.TurnCollection, userID uint) bool {
	for _, id := range collect.UserIDs {
		if id == userID {
			return true
		}
	}
	return false
}

// turnCollectionRejectReason 解释遭遇按批次收集时，一名存活玩家为什么此刻不能提交：
// 完全不在collect.Order里(D7旁观锁定，未参与这场战斗/追逐)，或在Order里但不在当前
// 批次(还没轮到，报出当前批次成员姓名)。只应在collect.Batched时调用。
func turnCollectionRejectReason(collect agent.TurnCollection, userID uint) string {
	var batchNames []string
	inOrder := false
	for _, a := range collect.Order {
		if a.UserID == userID && !a.IsNPC {
			inOrder = true
		}
		if a.InBatch {
			batchNames = append(batchNames, a.Name)
		}
	}
	if !inOrder {
		return "战斗/追逐进行中，你未参与本场遭遇，暂时无法提交行动"
	}
	if len(batchNames) == 0 {
		return "还没轮到你行动，请稍候"
	}
	return fmt.Sprintf("还没轮到你行动，当前轮到 %s，请稍候", strings.Join(batchNames, "、"))
}

type chatStatusResponse struct {
	Phase             string         `json:"phase"`
	Processing        bool           `json:"processing"`
	WaitingForPlayers bool           `json:"waiting_for_players"`
	Submitted         bool           `json:"submitted"`
	StartedAt         *time.Time     `json:"started_at"`
	SubmittedAt       *time.Time     `json:"submitted_at"`
	Waiting           waitingPayload `json:"waiting"`
}

func setChatSSEHeaders(c *gin.Context) {
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Header("X-Accel-Buffering", "no")
}

// GetChatStatus 返回当前用户可恢复的聊天状态。
func (h *SessionHandlers) GetChatStatus(c *gin.Context) {
	sessionID, _ := strconv.ParseUint(c.Param("id"), 10, 64)
	userID := c.GetUint("user_id")

	lock := getSessionLock(uint(sessionID))
	lock.Lock()
	defer lock.Unlock()

	var session models.GameSession
	if err := models.DB.
		Preload("Players.User").
		Preload("Players.CharacterCard").
		First(&session, sessionID).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "房间不存在"})
		return
	}

	processingState, processing := getSessionProcessing(session.ID)
	collect := agent.BuildTurnCollection(session)
	wPayload, err := buildWaitingSSEPayload(models.DB, session, collect)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "查询本轮状态失败"})
		return
	}

	var submittedAction models.SessionTurnAction
	submitted := false
	if turnCollectionIncludesUser(collect, userID) {
		if err := models.DB.Where("session_id = ? AND round = ? AND user_id = ?", session.ID, session.TurnRound, userID).
			Order("created_at DESC, id DESC").First(&submittedAction).Error; err == nil {
			submitted = true
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "查询行动提交状态失败"})
			return
		}
	}

	submittedCount := wPayload.Total - wPayload.Pending
	waitingForPlayers := session.Status == models.SessionStatusPlaying && len(session.Players) > 1 && wPayload.Total > 1 && submittedCount > 0 && wPayload.Pending > 0
	phase := "idle"
	if waitingForPlayers {
		phase = "waiting"
	}
	var startedAt *time.Time
	if processing {
		phase = "processing"
		started := processingState.StartedAt
		startedAt = &started
	}
	var submittedAt *time.Time
	if submitted {
		at := submittedAction.CreatedAt
		submittedAt = &at
	}

	c.JSON(http.StatusOK, chatStatusResponse{
		Phase:             phase,
		Processing:        processing,
		WaitingForPlayers: waitingForPlayers,
		Submitted:         submitted,
		StartedAt:         startedAt,
		SubmittedAt:       submittedAt,
		Waiting:           wPayload,
	})
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
	if _, processing := getSessionProcessing(uint(sessionID)); processing {
		c.JSON(http.StatusConflict, gin.H{"error": "当前房间正在处理上一条消息，请稍候"})
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
	if _, processing := getSessionProcessing(session.ID); processing {
		c.JSON(http.StatusConflict, gin.H{"error": "当前房间正在处理上一条消息，请稍候"})
		return
	}

	chatLog.Debug("chat request", "session", sessionID, "user", username, "content_len", len([]rune(content)), "round", session.TurnRound)

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

	collect := agent.BuildTurnCollection(session)
	activePlayerIDs := activeTurnPlayerIDs(session.Players)
	activePlayerCount := len(activePlayerIDs)
	isActiveTurnPlayer := activePlayerIDs[userID]
	if playerCount > 1 {
		if activePlayerCount > 0 && (!isTrackedPlayer || !isActiveTurnPlayer) {
			chatLog.Warn("chat rejected dead or non-player input", "session", sessionID, "user", username)
			setChatSSEHeaders(c)
			c.SSEvent("error", "当前仍有存活调查员，只有非死亡玩家可以提交本轮行动")
			c.Writer.Flush()
			c.SSEvent("done", "")
			c.Writer.Flush()
			return
		}
		if activePlayerCount == 0 && !isTrackedPlayer {
			chatLog.Warn("chat rejected non-player input after party wipe", "session", sessionID, "user", username)
			setChatSSEHeaders(c)
			c.SSEvent("error", "所有调查员均已死亡时，只有房间内玩家可以推进剧情")
			c.Writer.Flush()
			c.SSEvent("done", "")
			c.Writer.Flush()
			return
		}
		// D7: 遭遇激活时按DEX批次收集——存活且受追踪的玩家如果不在当前批次里，要么是
		// 旁观者(未参与这场战斗/追逐)，要么是还没轮到，均拒绝提交，不静默吞掉输入。
		if isTrackedPlayer && isActiveTurnPlayer && collect.Batched && !turnCollectionIncludesUser(collect, userID) {
			chatLog.Warn("chat rejected: not in current encounter batch", "session", sessionID, "user", username)
			setChatSSEHeaders(c)
			c.SSEvent("error", turnCollectionRejectReason(collect, userID))
			c.Writer.Flush()
			c.SSEvent("done", "")
			c.Writer.Flush()
			return
		}
	}

	if playerCount > 1 && isTrackedPlayer && isActiveTurnPlayer {
		// Use a DB transaction so that record + count is atomic, preventing the
		// race where two simultaneous last-submitters both try to run the agent.
		batchUserIDs := collect.UserIDs
		var isLastToSubmit bool
		err := models.DB.Transaction(func(tx *gorm.DB) error {
			var existing models.SessionTurnAction
			err := tx.Where("session_id = ? AND round = ? AND user_id = ?", session.ID, session.TurnRound, userID).
				First(&existing).Error
			if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
				return err
			}
			if errors.Is(err, gorm.ErrRecordNotFound) {
				if err := tx.Create(&models.SessionTurnAction{
					SessionID:     session.ID,
					Round:         session.TurnRound,
					UserID:        userID,
					Username:      playerDisplayName,
					ActionSummary: content,
				}).Error; err != nil {
					return err
				}
			} else if err := tx.Model(&existing).Updates(map[string]any{
				"username":       playerDisplayName,
				"action_summary": content,
				"created_at":     time.Now().UTC(),
			}).Error; err != nil {
				return err
			}
			submitted := countSubmittedTurnPlayers(tx, session.ID, session.TurnRound, batchUserIDs)
			isLastToSubmit = submitted >= int64(len(batchUserIDs))
			return nil
		})
		if err != nil {
			chatLog.Error("chat transaction failed", "session", sessionID, "user", username, "err", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "保存行动失败"})
			return
		}

		if !isLastToSubmit {
			setChatSSEHeaders(c)
			// NOTE: 构建含已提交/待提交姓名及批次信息的等待载荷，查询失败时降级为计数格式
			wPayload, wErr := buildWaitingSSEPayload(models.DB, session, collect)
			if wErr != nil {
				chatLog.Error("chat waiting payload failed", "session", sessionID, "user", username, "err", wErr)
				submitted := countSubmittedTurnPlayers(models.DB, session.ID, session.TurnRound, batchUserIDs)
				wPayload = waitingPayload{
					Pending:        len(batchUserIDs) - int(submitted),
					Total:          len(batchUserIDs),
					SubmittedNames: []string{},
					PendingNames:   []string{},
				}
			}
			chatLog.Debug("chat waiting pending", "session", sessionID, "user", username, "pending", wPayload.Pending, "total", wPayload.Total)
			sendWaitingSSE(c, wPayload)
			c.SSEvent("done", "")
			c.Writer.Flush()
			return
		}

		// Last to submit: load every player's latest unconsumed declaration for the KP
		// prompt——不只是当前批次，还包含D4精确清理跨run保留下来的、之前批次未被消费
		// 的声明(被冻结的攻击方、还没轮到的参战者)，否则声明可见性闸门会看不到它们。
		turnActions = loadLatestTurnActions(session.ID, session.TurnRound, session.Players)
		if !turnActionsCoverBatch(turnActions, batchUserIDs) {
			setChatSSEHeaders(c)
			// NOTE: 构建含已提交/待提交姓名的等待载荷，查询失败时降级为计数格式
			wPayload, wErr := buildWaitingSSEPayload(models.DB, session, collect)
			if wErr != nil {
				chatLog.Error("chat waiting payload failed", "session", sessionID, "user", username, "err", wErr)
				wPayload = waitingPayload{
					Pending:        len(batchUserIDs),
					Total:          len(batchUserIDs),
					SubmittedNames: []string{},
					PendingNames:   []string{},
				}
			}
			chatLog.Debug("chat waiting after load pending", "session", sessionID, "user", username, "pending", wPayload.Pending, "total", wPayload.Total)
			sendWaitingSSE(c, wPayload)
			c.SSEvent("done", "")
			c.Writer.Flush()
			return
		}
		for _, ta := range turnActions {
			var user models.User
			models.DB.First(&user, ta.UserID)
			pendingActions = append(pendingActions, agent.PlayerAction{
				UserID:     ta.UserID,
				IsAdmin:    user.Role == models.RoleAdmin,
				PlayerName: ta.Username,
				Content:    ta.ActionSummary,
			})
		}
	} else {
		// Single-player or creator/spectator: keep only the latest action for this round.
		var existing models.SessionTurnAction
		err := models.DB.Where("session_id = ? AND round = ? AND user_id = ?", session.ID, session.TurnRound, userID).
			First(&existing).Error
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "保存行动失败"})
			return
		}
		if errors.Is(err, gorm.ErrRecordNotFound) {
			err = models.DB.Create(&models.SessionTurnAction{
				SessionID:     session.ID,
				Round:         session.TurnRound,
				UserID:        userID,
				Username:      playerDisplayName,
				ActionSummary: content,
			}).Error
		} else {
			err = models.DB.Model(&existing).Updates(map[string]any{
				"username":       playerDisplayName,
				"action_summary": content,
				"created_at":     time.Now().UTC(),
			}).Error
		}
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "保存行动失败"})
			return
		}
		// D6: 单人房/创建者兜底路径不经过loadLatestTurnActions，需要手动补一条自己的
		// 声明，否则战斗/追逐激活时声明可见性闸门会把这个PC拒死。
		pendingActions = append(pendingActions, agent.PlayerAction{
			UserID:     userID,
			IsAdmin:    user.Role == models.RoleAdmin,
			PlayerName: playerDisplayName,
			Content:    content,
		})
	}

	// D4: 跨run保留的旧声明不该被重复落成聊天记录；只把本次KP回复之后新建/更新的
	// 记录转成消息，此前已经落过消息的保留行仍用于pendingActions但不重复入库。
	if len(turnActions) > 0 {
		if cutoff, ok := lastKPReplyTime(session.ID); ok {
			turnActions = filterTurnActionsSince(turnActions, cutoff)
		}
	}

	setChatSSEHeaders(c)

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
	if !beginSessionProcessing(session.ID) {
		c.SSEvent("error", "当前房间正在处理上一条消息，请稍候")
		c.Writer.Flush()
		c.SSEvent("done", "")
		c.Writer.Flush()
		return
	}
	processingOwned := true
	defer func() {
		if processingOwned {
			finishSessionProcessing(session.ID)
		}
	}()

	// NOTE: 行动收集完成后立即释放短临界区；长耗时任务由 processing 状态防止重复进入。
	lock.Unlock()
	lockReleased = true

	// ── Run agent pipeline ────────────────────────────────────────────────────
	chatLog.Debug("chat pipeline start", "session", sessionID, "user", username, "round", session.TurnRound)
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
				chatLog.Error("chat pipeline failed", "session", sessionID, "user", username, "elapsed_ms", float64(time.Since(pipelineStart).Milliseconds()), "err", res.err)
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
					chatLog.Error("chat save after disconnect failed", "session", sessionID, "user", username, "err", err)
				} else {
					finishSessionProcessing(session.ID)
					processingOwned = false
					if assistantMsg != nil {
						h.startWriterJob(assistantMsg.ID, gctx, res.output, nil)
						if len(res.output.ImagePrompts) > 0 {
							h.startPainterJob(assistantMsg.ID, gctx, res.output.ImagePrompts[0], nil)
						}
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
		chatLog.Error("chat save failed", "session", sessionID, "user", username, "err", err)
		c.SSEvent("error", "保存消息失败")
		c.Writer.Flush()
		return
	}
	sendProgress("KP主流程已保存")
	finishSessionProcessing(session.ID)
	processingOwned = false

	// NOTE: KP主流程结果已落库，立即解除输入锁；Writer/Painter 继续异步更新当前消息。
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

	var writerCh <-chan writerJobResult
	if assistantMsg != nil {
		writerClientDone := make(chan struct{})
		defer close(writerClientDone)
		writerCh = h.startWriterJob(assistantMsg.ID, gctx, output, writerClientDone)
	}
	var painterCh <-chan painterJobResult
	if len(output.ImagePrompts) > 0 {
		imageRequest := output.ImagePrompts[0]
		chatLog.Debug("chat painter queued", "session", sessionID, "user", username, "prompt_len", len([]rune(imageRequest.Prompt)), "prompt", chatTruncate(imageRequest.Prompt, 200))
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
					chatLog.Error("chat writer async failed", "session", sessionID, "user", username, "err", wr.err)
				}
				writerCh = nil
			case pr, ok := <-painterCh:
				if !ok {
					painterCh = nil
					continue
				}
				if pr.err != nil {
					chatLog.Error("chat painter async failed", "session", sessionID, "user", username, "err", pr.err)
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
	chatLog.Debug("chat done", "session", sessionID, "user", username, "tokens", len([]rune(fullReply)), "elapsed_ms", float64(time.Since(pipelineStart).Milliseconds()))
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
		text, err := h.Runner.RunWriterStream(context.Background(), gctx, direction, output.WriterNSFW, func(token string) {
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
			chatLog.Error("chat writer message update failed", "err", dbErr)
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
		chatLog.Debug("chat painter async start", "session", gctx.Session.ID, "prompt_len", len([]rune(prompt)), "prompt", chatTruncate(prompt, 200))
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
			chatLog.Error("chat painter async failed", "session", gctx.Session.ID, "elapsed_ms", float64(time.Since(start).Microseconds())/1000, "err", err)
		} else {
			chatLog.Debug("chat painter async success", "session", gctx.Session.ID, "elapsed_ms", float64(time.Since(start).Microseconds())/1000)
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
	chatLog.Debug("chat saving messages", "session", sessionID, "user", playerDisplayName, "content_len", len([]rune(content)), "reply_len", len([]rune(fullReply)), "turn_actions", len(turnActions))
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
		if output.DirectorSteps > 0 {
			elapsed := output.DirectorElapsedMs
			steps := output.DirectorSteps
			assistantMsg.DirectorElapsedMs = &elapsed
			assistantMsg.DirectorSteps = &steps
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

// NOTE: isCardLockedInSession 判断人物卡是否正被某个未结束(lobby/playing)的房间占用。
// 传入 db 以便调用方在事务内复用同一连接。
func isCardLockedInSession(db *gorm.DB, cardID uint) bool {
	var lockedCount int64
	db.Model(&models.SessionPlayer{}).
		Joins("JOIN game_sessions ON game_sessions.id = session_players.session_id").
		Where("session_players.character_card_id = ? AND game_sessions.status != ?",
			cardID, models.SessionStatusEnded).
		Count(&lockedCount)
	return lockedCount > 0
}

// NOTE: lockedCardIDs 批量返回给定人物卡中正被未结束房间占用的 ID 集合，避免逐个查询导致 N+1。
func lockedCardIDs(db *gorm.DB, cardIDs []uint) map[uint]bool {
	result := make(map[uint]bool)
	if len(cardIDs) == 0 {
		return result
	}
	var ids []uint
	db.Model(&models.SessionPlayer{}).
		Joins("JOIN game_sessions ON game_sessions.id = session_players.session_id").
		Where("session_players.character_card_id IN ? AND game_sessions.status != ?",
			cardIDs, models.SessionStatusEnded).
		Distinct().
		Pluck("session_players.character_card_id", &ids)
	for _, id := range ids {
		result[id] = true
	}
	return result
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

	// NOTE: HTTP 手动结束由后端固定传 win=false（失败无角色成长），不解析客户端胜负。
	result, txErr := agent.RunEndSession(context.Background(), &session, messages, false)

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

// ListMyFavoriteSessions returns the user's favorite sessions, paginated.
func ListMyFavoriteSessions(c *gin.Context) {
	page, pageSize, ok := parseAdminPagination(c)
	if !ok {
		return
	}
	userID := c.GetUint("user_id")

	var total int64
	if err := models.DB.Model(&models.GameSession{}).
		Joins("JOIN session_favorites ON session_favorites.session_id = game_sessions.id").
		Where("session_favorites.user_id = ? AND game_sessions.status = ?", userID, models.SessionStatusEnded).
		Count(&total).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "查询收藏房间总数失败"})
		return
	}

	sessions := make([]models.GameSession, 0)
	if err := models.DB.
		Joins("JOIN session_favorites ON session_favorites.session_id = game_sessions.id").
		Preload("Scenario").
		Preload("Creator").
		Preload("Players.User").
		Preload("Players.CharacterCard").
		Where("session_favorites.user_id = ? AND game_sessions.status = ?", userID, models.SessionStatusEnded).
		Order("session_favorites.created_at DESC").
		Limit(pageSize).
		Offset((page - 1) * pageSize).
		Find(&sessions).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "查询收藏房间列表失败"})
		return
	}

	c.JSON(http.StatusOK, newPaginatedResponse(sessions, page, pageSize, total))
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

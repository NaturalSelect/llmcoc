package handlers

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

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
	// Guard 对玩家输入做越权请求分类，为 nil 时用 agent.ClassifyPlayerInput；测试里替换它避免依赖数据库里的 Provider。
	Guard func(ctx context.Context, content string) agent.InputVerdict
	// Companion 驱动 AI 陪玩的回应，为 nil 时用 agent.DefaultCompanion；测试里替换它避免依赖数据库里的 Provider。
	Companion agent.CompanionRunner
}

// NewSessionHandlers returns a SessionHandlers wired to the given runner.
func NewSessionHandlers(r agent.AgentRunner) *SessionHandlers {
	return &SessionHandlers{Runner: r}
}

func (h *SessionHandlers) classify(ctx context.Context, content string) agent.InputVerdict {
	if h.Guard != nil {
		return h.Guard(ctx, content)
	}
	return agent.ClassifyPlayerInput(ctx, content)
}

type CreateSessionReq struct {
	Name       string `json:"name" binding:"required,max=200"`
	ScenarioID uint   `json:"scenario_id" binding:"required"`
	MaxPlayers int    `json:"max_players"`
	Password   string `json:"password"`
	EnableNSFW bool   `json:"enable_nsfw"`
	// NOTE: 胜利结算时是否改写存活调查员的个人经历。
	EvolveBackstory bool `json:"evolve_backstory"`
	// NOTE: 叙事文风预设："" / "plain" / "lovecraft" / "cinematic"。
	NarrativeStyle string `json:"narrative_style"`
}

type JoinSessionReq struct {
	CharacterCardID uint   `json:"character_card_id" binding:"required"`
	Password        string `json:"password"`
	// NOTE: 认领模组预设 HO 身份卡：填编号，或 "random" 随机抽签；留空不认领。
	HandoutCode string `json:"handout_code"`
}

type messageResponse struct {
	ID                uint                 `json:"id"`
	SessionID         uint                 `json:"session_id"`
	UserID            *uint                `json:"user_id"`
	Role              models.MessageRole   `json:"role"`
	Kind              models.MessageKind   `json:"kind"`
	Status            models.MessageStatus `json:"status"`
	Floor             int                  `json:"floor"`
	Content           string               `json:"content"`
	Meta              *models.MessageMeta  `json:"meta"`
	Username          string               `json:"username"`
	CreatedAt         time.Time            `json:"created_at"`
	Images            []string             `json:"images"`
	DirectorElapsedMs *int64               `json:"director_elapsed_ms"`
	DirectorSteps     *int                 `json:"director_steps"`
}

// newMessageResponseForViewer 构造返回给前端的消息结构。
// NOTE: revealSecretMeta=false 时剥离幻觉耳语等仅管理员/结团复盘可见的元信息，防止对局中剧透。
func newMessageResponseForViewer(msg models.Message, revealSecretMeta bool) messageResponse {
	images := extractImageSources(msg.Content)
	if images == nil {
		images = []string{}
	}
	meta := msg.Meta.Data
	if meta != nil && !revealSecretMeta && meta.Hallucination {
		metaCopy := *meta
		metaCopy.Hallucination = false
		meta = &metaCopy
	}
	return messageResponse{
		ID:                msg.ID,
		SessionID:         msg.SessionID,
		UserID:            msg.UserID,
		Role:              msg.Role,
		Kind:              msg.Kind,
		Status:            msg.Status,
		Floor:             msg.Floor,
		Content:           stripInternalImageTags(msg.Content),
		Meta:              meta,
		Username:          msg.Username,
		CreatedAt:         msg.CreatedAt,
		Images:            images,
		DirectorElapsedMs: msg.DirectorElapsedMs,
		DirectorSteps:     msg.DirectorSteps,
	}
}

func newMessageResponse(msg models.Message) messageResponse {
	return newMessageResponseForViewer(msg, false)
}

func newMessageResponsesForViewer(messages []models.Message, revealSecretMeta bool) []messageResponse {
	responses := make([]messageResponse, 0, len(messages))
	for _, msg := range messages {
		responses = append(responses, newMessageResponseForViewer(msg, revealSecretMeta))
	}
	return responses
}

func newMessageResponses(messages []models.Message) []messageResponse {
	return newMessageResponsesForViewer(messages, false)
}

// hideAdminOnlySessions 让非管理员的房间查询排除仅管理员可用模组的房间。
func hideAdminOnlySessions(db *gorm.DB) *gorm.DB {
	return db.Where("scenario_id NOT IN (?)",
		models.DB.Model(&models.Scenario{}).Select("id").Where("admin_only = ?", true))
}

func ListSessions(c *gin.Context) {
	page, pageSize, ok := parseAdminPagination(c)
	if !ok {
		return
	}

	userID := c.GetUint("user_id")
	admin := isAdminUser(c, userID)

	countQuery := models.DB.Model(&models.GameSession{}).
		Where("status IN ?", []string{"lobby", "playing"})
	if !admin {
		countQuery = hideAdminOnlySessions(countQuery)
	}
	var total int64
	if err := countQuery.Count(&total).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "查询活跃房间总数失败"})
		return
	}

	listQuery := models.DB.
		Preload("Scenario").
		Preload("Creator").
		Preload("Players.User").
		Preload("Players.CharacterCard").
		Where("status IN ?", []string{"lobby", "playing"})
	if !admin {
		listQuery = hideAdminOnlySessions(listQuery)
	}

	sessions := make([]models.GameSession, 0)
	if err := listQuery.
		Order("created_at DESC").
		Limit(pageSize).
		Offset((page - 1) * pageSize).
		Find(&sessions).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "查询活跃房间列表失败"})
		return
	}

	for i := range sessions {
		populateScenarioPreview(&sessions[i].Scenario)
		sanitizeScenarioHandouts(&sessions[i].Scenario, admin)
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

	historyAdmin := isAdminUser(c, userID)
	for i := range sessions {
		populateScenarioPreview(&sessions[i].Scenario)
		sanitizeScenarioHandouts(&sessions[i].Scenario, historyAdmin)
	}
	c.JSON(http.StatusOK, newPaginatedResponse(sessions, page, pageSize, total))
}

func GetSession(c *gin.Context) {
	id, _ := strconv.ParseUint(c.Param("id"), 10, 64)
	viewerID := c.GetUint("user_id")
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
	viewerAdmin := isAdminUser(c, viewerID)
	populateScenarioPreview(&session.Scenario)
	sanitizeScenarioHandouts(&session.Scenario, viewerAdmin)
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
	if scenario.AdminOnly && !isAdminUser(c, userID) {
		c.JSON(http.StatusForbidden, gin.H{"error": "该剧本仅管理员可创建房间"})
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

	narrativeStyle := strings.TrimSpace(req.NarrativeStyle)
	switch narrativeStyle {
	case "", "plain", "lovecraft", "cinematic":
	default:
		c.JSON(http.StatusBadRequest, gin.H{"error": "无效的叙事风格，可选值：plain / lovecraft / cinematic"})
		return
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
		Name:            req.Name,
		ScenarioID:      req.ScenarioID,
		Status:          models.SessionStatusLobby,
		MaxPlayers:      req.MaxPlayers,
		Password:        pwHash,
		HasPassword:     hasPassword,
		EnableNSFW:      req.EnableNSFW,
		EvolveBackstory: req.EvolveBackstory,
		NarrativeStyle:  narrativeStyle,
		CreatedBy:       userID,
	}

	if err := models.DB.Create(&session).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "创建房间失败"})
		return
	}

	// Inject initial system message
	systemMsg := models.Message{
		SessionID: session.ID,
		Role:      models.MessageRoleSystem,
		Kind:      models.MessageKindSystem,
		Status:    models.MessageStatusDone,
		Content:   fmt.Sprintf("房间「%s」已创建,等待玩家加入。剧本:%s", session.Name, scenario.Name),
		Username:  "系统",
	}
	models.DB.Create(&systemMsg)

	models.DB.Preload("Scenario").Preload("Creator").First(&session, session.ID)
	sanitizeScenarioHandouts(&session.Scenario, isAdminUser(c, userID))
	c.JSON(http.StatusCreated, session)
}

func JoinSession(c *gin.Context) {
	userID := c.GetUint("user_id")
	sessionID, _ := strconv.ParseUint(c.Param("id"), 10, 64)

	var session models.GameSession
	if err := models.DB.Preload("Scenario").Preload("Players").First(&session, sessionID).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "房间不存在"})
		return
	}
	if session.Scenario.AdminOnly && !isAdminUser(c, userID) {
		c.JSON(http.StatusForbidden, gin.H{"error": "该房间仅管理员可加入"})
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

	handout, terr := joinSessionPlayer(uint(sessionID), userID, req.CharacterCardID, req.HandoutCode, c.GetString("username"))
	if terr != nil {
		respondTurnError(c, terr)
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "加入成功", "handout_code": handout})
}

// joinSessionPlayer 是 JoinSession 在「房间处于 lobby、未满员、未重复加入、密码正确」都已确认之后的核心：
// 校验人物卡归属和可用性、认领 HO，写入 SessionPlayer，发系统消息并广播 join。
// 真人处理器和 AI 陪玩共用；上面这些前置检查不在这里，AI 调用方需要自己先做。
//
// 锁：本函数自己不取会话锁。现有 JoinSession 本来就不取（HO 重复认领靠事务内重读 claimed 兜底，
// 卡占用靠 isCardLockedInSession 的读），所以这里与原行为一致。调用方可以持有 getSessionLock(sessionID)，
// 也可以不持有；本函数内部不会再取，不会死锁。
//
// announce 是系统消息里「」中的加入者称呼（真人传用户名，原文案「X」以调查员「Y」的身份加入了房间。里的 X）。
// 调查员名和 HO 编号只有在本函数里才知道，所以文案的其余部分仍在这里拼。
// handoutCode 允许带首尾空白，函数内会 trim。成功时返回认领到的 HO 编号（没认领为空串）。
func joinSessionPlayer(sessionID, ownerID, cardID uint, handoutCode, announce string) (handout string, terr *turnError) {
	// Verify character belongs to user
	var card models.CharacterCard
	if err := models.DB.First(&card, cardID).Error; err != nil {
		return "", &turnError{status: http.StatusBadRequest, msg: "人物卡不存在"}
	}
	if card.UserID != ownerID {
		return "", &turnError{status: http.StatusForbidden, msg: "无权使用此人物卡"}
	}
	hotFixChar(&card)
	// NOTE: 仅活跃且未死亡且HP>0的人物卡允许加入游戏，死亡卡由商城/复活流程处理
	if !checkCardCanJoinSession(&card) {
		return "", &turnError{status: http.StatusBadRequest, msg: "该人物卡已死亡或无法继续冒险"}
	}

	// Lock check: a character card may only participate in one active session at a time.
	if isCardLockedInSession(models.DB, cardID) {
		return "", &turnError{status: http.StatusConflict, msg: "该人物卡正在另一场游戏中使用,副本结束后才能再次使用"}
	}

	handoutCode = strings.TrimSpace(handoutCode)
	var handouts []models.ScenarioHandout
	if handoutCode != "" {
		var session models.GameSession
		if err := models.DB.Preload("Scenario").First(&session, sessionID).Error; err != nil {
			return "", &turnError{status: http.StatusNotFound, msg: "房间不存在"}
		}
		handouts = session.Scenario.Content.Data.Handouts
		if len(handouts) == 0 {
			return "", &turnError{status: http.StatusBadRequest, msg: "该模组没有预设身份卡"}
		}
	}

	player := models.SessionPlayer{
		SessionID:       sessionID,
		UserID:          ownerID,
		CharacterCardID: cardID,
		JoinedAt:        time.Now(),
	}
	var joinErr *handoutJoinError
	if err := models.DB.Transaction(func(tx *gorm.DB) error {
		if handoutCode != "" {
			// NOTE: 在事务内重新读取已认领的 HO，避免两名玩家同时认领同一张身份卡。
			var claimed []string
			if err := tx.Model(&models.SessionPlayer{}).
				Where("session_id = ? AND handout_code <> ''", sessionID).
				Pluck("handout_code", &claimed).Error; err != nil {
				return err
			}
			h, herr := pickScenarioHandout(handouts, claimed, handoutCode)
			if herr != nil {
				joinErr = herr
				return herr
			}
			player.HandoutCode = h.Code
		}
		return tx.Create(&player).Error
	}); err != nil {
		if joinErr != nil {
			return "", &turnError{status: joinErr.status, msg: joinErr.msg}
		}
		return "", &turnError{status: http.StatusInternalServerError, msg: "加入房间失败"}
	}

	// Announce join
	joinMsg := models.Message{
		SessionID: sessionID,
		Role:      models.MessageRoleSystem,
		Kind:      models.MessageKindSystem,
		Status:    models.MessageStatusDone,
		Content:   fmt.Sprintf("「%s」以调查员「%s」的身份加入了房间。", announce, card.Name),
		Username:  "系统",
	}
	if player.HandoutCode != "" {
		joinMsg.Content = fmt.Sprintf("「%s」以调查员「%s」的身份加入了房间，认领身份卡 %s。", announce, card.Name, player.HandoutCode)
	}
	models.DB.Create(&joinMsg)
	publishSessionChanged(sessionID, models.SessionStatusLobby, "join")

	return player.HandoutCode, nil
}

// joinSessionPlayer 的方法形式，给持有 SessionHandlers 的调用方（AI 陪玩）用。
// JoinSession 是不带接收者的包级处理器（路由直接引用 handlers.JoinSession），所以它调用上面的包级函数。
func (h *SessionHandlers) joinSessionPlayer(sessionID, ownerID, cardID uint, handoutCode, announce string) (handout string, terr *turnError) {
	return joinSessionPlayer(sessionID, ownerID, cardID, handoutCode, announce)
}

type handoutJoinError struct {
	status int
	msg    string
}

func (e *handoutJoinError) Error() string { return e.msg }

// pickScenarioHandout 按编号认领预设 HO；code 为 random 时从未被认领的身份卡里随机抽一张。
func pickScenarioHandout(handouts []models.ScenarioHandout, claimed []string, code string) (models.ScenarioHandout, *handoutJoinError) {
	isClaimed := func(c string) bool {
		for _, x := range claimed {
			if strings.EqualFold(strings.TrimSpace(x), strings.TrimSpace(c)) {
				return true
			}
		}
		return false
	}
	if strings.EqualFold(code, "random") {
		free := make([]models.ScenarioHandout, 0, len(handouts))
		for _, h := range handouts {
			if strings.TrimSpace(h.Code) != "" && !isClaimed(h.Code) {
				free = append(free, h)
			}
		}
		if len(free) == 0 {
			return models.ScenarioHandout{}, &handoutJoinError{http.StatusConflict, "身份卡已被全部认领"}
		}
		return free[rand.IntN(len(free))], nil
	}
	for _, h := range handouts {
		if strings.EqualFold(strings.TrimSpace(h.Code), code) {
			if isClaimed(h.Code) {
				return models.ScenarioHandout{}, &handoutJoinError{http.StatusConflict, fmt.Sprintf("身份卡 %s 已被其他玩家认领", h.Code)}
			}
			return h, nil
		}
	}
	return models.ScenarioHandout{}, &handoutJoinError{http.StatusBadRequest, fmt.Sprintf("身份卡 %s 不存在", code)}
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
	// NOTE: 必须在事务外取 bot 用户 ID：SQLite 单连接下事务内再用全局 DB 查询会死锁。
	botID := models.CompanionUserID()
	deletedSession := false
	if err := models.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Delete(&player).Error; err != nil {
			return err
		}

		// NOTE: 最后一名真人离开后，只剩 AI 陪玩的房间没有意义，连 AI 席位一起清掉，让房间走下面的解散分支。
		if botID != 0 {
			var humans int64
			if err := tx.Model(&models.SessionPlayer{}).
				Where("session_id = ? AND user_id <> ?", sessionID, botID).
				Count(&humans).Error; err != nil {
				return err
			}
			if humans == 0 {
				if err := tx.Where("session_id = ? AND user_id = ?", sessionID, botID).
					Delete(&models.SessionPlayer{}).Error; err != nil {
					return err
				}
			}
		}

		var remain int64
		if err := tx.Model(&models.SessionPlayer{}).
			Where("session_id = ?", sessionID).
			Count(&remain).Error; err != nil {
			return err
		}

		if remain == 0 {
			if err := tx.Where("session_id = ?", sessionID).Delete(&models.SessionOOCMessage{}).Error; err != nil {
				return err
			}
			if err := tx.Where("session_id = ?", sessionID).Delete(&models.SessionClue{}).Error; err != nil {
				return err
			}
			if err := tx.Where("session_id = ?", sessionID).Delete(&models.SessionNote{}).Error; err != nil {
				return err
			}
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
			Kind:      models.MessageKindSystem,
			Status:    models.MessageStatusDone,
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

	publishSessionChanged(uint(sessionID), models.SessionStatusLobby, "leave")
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
		c.JSON(http.StatusBadRequest, gin.H{"error": "房间里还没有调查员，请先携带人物卡加入房间（房主也需要加入）再开始"})
		return
	}
	if !hasHumanPlayer(session.Players) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "至少需要一名真人调查员才能开始游戏"})
		return
	}

	// NOTE: 新开局的房间由 KP 驱动：空 Spotlight 表示 KP 持有发言权，开场段由房主调用 kp-turn 触发。
	startTime := session.Scenario.Content.Data.GameStartTime
	models.DB.Model(&session).Updates(map[string]any{
		"status":          models.SessionStatusPlaying,
		"spotlight":       models.JSONField[*models.SpotlightState]{Data: &models.SpotlightState{}},
		"game_start_time": startTime,
		"game_time":       startTime,
	})

	publishSessionChanged(session.ID, models.SessionStatusPlaying, "start")

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

	var reviveMsg models.Message
	if err := models.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&session).Update("status", models.SessionStatusPlaying).Error; err != nil {
			return err
		}
		reviveMsg = models.Message{
			SessionID: session.ID,
			Role:      models.MessageRoleSystem,
			Kind:      models.MessageKindSystem,
			Status:    models.MessageStatusDone,
			Content:   fmt.Sprintf("管理员「%s」复活了房间，游戏继续。", username),
			Username:  "系统",
		}
		return tx.Create(&reviveMsg).Error
	}); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "复活房间失败"})
		return
	}

	hubFor(session.ID).Publish("message_new", gin.H{"message": newMessageResponse(reviveMsg)})
	publishSessionChanged(session.ID, models.SessionStatusPlaying, "revive")
	c.JSON(http.StatusOK, gin.H{"message": "房间已复活"})
}

// NOTE: 查看权限：无密码房间所有登录用户可看；有密码房间只允许成员和管理员。session 必须已 Preload("Players")，
// GetMessages 与 WebSocket 订阅共用这一判断，避免两处口径漂移。
func canViewSession(session *models.GameSession, userID uint) bool {
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
			return false
		}
	}
	return true
}

// canSeeMessage 判断指定用户能否看到一条房间消息。
// 公开消息(VisibleTo 为空)所有人可见；密讯仅接收者与管理员可见；游戏结束后对本局参与者与房主解锁。
func canSeeMessage(m models.Message, session *models.GameSession, userID uint, isAdmin bool) bool {
	if len(m.VisibleTo.Data) == 0 || isAdmin {
		return true
	}
	for _, id := range m.VisibleTo.Data {
		if id == userID {
			return true
		}
	}
	if session != nil && session.Status == models.SessionStatusEnded {
		if session.CreatedBy == userID {
			return true
		}
		for _, p := range session.Players {
			if p.UserID == userID {
				return true
			}
		}
	}
	return false
}

func GetMessages(c *gin.Context) {
	sessionID, _ := strconv.ParseUint(c.Param("id"), 10, 64)
	userID := c.GetUint("user_id")

	var session models.GameSession
	if err := models.DB.Preload("Players").First(&session, sessionID).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "房间不存在"})
		return
	}

	if !canViewSession(&session, userID) {
		messages := []messageResponse{}
		c.JSON(http.StatusOK, messages)
		return
	}

	var messages []models.Message
	q := models.DB.Where("session_id = ? AND (role != ? OR content LIKE ?)", sessionID, models.MessageRoleSystem, "管理员「%」复活了房间，游戏继续。")
	// NOTE: 排在未写完的 writer 之后、还没广播的消息不能出现在快照里，否则重连的客户端会先读到 KP 的话。
	if held := heldMessageIDs(uint(sessionID)); len(held) > 0 {
		q = q.Where("id NOT IN ?", held)
	}
	q.Order("created_at ASC, id ASC").Find(&messages)
	isAdmin := isAdminUser(c, userID)
	visible := make([]models.Message, 0, len(messages))
	for _, m := range messages {
		if canSeeMessage(m, &session, userID, isAdmin) {
			visible = append(visible, m)
		}
	}
	revealSecretMeta := isAdmin || session.Status == models.SessionStatusEnded
	c.JSON(http.StatusOK, newMessageResponsesForViewer(visible, revealSecretMeta))
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

// lastKPReplyTime 返回 KP 最近一条 kp/ask 消息的时间：只有在它之后新建的玩家声明才需要落成玩家消息。
func lastKPReplyTime(sessionID uint) (time.Time, bool) {
	var last models.Message
	if err := models.DB.
		Where("session_id = ? AND kind IN ?", sessionID, []models.MessageKind{models.MessageKindKP, models.MessageKindAsk}).
		Order("created_at DESC, id DESC").
		First(&last).Error; err != nil {
		return time.Time{}, false
	}
	return last.CreatedAt, true
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

// filterTurnActionsSince 只保留cutoff之后新建/更新的记录，把"跨run保留、之前已经落过玩家消息"
// 的旧声明排除在外，避免同一条声明被重复写成玩家消息。
func filterTurnActionsSince(actions []models.SessionTurnAction, cutoff time.Time) []models.SessionTurnAction {
	filtered := make([]models.SessionTurnAction, 0, len(actions))
	for _, a := range actions {
		if a.CreatedAt.After(cutoff) {
			filtered = append(filtered, a)
		}
	}
	return filtered
}

// waitingPayload 是"等待被点名玩家到齐"的 waiting 事件/chat-status 的 JSON 结构。
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

// buildWaitingPayload 查询collect.UserIDs里已提交行动的玩家集合，按房间 Players
// 顺序生成已提交/待提交姓名列表。查询失败时返回 error，由调用方决定如何降级。
func buildWaitingPayload(db *gorm.DB, session models.GameSession, collect agent.TurnCollection) (waitingPayload, error) {
	ids := collect.UserIDs
	total := len(ids)
	if total == 0 {
		return waitingPayload{SubmittedNames: []string{}, PendingNames: []string{}}, nil
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

	// NOTE: 按房间 Players 顺序遍历，保证姓名列表顺序稳定
	submittedNames := make([]string, 0, len(rows))
	pendingNames := make([]string, 0, total)
	for _, p := range session.Players {
		if !idSet[p.UserID] {
			continue
		}
		if submittedSet[p.UserID] {
			submittedNames = append(submittedNames, sessionPlayerDisplayName(p))
		} else {
			pendingNames = append(pendingNames, sessionPlayerDisplayName(p))
		}
	}

	pending := total - len(rows)
	if pending < 0 {
		pending = 0
	}
	return waitingPayload{Pending: pending, Total: total, SubmittedNames: submittedNames, PendingNames: pendingNames}, nil
}

// turnCollectionIncludesUser 判断userID是否在collect.UserIDs(KP 当前点名且仍存活的玩家)里。
func turnCollectionIncludesUser(collect agent.TurnCollection, userID uint) bool {
	for _, id := range collect.UserIDs {
		if id == userID {
			return true
		}
	}
	return false
}

type chatStatusResponse struct {
	// Phase: idle(房间不在进行中) | kp_running(KP 正在发言) | kp_floor(KP 持有发言权但没在跑，等房主继续)
	// | awaiting_me(KP 在等我回应) | waiting(KP 在等被点名的玩家)。
	Phase             string         `json:"phase"`
	KPRunning         bool           `json:"kp_running"`
	WaitingForPlayers bool           `json:"waiting_for_players"`
	Submitted         bool           `json:"submitted"`
	SubmittedAt       *time.Time     `json:"submitted_at"`
	SubmittedContent  string         `json:"submitted_content"` // NOTE: 本轮自己最近一次提交的原文，重连后前端靠它回填「修改」框；未提交为空串
	Waiting           waitingPayload `json:"waiting"`
	Spotlight         spotlightInfo  `json:"spotlight"`
	Floor             *floorInfo     `json:"floor"`
	CanKPContinue     bool           `json:"can_kp_continue"`
	CanKPStop         bool           `json:"can_kp_stop"`
	// CompanionStatus 是 AI 陪玩正在回应时的状态文案，没有任务在跑为空串；重连后前端靠它补回临时状态。
	CompanionStatus string `json:"companion_status"`
}

// spotlightInfo 是 chat-status 里的发言权状态；KPFloor 为 true 表示 KP 持有，Stop 是上一个周期被强制结束的原因。
type spotlightInfo struct {
	UserIDs  []uint   `json:"user_ids"`
	Names    []string `json:"names"`
	Question string   `json:"question"`
	KPFloor  bool     `json:"kp_floor"`
	AskedMe  bool     `json:"asked_me"`
	Stop     string   `json:"stop"`
}

// floorInfo 描述正在进行的 KP 周期；Beats 是它已经发布的 kp/ask 消息条数。
type floorInfo struct {
	Floor     int       `json:"floor"`
	StartedAt time.Time `json:"started_at"`
	Beats     int       `json:"beats"`
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

	_, running := getSessionProcessing(session.ID)
	collect := agent.BuildTurnCollection(session)
	waiting, err := buildWaitingPayload(models.DB, session, collect)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "查询本轮状态失败"})
		return
	}

	named := make(map[uint]bool, len(collect.UserIDs))
	names := make([]string, 0, len(collect.UserIDs))
	for _, id := range collect.UserIDs {
		named[id] = true
	}
	for _, p := range session.Players {
		if named[p.UserID] {
			names = append(names, sessionPlayerDisplayName(p))
		}
	}
	ids := collect.UserIDs
	if ids == nil {
		ids = []uint{}
	}
	stop := ""
	if sp := session.Spotlight.Data; sp != nil {
		stop = sp.Stop
	}

	var submittedAction models.SessionTurnAction
	submitted := false
	if named[userID] {
		err := models.DB.Where("session_id = ? AND round = ? AND user_id = ?", session.ID, session.TurnRound, userID).
			Order("created_at DESC, id DESC").First(&submittedAction).Error
		switch {
		case err == nil:
			submitted = true
		case !errors.Is(err, gorm.ErrRecordNotFound):
			c.JSON(http.StatusInternalServerError, gin.H{"error": "查询行动提交状态失败"})
			return
		}
	}
	var submittedAt *time.Time
	if submitted {
		at := submittedAction.CreatedAt
		submittedAt = &at
	}

	playing := session.Status == models.SessionStatusPlaying
	phase := "idle"
	switch {
	case !playing:
	case running:
		phase = "kp_running"
	case len(ids) == 0:
		phase = "kp_floor"
	case named[userID] && !submitted:
		phase = "awaiting_me"
	default:
		phase = "waiting"
	}

	var floor *floorInfo
	if af := getActiveFloor(session.ID); af != nil && running {
		floor = &floorInfo{Floor: int(af.floor.Load()), StartedAt: af.startedAt, Beats: int(af.beats.Load())}
	}
	isHost := playing && (session.CreatedBy == userID || isAdminUser(c, userID))

	submittedCount := waiting.Total - waiting.Pending
	c.JSON(http.StatusOK, chatStatusResponse{
		Phase:             phase,
		KPRunning:         running,
		WaitingForPlayers: playing && !running && waiting.Total > 1 && submittedCount > 0 && waiting.Pending > 0,
		Submitted:         submitted,
		SubmittedAt:       submittedAt,
		SubmittedContent:  submittedAction.ActionSummary,
		Waiting:           waiting,
		Spotlight: spotlightInfo{
			UserIDs:  ids,
			Names:    names,
			Question: collect.Question,
			KPFloor:  len(ids) == 0,
			AskedMe:  named[userID],
			Stop:     stop,
		},
		Floor:           floor,
		CanKPContinue:   isHost && !running,
		CanKPStop:       isHost && running,
		CompanionStatus: companionStatusText(session.ID),
	})
}

// saveTurnAction 记录或更新玩家本轮的声明：同一轮重复提交只保留最新一条。
func saveTurnAction(session models.GameSession, userID uint, displayName, content string) error {
	return models.DB.Transaction(func(tx *gorm.DB) error {
		var existing models.SessionTurnAction
		err := tx.Where("session_id = ? AND round = ? AND user_id = ?", session.ID, session.TurnRound, userID).
			First(&existing).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return tx.Create(&models.SessionTurnAction{
				SessionID:     session.ID,
				Round:         session.TurnRound,
				UserID:        userID,
				Username:      displayName,
				ActionSummary: content,
			}).Error
		}
		if err != nil {
			return err
		}
		return tx.Model(&existing).Updates(map[string]any{
			"username":       displayName,
			"action_summary": content,
			"created_at":     time.Now().UTC(),
		}).Error
	})
}

// ChatStream 接收被 KP 点名的玩家的回应，立即返回 202；KP 的进展通过房间的 WebSocket 推送。
//
// 流程：
//  0. 内容校验和越权请求分类在拿会话锁之前做：非管理员的输入先交给 systemone 分类器，命中返回 422，不落库、不开周期；
//     分类器未启用或调用失败时放行。这样第三方调用不会占住会话锁。
//  1. 只有被点名且存活的玩家可以提交；其他人被拒绝(KP 持有发言权时 409，没被点名时 403)。
//  2. 声明写入 SessionTurnAction，同一轮重复提交只保留最新一条。
//  3. 被点名的人没到齐：广播 waiting，返回 floor_started=false。
//  4. 到齐：开始一个 KP 周期，返回 floor_started=true。
//
// 1-4 在 submitTurn 里，真人和 AI 陪玩共用；ChatStream 只保留表单解析、长度校验、输入分类器和响应映射。
func (h *SessionHandlers) ChatStream(c *gin.Context) {
	sessionID, _ := strconv.ParseUint(c.Param("id"), 10, 64)
	userID := c.GetUint("user_id")
	username := c.GetString("username")

	content := c.PostForm("content")
	if content == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "消息内容不能为空"})
		return
	}
	if utf8.RuneCountInString(content) > 2000 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "消息过长(最多2000字)"})
		return
	}
	// NOTE: 管理员的 DEBUG 指令也走这个接口，必须跳过分类，否则调试指令会被当成越权请求拦掉。
	if !isAdminUser(c, userID) {
		if v := h.classify(c.Request.Context(), content); v.Blocked {
			chatLog.Warn("chat blocked by input guard", "session", sessionID, "user", username, "category", v.Category, "score", v.Score)
			c.JSON(http.StatusUnprocessableEntity, gin.H{"error": "这条输入像是在要求越权或索要谜底，KP 不会执行。请写成你的调查员此刻要做的事或要说的话。"})
			return
		}
	}

	floorStarted, waiting, terr := h.submitTurn(uint(sessionID), userID, username, content)
	if terr != nil {
		respondTurnError(c, terr)
		return
	}
	c.JSON(http.StatusAccepted, gin.H{"floor_started": floorStarted, "waiting": waiting})
}

// submitTurn 是 ChatStream 去掉表单解析、长度校验和输入分类器之后的核心：真人处理器和 AI 陪玩共用。
// 它自己拿会话锁（调用方不要先持有，sync.Mutex 不可重入），从校验发言资格一直做到开周期。
// 输入分类器只在 ChatStream 里调用，AI 路径有意跳过。
//
// username 只用于日志和显示名兜底：玩家有人物卡角色名时消息里用角色名；角色名为空时才用 username；
// username 也为空（AI 路径）时回落到库里的用户名。
// 返回值：floorStarted 表示这次提交让被点名的人到齐、已开出一个 KP 周期；成功时 waiting 是本轮到齐进度；
// 失败时 terr 非空，terr.notAsked 表示失败来自点名闸门（此刻没轮到这个人），其余失败为 false。
func (h *SessionHandlers) submitTurn(sessionID, userID uint, username, content string) (floorStarted bool, waiting waitingPayload, terr *turnError) {
	lock := getSessionLock(sessionID)
	lock.Lock()
	defer lock.Unlock()

	session, err := loadSessionForFloor(sessionID)
	if err != nil {
		return false, waitingPayload{}, &turnError{status: http.StatusNotFound, msg: "房间不存在"}
	}
	if session.Status != models.SessionStatusPlaying {
		return false, waitingPayload{}, &turnError{status: http.StatusBadRequest, msg: "游戏尚未开始"}
	}

	isPlayer := false
	displayName := username
	for _, p := range session.Players {
		if p.UserID == userID {
			isPlayer = true
			if p.CharacterCard.Name != "" {
				displayName = p.CharacterCard.Name
			}
			break
		}
	}
	if displayName == "" {
		var u models.User
		if err := models.DB.Select("username").First(&u, userID).Error; err == nil {
			displayName = u.Username
		}
	}
	if !isPlayer && session.CreatedBy != userID {
		return false, waitingPayload{}, &turnError{status: http.StatusForbidden, msg: "观战模式下无法发言"}
	}

	if _, running := getSessionProcessing(session.ID); running {
		return false, waitingPayload{}, &turnError{status: http.StatusConflict, msg: "KP 正在发言，请稍候"}
	}

	chatLog.Debug("chat request", "session", sessionID, "user", username, "content_len", len([]rune(content)), "round", session.TurnRound)

	collect := agent.BuildTurnCollection(session)
	if !turnCollectionIncludesUser(collect, userID) {
		chatLog.Warn("chat rejected: not named by KP", "session", sessionID, "user", username)
		status := http.StatusForbidden
		if len(collect.UserIDs) == 0 {
			status = http.StatusConflict
		}
		return false, waitingPayload{}, &turnError{status: status, msg: spotlightRejectReason(session, collect), notAsked: true}
	}

	if err := saveTurnAction(session, userID, displayName, content); err != nil {
		chatLog.Error("chat save turn action failed", "session", sessionID, "user", username, "err", err)
		return false, waitingPayload{}, &turnError{status: http.StatusInternalServerError, msg: "保存行动失败"}
	}

	waiting, err = buildWaitingPayload(models.DB, session, collect)
	if err != nil {
		chatLog.Error("chat waiting payload failed", "session", sessionID, "user", username, "err", err)
		return false, waitingPayload{}, &turnError{status: http.StatusInternalServerError, msg: "查询本轮状态失败"}
	}
	if waiting.Pending > 0 {
		chatLog.Debug("chat waiting pending", "session", sessionID, "user", username, "pending", waiting.Pending, "total", waiting.Total)
		hubFor(session.ID).Publish("waiting", waiting)
		return false, waiting, nil
	}

	// NOTE: 读出每个玩家本轮最新、尚未被消费的声明，而不只是被点名的人：跨周期保留下来的战斗/追逐声明
	// 也要让 Director 的声明可见性闸门看到。
	seg := buildKPSegment(session, "", loadLatestTurnActions(session.ID, session.TurnRound, session.Players))
	switch err := h.startFloor(seg); {
	case errors.Is(err, errFloorBusy):
		return false, waitingPayload{}, &turnError{status: http.StatusConflict, msg: "KP 正在发言，请稍候"}
	case err != nil:
		chatLog.Error("chat start floor failed", "session", sessionID, "user", username, "err", err)
		return false, waitingPayload{}, &turnError{status: http.StatusInternalServerError, msg: "启动 KP 失败"}
	}
	return true, waiting, nil
}

const imageDataURLStartTag = "<image_data_url>"
const imageDataURLTagOpenPrefix = "<image_data_url"
const imageDataURLEndTag = "</image_data_url>"
const imageRefTagName = "image_ref"

var imageRefTagPattern = regexp.MustCompile(`(?is)<image_ref\b[^>]*(?:/>|>\s*</image_ref>)`)
var imageRefAttrPattern = regexp.MustCompile(`(?is)\b(hash|mime)\s*=\s*(?:"([^"]*)"|'([^']*)')`)

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
	// NOTE: AI 陪玩账号没有金币，也不参与结束游戏的收费。
	endSessionCost := siteSettingInt("end_session_cost", 200)
	var brokePlayers []string
	for i := range session.Players {
		p := &session.Players[i]
		if p.User.Role == models.RoleBot {
			continue
		}
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
		if p.User.Role == models.RoleBot {
			continue
		}
		newCoins := p.User.Coins - endSessionCost
		if err := models.DB.Model(&p.User).Update("coins", newCoins).Error; err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "扣费失败: " + p.User.Username})
			return
		}
	}

	cancelCompanion(session.ID, errCompanionSkipped)
	models.DB.Model(&session).Update("status", models.SessionStatusEnded)
	publishSessionChanged(session.ID, models.SessionStatusEnded, "end")

	// Load recent messages as context for relation promotion
	messages := agent.LoadEndGameMessages(uint(sessionID))
	stripMessageImageDataURLTags(messages)

	// NOTE: HTTP 手动结束没有 Director 填写的结算，固定传 win=false 且不带评分，只发基础金币。
	_, txErr := agent.RunEndSession(context.Background(), &session, messages, false, nil)

	removeSessionLock(session.ID)

	if txErr != nil {
		c.JSON(http.StatusOK, gin.H{"message": "游戏已结束(奖励结算失败,请联系管理员)"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "游戏已结束"})
}

// AdminEndAllSessions 一键强制结束所有运行中(lobby/playing)的房间，供后端升级后清理与
// 新版本编排逻辑不兼容的存量房间使用。与玩家自行结束不同：不收取 end_session_cost 费用
// (强制下线非玩家自愿)，但复用 RunEndSession 做基础结算与角色恢复(回满HP/MP/SAN、撕卡、
// 清疯狂)，win 固定为 false。RunEndSession 不读取 DirectorHistory，对旧数据结构安全。
func AdminEndAllSessions(c *gin.Context) {
	adminID := c.GetUint("user_id")

	var sessions []models.GameSession
	if err := models.DB.
		Preload("Players.User").
		Preload("Players.CharacterCard").
		Where("status IN ?", []string{string(models.SessionStatusLobby), string(models.SessionStatusPlaying)}).
		Find(&sessions).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "查询运行中房间失败"})
		return
	}

	type endAllResult struct {
		SessionID uint   `json:"session_id"`
		Name      string `json:"name"`
		Settled   bool   `json:"settled"`
	}
	results := make([]endAllResult, 0, len(sessions))

	for i := range sessions {
		session := &sessions[i]
		cancelCompanion(session.ID, errCompanionSkipped)
		models.DB.Model(session).Update("status", models.SessionStatusEnded)
		publishSessionChanged(session.ID, models.SessionStatusEnded, "end")

		messages := agent.LoadEndGameMessages(session.ID)
		stripMessageImageDataURLTags(messages)

		_, txErr := agent.RunEndSession(context.Background(), session, messages, false, nil)
		removeSessionLock(session.ID)

		results = append(results, endAllResult{SessionID: session.ID, Name: session.Name, Settled: txErr == nil})
	}

	adminLog.Info("end all sessions", "admin_id", adminID, "count", len(results))
	c.JSON(http.StatusOK, gin.H{
		"message": fmt.Sprintf("已强制结束 %d 个房间", len(results)),
		"count":   len(results),
		"results": results,
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

	favAdmin := isAdminUser(c, userID)
	for i := range sessions {
		sanitizeScenarioHandouts(&sessions[i].Scenario, favAdmin)
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

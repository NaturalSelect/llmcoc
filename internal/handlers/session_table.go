package handlers

import (
	"fmt"
	"net/http"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/gin-gonic/gin"
	"github.com/llmcoc/server/internal/models"
)

const (
	maxOOCContentRunes = 500
	oocHistoryLimit    = 200
)

func isSessionMemberOrHost(session *models.GameSession, userID uint, isAdmin bool) bool {
	if isAdmin || session.CreatedBy == userID {
		return true
	}
	for _, p := range session.Players {
		if p.UserID == userID {
			return true
		}
	}
	return false
}

// GetOOCMessages 返回房间最近 200 条桌边闲聊(OOC)消息（按时间升序）。
func GetOOCMessages(c *gin.Context) {
	sessionID, _ := strconv.ParseUint(c.Param("id"), 10, 64)
	userID := c.GetUint("user_id")

	var session models.GameSession
	if err := models.DB.Preload("Players").First(&session, sessionID).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "房间不存在"})
		return
	}
	if !canViewSession(&session, userID) {
		c.JSON(http.StatusForbidden, gin.H{"error": "无权查看该房间"})
		return
	}

	var msgs []models.SessionOOCMessage
	models.DB.Where("session_id = ?", session.ID).
		Order("created_at DESC, id DESC").
		Limit(oocHistoryLimit).
		Find(&msgs)
	slices.Reverse(msgs)
	if msgs == nil {
		msgs = []models.SessionOOCMessage{}
	}
	c.JSON(http.StatusOK, msgs)
}

type postOOCReq struct {
	Content string `json:"content"`
}

// PostOOCMessage 在桌边频道发言：房间参与者、房主与管理员可用，不限房间阶段，不触碰 KP 叙事流。
func PostOOCMessage(c *gin.Context) {
	sessionID, _ := strconv.ParseUint(c.Param("id"), 10, 64)
	userID := c.GetUint("user_id")

	var session models.GameSession
	if err := models.DB.Preload("Players.CharacterCard").First(&session, sessionID).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "房间不存在"})
		return
	}
	isAdmin := isAdminUser(c, userID)
	if !isSessionMemberOrHost(&session, userID, isAdmin) {
		c.JSON(http.StatusForbidden, gin.H{"error": "只有房间成员可以发送桌边讨论消息"})
		return
	}

	var req postOOCReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	content := strings.TrimSpace(req.Content)
	if content == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "消息内容不能为空"})
		return
	}
	if utf8.RuneCountInString(content) > maxOOCContentRunes {
		c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("桌边消息最多 %d 字", maxOOCContentRunes)})
		return
	}

	username := c.GetString("username")
	if username == "" {
		var u models.User
		if err := models.DB.Select("username").First(&u, userID).Error; err == nil {
			username = u.Username
		}
	}
	charName := ""
	for _, p := range session.Players {
		if p.UserID == userID {
			charName = p.CharacterCard.Name
			break
		}
	}

	msg := models.SessionOOCMessage{
		SessionID:     session.ID,
		UserID:        userID,
		Username:      username,
		CharacterName: charName,
		Content:       content,
	}
	if err := models.DB.Create(&msg).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "发送桌边消息失败"})
		return
	}

	hubFor(session.ID).Publish("ooc_message", msg)
	c.JSON(http.StatusCreated, msg)
}

const (
	maxClueTitleRunes   = 100
	maxClueContentRunes = 1000
	maxClueSourceRunes  = 100
	maxNoteContentRunes = 1000
)

type handbookLocation struct {
	Name      string   `json:"name"`
	Occupants []string `json:"occupants"`
}

// handbookNPC 只暴露调查员在桌上公开可观察的字段，严禁输出 secret/goal/attitude/stats/skills/race/session_memory。
type handbookNPC struct {
	Name       string `json:"name"`
	Occupation string `json:"occupation,omitempty"`
	Location   string `json:"location,omitempty"`
	IsAlive    bool   `json:"is_alive"`
}

type handbookCombatParticipant struct {
	Name                 string `json:"name"`
	DEX                  int    `json:"dex"`
	IsNPC                bool   `json:"is_npc"`
	HasActed             bool   `json:"has_acted"`
	HasDodgedOrFB        bool   `json:"has_dodged_or_fb"`
	IsAiming             bool   `json:"is_aiming"`
	PendingClarification bool   `json:"pending_clarification"`
	PendingQuestion      string `json:"pending_question,omitempty"`
	IsAlive              bool   `json:"is_alive"`
}

type handbookCombat struct {
	Active       bool                        `json:"active"`
	Round        int                         `json:"round"`
	ActorIndex   int                         `json:"actor_index"`
	Participants []handbookCombatParticipant `json:"participants"`
}

type handbookChaseParticipant struct {
	Name                 string `json:"name"`
	DEX                  int    `json:"dex"`
	MOV                  int    `json:"mov"`
	Location             int    `json:"location"`
	IsNPC                bool   `json:"is_npc"`
	IsPursuer            bool   `json:"is_pursuer"`
	HasActed             bool   `json:"has_acted"`
	PendingClarification bool   `json:"pending_clarification"`
	PendingQuestion      string `json:"pending_question,omitempty"`
}

type handbookChase struct {
	Active       bool                       `json:"active"`
	Round        int                        `json:"round"`
	MinMOV       int                        `json:"min_mov"`
	ActorIndex   int                        `json:"actor_index"`
	Participants []handbookChaseParticipant `json:"participants"`
	Obstacles    []models.ChaseObstacle     `json:"obstacles"`
}

type handbookResponse struct {
	Clues     []models.SessionClue `json:"clues"`
	Notes     []models.SessionNote `json:"notes"`
	Locations []handbookLocation   `json:"locations"`
	NPCs      []handbookNPC        `json:"npcs"`
	Combat    *handbookCombat      `json:"combat"`
	Chase     *handbookChase       `json:"chase"`
}

func resolveMemberAuthorName(session *models.GameSession, c *gin.Context, userID uint) string {
	for _, p := range session.Players {
		if p.UserID == userID && strings.TrimSpace(p.CharacterCard.Name) != "" {
			return p.CharacterCard.Name
		}
	}
	if name := c.GetString("username"); name != "" {
		return name
	}
	var u models.User
	if err := models.DB.Select("username").First(&u, userID).Error; err == nil && u.Username != "" {
		return u.Username
	}
	return "调查员"
}

// GetSessionHandbook 返回面向调查员的防剧透手册数据：线索板、笔记、已到访地点、已登场 NPC 公开投影与实时战斗/追逐状态。
func GetSessionHandbook(c *gin.Context) {
	sessionID, _ := strconv.ParseUint(c.Param("id"), 10, 64)
	userID := c.GetUint("user_id")

	var session models.GameSession
	if err := models.DB.Preload("Players.CharacterCard").Preload("Players.User").First(&session, sessionID).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "房间不存在"})
		return
	}
	if !canViewSession(&session, userID) {
		c.JSON(http.StatusForbidden, gin.H{"error": "无权查看该房间"})
		return
	}
	isAdmin := isAdminUser(c, userID)

	var clues []models.SessionClue
	models.DB.Where("session_id = ?", session.ID).Order("id ASC").Find(&clues)
	if clues == nil {
		clues = []models.SessionClue{}
	}

	var notes []models.SessionNote
	noteQ := models.DB.Where("session_id = ?", session.ID)
	if !isAdmin {
		noteQ = noteQ.Where("is_shared = ? OR user_id = ?", true, userID)
	}
	noteQ.Order("id ASC").Find(&notes)
	if notes == nil {
		notes = []models.SessionNote{}
	}

	// NOTE: 从公开的 event 消息和当前角色位置汇总已到访地点与公开社会关系人物，绝不读取模组未触发设定。
	var eventMsgs []models.Message
	models.DB.Select("meta").Where("session_id = ? AND kind = ?", session.ID, models.MessageKindEvent).Order("id ASC").Find(&eventMsgs)

	locOrder := make([]string, 0)
	locSeen := make(map[string]bool)
	addLoc := func(loc string) {
		loc = strings.TrimSpace(loc)
		if loc == "" || locSeen[loc] {
			return
		}
		locSeen[loc] = true
		locOrder = append(locOrder, loc)
	}
	relationNPCs := make([]string, 0)
	relSeen := make(map[string]bool)
	for _, m := range eventMsgs {
		if m.Meta.Data == nil {
			continue
		}
		for _, ev := range m.Meta.Data.Events {
			if ev.Type == "location" {
				addLoc(ev.Location)
			} else if ev.Type == "item" && ev.Category == "relation" {
				name := strings.TrimSpace(ev.Name)
				if name != "" && !relSeen[name] {
					relSeen[name] = true
					relationNPCs = append(relationNPCs, name)
				}
			}
		}
	}

	occupantsByLoc := make(map[string][]string)
	pcAlive := make(map[string]bool, len(session.Players))
	for _, p := range session.Players {
		charName := strings.TrimSpace(p.CharacterCard.Name)
		if charName == "" {
			charName = p.User.Username
		}
		pcAlive[charName] = p.CharacterCard.WoundState != "dead"
		loc := strings.TrimSpace(p.Location)
		if loc != "" {
			addLoc(loc)
			occupantsByLoc[loc] = append(occupantsByLoc[loc], charName)
		}
	}
	locations := make([]handbookLocation, 0, len(locOrder))
	for _, loc := range locOrder {
		occ := occupantsByLoc[loc]
		if occ == nil {
			occ = []string{}
		}
		locations = append(locations, handbookLocation{Name: loc, Occupants: occ})
	}

	var sessionNPCs []models.SessionNPC
	models.DB.Select("name", "occupation", "location", "is_alive").
		Where("session_id = ?", session.ID).
		Order("id ASC").
		Find(&sessionNPCs)
	npcs := make([]handbookNPC, 0, len(sessionNPCs)+len(relationNPCs))
	npcSeen := make(map[string]bool, len(sessionNPCs))
	npcAlive := make(map[string]bool, len(sessionNPCs))
	for _, n := range sessionNPCs {
		name := strings.TrimSpace(n.Name)
		if name == "" {
			continue
		}
		npcSeen[name] = true
		npcAlive[name] = n.IsAlive
		npcs = append(npcs, handbookNPC{
			Name:       name,
			Occupation: strings.TrimSpace(n.Occupation),
			Location:   strings.TrimSpace(n.Location),
			IsAlive:    n.IsAlive,
		})
	}
	for _, name := range relationNPCs {
		if npcSeen[name] {
			continue
		}
		npcSeen[name] = true
		npcs = append(npcs, handbookNPC{Name: name, IsAlive: true})
	}

	var combat *handbookCombat
	if cs := session.CombatState.Data; cs != nil && cs.Active {
		parts := make([]handbookCombatParticipant, 0, len(cs.Participants))
		for _, p := range cs.Participants {
			alive := true
			if p.IsNPC {
				if v, ok := npcAlive[p.Name]; ok {
					alive = v
				}
			} else if v, ok := pcAlive[p.Name]; ok {
				alive = v
			}
			parts = append(parts, handbookCombatParticipant{
				Name:                 p.Name,
				DEX:                  p.DEX,
				IsNPC:                p.IsNPC,
				HasActed:             p.HasActed,
				HasDodgedOrFB:        p.HasDodgedOrFB,
				IsAiming:             p.IsAiming,
				PendingClarification: p.PendingClarification,
				PendingQuestion:      p.PendingQuestion,
				IsAlive:              alive,
			})
		}
		combat = &handbookCombat{
			Active:       true,
			Round:        cs.Round,
			ActorIndex:   cs.ActorIndex,
			Participants: parts,
		}
	}

	var chase *handbookChase
	if chs := session.ChaseState.Data; chs != nil && chs.Active {
		parts := make([]handbookChaseParticipant, 0, len(chs.Participants))
		for _, p := range chs.Participants {
			parts = append(parts, handbookChaseParticipant{
				Name:                 p.Name,
				DEX:                  p.DEX,
				MOV:                  p.MOV,
				Location:             p.Location,
				IsNPC:                p.IsNPC,
				IsPursuer:            p.IsPursuer,
				HasActed:             p.HasActed,
				PendingClarification: p.PendingClarification,
				PendingQuestion:      p.PendingQuestion,
			})
		}
		obstacles := chs.Obstacles
		if obstacles == nil {
			obstacles = []models.ChaseObstacle{}
		}
		chase = &handbookChase{
			Active:       true,
			Round:        chs.Round,
			MinMOV:       chs.MinMOV,
			ActorIndex:   chs.ActorIndex,
			Participants: parts,
			Obstacles:    obstacles,
		}
	}

	c.JSON(http.StatusOK, handbookResponse{
		Clues:     clues,
		Notes:     notes,
		Locations: locations,
		NPCs:      npcs,
		Combat:    combat,
		Chase:     chase,
	})
}

type createClueReq struct {
	Title   string `json:"title"`
	Content string `json:"content"`
	Source  string `json:"source"`
}

// CreateSessionClue 允许房间成员手工添加或从叙事消息钉选一条线索到全队线索板。
func CreateSessionClue(c *gin.Context) {
	sessionID, _ := strconv.ParseUint(c.Param("id"), 10, 64)
	userID := c.GetUint("user_id")

	var session models.GameSession
	if err := models.DB.Preload("Players.CharacterCard").First(&session, sessionID).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "房间不存在"})
		return
	}
	isAdmin := isAdminUser(c, userID)
	if !isSessionMemberOrHost(&session, userID, isAdmin) {
		c.JSON(http.StatusForbidden, gin.H{"error": "只有房间成员可以添加线索"})
		return
	}

	var req createClueReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	title := strings.TrimSpace(req.Title)
	content := strings.TrimSpace(req.Content)
	source := strings.TrimSpace(req.Source)
	if title == "" || content == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "线索标题和内容不能为空"})
		return
	}
	if utf8.RuneCountInString(title) > maxClueTitleRunes {
		c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("线索标题最多 %d 字", maxClueTitleRunes)})
		return
	}
	if utf8.RuneCountInString(content) > maxClueContentRunes {
		c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("线索内容最多 %d 字", maxClueContentRunes)})
		return
	}
	if utf8.RuneCountInString(source) > maxClueSourceRunes {
		c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("线索来源最多 %d 字", maxClueSourceRunes)})
		return
	}

	uid := userID
	clue := models.SessionClue{
		SessionID: session.ID,
		Title:     title,
		Content:   content,
		Source:    source,
		AddedBy:   resolveMemberAuthorName(&session, c, userID),
		UserID:    &uid,
	}
	if err := models.DB.Create(&clue).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "保存线索失败"})
		return
	}
	hubFor(session.ID).Publish("handbook_update", gin.H{"reason": "clue"})
	c.JSON(http.StatusCreated, clue)
}

// DeleteSessionClue 删除线索板条目：仅添加人、房主或管理员可删除。
func DeleteSessionClue(c *gin.Context) {
	sessionID, _ := strconv.ParseUint(c.Param("id"), 10, 64)
	clueID, _ := strconv.ParseUint(c.Param("clueId"), 10, 64)
	userID := c.GetUint("user_id")

	var session models.GameSession
	if err := models.DB.Preload("Players").First(&session, sessionID).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "房间不存在"})
		return
	}
	var clue models.SessionClue
	if err := models.DB.Where("id = ? AND session_id = ?", clueID, session.ID).First(&clue).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "线索不存在"})
		return
	}
	isAdmin := isAdminUser(c, userID)
	isOwner := clue.UserID != nil && *clue.UserID == userID
	if !isAdmin && session.CreatedBy != userID && !isOwner {
		c.JSON(http.StatusForbidden, gin.H{"error": "只有添加人、房主或管理员可以删除该线索"})
		return
	}
	if err := models.DB.Delete(&clue).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "删除线索失败"})
		return
	}
	hubFor(session.ID).Publish("handbook_update", gin.H{"reason": "clue"})
	c.JSON(http.StatusOK, gin.H{"message": "已删除线索"})
}

type createNoteReq struct {
	Content  string `json:"content"`
	IsShared *bool  `json:"is_shared"`
}

// CreateSessionNote 创建调查笔记（默认全队共享，设为 private 时仅本人和管理员可见）。
func CreateSessionNote(c *gin.Context) {
	sessionID, _ := strconv.ParseUint(c.Param("id"), 10, 64)
	userID := c.GetUint("user_id")

	var session models.GameSession
	if err := models.DB.Preload("Players.CharacterCard").First(&session, sessionID).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "房间不存在"})
		return
	}
	isAdmin := isAdminUser(c, userID)
	if !isSessionMemberOrHost(&session, userID, isAdmin) {
		c.JSON(http.StatusForbidden, gin.H{"error": "只有房间成员可以添加笔记"})
		return
	}

	var req createNoteReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	content := strings.TrimSpace(req.Content)
	if content == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "笔记内容不能为空"})
		return
	}
	if utf8.RuneCountInString(content) > maxNoteContentRunes {
		c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("笔记最多 %d 字", maxNoteContentRunes)})
		return
	}
	shared := true
	if req.IsShared != nil {
		shared = *req.IsShared
	}
	note := models.SessionNote{
		SessionID:  session.ID,
		UserID:     userID,
		AuthorName: resolveMemberAuthorName(&session, c, userID),
		Content:    content,
		IsShared:   shared,
	}
	if err := models.DB.Create(&note).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "保存笔记失败"})
		return
	}
	if shared {
		hubFor(session.ID).Publish("handbook_update", gin.H{"reason": "note"})
	}
	c.JSON(http.StatusCreated, note)
}

// DeleteSessionNote 删除笔记：仅作者、房主或管理员可删除。
func DeleteSessionNote(c *gin.Context) {
	sessionID, _ := strconv.ParseUint(c.Param("id"), 10, 64)
	noteID, _ := strconv.ParseUint(c.Param("noteId"), 10, 64)
	userID := c.GetUint("user_id")

	var session models.GameSession
	if err := models.DB.Preload("Players").First(&session, sessionID).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "房间不存在"})
		return
	}
	var note models.SessionNote
	if err := models.DB.Where("id = ? AND session_id = ?", noteID, session.ID).First(&note).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "笔记不存在"})
		return
	}
	isAdmin := isAdminUser(c, userID)
	if !isAdmin && session.CreatedBy != userID && note.UserID != userID {
		c.JSON(http.StatusForbidden, gin.H{"error": "只有作者、房主或管理员可以删除该笔记"})
		return
	}
	wasShared := note.IsShared
	if err := models.DB.Delete(&note).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "删除笔记失败"})
		return
	}
	if wasShared {
		hubFor(session.ID).Publish("handbook_update", gin.H{"reason": "note"})
	}
	c.JSON(http.StatusOK, gin.H{"message": "已删除笔记"})
}

// ── 结团复盘与模组真相（仅 status=ended 解锁）────────────────────────────────

type debriefDiceStat struct {
	Character string `json:"character"`
	Total     int    `json:"total"`
	Criticals int    `json:"criticals"`
	Fumbles   int    `json:"fumbles"`
	Successes int    `json:"successes"`
	Failures  int    `json:"failures"`
	BestRoll  int    `json:"best_roll"`
	WorstRoll int    `json:"worst_roll"`
}

type debriefNPC struct {
	Name        string `json:"name"`
	Occupation  string `json:"occupation,omitempty"`
	Attitude    string `json:"attitude,omitempty"`
	Description string `json:"description,omitempty"`
}

type debriefPlayerSecret struct {
	CharacterName    string `json:"character_name"`
	Username         string `json:"username,omitempty"`
	SecretBackground string `json:"secret_background"`
}

type debriefTruth struct {
	CoreTruth         string                `json:"core_truth,omitempty"`
	AntagonistDossier string                `json:"antagonist_dossier,omitempty"`
	MythosCore        string                `json:"mythos_core,omitempty"`
	Endings           []models.EndingData   `json:"endings"`
	Clues             []models.ClueData     `json:"clues"`
	NPCs              []debriefNPC          `json:"npcs"`
	PlayerSecrets     []debriefPlayerSecret `json:"player_secrets"`
}

type debriefResponse struct {
	SessionName  string                    `json:"session_name"`
	ScenarioName string                    `json:"scenario_name"`
	EndedAt      time.Time                 `json:"ended_at"`
	Evaluation   *models.EvaluationContent `json:"evaluation"`
	DiceStats    []debriefDiceStat         `json:"dice_stats"`
	CluesFound   int64                     `json:"clues_found"`
	TotalClues   int                       `json:"total_clues"`
	Truth        debriefTruth              `json:"truth"`
}

// GetSessionDebrief 返回已结束房间的结团复盘数据：战绩评估、公开骰运统计、线索收集进度与模组完整真相/全员 HO。
// NOTE: 严格校验 status == ended，未结团时一律返回 400，防止任何角色在跑团中途提前剧透。
func GetSessionDebrief(c *gin.Context) {
	sessionID, _ := strconv.ParseUint(c.Param("id"), 10, 64)
	userID := c.GetUint("user_id")

	var session models.GameSession
	if err := models.DB.
		Preload("Scenario").
		Preload("Players.User").
		Preload("Players.CharacterCard").
		First(&session, sessionID).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "房间不存在"})
		return
	}
	if !canViewSession(&session, userID) {
		c.JSON(http.StatusForbidden, gin.H{"error": "无权查看该房间"})
		return
	}
	if session.Status != models.SessionStatusEnded {
		c.JSON(http.StatusBadRequest, gin.H{"error": "游戏尚未结束，无法查看复盘真相"})
		return
	}

	var evalPtr *models.EvaluationContent
	var eval models.GameEvaluation
	if err := models.DB.Where("session_id = ?", session.ID).Order("id DESC").First(&eval).Error; err == nil {
		content := eval.Content.Data
		evalPtr = &content
	}

	var cluesFound int64
	models.DB.Model(&models.SessionClue{}).Where("session_id = ?", session.ID).Count(&cluesFound)

	var eventMsgs []models.Message
	models.DB.Where("session_id = ? AND kind = ?", session.ID, models.MessageKindEvent).
		Order("id ASC").
		Find(&eventMsgs)
	diceStats := aggregateDebriefDiceStats(eventMsgs)

	sc := session.Scenario.Content.Data
	truth := debriefTruth{
		MythosCore:    strings.TrimSpace(sc.MythosCore),
		Endings:       sc.Endings,
		Clues:         sc.Clues,
		NPCs:          make([]debriefNPC, 0, len(sc.NPCs)),
		PlayerSecrets: make([]debriefPlayerSecret, 0),
	}
	if truth.Endings == nil {
		truth.Endings = []models.EndingData{}
	}
	if truth.Clues == nil {
		truth.Clues = []models.ClueData{}
	}
	if sc.KeeperAppendix != nil {
		truth.CoreTruth = strings.TrimSpace(sc.KeeperAppendix.CoreTruth)
		truth.AntagonistDossier = strings.TrimSpace(sc.KeeperAppendix.AntagonistDossier)
	}
	for _, npc := range sc.NPCs {
		truth.NPCs = append(truth.NPCs, debriefNPC{
			Name:        npc.Name,
			Occupation:  npc.Occupation,
			Attitude:    npc.Attitude,
			Description: npc.Description,
		})
	}
	for _, p := range session.Players {
		secret := strings.TrimSpace(p.SecretBackground)
		if secret == "" {
			continue
		}
		truth.PlayerSecrets = append(truth.PlayerSecrets, debriefPlayerSecret{
			CharacterName:    p.CharacterCard.Name,
			Username:         p.User.Username,
			SecretBackground: secret,
		})
	}

	c.JSON(http.StatusOK, debriefResponse{
		SessionName:  session.Name,
		ScenarioName: session.Scenario.Name,
		EndedAt:      session.UpdatedAt,
		Evaluation:   evalPtr,
		DiceStats:    diceStats,
		CluesFound:   cluesFound,
		TotalClues:   len(sc.Clues),
		Truth:        truth,
	})
}

func aggregateDebriefDiceStats(eventMsgs []models.Message) []debriefDiceStat {
	byChar := make(map[string]*debriefDiceStat)
	for _, msg := range eventMsgs {
		if msg.Meta.Data == nil {
			continue
		}
		for _, ev := range msg.Meta.Data.Events {
			if ev.Type != "dice" || ev.Hidden {
				continue
			}
			charName := strings.TrimSpace(ev.Character)
			if charName == "" {
				continue
			}
			result := strings.TrimSpace(ev.Result)
			// 仅统计带判定目标或检定结果的 D100 检定，排除纯伤害/回复骰（如 1D6）。
			if ev.Target <= 0 && result == "" {
				continue
			}
			st, ok := byChar[charName]
			if !ok {
				st = &debriefDiceStat{Character: charName}
				byChar[charName] = st
			}
			st.Total++
			switch {
			case result == "大成功":
				st.Criticals++
				st.Successes++
			case result == "大失败":
				st.Fumbles++
				st.Failures++
			case strings.Contains(result, "成功"):
				st.Successes++
			case strings.Contains(result, "失败"):
				st.Failures++
			}
			if ev.Roll >= 1 && ev.Roll <= 100 {
				if st.BestRoll == 0 || ev.Roll < st.BestRoll {
					st.BestRoll = ev.Roll
				}
				if ev.Roll > st.WorstRoll {
					st.WorstRoll = ev.Roll
				}
			}
		}
	}
	out := make([]debriefDiceStat, 0, len(byChar))
	for _, st := range byChar {
		out = append(out, *st)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Total != out[j].Total {
			return out[i].Total > out[j].Total
		}
		return out[i].Character < out[j].Character
	})
	return out
}


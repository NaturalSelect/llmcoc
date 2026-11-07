package handlers

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/llmcoc/server/internal/models"
	"github.com/llmcoc/server/internal/services/agent"
)

// NOTE: KP 自动续跑期间一直占着 processing，Writer 卡死会让整个房间无法输入，所以只在续跑时给 Writer 设等待上限。
const kpWriterWaitTimeout = 3 * time.Minute

// kpSegment 是 KP 驱动器要跑的一段。玩家回应段带用户输入；KP 主动段(开场/续跑/房主继续)不带 content。
type kpSegment struct {
	gctx        agent.GameContext
	userID      uint
	displayName string
	content     string
	turnActions []models.SessionTurnAction
}

func kpAutoContinueMax() int {
	n := siteSettingInt("kp_auto_continue_max", 2)
	if n < 0 {
		return 0
	}
	return n
}

// nextSpotlight 计算一段 KP 主流程结束后的发言权。retain=true 表示 KP 仍持有发言权，服务端应自动再跑一段。
func nextSpotlight(prev *models.SpotlightState, ask *agent.AskResult, players []models.SessionPlayer, maxAuto int) (next *models.SpotlightState, retain bool) {
	if ask != nil {
		return &models.SpotlightState{UserIDs: ask.UserIDs, Names: ask.Names, Question: ask.Question}, false
	}
	n := 1
	if prev != nil {
		n = prev.AutoContinues + 1
	}
	if n <= maxAuto {
		return &models.SpotlightState{AutoContinues: n}, true
	}
	open := &models.SpotlightState{}
	for _, p := range players {
		if p.CharacterCard.WoundState == "dead" {
			continue
		}
		open.UserIDs = append(open.UserIDs, p.UserID)
		open.Names = append(open.Names, sessionPlayerDisplayName(p))
	}
	// NOTE: 没有任何存活玩家可以开放时保留计数，由房主用"让 KP 继续"推进，避免无人可点名时反复续跑。
	if len(open.UserIDs) == 0 {
		open.AutoContinues = n
	}
	return open, false
}

// advanceSpotlight 把一段 KP 主流程的结果写进 Spotlight。老房间(Spotlight 为空)和已结束的房间保持原样，返回 nil。
func advanceSpotlight(sessionID uint, output agent.RunOutput) (*models.SpotlightState, bool, error) {
	var session models.GameSession
	if err := models.DB.
		Preload("Players.User").
		Preload("Players.CharacterCard").
		First(&session, sessionID).Error; err != nil {
		return nil, false, err
	}
	prev := session.Spotlight.Data
	if prev == nil || session.Status != models.SessionStatusPlaying {
		return nil, false, nil
	}
	next, retain := nextSpotlight(prev, output.Ask, session.Players, kpAutoContinueMax())
	if err := models.DB.Model(&models.GameSession{}).
		Where("id = ?", sessionID).
		Update("spotlight", models.JSONField[*models.SpotlightState]{Data: next}).Error; err != nil {
		return nil, false, err
	}
	return next, retain, nil
}

func spotlightEventPayload(sp *models.SpotlightState) gin.H {
	ids := sp.UserIDs
	if ids == nil {
		ids = []uint{}
	}
	names := sp.Names
	if names == nil {
		names = []string{}
	}
	return gin.H{"user_ids": ids, "names": names, "question": sp.Question, "kp_floor": len(ids) == 0}
}

// spotlightRejectReason 解释 Spotlight 房间里没被点名的玩家为什么此刻不能发言。
func spotlightRejectReason(session models.GameSession, collect agent.TurnCollection) string {
	if len(collect.UserIDs) == 0 {
		return "KP 正在推进剧情，请稍候"
	}
	named := make(map[uint]bool, len(collect.UserIDs))
	for _, id := range collect.UserIDs {
		named[id] = true
	}
	var names []string
	for _, p := range session.Players {
		if named[p.UserID] {
			names = append(names, sessionPlayerDisplayName(p))
		}
	}
	msg := fmt.Sprintf("KP 正在等待 %s 回应", strings.Join(names, "、"))
	if collect.Question != "" {
		msg += "：" + collect.Question
	}
	return msg
}

func isAdminUser(c *gin.Context, userID uint) bool {
	if c.GetString("role") == string(models.RoleAdmin) {
		return true
	}
	var u models.User
	if err := models.DB.Select("role").First(&u, userID).Error; err != nil {
		return false
	}
	return u.Role == models.RoleAdmin
}

// loadAgentHistory 按时间顺序加载喂给 Agent 的历史消息(不含 system 角色，图片数据已剥离)。
func loadAgentHistory(sessionID uint) []models.Message {
	var msgs []models.Message
	models.DB.Where("session_id = ? AND role != ?", sessionID, models.MessageRoleSystem).
		Order("created_at DESC").
		Find(&msgs)
	stripMessageImageDataURLTags(msgs)
	for i, j := 0, len(msgs)-1; i < j; i, j = i+1, j-1 {
		msgs[i], msgs[j] = msgs[j], msgs[i]
	}
	return msgs
}

// buildPendingActions 把本轮已提交的行动转成喂给 Director 的 PlayerAction。
func buildPendingActions(turnActions []models.SessionTurnAction) []agent.PlayerAction {
	actions := make([]agent.PlayerAction, 0, len(turnActions))
	for _, ta := range turnActions {
		var user models.User
		models.DB.First(&user, ta.UserID)
		actions = append(actions, agent.PlayerAction{
			UserID:     ta.UserID,
			IsAdmin:    user.Role == models.RoleAdmin,
			PlayerName: ta.Username,
			Content:    ta.ActionSummary,
		})
	}
	return actions
}

// loadKPFloorContext 为 KP 自动续跑的下一段重新读取房间状态：上一段的工具调用已经改写了战斗、角色和时间。
func loadKPFloorContext(sessionID uint, floor string) (agent.GameContext, error) {
	var session models.GameSession
	if err := models.DB.
		Preload("Scenario").
		Preload("Players.User").
		Preload("Players.CharacterCard").
		First(&session, sessionID).Error; err != nil {
		return agent.GameContext{}, err
	}
	return agent.GameContext{Session: session, History: loadAgentHistory(sessionID), KPFloor: floor}, nil
}

// buildKPTurnSegment 构造房主/管理员触发的 KP 主动段：还没有任何 KP 段跑过时是开场，否则是房主继续。
func buildKPTurnSegment(session models.GameSession) kpSegment {
	gctx := agent.GameContext{Session: session, History: loadAgentHistory(session.ID), KPFloor: agent.KPFloorHostContinue}

	// NOTE: StartSession 会写一条剧本介绍助手消息，所以"只有介绍、没有玩家发言"才算还没开场。
	var kpCount, userCount int64
	models.DB.Model(&models.Message{}).
		Where("session_id = ? AND role = ?", session.ID, models.MessageRoleAssistant).Count(&kpCount)
	models.DB.Model(&models.Message{}).
		Where("session_id = ? AND role = ?", session.ID, models.MessageRoleUser).Count(&userCount)
	if kpCount <= 1 && userCount == 0 {
		gctx.KPFloor = agent.KPFloorOpening
		return kpSegment{gctx: gctx}
	}

	collect := agent.BuildTurnCollection(session)
	all := loadLatestTurnActions(session.ID, session.TurnRound, session.Players)
	gctx.PendingActions = buildPendingActions(all)
	if len(gctx.PendingActions) == 1 {
		only := gctx.PendingActions[0]
		gctx.UserInput, gctx.UserName, gctx.UserInputAdmin = only.Content, only.PlayerName, only.IsAdmin
	}

	submitted := make(map[uint]bool, len(all))
	for _, ta := range all {
		submitted[ta.UserID] = true
	}
	named := make(map[uint]bool, len(collect.UserIDs))
	for _, id := range collect.UserIDs {
		named[id] = true
	}
	for _, p := range session.Players {
		if named[p.UserID] && !submitted[p.UserID] {
			gctx.SkippedNames = append(gctx.SkippedNames, sessionPlayerDisplayName(p))
		}
	}

	turnActions := all
	if cutoff, ok := lastKPReplyTime(session.ID); ok {
		turnActions = filterTurnActionsSince(all, cutoff)
	}
	return kpSegment{gctx: gctx, turnActions: turnActions}
}

// KPTurn 让房主或管理员触发一段 KP 主动叙事：开场，或不再等待未回应的被点名玩家。
func (h *SessionHandlers) KPTurn(c *gin.Context) {
	sessionID, _ := strconv.ParseUint(c.Param("id"), 10, 64)
	userID := c.GetUint("user_id")

	lock := getSessionLock(uint(sessionID))
	lock.Lock()
	unlocked := false
	defer func() {
		if !unlocked {
			lock.Unlock()
		}
	}()

	var session models.GameSession
	if err := models.DB.
		Preload("Scenario").
		Preload("Players.User").
		Preload("Players.CharacterCard").
		First(&session, sessionID).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "房间不存在"})
		return
	}
	if session.CreatedBy != userID && !isAdminUser(c, userID) {
		c.JSON(http.StatusForbidden, gin.H{"error": "只有房主或管理员可以让 KP 继续"})
		return
	}
	if session.Status != models.SessionStatusPlaying {
		c.JSON(http.StatusBadRequest, gin.H{"error": "游戏尚未开始"})
		return
	}
	if session.Spotlight.Data == nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "该房间由玩家驱动，不支持让 KP 主动推进"})
		return
	}
	seg := buildKPTurnSegment(session)
	if !beginSessionProcessing(session.ID) {
		c.JSON(http.StatusConflict, gin.H{"error": "当前房间正在处理上一条消息，请稍候"})
		return
	}
	lock.Unlock()
	unlocked = true

	chatLog.Debug("kp turn", "session", sessionID, "user", userID, "floor", seg.gctx.KPFloor)
	setChatSSEHeaders(c)
	h.driveKP(c, seg)
}

// runKPSegment 在 goroutine 里跑一段 Director，同时每 600ms 推一次 thinking，并转发真实进度。
func (h *SessionHandlers) runKPSegment(gctx agent.GameContext, send func(string, any)) (agent.RunOutput, error) {
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

	ticker := time.NewTicker(600 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case res := <-resultCh:
			for {
				select {
				case progress := <-progressCh:
					send("progress", progress)
				default:
					return res.output, res.err
				}
			}
		case progress := <-progressCh:
			send("progress", progress)
		case <-ticker.C:
			send("thinking", "")
		}
	}
}

// driveKP 驱动 KP 主流程：跑一段、落库、更新发言权、推流；KP 仍持有发言权时等这一段 Writer 结束后自动再跑一段。
// 调用方必须已经成功 beginSessionProcessing；processing 的释放由 driveKP 负责。
// 客户端断开后循环照常跑完并落库，只是不再写 SSE。
func (h *SessionHandlers) driveKP(c *gin.Context, seg kpSegment) {
	sessionID := seg.gctx.Session.ID
	processingOwned := true
	defer func() {
		if processingOwned {
			finishSessionProcessing(sessionID)
		}
	}()
	// NOTE: 任何一次返回都会关闭它，让还在后台跑的 Writer/Painter 不再阻塞在已结束的推流上。
	driverDone := make(chan struct{})
	defer close(driverDone)

	clientCtx := c.Request.Context()
	send := func(event string, data any) {
		if clientCtx.Err() != nil {
			return
		}
		c.SSEvent(event, data)
		c.Writer.Flush()
	}
	sendChunks := func(event, text string) {
		runes := []rune(text)
		for i := 0; i < len(runes); {
			end := i + 4
			if end > len(runes) {
				end = len(runes)
			}
			send(event, string(runes[i:end]))
			i = end
		}
	}

	for {
		gctx := seg.gctx
		pipelineStart := time.Now()
		chatLog.Debug("chat pipeline start", "session", sessionID, "user", seg.displayName, "floor", gctx.KPFloor, "round", gctx.Session.TurnRound)
		if gctx.KPFloor == "" {
			send("progress", "已收到行动,正在整理本轮信息")
		} else {
			send("progress", "KP正在推进剧情")
		}

		output, err := h.runKPSegment(gctx, send)
		if err != nil {
			chatLog.Error("chat pipeline failed", "session", sessionID, "user", seg.displayName, "elapsed_ms", float64(time.Since(pipelineStart).Milliseconds()), "err", err)
			send("error", err.Error())
			return
		}

		assistantMsg, err := saveChatMessages(uint64(sessionID), seg.userID, seg.displayName, seg.content, seg.turnActions, output)
		if err != nil {
			chatLog.Error("chat save failed", "session", sessionID, "user", seg.displayName, "err", err)
			send("error", "保存消息失败")
			return
		}
		spot, retain, err := advanceSpotlight(sessionID, output)
		if err != nil {
			chatLog.Error("chat spotlight update failed", "session", sessionID, "err", err)
			retain = false
		}
		send("progress", "KP主流程已保存")
		// NOTE: KP主流程结果已落库，立即解除输入锁；Writer/Painter 继续异步更新当前消息。KP 要自动续跑时保持锁，直到下一段开始。
		if !retain {
			finishSessionProcessing(sessionID)
			processingOwned = false
		}

		if output.KPReply != "" {
			sendChunks("narration", output.KPReply)
		}
		if assistantMsg != nil {
			send("kp_done", gin.H{
				"message_id":  assistantMsg.ID,
				"created_at":  assistantMsg.CreatedAt,
				"has_writer":  output.WriterText != "" || output.WriterDirection != "",
				"writer_done": output.WriterText != "" && output.WriterDirection == "",
			})
		} else {
			send("kp_done", gin.H{})
		}
		if spot != nil {
			send("spotlight", spotlightEventPayload(spot))
		}

		var writerCh <-chan writerJobResult
		if assistantMsg != nil {
			writerCh = h.startWriterJob(assistantMsg.ID, gctx, output, driverDone)
		}
		var painterCh <-chan painterJobResult
		if len(output.ImagePrompts) > 0 {
			imageRequest := output.ImagePrompts[0]
			chatLog.Debug("chat painter queued", "session", sessionID, "user", seg.displayName, "prompt_len", len([]rune(imageRequest.Prompt)), "prompt", chatTruncate(imageRequest.Prompt, 200))
			messageID := uint(0)
			if assistantMsg != nil {
				messageID = assistantMsg.ID
			}
			if retain {
				// NOTE: 续跑时不等配图：图片只落库挂到这条消息上，前端后续刷新消息列表时可见，避免 image 事件被前端挂到下一段消息上。
				h.startPainterJob(messageID, gctx, imageRequest, nil)
			} else {
				painterCh = h.startPainterJob(messageID, gctx, imageRequest, driverDone)
			}
		}

		streamedWriter := output.WriterText
		if streamedWriter != "" {
			send("progress", "叙事正文生成中")
			sendChunks("token", streamedWriter)
		}
		if writerCh != nil {
			send("progress", "叙事正文生成中")
		} else if painterCh != nil {
			send("progress", "场景图像生成中")
		}
		clientGone := clientCtx.Done()
		var waitTimeout <-chan time.Time
		var timer *time.Timer
		if retain {
			timer = time.NewTimer(kpWriterWaitTimeout)
			waitTimeout = timer.C
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
					send("token", wr.token)
				}
				if !wr.done {
					continue
				}
				if wr.text != "" {
					streamedWriter = wr.text
				}
				if wr.err != nil {
					chatLog.Error("chat writer async failed", "session", sessionID, "user", seg.displayName, "err", wr.err)
				}
				writerCh = nil
			case pr, ok := <-painterCh:
				if !ok {
					painterCh = nil
					continue
				}
				if pr.err != nil {
					chatLog.Error("chat painter async failed", "session", sessionID, "user", seg.displayName, "err", pr.err)
					painterCh = nil
					continue
				}
				if pr.dataURL != "" {
					send("image", pr.dataURL)
				}
				painterCh = nil
			case <-clientGone:
				if !retain {
					return
				}
				clientGone = nil
			case <-waitTimeout:
				chatLog.Warn("kp writer wait timed out, continuing", "session", sessionID)
				writerCh = nil
				waitTimeout = nil
			}
		}
		if timer != nil {
			timer.Stop()
		}

		if !retain {
			fullReply := buildAssistantContent(streamedWriter, output.KPReply)
			chatLog.Debug("chat done", "session", sessionID, "user", seg.displayName, "tokens", len([]rune(fullReply)), "elapsed_ms", float64(time.Since(pipelineStart).Milliseconds()))
			send("done", "")
			return
		}

		next, err := loadKPFloorContext(sessionID, agent.KPFloorContinue)
		if err != nil {
			chatLog.Error("kp continue context failed", "session", sessionID, "err", err)
			send("error", "加载房间状态失败")
			return
		}
		seg = kpSegment{gctx: next}
	}
}

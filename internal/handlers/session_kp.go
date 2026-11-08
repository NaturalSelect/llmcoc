package handlers

// NOTE: KP 发言权周期（floor）的驱动。一次 Runner.Run 就是一个周期：KP 在里面任意多轮调用工具，每一轮产生的消息
// 通过 gctx.Publish 同步落库并广播，只有 ask_players / end_game 自然结束周期。命令（/chat、/kp-turn、/kp-stop）
// 都是立即返回的 REST 接口，进展全部走房间的 WebSocket。一个周期从开始到结束始终占着 processing，期间所有输入都被拒绝。

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/llmcoc/server/internal/models"
	"github.com/llmcoc/server/internal/services/agent"
	"gorm.io/gorm"
)

// floorShutdownWait 是进程关闭时等待在跑的周期收尾（落库 CommitTail、写发言权）的上限。
const floorShutdownWait = 10 * time.Second

var errFloorBusy = errors.New("room is already running a floor")

var errServerShutdown = errors.New("server shutting down")

// kpSegment 是一个周期的输入。玩家回应周期带本轮提交的行动；KP 主动周期(开场/房主继续/续跑)不带或只带跨轮保留的声明。
type kpSegment struct {
	gctx agent.GameContext
	// turnActions 是需要落成玩家消息的声明(已排除上一条 KP 消息之前就落过消息的旧声明)。
	turnActions []models.SessionTurnAction
}

// reason 是 floor_start 事件里的触发原因。
func (s kpSegment) reason() string {
	if s.gctx.KPFloor == "" {
		return "player"
	}
	return s.gctx.KPFloor
}

// activeFloor 是一个正在跑的周期，kp-stop 和关机通过它取消 ctx。
type activeFloor struct {
	sessionID uint
	startedAt time.Time
	cancel    context.CancelCauseFunc
	done      chan struct{}
	floor     atomic.Int32
	beats     atomic.Int32 // 已经发布的 kp/ask 消息数
}

var activeFloors = struct {
	mu sync.Mutex
	m  map[uint]*activeFloor
}{m: make(map[uint]*activeFloor)}

func registerFloor(af *activeFloor) {
	activeFloors.mu.Lock()
	defer activeFloors.mu.Unlock()
	activeFloors.m[af.sessionID] = af
}

// unregisterFloor 只移除自己：新周期可能已经在同一会话上登记了。
func unregisterFloor(af *activeFloor) {
	activeFloors.mu.Lock()
	defer activeFloors.mu.Unlock()
	if activeFloors.m[af.sessionID] == af {
		delete(activeFloors.m, af.sessionID)
	}
}

func getActiveFloor(sessionID uint) *activeFloor {
	activeFloors.mu.Lock()
	defer activeFloors.mu.Unlock()
	return activeFloors.m[sessionID]
}

// cancelFloor 取消会话正在跑的周期，返回当时是否有周期在跑。
func cancelFloor(sessionID uint, cause error) bool {
	af := getActiveFloor(sessionID)
	if af == nil {
		return false
	}
	af.cancel(cause)
	return true
}

// StopAllFloors 在进程关闭时调用：取消所有在跑的周期（以 StopError 结束并落库尾部），等它们收尾或超时。
func StopAllFloors() {
	activeFloors.mu.Lock()
	floors := make([]*activeFloor, 0, len(activeFloors.m))
	for _, af := range activeFloors.m {
		floors = append(floors, af)
	}
	activeFloors.mu.Unlock()

	for _, af := range floors {
		af.cancel(errServerShutdown)
	}
	deadline := time.After(floorShutdownWait)
	for _, af := range floors {
		select {
		case <-af.done:
		case <-deadline:
			chatLog.Warn("floor did not stop before shutdown deadline", "session", af.sessionID)
			return
		}
	}
}

// nextSpotlight 计算一个周期结束后的发言权：KP 点名了人就交给他们，否则 KP 继续持有并记下被强制结束的原因。
func nextSpotlight(out agent.RunOutput) *models.SpotlightState {
	if out.Ask != nil {
		return &models.SpotlightState{UserIDs: out.Ask.UserIDs, Names: out.Ask.Names, Question: out.Ask.Question}
	}
	return &models.SpotlightState{Stop: string(out.Stop)}
}

// advanceSpotlight 把一个周期的结果写进 Spotlight。已经结束的房间保持原样，返回 nil。
func advanceSpotlight(sessionID uint, out agent.RunOutput) (*models.SpotlightState, error) {
	var status models.SessionStatus
	if err := models.DB.Model(&models.GameSession{}).Where("id = ?", sessionID).Select("status").Scan(&status).Error; err != nil {
		return nil, err
	}
	if status != models.SessionStatusPlaying {
		return nil, nil
	}
	next := nextSpotlight(out)
	if err := models.DB.Model(&models.GameSession{}).
		Where("id = ?", sessionID).
		Update("spotlight", models.JSONField[*models.SpotlightState]{Data: next}).Error; err != nil {
		return nil, err
	}
	return next, nil
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
	return gin.H{"user_ids": ids, "names": names, "question": sp.Question, "kp_floor": len(ids) == 0, "stop": sp.Stop}
}

// spotlightRejectReason 解释没被点名的玩家为什么此刻不能发言。
func spotlightRejectReason(session models.GameSession, collect agent.TurnCollection) string {
	if len(collect.UserIDs) == 0 {
		if sp := session.Spotlight.Data; sp != nil && sp.Stop != "" {
			return "KP 已暂停，请等待房主让 KP 继续"
		}
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
		Order("created_at DESC, id DESC").
		Find(&msgs)
	stripMessageImageDataURLTags(msgs)
	for i, j := 0, len(msgs)-1; i < j; i, j = i+1, j-1 {
		msgs[i], msgs[j] = msgs[j], msgs[i]
	}
	return msgs
}

// buildPendingActions 把已提交的行动转成喂给 Director 的 PlayerAction。
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

// buildKPSegment 构造一个周期的输入。kpFloor 为空是玩家回应周期；all 是每个玩家本轮最新的声明。
// 开场周期没有任何玩家输入。
func buildKPSegment(session models.GameSession, kpFloor string, all []models.SessionTurnAction) kpSegment {
	gctx := agent.GameContext{Session: session, History: loadAgentHistory(session.ID), KPFloor: kpFloor}
	if kpFloor == agent.KPFloorOpening {
		return kpSegment{gctx: gctx}
	}

	gctx.PendingActions = buildPendingActions(all)
	if len(gctx.PendingActions) == 1 {
		only := gctx.PendingActions[0]
		gctx.UserInput, gctx.UserName, gctx.UserInputAdmin = only.Content, only.PlayerName, only.IsAdmin
	}

	collect := agent.BuildTurnCollection(session)
	switch kpFloor {
	case "":
		gctx.AskedQuestion = collect.Question
	case agent.KPFloorHostContinue:
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
		if sp := session.Spotlight.Data; sp != nil {
			gctx.LastStop = agent.StopReason(sp.Stop)
		}
	}

	// NOTE: 续跑周期不重复落玩家消息：这些声明在被消费它们的上一个周期开头已经落过。
	if kpFloor == agent.KPFloorResume {
		return kpSegment{gctx: gctx}
	}
	turnActions := all
	if cutoff, ok := lastKPReplyTime(session.ID); ok {
		turnActions = filterTurnActionsSince(all, cutoff)
	}
	return kpSegment{gctx: gctx, turnActions: turnActions}
}

// hasKPStarted 报告房间里是否已经出现过玩家或 KP 的消息：没有时，KP 主动周期是开场。
func hasKPStarted(sessionID uint) bool {
	var n int64
	models.DB.Model(&models.Message{}).
		Where("session_id = ? AND kind IN ?", sessionID,
			[]models.MessageKind{models.MessageKindPlayer, models.MessageKindWriter, models.MessageKindKP, models.MessageKindAsk}).
		Count(&n)
	return n > 0
}

func loadSessionForFloor(sessionID uint) (models.GameSession, error) {
	var session models.GameSession
	err := models.DB.
		Preload("Scenario").
		Preload("Players.User").
		Preload("Players.CharacterCard").
		First(&session, sessionID).Error
	return session, err
}

// KPTurn 让房主或管理员触发一个 KP 主动周期：开场，或不再等待未回应的被点名玩家。立即返回 202。
func (h *SessionHandlers) KPTurn(c *gin.Context) {
	sessionID, _ := strconv.ParseUint(c.Param("id"), 10, 64)
	userID := c.GetUint("user_id")

	lock := getSessionLock(uint(sessionID))
	lock.Lock()
	defer lock.Unlock()

	session, err := loadSessionForFloor(uint(sessionID))
	if err != nil {
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
	if _, running := getSessionProcessing(session.ID); running {
		c.JSON(http.StatusConflict, gin.H{"error": "KP 正在发言，请稍候"})
		return
	}

	var seg kpSegment
	if hasKPStarted(session.ID) {
		all := loadLatestTurnActions(session.ID, session.TurnRound, session.Players)
		seg = buildKPSegment(session, agent.KPFloorHostContinue, all)
	} else {
		seg = buildKPSegment(session, agent.KPFloorOpening, nil)
	}

	chatLog.Debug("kp turn", "session", sessionID, "user", userID, "floor", seg.gctx.KPFloor)
	switch err := h.startFloor(seg); {
	case errors.Is(err, errFloorBusy):
		c.JSON(http.StatusConflict, gin.H{"error": "KP 正在发言，请稍候"})
	case err != nil:
		chatLog.Error("kp turn start failed", "session", sessionID, "err", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "启动 KP 失败"})
	default:
		c.JSON(http.StatusAccepted, gin.H{"floor_started": true, "reason": seg.reason()})
	}
}

// KPStop 让房主或管理员硬打断 KP 正在进行的周期。立即返回 202，周期收尾后会推送 floor_end(host_stop)。
func (h *SessionHandlers) KPStop(c *gin.Context) {
	sessionID, _ := strconv.ParseUint(c.Param("id"), 10, 64)
	userID := c.GetUint("user_id")

	var session models.GameSession
	if err := models.DB.Select("id", "created_by").First(&session, sessionID).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "房间不存在"})
		return
	}
	if session.CreatedBy != userID && !isAdminUser(c, userID) {
		c.JSON(http.StatusForbidden, gin.H{"error": "只有房主或管理员可以打断 KP"})
		return
	}
	if !cancelFloor(session.ID, agent.ErrHostStop) {
		c.JSON(http.StatusConflict, gin.H{"error": "KP 当前没有在发言"})
		return
	}
	chatLog.Info("kp stop requested", "session", sessionID, "user", userID)
	c.JSON(http.StatusAccepted, gin.H{"message": "已通知 KP 停止"})
}

// startFloor 开始一个周期：占用 processing，把本轮声明落成玩家消息并把发言权收回 KP，然后在后台跑 Director。
// 调用方应持有会话锁。processing 的释放由周期收尾负责。
func (h *SessionHandlers) startFloor(seg kpSegment) error {
	sessionID := seg.gctx.Session.ID
	if !beginSessionProcessing(sessionID) {
		return errFloorBusy
	}
	floor := agent.NextDirectorFloor(sessionID)
	players, err := persistFloorStart(sessionID, floor, seg.turnActions)
	if err != nil {
		finishSessionProcessing(sessionID)
		return err
	}

	ctx, cancel := context.WithCancelCause(context.Background())
	af := &activeFloor{sessionID: sessionID, startedAt: time.Now(), cancel: cancel, done: make(chan struct{})}
	af.floor.Store(int32(floor))
	// NOTE: 先登记再广播：客户端收到 floor_start 立刻调用 kp-stop 时，周期必须已经可以被取消。
	registerFloor(af)

	hub := hubFor(sessionID)
	for _, m := range players {
		hub.Publish("message_new", gin.H{"message": newMessageResponse(m)})
	}
	hub.Publish("spotlight", spotlightEventPayload(&models.SpotlightState{}))
	hub.Publish("floor_start", gin.H{"floor": floor, "reason": seg.reason()})

	go h.runFloor(ctx, af, seg)
	return nil
}

// persistFloorStart 在一个事务里把玩家声明落成消息，并把发言权收回 KP。
func persistFloorStart(sessionID uint, floor int, turnActions []models.SessionTurnAction) ([]models.Message, error) {
	msgs := make([]models.Message, 0, len(turnActions))
	err := models.DB.Transaction(func(tx *gorm.DB) error {
		for _, ta := range turnActions {
			uid := ta.UserID
			m := models.Message{
				SessionID: sessionID,
				UserID:    &uid,
				Role:      models.MessageRoleUser,
				Kind:      models.MessageKindPlayer,
				Status:    models.MessageStatusDone,
				Floor:     floor,
				Content:   ta.ActionSummary,
				Username:  ta.Username,
			}
			if err := tx.Create(&m).Error; err != nil {
				return err
			}
			msgs = append(msgs, m)
		}
		return tx.Model(&models.GameSession{}).Where("id = ?", sessionID).
			Update("spotlight", models.JSONField[*models.SpotlightState]{Data: &models.SpotlightState{}}).Error
	})
	return msgs, err
}

// runFloor 在后台跑一个周期；上下文已满时自动总结后续跑新周期，直到自然结束、被强制结束或续跑没有进展。
func (h *SessionHandlers) runFloor(ctx context.Context, af *activeFloor, seg kpSegment) {
	defer close(af.done)
	defer af.cancel(nil)
	sessionID := af.sessionID

	var out agent.RunOutput
	for {
		out = h.runOneFloor(ctx, af, seg)
		if !shouldResume(ctx, seg, out) {
			break
		}
		hubFor(sessionID).Publish("floor_end", floorEndPayload(int(af.floor.Load()), out))

		session, err := loadSessionForFloor(sessionID)
		if err != nil {
			chatLog.Error("kp resume context failed", "session", sessionID, "err", err)
			out.Stop = agent.StopError
			break
		}
		seg = buildKPSegment(session, agent.KPFloorResume, loadLatestTurnActions(sessionID, session.TurnRound, session.Players))
		floor := agent.NextDirectorFloor(sessionID)
		af.floor.Store(int32(floor))
		hubFor(sessionID).Publish("floor_start", gin.H{"floor": floor, "reason": seg.reason()})
	}
	h.finishFloor(af, out)
}

func (h *SessionHandlers) runOneFloor(ctx context.Context, af *activeFloor, seg kpSegment) agent.RunOutput {
	sessionID := af.sessionID
	gctx := seg.gctx
	gctx.Progress = func(text string) {
		if text = strings.TrimSpace(text); text != "" {
			hubFor(sessionID).Ephemeral("kp_status", gin.H{"text": text})
		}
	}
	gctx.Publish = func(ro agent.RoundOutputs) error { return h.publishRound(ctx, af, ro) }

	start := time.Now()
	out, err := h.Runner.Run(ctx, gctx)
	if err != nil {
		chatLog.Error("kp floor failed", "session", sessionID, "elapsed_ms", float64(time.Since(start).Milliseconds()), "err", err)
		if out.Stop == agent.StopNone {
			out.Stop = agent.StopError
		}
	}
	chatLog.Debug("kp floor done", "session", sessionID, "stop", out.Stop, "messages", out.Messages,
		"elapsed_ms", float64(time.Since(start).Milliseconds()))
	return out
}

// shouldResume 判断上下文已满的周期是否应该自动续跑：续跑周期如果一条消息都没产生就又溢出，说明总结也救不了，不再续跑。
func shouldResume(ctx context.Context, seg kpSegment, out agent.RunOutput) bool {
	if out.Stop != agent.StopContext || ctx.Err() != nil {
		return false
	}
	return !(seg.gctx.KPFloor == agent.KPFloorResume && out.Messages == 0)
}

func floorEndReason(out agent.RunOutput) string {
	switch {
	case out.Ended:
		return "end_game"
	case out.Ask != nil:
		return "ask"
	case out.Stop != agent.StopNone:
		return string(out.Stop)
	}
	return "done"
}

func floorEndPayload(floor int, out agent.RunOutput) gin.H {
	return gin.H{"floor": floor, "reason": floorEndReason(out), "messages": out.Messages}
}

// NOTE: session 事件只是信号，客户端收到后重新拉取 GET /api/sessions/:id；reason 说明触发原因。
func publishSessionChanged(sessionID uint, status models.SessionStatus, reason string) {
	hubFor(sessionID).Publish("session", gin.H{"status": status, "reason": reason})
}

// finishFloor 写入新的发言权、释放 processing，之后才广播：客户端看到 spotlight 点了自己就会立刻发言，
// 此时 processing 必须已经释放，否则会被 409 拒绝。
func (h *SessionHandlers) finishFloor(af *activeFloor, out agent.RunOutput) {
	sessionID := af.sessionID
	spot, err := advanceSpotlight(sessionID, out)
	if err != nil {
		chatLog.Error("kp spotlight update failed", "session", sessionID, "err", err)
	}
	unregisterFloor(af)
	finishSessionProcessing(sessionID)

	hub := hubFor(sessionID)
	if spot != nil {
		hub.Publish("spotlight", spotlightEventPayload(spot))
	}
	hub.Publish("floor_end", floorEndPayload(int(af.floor.Load()), out))

	// NOTE: end_game 在周期内已把房间改成 ended；周期结束后再发一次，客户端据此刷新状态和人物卡。
	status := models.SessionStatusPlaying
	if out.Ended {
		status = models.SessionStatusEnded
	}
	publishSessionChanged(sessionID, status, "cards")
}

// roundItem 是一轮里待落库的一条消息和产生它的 Output(事件消息没有 Output)。
type roundItem struct {
	msg  models.Message
	item agent.Output
}

// publishRound 是注入给 Director 的 gctx.Publish：同步把一轮产生的消息落库并广播，writer/image 消息交给后台任务补全。
// kp/ask 要等会话里已入队的 writer 全部写完才落库广播：玩家先读到叙事，再看到 KP 的话和提问。
// 等待期间 Director 停在这一轮；周期被取消(房主打断、关机)时不再等，直接发布。
// 落库失败时返回 err，周期随之以 StopError 结束。
func (h *SessionHandlers) publishRound(ctx context.Context, af *activeFloor, ro agent.RoundOutputs) error {
	sessionID := af.sessionID
	var first, beats []roundItem
	// NOTE: 事件消息先落库，id 比同轮其他消息小，前端按 id 排序时就排在这一轮叙事前面。
	if msg, ok := roundEventMessage(sessionID, ro); ok {
		first = append(first, roundItem{msg: msg})
	}
	for _, it := range ro.Items {
		msg, ok := roundMessage(sessionID, ro, it)
		if !ok {
			continue
		}
		if msg.Kind == models.MessageKindKP || msg.Kind == models.MessageKindAsk {
			beats = append(beats, roundItem{msg: msg, item: it})
			// NOTE: 耗时和轮数只属于这一轮的第一条 kp/ask，后面的不重复记。
			ro.Steps = 0
			continue
		}
		first = append(first, roundItem{msg: msg, item: it})
	}

	if err := h.saveAndBroadcast(sessionID, first, ro); err != nil {
		return err
	}
	if len(beats) == 0 {
		if eventsTouchCards(ro.Events) {
			publishCardsChanged(sessionID)
		}
		return nil
	}
	waitWritersForBeat(ctx, sessionID)
	if err := h.saveAndBroadcast(sessionID, beats, ro); err != nil {
		return err
	}
	af.beats.Add(int32(len(beats)))
	// NOTE: 每一拍结算后人物卡（HP/SAN 等）可能已变化，通知客户端刷新。
	publishCardsChanged(sessionID)
	return nil
}

// waitWritersForBeat 等会话里已入队的 writer 全部结束；ctx 取消时提前返回。
func waitWritersForBeat(ctx context.Context, sessionID uint) {
	done := writersDone(sessionID)
	if done == nil {
		return
	}
	select {
	case <-done:
		return
	default:
	}
	hubFor(sessionID).Ephemeral("kp_status", gin.H{"text": "正在撰写叙事"})
	select {
	case <-done:
	case <-ctx.Done():
	}
}

// publishCardsChanged 通知客户端刷新人物卡。end_game 的工具在发布前已把房间改成 ended，所以状态从库里读，不能写死 playing。
func publishCardsChanged(sessionID uint) {
	var status models.SessionStatus
	if err := models.DB.Model(&models.GameSession{}).Where("id = ?", sessionID).Select("status").Scan(&status).Error; err != nil || status == "" {
		status = models.SessionStatusPlaying
	}
	publishSessionChanged(sessionID, status, "cards")
}

// saveAndBroadcast 在一个事务里落库这批消息，再逐条广播 message_new，并把 writer/image 交给后台任务。
func (h *SessionHandlers) saveAndBroadcast(sessionID uint, items []roundItem, ro agent.RoundOutputs) error {
	if len(items) == 0 {
		return nil
	}
	err := models.DB.Transaction(func(tx *gorm.DB) error {
		for i := range items {
			if err := tx.Create(&items[i].msg).Error; err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("save round messages: %w", err)
	}

	hub := hubFor(sessionID)
	for _, p := range items {
		hub.Publish("message_new", gin.H{"message": newMessageResponse(p.msg)})
		switch p.msg.Kind {
		case models.MessageKindWriter:
			h.enqueueWriter(sessionID, writerTask{messageID: p.msg.ID, gctx: ro.WriterCtx, direction: strings.TrimSpace(p.item.Text), nsfw: p.item.NSFW})
		case models.MessageKindImage:
			go h.runPainterTask(p.msg.ID, ro.WriterCtx, *p.item.Image)
		}
	}
	return nil
}

// roundMessage 把一次产生消息的工具调用变成一条待落库的消息；没有内容可发的调用返回 false。
func roundMessage(sessionID uint, ro agent.RoundOutputs, it agent.Output) (models.Message, bool) {
	msg := models.Message{
		SessionID: sessionID,
		Role:      models.MessageRoleAssistant,
		Status:    models.MessageStatusDone,
		Floor:     ro.Floor,
		Username:  "KP",
	}
	meta := &models.MessageMeta{}
	switch it.Kind {
	case agent.OutputWriter:
		if strings.TrimSpace(it.Text) == "" {
			return msg, false
		}
		msg.Kind = models.MessageKindWriter
		msg.Status = models.MessageStatusPending
		meta.NSFW, meta.Auto = it.NSFW, it.Auto
	case agent.OutputImage:
		if it.Image == nil || strings.TrimSpace(it.Image.Prompt) == "" {
			return msg, false
		}
		msg.Kind = models.MessageKindImage
		msg.Status = models.MessageStatusPending
		meta.NSFW = it.NSFW || it.Image.NSFW
	case agent.OutputKP:
		msg.Kind = models.MessageKindKP
		msg.Content = strings.TrimSpace(it.Text)
		meta.TimePoint, meta.Ended = it.TimePoint, it.Ended
		setDirectorTiming(&msg, ro)
	case agent.OutputAsk:
		msg.Kind = models.MessageKindAsk
		msg.Content = strings.TrimSpace(it.Text)
		meta.TimePoint, meta.Options = it.TimePoint, it.Options
		if it.Ask != nil {
			meta.Ask = &models.AskMeta{UserIDs: it.Ask.UserIDs, Names: it.Ask.Names}
		}
		setDirectorTiming(&msg, ro)
	default:
		return msg, false
	}
	msg.Meta = models.JSONField[*models.MessageMeta]{Data: meta}
	return msg, true
}

// roundEventMessage 把一轮的工具事件打包成一条 event 消息；没有事件时返回 false。
func roundEventMessage(sessionID uint, ro agent.RoundOutputs) (models.Message, bool) {
	if len(ro.Events) == 0 {
		return models.Message{}, false
	}
	return models.Message{
		SessionID: sessionID,
		Role:      models.MessageRoleAssistant,
		Kind:      models.MessageKindEvent,
		Status:    models.MessageStatusDone,
		Floor:     ro.Floor,
		Username:  "KP",
		Meta:      models.JSONField[*models.MessageMeta]{Data: &models.MessageMeta{Events: ro.Events}},
	}, true
}

// eventsTouchCards 判断事件里是否有会改变人物卡或房间状态的结算(骰子和时间推进不算)。
func eventsTouchCards(events []models.GameEvent) bool {
	for _, ev := range events {
		if ev.Type != "dice" && ev.Type != "time" {
			return true
		}
	}
	return false
}

// setDirectorTiming 把从上一条 kp/ask 到这一条的耗时与轮数记在消息上；一轮里有多条 kp/ask 时只记在第一条。
func setDirectorTiming(msg *models.Message, ro agent.RoundOutputs) {
	if ro.Steps <= 0 {
		return
	}
	elapsed, steps := ro.ElapsedMs, ro.Steps
	msg.DirectorElapsedMs, msg.DirectorSteps = &elapsed, &steps
}

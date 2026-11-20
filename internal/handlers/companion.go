// NOTE: companion.go 调度房间里的 AI 陪玩。KP 的周期结束后如果点名了 bot 用户，就在后台启动一次 agent.RunCompanion；
// AI 通过 companionEnv 看到的房间内容和提交的回应，都走与真人相同的核心函数（submitTurn、postOOC、createNote、buildHandbook），
// 所以可见性过滤、点名闸门、广播都不另写一套。任务登记仿照 activeFloors：房主点继续、结束游戏、关机时取消。
package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/llmcoc/server/internal/logging"
	"github.com/llmcoc/server/internal/models"
	"github.com/llmcoc/server/internal/services/agent"
)

var companionLog = logging.For("companion")

const (
	// companionJobTimeout 是一次陪玩运行的上限；超时按「没有给出回应」兜底提交，不会卡住房间。
	companionJobTimeout = 5 * time.Minute
	// companionShutdownWait 是进程关闭时等待陪玩任务收尾的上限。
	companionShutdownWait = 10 * time.Second
	// companionMessageBatch 是 Messages 每次从库里多取的条数下限，用来补偿被可见性过滤掉的密讯。
	companionMessageBatch = 50
)

// errCompanionSkipped 是房主点继续或结束游戏时取消陪玩任务的原因：此时房间状态已被别处接管，不做兜底提交。
var errCompanionSkipped = errors.New("companion skipped: the host took over the room")

// companionJob 是一个房间的陪玩任务：可能连续跑几次（运行期间又被点名就再来一遍），整体登记在 activeCompanions 里。
type companionJob struct {
	sessionID uint
	status    string
	done      chan struct{}

	// NOTE: 以下字段由 activeCompanions.mu 保护。
	cancel  context.CancelCauseFunc // 当前这次运行的取消函数
	rerun   bool                    // 运行期间房间又点名了 AI：这次跑完再来一遍
	stopped bool                    // 进程正在关闭：不再重跑
}

// newRunCtx 为一次运行创建上下文并登记它的取消函数，调用方用完调 release。
// NOTE: 超时的 cause 是 context.DeadlineExceeded，agent 据此区分「超时要兜底」和「被接管不兜底」。
func (j *companionJob) newRunCtx() (ctx context.Context, release func()) {
	base, cancel := context.WithCancelCause(context.Background())
	ctx, stop := context.WithTimeout(base, companionJobTimeout)
	activeCompanions.mu.Lock()
	j.cancel = cancel
	activeCompanions.mu.Unlock()
	return ctx, func() { stop(); cancel(nil) }
}

var activeCompanions = struct {
	mu sync.Mutex
	m  map[uint]*companionJob
}{m: make(map[uint]*companionJob)}

// registerCompanion 登记任务。同一房间已有任务在跑时，不新建，只给它记一次「跑完再来一遍」并返回 false：
// 否则 AI 刚提交完、新一轮又点了它，而上一次运行还没退出时，这次点名会被丢掉。
func registerCompanion(job *companionJob) bool {
	activeCompanions.mu.Lock()
	defer activeCompanions.mu.Unlock()
	if existing := activeCompanions.m[job.sessionID]; existing != nil {
		existing.rerun = true
		return false
	}
	activeCompanions.m[job.sessionID] = job
	return true
}

// settleCompanion 在一次运行结束后调用：有待办的重跑就返回 true（任务继续留在登记表里）；
// 否则移除任务并在同一把锁里广播状态清空，这样紧接着登记的新任务发出的状态不会被这条清空盖掉。
func settleCompanion(job *companionJob) (again bool) {
	activeCompanions.mu.Lock()
	defer activeCompanions.mu.Unlock()
	if job.rerun && !job.stopped {
		job.rerun = false
		return true
	}
	if activeCompanions.m[job.sessionID] == job {
		delete(activeCompanions.m, job.sessionID)
	}
	publishCompanionStatus(job.sessionID, "")
	return false
}

// cancelCompanion 取消房间正在跑的陪玩任务，返回当时是否有任务在跑。不等待任务退出。
func cancelCompanion(sessionID uint, cause error) bool {
	activeCompanions.mu.Lock()
	defer activeCompanions.mu.Unlock()
	job := activeCompanions.m[sessionID]
	if job == nil {
		return false
	}
	if job.cancel != nil {
		job.cancel(cause)
	}
	return true
}

// companionStatusText 返回房间此刻应显示的陪玩状态文案，没有任务在跑时为空串。
func companionStatusText(sessionID uint) string {
	activeCompanions.mu.Lock()
	defer activeCompanions.mu.Unlock()
	if job := activeCompanions.m[sessionID]; job != nil {
		return job.status
	}
	return ""
}

// StopAllCompanions 在进程关闭时调用：取消所有陪玩任务（不兜底），等它们收尾或超时。
func StopAllCompanions() {
	activeCompanions.mu.Lock()
	jobs := make([]*companionJob, 0, len(activeCompanions.m))
	for _, job := range activeCompanions.m {
		job.stopped = true
		if job.cancel != nil {
			job.cancel(errServerShutdown)
		}
		jobs = append(jobs, job)
	}
	activeCompanions.mu.Unlock()

	deadline := time.After(companionShutdownWait)
	for _, job := range jobs {
		select {
		case <-job.done:
		case <-deadline:
			companionLog.Warn("companion did not stop before shutdown deadline", "session", job.sessionID)
			return
		}
	}
}

func (h *SessionHandlers) companionRunner() agent.CompanionRunner {
	if h.Companion != nil {
		return h.Companion
	}
	return agent.DefaultCompanion{}
}

func publishCompanionStatus(sessionID uint, text string) {
	hubFor(sessionID).Ephemeral("companion_status", gin.H{"text": text})
}

// spotlightNamesBot 报告发言权是否在 bot 手里。
func spotlightNamesBot(sp *models.SpotlightState, botID uint) bool {
	return sp != nil && botID != 0 && slices.Contains(sp.UserIDs, botID)
}

// startCompanion 在后台启动一次陪玩运行。调用方保证处理锁已释放（finishFloor 里在广播发言权之后调用），
// 这样 AI 看到的是已放完的完整正文，提交也不会撞上 409。同一房间已有任务时只记一次重跑，不并行。
func (h *SessionHandlers) startCompanion(sessionID uint) {
	botID := models.CompanionUserID()
	if botID == 0 {
		return
	}
	session, err := loadSessionForFloor(sessionID)
	if err != nil {
		companionLog.Error("companion start: load session failed", "session", sessionID, "err", err)
		return
	}
	if session.Status != models.SessionStatusPlaying || !spotlightNamesBot(session.Spotlight.Data, botID) {
		return
	}
	name := "AI 陪玩"
	for _, p := range session.Players {
		if p.UserID == botID {
			name = sessionPlayerDisplayName(p)
			break
		}
	}

	job := &companionJob{
		sessionID: sessionID,
		status:    fmt.Sprintf("%s 正在思考…", name),
		done:      make(chan struct{}),
	}
	if !registerCompanion(job) {
		companionLog.Debug("companion already running, queued a rerun", "session", sessionID)
		return
	}
	// NOTE: 取消函数在登记之后、起协程之前就位，这样登记完立刻被取消也能生效。
	ctx, release := job.newRunCtx()
	go h.runCompanionJob(ctx, release, job, botID)
}

func (h *SessionHandlers) runCompanionJob(ctx context.Context, release func(), job *companionJob, botID uint) {
	defer close(job.done)
	publishCompanionStatus(job.sessionID, job.status)
	for {
		h.runCompanionOnce(ctx, job, botID)
		release()
		if !settleCompanion(job) {
			return
		}
		ctx, release = job.newRunCtx()
	}
}

func (h *SessionHandlers) runCompanionOnce(ctx context.Context, job *companionJob, botID uint) {
	start := time.Now()
	env := &companionEnv{h: h, sessionID: job.sessionID, botID: botID}
	err := h.companionRunner().RunCompanion(ctx, env)
	switch {
	case err == nil:
		companionLog.Debug("companion run done", "session", job.sessionID, "elapsed_ms", time.Since(start).Milliseconds())
	case errors.Is(err, errCompanionSkipped), errors.Is(err, errServerShutdown),
		errors.Is(err, agent.ErrCompanionNotAsked), errors.Is(err, agent.ErrCompanionBusy):
		companionLog.Info("companion run stopped", "session", job.sessionID, "reason", err)
	default:
		companionLog.Error("companion run failed", "session", job.sessionID, "err", err)
	}
}

// ResumeCompanions 在进程启动时调用：重启前正在等 AI 回应的房间，重新触发一次陪玩运行。
func (h *SessionHandlers) ResumeCompanions() {
	botID := models.CompanionUserID()
	if botID == 0 {
		return
	}
	var ids []uint
	if err := models.DB.Model(&models.GameSession{}).Where("status = ?", models.SessionStatusPlaying).Pluck("id", &ids).Error; err != nil {
		companionLog.Error("companion resume: scan sessions failed", "err", err)
		return
	}
	for _, id := range ids {
		if _, running := getSessionProcessing(id); running {
			continue
		}
		h.startCompanion(id)
	}
}

// companionEnv 把房间的一个 bot 席位包装成 agent.CompanionEnv。每个方法都现读库，不缓存。
type companionEnv struct {
	h         *SessionHandlers
	sessionID uint
	botID     uint
}

var _ agent.CompanionEnv = (*companionEnv)(nil)

func (e *companionEnv) loadSession() (models.GameSession, error) {
	return loadSessionForFloor(e.sessionID)
}

func (e *companionEnv) View() (agent.CompanionView, error) {
	session, err := e.loadSession()
	if err != nil {
		return agent.CompanionView{}, err
	}
	// NOTE: 发言权已不在 AI 手里（房主点了继续、房间结束等）时不必开始，返回 ErrCompanionNotAsked，agent 不会兜底。
	if session.Status != models.SessionStatusPlaying || !spotlightNamesBot(session.Spotlight.Data, e.botID) {
		return agent.CompanionView{}, agent.ErrCompanionNotAsked
	}

	view := agent.CompanionView{
		SessionID:     session.ID,
		ScenarioName:  session.Scenario.Name,
		ScenarioIntro: session.Scenario.Content.Data.Intro,
		GameTime:      session.GameTime,
		Combat:        session.CombatState.Data,
		Chase:         session.ChaseState.Data,
	}
	seated := false
	for _, p := range session.Players {
		if p.UserID == e.botID {
			view.Self = p
			seated = true
			continue
		}
		view.Party = append(view.Party, p)
	}
	if !seated {
		return agent.CompanionView{}, agent.ErrCompanionNotAsked
	}

	var ask models.Message
	if err := models.DB.Where("session_id = ? AND kind = ?", session.ID, models.MessageKindAsk).
		Order("id DESC").First(&ask).Error; err == nil {
		view.Ask = agent.CompanionAsk{MessageID: ask.ID, Question: ask.Content}
		if ask.Meta.Data != nil {
			view.Ask.Options = ask.Meta.Data.Options
		}
	}
	if view.Ask.Question == "" {
		view.Ask.Question = session.Spotlight.Data.Question
	}
	return view, nil
}

// Messages 返回 (afterID, beforeID) 开区间内 bot 看得到的消息，按 id 升序；limit>0 时只取最新的 limit 条。
// 可见性与真人的 GetMessages 一致：排除 system、尚未广播的消息，密讯按接收者过滤；另外排除还在写的 writer。
func (e *companionEnv) Messages(afterID, beforeID uint, limit int) ([]models.Message, error) {
	session, err := e.loadSession()
	if err != nil {
		return nil, err
	}
	held := heldMessageIDs(e.sessionID)

	var visible []models.Message
	cursor := beforeID
	batch := max(limit*2, companionMessageBatch)
	for {
		q := models.DB.Where("session_id = ? AND role != ? AND id > ?", e.sessionID, models.MessageRoleSystem, afterID).
			Where("NOT (kind = ? AND status = ?)", models.MessageKindWriter, models.MessageStatusPending)
		if cursor > 0 {
			q = q.Where("id < ?", cursor)
		}
		if len(held) > 0 {
			q = q.Where("id NOT IN ?", held)
		}
		if limit <= 0 {
			var all []models.Message
			if err := q.Order("id ASC").Find(&all).Error; err != nil {
				return nil, err
			}
			for _, m := range all {
				if canSeeMessage(m, &session, e.botID, false) {
					visible = append(visible, m)
				}
			}
			break
		}
		var page []models.Message
		if err := q.Order("id DESC").Limit(batch).Find(&page).Error; err != nil {
			return nil, err
		}
		for _, m := range page {
			if canSeeMessage(m, &session, e.botID, false) {
				visible = append(visible, m)
			}
		}
		if len(visible) >= limit || len(page) < batch {
			break
		}
		cursor = page[len(page)-1].ID
	}
	if limit > 0 {
		// 此时 visible 是按 id 降序累积的。
		if len(visible) > limit {
			visible = visible[:limit]
		}
		slices.Reverse(visible)
	}

	stripMessageImageDataURLTags(visible)
	for i := range visible {
		// NOTE: 导演指令可能含模组秘密，只有管理员接口能读；玩家（含 AI）拿到的消息里不带它。
		visible[i].WriterDirection = ""
		// NOTE: 幻觉耳语的标记只有管理员和复盘能看到；玩家（含 AI）读到的只是一条普通密讯。
		if meta := visible[i].Meta.Data; meta != nil && meta.Hallucination {
			copied := *meta
			copied.Hallucination = false
			visible[i].Meta.Data = &copied
		}
	}
	return visible, nil
}

func (e *companionEnv) Handbook() (string, error) {
	session, err := e.loadSession()
	if err != nil {
		return "", err
	}
	raw, err := json.Marshal(buildHandbook(session, e.botID, false))
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

func (e *companionEnv) OOC(limit int) ([]models.SessionOOCMessage, error) {
	if limit <= 0 || limit > oocHistoryLimit {
		limit = oocHistoryLimit
	}
	var msgs []models.SessionOOCMessage
	if err := models.DB.Where("session_id = ?", e.sessionID).Order("id DESC").Limit(limit).Find(&msgs).Error; err != nil {
		return nil, err
	}
	slices.Reverse(msgs)
	return msgs, nil
}

// Submit 与真人一样先过输入分类器，再走相同的 submitTurn。点名已变化或房主已让 KP 接管时返回 ErrCompanionNotAsked，
// 被分类器拦下时返回 ErrCompanionInputBlocked（不落库，AI 可改写重提）。
func (e *companionEnv) Submit(content string) error {
	// NOTE: AI 的输出来自 LLM，对抗性的模组文本或其他玩家的发言可能诱导它说出越权内容；bot 不是管理员，没有跳过分类的情形。
	if v := e.h.classify(context.Background(), e.sessionID, e.botID, content); v.Blocked {
		companionLog.Warn("companion input blocked by input guard", "session", e.sessionID, "category", v.Category, "score", v.Score)
		return agent.ErrCompanionInputBlocked
	}
	_, _, terr := e.h.submitTurn(e.sessionID, e.botID, "", content)
	if terr == nil {
		return nil
	}
	// NOTE: 409 表示 KP 正在发言（房主点了继续），此时 AI 同样已经不在发言权里。
	if terr.notAsked || terr.status == http.StatusConflict {
		return agent.ErrCompanionNotAsked
	}
	return terr
}

func (e *companionEnv) WriteNote(content string, shared bool) error {
	session, err := e.loadSession()
	if err != nil {
		return err
	}
	if _, terr := createNote(session, e.botID, content, shared); terr != nil {
		return terr
	}
	return nil
}

func (e *companionEnv) PostOOC(content string) error {
	session, err := e.loadSession()
	if err != nil {
		return err
	}
	if _, terr := postOOC(session, e.botID, "", content); terr != nil {
		return terr
	}
	return nil
}

package handlers

// NOTE: AI 卡池批量生成：GET /api/admin/companion-cards/generate/ws。传输协议与模组生成相同（见 scenario_ws.go）：
// 连接后第一帧发 {count} 启动任务，之后服务端推 card_start / card_done / card_error / batch_done，请求被拒推 rejected。
// 性别、年龄、职业都随机；姓名走取名接口并保证是中文；其余沿用 AI 车卡流程（generateCharacterCard）。卡直接记在 bot 名下，
// 不占任何管理员的卡槽。任务不依附连接：客户端中途断开不影响它，照常跑完并入库。
// 事件记在进程内存的 companionGen 里，页面刷新后发 {attach:true} 重新接上：服务端先推一帧 snapshot（任务是否还在跑加
// 已发生的全部事件），任务还在跑就继续推后续事件，直到结束；没有任务时只回放上一批的记录。记录不落库，重启即清空。

import (
	"context"
	"fmt"
	"math/rand/v2"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gin-gonic/gin/binding"
	"github.com/llmcoc/server/internal/models"
	"github.com/llmcoc/server/internal/services/agent"
	"github.com/llmcoc/server/internal/services/game"
)

const (
	maxCompanionGenCount = 10
	// 每张卡至少有叙述和技能两次 LLM 调用，限制并发避免一次占满 Provider。
	companionGenConcurrency = 3
	// 单张卡的整体超时，防止某次调用卡死后一直占着「已有任务在运行」。
	companionGenCardTimeout = 10 * time.Minute
	companionGenMinAge      = 20
	companionGenMaxAge      = 50
)

// pickCompanionCardName 是取名入口，测试里替换。
var pickCompanionCardName = agent.PickCompanionCardName

// 每张卡最多推 card_start 和 card_done / card_error 两帧，外加一帧 batch_done。订阅者的队列按这个上限留足，
// publish 因此不会被慢连接堵住；新增事件类型时要同步调整。
const companionGenSubBuffer = 2*maxCompanionGenCount + 1

// companionGenFrame 是任务推送过的一帧。At 是毫秒时间戳，只在 snapshot 里带给客户端，用来还原日志行的时间。
type companionGenFrame struct {
	Type string `json:"type"`
	Data any    `json:"data"`
	At   int64  `json:"at"`
}

type companionGenSnapshot struct {
	Running bool                `json:"running"`
	Events  []companionGenFrame `json:"events"`
}

// companionGenState 是当前（或最近一次）批量任务的内存状态。running 同时充当「同一时刻只有一个批量任务」的闸门，
// 防止重复点击或多个管理员叠加。任务结束后 events 保留到下一次启动，刷新页面还能看到上一批的结果。
type companionGenState struct {
	mu      sync.Mutex
	running bool
	events  []companionGenFrame
	subs    map[chan companionGenFrame]struct{}
}

var companionGen companionGenState

// tryStart 抢占任务位并清掉上一批的记录；已有任务在跑返回 false。
func (s *companionGenState) tryStart() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.running {
		return false
	}
	s.running = true
	s.events = nil
	return true
}

// publish 记录一帧并分发给所有订阅者，可直接当作 scenarioEmit 使用。
func (s *companionGenState) publish(name string, data any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	frame := companionGenFrame{Type: name, Data: data, At: time.Now().UnixMilli()}
	s.events = append(s.events, frame)
	for ch := range s.subs {
		select {
		case ch <- frame:
		default:
			close(ch)
			delete(s.subs, ch)
		}
	}
}

// finish 释放任务位并关闭所有订阅者的通道，订阅者读完队列里剩下的帧后退出。
func (s *companionGenState) finish() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.running = false
	for ch := range s.subs {
		close(ch)
	}
	s.subs = nil
}

// subscribe 在同一把锁里取快照并登记订阅，所以快照之后的帧一帧不漏、也不重复。
// 没有任务在跑时返回的通道已关闭。cancel 可重复调用。
func (s *companionGenState) subscribe() (snap companionGenSnapshot, ch <-chan companionGenFrame, cancel func()) {
	s.mu.Lock()
	defer s.mu.Unlock()
	snap = companionGenSnapshot{Running: s.running, Events: make([]companionGenFrame, len(s.events))}
	copy(snap.Events, s.events)
	sub := make(chan companionGenFrame, companionGenSubBuffer)
	if !s.running {
		close(sub)
		return snap, sub, func() {}
	}
	if s.subs == nil {
		s.subs = map[chan companionGenFrame]struct{}{}
	}
	s.subs[sub] = struct{}{}
	return snap, sub, func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		if _, ok := s.subs[sub]; ok {
			delete(s.subs, sub)
			close(sub)
		}
	}
}

type GenerateCompanionCardsReq struct {
	Count int `json:"count"`
	// Attach 为 true 时不启动任务，只接上正在跑（或最近一次）的任务，此时忽略 Count。
	Attach bool `json:"attach"`
}

type companionGenResult struct {
	Index      int    `json:"index"`
	CardID     uint   `json:"card_id,omitempty"`
	Name       string `json:"name,omitempty"`
	Occupation string `json:"occupation,omitempty"`
	Error      string `json:"error,omitempty"`
}

// AdminGenerateCompanionCardsWS 以 WebSocket 方式批量生成 AI 陪玩人物卡并直接放进卡池，或接上已有的任务。
func AdminGenerateCompanionCardsWS(c *gin.Context) {
	serveScenarioWatchWS(c, func(raw []byte) (scenarioWatchJob, string) {
		var req GenerateCompanionCardsReq
		if err := binding.JSON.BindBody(raw, &req); err != nil {
			return nil, err.Error()
		}
		if req.Attach {
			return func(emit scenarioEmit, gone <-chan struct{}) {
				snap, ch, cancel := companionGen.subscribe()
				defer cancel()
				emit("snapshot", snap)
				forwardCompanionGen(emit, gone, ch)
			}, ""
		}
		count := req.Count
		if count <= 0 {
			count = 1
		}
		if count > maxCompanionGenCount {
			return nil, fmt.Sprintf("生成数量不能超过 %d", maxCompanionGenCount)
		}
		botID := models.CompanionUserID()
		if botID == 0 {
			return nil, "AI 陪玩用户不存在"
		}
		if !companionGen.tryStart() {
			return nil, "已有批量生成任务在运行，请等它结束后再试"
		}
		return func(emit scenarioEmit, gone <-chan struct{}) {
			// NOTE: 先订阅再开跑：任务一帧都没发出前订阅，这条连接才不需要快照。
			_, ch, cancel := companionGen.subscribe()
			defer cancel()
			go func() {
				defer companionGen.finish()
				runCompanionCardGeneration(botID, count, companionGen.publish)
			}()
			forwardCompanionGen(emit, gone, ch)
		}, ""
	})
}

// forwardCompanionGen 把订阅到的帧转给连接，直到任务结束（通道关闭）或连接断开。
func forwardCompanionGen(emit scenarioEmit, gone <-chan struct{}, ch <-chan companionGenFrame) {
	for {
		select {
		case frame, ok := <-ch:
			if !ok {
				return
			}
			emit(frame.Type, frame.Data)
		case <-gone:
			return
		}
	}
}

// runCompanionCardGeneration 同步生成 count 张卡并推送事件，调用方负责放进后台协程。
func runCompanionCardGeneration(botID uint, count int, emit scenarioEmit) {
	used := map[string]bool{}
	var existing []string
	models.DB.Model(&models.CharacterCard{}).Where("user_id = ? AND is_deleted = ?", botID, false).Pluck("name", &existing)
	for _, name := range existing {
		used[strings.ToLower(strings.TrimSpace(name))] = true
	}
	var usedMu sync.Mutex

	results := make([]companionGenResult, count)
	sem := make(chan struct{}, companionGenConcurrency)
	var wg sync.WaitGroup
	for i := range count {
		sem <- struct{}{}
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			results[i] = generateCompanionCard(botID, i+1, used, &usedMu, emit)
		}()
	}
	wg.Wait()

	succeeded := 0
	for _, r := range results {
		if r.Error == "" {
			succeeded++
		}
	}
	emit("batch_done", gin.H{"total": count, "succeeded": succeeded, "failed": count - succeeded, "results": results})
}

// generateCompanionCard 随机抽性别、年龄、职业，取名后走 AI 车卡流程并入库；失败只影响这一张。
func generateCompanionCard(botID uint, index int, used map[string]bool, usedMu *sync.Mutex, emit scenarioEmit) companionGenResult {
	ctx, cancel := context.WithTimeout(context.Background(), companionGenCardTimeout)
	defer cancel()

	req := GenerateCharacterReq{
		Gender:     models.GenderMale,
		Age:        companionGenMinAge + rand.IntN(companionGenMaxAge-companionGenMinAge+1),
		Occupation: game.RandomOccupation(),
	}
	if rand.IntN(2) == 1 {
		req.Gender = models.GenderFemale
	}
	fail := func(message string) companionGenResult {
		emit("card_error", gin.H{"index": index, "name": req.Name, "occupation": req.Occupation, "message": message})
		return companionGenResult{Index: index, Name: req.Name, Occupation: req.Occupation, Error: message}
	}

	// NOTE: 取名要看到其他并发任务已发放的姓名，所以整个取名过程持锁；取名只有一次短调用，不会拖慢整体。
	usedMu.Lock()
	name, err := pickCompanionCardName(ctx, req.Gender, used)
	if err == nil {
		used[strings.ToLower(name)] = true
	}
	usedMu.Unlock()
	if err != nil {
		companionLog.Warn("companion card name failed", "index", index, "err", err)
		return fail("取名失败: " + err.Error())
	}
	req.Name = name
	emit("card_start", gin.H{"index": index, "name": name, "occupation": req.Occupation, "gender": req.Gender, "age": req.Age})

	card, err := generateCharacterCard(ctx, botID, req)
	if err != nil {
		return fail(err.Error())
	}
	if err := models.DB.Create(card).Error; err != nil {
		companionLog.Error("companion card create failed", "index", index, "err", err)
		return fail("创建人物卡失败")
	}
	emit("card_done", gin.H{"index": index, "card_id": card.ID, "name": card.Name, "occupation": card.Occupation})
	return companionGenResult{Index: index, CardID: card.ID, Name: card.Name, Occupation: card.Occupation}
}

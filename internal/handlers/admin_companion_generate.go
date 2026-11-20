package handlers

// NOTE: AI 卡池批量生成：GET /api/admin/companion-cards/generate/ws。传输协议与模组生成相同（见 scenario_ws.go）：
// 连接后第一帧发 {count}，之后服务端推 card_start / card_done / card_error / batch_done，请求被拒推 rejected。
// 性别、年龄、职业都随机；姓名走取名接口并保证是中文；其余沿用 AI 车卡流程（generateCharacterCard）。卡直接记在 bot 名下，
// 不占任何管理员的卡槽。客户端中途断开不影响任务，照常跑完并入库。

import (
	"context"
	"fmt"
	"math/rand/v2"
	"strings"
	"sync"
	"sync/atomic"
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

// companionGenRunning 保证同一时刻只有一个批量任务，防止重复点击或多个管理员叠加。
var companionGenRunning atomic.Bool

type GenerateCompanionCardsReq struct {
	Count int `json:"count"`
}

type companionGenResult struct {
	Index      int    `json:"index"`
	CardID     uint   `json:"card_id,omitempty"`
	Name       string `json:"name,omitempty"`
	Occupation string `json:"occupation,omitempty"`
	Error      string `json:"error,omitempty"`
}

// AdminGenerateCompanionCardsWS 以 WebSocket 方式批量生成 AI 陪玩人物卡并直接放进卡池。
func AdminGenerateCompanionCardsWS(c *gin.Context) {
	serveScenarioWS(c, func(raw []byte) (scenarioJob, string) {
		var req GenerateCompanionCardsReq
		if err := binding.JSON.BindBody(raw, &req); err != nil {
			return nil, err.Error()
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
		if !companionGenRunning.CompareAndSwap(false, true) {
			return nil, "已有批量生成任务在运行，请等它结束后再试"
		}
		return func(emit scenarioEmit) {
			defer companionGenRunning.Store(false)
			runCompanionCardGeneration(botID, count, emit)
		}, ""
	})
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

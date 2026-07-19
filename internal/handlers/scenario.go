package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math/rand"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/llmcoc/server/internal/models"
	"github.com/llmcoc/server/internal/services/agent"
)

func ListScenarios(c *gin.Context) {
	var scenarios []models.Scenario
	models.DB.Where("is_active = ?", true).
		Order("created_at DESC").
		Find(&scenarios)
	c.JSON(http.StatusOK, scenarios)
}

func GetScenario(c *gin.Context) {
	id, _ := strconv.ParseUint(c.Param("id"), 10, 64)
	var scenario models.Scenario
	if err := models.DB.First(&scenario, id).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "剧本不存在"})
		return
	}
	c.JSON(http.StatusOK, scenario)
}

// GetScenarioModule returns the raw module JSON payload for viewing/debugging.
func GetScenarioModule(c *gin.Context) {
	id, _ := strconv.ParseUint(c.Param("id"), 10, 64)
	var scenario models.Scenario
	if err := models.DB.First(&scenario, id).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "剧本不存在"})
		return
	}

	var module any
	if len(scenario.Data.Data) > 0 {
		if err := json.Unmarshal(scenario.Data.Data, &module); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "模组数据解析失败"})
			return
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"id":   scenario.ID,
		"name": scenario.Name,
		"data": module,
	})
}

// DeleteScenario soft-deletes a scenario by setting is_active = false.
func DeleteScenario(c *gin.Context) {
	id, _ := strconv.ParseUint(c.Param("id"), 10, 64)
	var scenario models.Scenario
	if err := models.DB.First(&scenario, id).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "剧本不存在"})
		return
	}
	if err := models.DB.Model(&scenario).Update("is_active", false).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "删除失败"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "已删除"})
}

type CreateScenarioReq struct {
	Name        string                 `json:"name" binding:"required,max=200"`
	Description string                 `json:"description"`
	Author      string                 `json:"author"`
	Tags        string                 `json:"tags"`
	MinPlayers  int                    `json:"min_players"`
	MaxPlayers  int                    `json:"max_players"`
	Difficulty  string                 `json:"difficulty"`
	Content     models.ScenarioContent `json:"content" binding:"required"`
}

type GenerateScenarioReq struct {
	Name         string `json:"name"`
	Theme        string `json:"theme"`
	Era          string `json:"era"`
	Brief        string `json:"brief"`
	TargetLength string `json:"target_length"`
	Difficulty   string `json:"difficulty"`
	MinPlayers   int    `json:"min_players"`
	MaxPlayers   int    `json:"max_players"`
	// Count 为批量生成数量，留空或 <=1 视为单个生成，上限 maxScenarioGenCount。
	Count int `json:"count"`
}

// maxScenarioGenCount 是单次批量生成的数量上限；生成受全局串行锁限制，数量过大会导致单次请求长时间占用生成槽位。
const maxScenarioGenCount = 10

// CompileStoryReq 是管理员上传故事直接编译为模组的请求：管理员只提供故事全文（及可选名称），
// 跳过 Story Architect 生成阶段，神话锚点与奖励概念由 anchor_extract 阶段从文档内容自动识别，
// 模型只做 ETL（编译为结构化数据）。
type CompileStoryReq struct {
	StoryDocument string `json:"story_document" binding:"required"`
	Name          string `json:"name"`
}

// scenarioBatchResult 记录批量生成中单个子任务的结果，用于 batch_done 汇总事件。
type scenarioBatchResult struct {
	Index      int    `json:"index"`
	ScenarioID uint   `json:"scenario_id,omitempty"`
	Name       string `json:"name,omitempty"`
	Error      string `json:"error,omitempty"`
}

type importScenarioFile struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description"`
	Author      string                 `json:"author"`
	Tags        string                 `json:"tags"`
	MinPlayers  int                    `json:"min_players"`
	MaxPlayers  int                    `json:"max_players"`
	Difficulty  string                 `json:"difficulty"`
	Content     models.ScenarioContent `json:"content"`
}

func CreateScenario(c *gin.Context) {
	var req CreateScenarioReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if req.MinPlayers == 0 {
		req.MinPlayers = 1
	}
	if req.MaxPlayers == 0 {
		req.MaxPlayers = 4
	}
	if req.MaxPlayers > 4 {
		req.MaxPlayers = 4
	}
	if req.Difficulty == "" {
		req.Difficulty = "normal"
	}

	scenario := models.Scenario{
		Name:        req.Name,
		Description: req.Description,
		Author:      req.Author,
		Tags:        req.Tags,
		MinPlayers:  req.MinPlayers,
		MaxPlayers:  req.MaxPlayers,
		Difficulty:  req.Difficulty,
		IsActive:    true,
		Content:     models.JSONField[models.ScenarioContent]{Data: req.Content},
	}
	if err := models.DB.Create(&scenario).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "创建剧本失败"})
		return
	}
	c.JSON(http.StatusCreated, scenario)
}

var ranGen = rand.New(rand.NewSource(time.Now().UnixMilli()))

func RandomSalt() string {
	const letters = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	b := make([]byte, 16)
	for i := range b {
		b[i] = letters[ranGen.Intn(len(letters))]
	}
	return string(b)
}

// scenarioGenEvent 是模组生成流水线推送给前端的 SSE 事件。
type scenarioGenEvent struct {
	name string
	data any
}

// GenerateScenarioByAgents 以 SSE 流式方式运行 AI 模组生成流水线，支持通过 count 批量生成。
// 生成在服务端后台完整执行并落库；即使客户端中途断开连接，结果仍会写入模组列表。
func GenerateScenarioByAgents(c *gin.Context) {
	var req GenerateScenarioReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	count := req.Count
	if count <= 0 {
		count = 1
	}
	if count > maxScenarioGenCount {
		c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("生成数量不能超过 %d", maxScenarioGenCount)})
		return
	}

	setChatSSEHeaders(c)

	events := make(chan scenarioGenEvent, 128)
	go func() {
		defer close(events)
		// NOTE: 进度回调在生成 goroutine 内同步发送；handler 侧会持续排空 channel
		// 直到其关闭（客户端断开也继续排空），因此此处阻塞发送不会死锁。
		progress := func(stage, status, detail string) {
			events <- scenarioGenEvent{name: "progress", data: gin.H{"stage": stage, "status": status, "detail": detail}}
		}

		results := make([]scenarioBatchResult, 0, count)
		for i := 0; i < count; i++ {
			index := i + 1
			events <- scenarioGenEvent{name: "batch_progress", data: gin.H{"current": index, "total": count}}
			progress("queued", "start", "请求已受理，等待生成槽位（全局同时仅运行一路生成任务）")

			name := req.Name
			if name != "" && index > 1 {
				name = fmt.Sprintf("%s-%d", name, index)
			}

			gen, err := agent.RunScripterScenarioTeamWithProgress(context.Background(), agent.ScenarioCreationRequest{
				Name:         name,
				Theme:        req.Theme,
				Era:          req.Era,
				Brief:        req.Brief,
				TargetLength: req.TargetLength,
				Difficulty:   req.Difficulty,
				MinPlayers:   req.MinPlayers,
				MaxPlayers:   req.MaxPlayers,
				Salt:         RandomSalt(),
			}, progress)
			if err != nil {
				log.Printf("[agent] scenario generation failed (index=%d/%d): %v", index, count, err)
				msg := "模组生成失败: " + err.Error()
				events <- scenarioGenEvent{name: "error", data: gin.H{"index": index, "message": msg}}
				results = append(results, scenarioBatchResult{Index: index, Error: msg})
				continue
			}

			if gen.Draft.Name == "" {
				gen.Draft.Name = "AI模组-" + time.Now().Format("20060102150405")
				if index > 1 {
					gen.Draft.Name = fmt.Sprintf("%s-%d", gen.Draft.Name, index)
				}
			}

			scenario := models.Scenario{
				Name:        gen.Draft.Name,
				Description: gen.Draft.Description,
				Author:      gen.Draft.Author,
				Tags:        gen.Draft.Tags,
				MinPlayers:  gen.Draft.MinPlayers,
				MaxPlayers:  gen.Draft.MaxPlayers,
				Difficulty:  gen.Draft.Difficulty,
				Content:     models.JSONField[models.ScenarioContent]{Data: gen.Draft.Content},
				IsActive:    true,
			}

			if scenario.MinPlayers == 0 {
				scenario.MinPlayers = 1
			}
			if scenario.MaxPlayers == 0 {
				scenario.MaxPlayers = 4
			}
			if scenario.Difficulty == "" {
				scenario.Difficulty = "normal"
			}

			if err := models.DB.Create(&scenario).Error; err != nil {
				log.Printf("[agent] failed to save generated scenario to DB: %v", err)
				msg := "模组入库失败，请联系管理员查看服务端日志"
				events <- scenarioGenEvent{name: "error", data: gin.H{"index": index, "message": msg}}
				results = append(results, scenarioBatchResult{Index: index, Error: msg})
				continue
			}

			generationLog := models.ScenarioGenerationLog{
				ScenarioID:   scenario.ID,
				ScenarioName: scenario.Name,
				LogText:      strings.TrimSpace(gen.GenerationLog),
			}
			if generationLog.LogText == "" {
				generationLog.LogText = "本次 AI 生成未捕获到 LLM 对话记录。"
			}
			if err := models.DB.Create(&generationLog).Error; err != nil {
				log.Printf("[agent] failed to save scenario generation log scenario_id=%d: %v", scenario.ID, err)
			}

			events <- scenarioGenEvent{name: "done", data: gin.H{"index": index, "scenario_id": scenario.ID, "name": scenario.Name}}
			results = append(results, scenarioBatchResult{Index: index, ScenarioID: scenario.ID, Name: scenario.Name})
		}

		succeeded := 0
		for _, r := range results {
			if r.Error == "" {
				succeeded++
			}
		}
		events <- scenarioGenEvent{name: "batch_done", data: gin.H{
			"total":     count,
			"succeeded": succeeded,
			"failed":    count - succeeded,
			"results":   results,
		}}
	}()

	// NOTE: 客户端断开后继续排空事件通道直到生成结束，保证后台生成与落库完整执行。
	disconnected := false
	for evt := range events {
		if !disconnected {
			select {
			case <-c.Request.Context().Done():
				disconnected = true
				log.Printf("[agent] scenario generation client disconnected, continuing in background")
			default:
			}
		}
		if disconnected {
			continue
		}
		c.SSEvent(evt.name, evt.data)
		c.Writer.Flush()
	}
}

// CompileStoryByUpload 以 SSE 流式方式将管理员上传的故事文档直接编译为结构化模组，
// 跳过 Story Architect 生成阶段（模型只做 ETL）。不支持批量：一次请求编译一篇故事。
func CompileStoryByUpload(c *gin.Context) {
	var req CompileStoryReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	setChatSSEHeaders(c)

	events := make(chan scenarioGenEvent, 128)
	go func() {
		defer close(events)
		progress := func(stage, status, detail string) {
			events <- scenarioGenEvent{name: "progress", data: gin.H{"stage": stage, "status": status, "detail": detail}}
		}

		gen, err := agent.RunCompileStoryWithProgress(context.Background(), agent.CompileStoryRequest{
			StoryDocument: req.StoryDocument,
			Name:          req.Name,
		}, progress)
		if err != nil {
			log.Printf("[agent] compile-story-upload failed: %v", err)
			msg := "模组编译失败: " + err.Error()
			events <- scenarioGenEvent{name: "error", data: gin.H{"index": 1, "message": msg}}
			return
		}

		if gen.Draft.Name == "" {
			gen.Draft.Name = "上传故事-" + time.Now().Format("20060102150405")
		}

		scenario := models.Scenario{
			Name:        gen.Draft.Name,
			Description: gen.Draft.Description,
			Author:      gen.Draft.Author,
			Tags:        gen.Draft.Tags,
			MinPlayers:  gen.Draft.MinPlayers,
			MaxPlayers:  gen.Draft.MaxPlayers,
			Difficulty:  gen.Draft.Difficulty,
			Content:     models.JSONField[models.ScenarioContent]{Data: gen.Draft.Content},
			IsActive:    true,
		}
		if scenario.MinPlayers == 0 {
			scenario.MinPlayers = 1
		}
		if scenario.MaxPlayers == 0 {
			scenario.MaxPlayers = 4
		}
		if scenario.Difficulty == "" {
			scenario.Difficulty = "normal"
		}

		if err := models.DB.Create(&scenario).Error; err != nil {
			log.Printf("[agent] failed to save compiled scenario to DB: %v", err)
			msg := "模组入库失败，请联系管理员查看服务端日志"
			events <- scenarioGenEvent{name: "error", data: gin.H{"index": 1, "message": msg}}
			return
		}

		generationLog := models.ScenarioGenerationLog{
			ScenarioID:   scenario.ID,
			ScenarioName: scenario.Name,
			LogText:      strings.TrimSpace(gen.GenerationLog),
		}
		if generationLog.LogText == "" {
			generationLog.LogText = "本次编译未捕获到 LLM 对话记录。"
		}
		if err := models.DB.Create(&generationLog).Error; err != nil {
			log.Printf("[agent] failed to save scenario generation log scenario_id=%d: %v", scenario.ID, err)
		}

		events <- scenarioGenEvent{name: "done", data: gin.H{"index": 1, "scenario_id": scenario.ID, "name": scenario.Name}}
	}()

	disconnected := false
	for evt := range events {
		if !disconnected {
			select {
			case <-c.Request.Context().Done():
				disconnected = true
				log.Printf("[agent] compile-story-upload client disconnected, continuing in background")
			default:
			}
		}
		if disconnected {
			continue
		}
		c.SSEvent(evt.name, evt.data)
		c.Writer.Flush()
	}
}

// UploadScenario imports a scenario JSON file into DB.
// form-data: file=<scenario.json>
func UploadScenario(c *gin.Context) {
	fh, err := c.FormFile("file")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请上传 JSON 文件(字段名 file)"})
		return
	}

	f, err := fh.Open()
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "打开上传文件失败"})
		return
	}
	defer f.Close()

	data, err := io.ReadAll(f)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "读取上传文件失败"})
		return
	}

	var in importScenarioFile
	if err := json.Unmarshal(data, &in); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "JSON 格式无效: " + err.Error()})
		return
	}

	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "模组名不能为空"})
		return
	}
	if in.MinPlayers <= 0 {
		in.MinPlayers = 1
	}
	if in.MaxPlayers <= 0 {
		in.MaxPlayers = 4
	}
	if in.MaxPlayers > 4 {
		in.MaxPlayers = 4
	}
	if in.Difficulty == "" {
		in.Difficulty = "normal"
	}
	if in.MaxPlayers < in.MinPlayers {
		c.JSON(http.StatusBadRequest, gin.H{"error": "max_players 不能小于 min_players"})
		return
	}

	var count int64
	models.DB.Model(&models.Scenario{}).Where("name = ?", in.Name).Count(&count)
	if count > 0 {
		c.JSON(http.StatusConflict, gin.H{"error": "已存在同名模组"})
		return
	}

	scenario := models.Scenario{
		Name:        in.Name,
		Description: in.Description,
		Author:      in.Author,
		Tags:        in.Tags,
		MinPlayers:  in.MinPlayers,
		MaxPlayers:  in.MaxPlayers,
		Difficulty:  in.Difficulty,
		Content:     models.JSONField[models.ScenarioContent]{Data: in.Content},
		IsActive:    true,
	}

	if err := models.DB.Create(&scenario).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "导入模组失败: " + err.Error()})
		return
	}

	c.JSON(http.StatusCreated, gin.H{"message": "导入成功", "scenario": scenario})
}

// DownloadScenarioTemplate returns a JSON template for module creation/import.
func DownloadScenarioTemplate(c *gin.Context) {
	tmpl := importScenarioFile{
		Name:        "新模组名称",
		Description: "模组简介",
		Author:      "your-name",
		Tags:        "调查,神话,单晚",
		MinPlayers:  1,
		MaxPlayers:  4,
		Difficulty:  "normal",
		Content: models.ScenarioContent{
			SystemPrompt:  "你是本场COC跑团的守秘人(KP)...",
			Setting:       "时代与地点背景",
			Intro:         "开场引子",
			GameStartSlot: 36,
			Scenes: []models.SceneData{{
				ID:          "arrival",
				Name:        "抵达",
				Description: "调查员抵达并获得第一条线索",
				Triggers:    []string{"start"},
			}},
			NPCs: []models.NPCData{{
				Name:        "关键NPC",
				Description: "NPC描述",
				Attitude:    "谨慎",
				Stats:       map[string]int{"STR": 50, "CON": 50},
			}},
			Clues:        []string{"线索1", "线索2"},
			WinCondition: "达成关键目标并安全撤离",
		},
	}

	name := fmt.Sprintf("scenario_template_%s.json", time.Now().Format("20060102_150405"))
	c.Header("Content-Type", "application/json; charset=utf-8")
	c.Header("Content-Disposition", "attachment; filename="+name)
	c.JSON(http.StatusOK, tmpl)
}

// SeedScenarios loads scenario JSON files from the scenarios directory
func SeedScenarios(dir string) {
	var existing int64
	models.DB.Model(&models.Scenario{}).Count(&existing)
	if existing > 0 {
		return
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			continue
		}

		var obj map[string]json.RawMessage
		if err := json.Unmarshal(data, &obj); err != nil {
			continue
		}

		var name string
		if rawName, ok := obj["name"]; ok {
			_ = json.Unmarshal(rawName, &name)
			delete(obj, "name")
		}
		name = strings.TrimSpace(name)
		if name == "" {
			name = strings.TrimSuffix(entry.Name(), filepath.Ext(entry.Name()))
		}

		payload, err := json.Marshal(obj)
		if err != nil {
			continue
		}

		scenario := models.Scenario{
			Name:     name,
			IsActive: true,
			Data:     models.JSONField[json.RawMessage]{Data: json.RawMessage(payload)},
		}
		models.DB.Create(&scenario)
	}
}

// SeedShopItems initializes default shop items with basic equipment from COC rules
func SeedShopItems() {
	defaultItems := []models.ShopItem{
		// 卡槽相关
		{
			Name:        "卡槽扩展(+1)",
			Description: "增加1个人物卡卡槽",
			ItemType:    models.ItemTypeCardSlot,
			Price:       50,
			Value:       1,
			IsActive:    true,
		},
		{
			Name:        "卡槽扩展(+3)",
			Description: "增加3个人物卡卡槽",
			ItemType:    models.ItemTypeCardSlot,
			Price:       120,
			Value:       3,
			IsActive:    true,
		},

		// 基础装备
		{
			Name:        "手电筒",
			Description: "便携式手电筒,提供照明",
			ItemType:    models.ItemTypeEquipment,
			Price:       15,
			Value:       1,
			IsActive:    true,
		},
		{
			Name:        "指南针",
			Description: "航海级指南针,帮助导航",
			ItemType:    models.ItemTypeEquipment,
			Price:       20,
			Value:       1,
			IsActive:    true,
		},
		{
			Name:        "修理工具包",
			Description: "基础修理工具,用于维修各类机械设备",
			ItemType:    models.ItemTypeEquipment,
			Price:       25,
			Value:       1,
			IsActive:    true,
		},
		{
			Name:        "医疗急救包",
			Description: "应急医疗用品,可治疗1D4生命值伤害",
			ItemType:    models.ItemTypeEquipment,
			Price:       30,
			Value:       1,
			IsActive:    true,
		},
		{
			Name:        "安全绳(30米)",
			Description: "高强度安全绳,用于攀爬和牵引",
			ItemType:    models.ItemTypeEquipment,
			Price:       20,
			Value:       1,
			IsActive:    true,
		},
		{
			Name:        "锁匠工具",
			Description: "专业开锁工具,提高撬锁技能的成功率",
			ItemType:    models.ItemTypeEquipment,
			Price:       35,
			Value:       1,
			IsActive:    true,
		},
		{
			Name:        "夜视镜",
			Description: "军用级夜视镜,在黑暗中提供视野",
			ItemType:    models.ItemTypeEquipment,
			Price:       60,
			Value:       1,
			IsActive:    true,
		},
		{
			Name:        "化学防护服",
			Description: "危险物质防护装备",
			ItemType:    models.ItemTypeEquipment,
			Price:       75,
			Value:       1,
			IsActive:    true,
		},

		// 武器
		{
			Name:        "短刀",
			Description: "便携式匕首,近战武器",
			ItemType:    models.ItemTypeWeapon,
			Price:       20,
			Value:       1,
			IsActive:    true,
		},
		{
			Name:        "长剑",
			Description: "标准长剑,增加近战伤害",
			ItemType:    models.ItemTypeWeapon,
			Price:       30,
			Value:       1,
			IsActive:    true,
		},
		{
			Name:        "左轮手枪(.38)",
			Description: "6发弹匣左轮手枪,近程射击武器",
			ItemType:    models.ItemTypeWeapon,
			Price:       50,
			Value:       1,
			IsActive:    true,
		},
		{
			Name:        "半自动步枪",
			Description: "军用级步枪,远程射击武器",
			ItemType:    models.ItemTypeWeapon,
			Price:       100,
			Value:       1,
			IsActive:    true,
		},
		// 配件
		{
			Name:        "照相机",
			Description: "便携式照相机,用于记录证据",
			ItemType:    models.ItemTypeAccessory,
			Price:       40,
			Value:       1,
			IsActive:    true,
		},
		{
			Name:        "钢笔",
			Description: "精致钢笔,用于记录调查细节",
			ItemType:    models.ItemTypeAccessory,
			Price:       10,
			Value:       1,
			IsActive:    true,
		},
		{
			Name:        "笔记本",
			Description: "精装笔记本,用于记录调查细节",
			ItemType:    models.ItemTypeAccessory,
			Price:       10,
			Value:       1,
			IsActive:    true,
		},
		{
			Name:        "防毒面具",
			Description: "防毒防尘面具",
			ItemType:    models.ItemTypeAccessory,
			Price:       45,
			Value:       1,
			IsActive:    true,
		},
	}

	for _, item := range defaultItems {
		// 检查是否已存在
		var count int64
		models.DB.Model(&models.ShopItem{}).Where("name = ?", item.Name).Count(&count)
		if count > 0 {
			continue
		}
		models.DB.Create(&item)
	}
}

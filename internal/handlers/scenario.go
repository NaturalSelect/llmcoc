package handlers

import (
	"encoding/json"
	"fmt"
	"io"
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

func GenerateScenarioByAgents(c *gin.Context) {
	var req GenerateScenarioReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	gen, err := agent.RunScripterScenarioTeam(c.Request.Context(), agent.ScenarioCreationRequest{
		Name:         req.Name,
		Theme:        req.Theme,
		Era:          req.Era,
		Brief:        req.Brief,
		TargetLength: req.TargetLength,
		Difficulty:   req.Difficulty,
		MinPlayers:   req.MinPlayers,
		MaxPlayers:   req.MaxPlayers,
	})
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": "agent team 生成失败: " + err.Error()})
		return
	}

	if gen.Draft.Name == "" {
		gen.Draft.Name = "AI模组-" + time.Now().Format("20060102150405")
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
		c.JSON(http.StatusInternalServerError, gin.H{"error": "保存生成模组失败: " + err.Error()})
		return
	}

	c.JSON(http.StatusCreated, gin.H{
		"scenario":   scenario,
		"qa":         gen.QA,
		"iterations": gen.Iterations,
	})
}

// UploadScenario imports a scenario JSON file into DB.
// form-data: file=<scenario.json>
func UploadScenario(c *gin.Context) {
	fh, err := c.FormFile("file")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请上传 JSON 文件（字段名 file）"})
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
			SystemPrompt:  "你是本场COC跑团的守秘人（KP）...",
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
			Name:        "卡槽扩展（+1）",
			Description: "增加1个人物卡卡槽",
			ItemType:    models.ItemTypeCardSlot,
			Price:       50,
			Value:       1,
			IsActive:    true,
		},
		{
			Name:        "卡槽扩展（+3）",
			Description: "增加3个人物卡卡槽",
			ItemType:    models.ItemTypeCardSlot,
			Price:       120,
			Value:       3,
			IsActive:    true,
		},

		// 基础装备
		{
			Name:        "手电筒",
			Description: "便携式手电筒，提供照明",
			ItemType:    models.ItemTypeEquipment,
			Price:       15,
			Value:       1,
			IsActive:    true,
		},
		{
			Name:        "指南针",
			Description: "航海级指南针，帮助导航",
			ItemType:    models.ItemTypeEquipment,
			Price:       20,
			Value:       1,
			IsActive:    true,
		},
		{
			Name:        "修理工具包",
			Description: "基础修理工具，用于维修各类机械设备",
			ItemType:    models.ItemTypeEquipment,
			Price:       25,
			Value:       1,
			IsActive:    true,
		},
		{
			Name:        "医疗急救包",
			Description: "应急医疗用品，可治疗1D4生命值伤害",
			ItemType:    models.ItemTypeEquipment,
			Price:       30,
			Value:       1,
			IsActive:    true,
		},
		{
			Name:        "安全绳（30米）",
			Description: "高强度安全绳，用于攀爬和牵引",
			ItemType:    models.ItemTypeEquipment,
			Price:       20,
			Value:       1,
			IsActive:    true,
		},
		{
			Name:        "锁匠工具",
			Description: "专业开锁工具，提高撬锁技能的成功率",
			ItemType:    models.ItemTypeEquipment,
			Price:       35,
			Value:       1,
			IsActive:    true,
		},
		{
			Name:        "夜视镜",
			Description: "军用级夜视镜，在黑暗中提供视野",
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
			Description: "便携式匕首，近战武器",
			ItemType:    models.ItemTypeWeapon,
			Price:       20,
			Value:       1,
			IsActive:    true,
		},
		{
			Name:        "长剑",
			Description: "标准长剑，增加近战伤害",
			ItemType:    models.ItemTypeWeapon,
			Price:       30,
			Value:       1,
			IsActive:    true,
		},
		{
			Name:        "左轮手枪（.38）",
			Description: "6发弹匣左轮手枪，近程射击武器",
			ItemType:    models.ItemTypeWeapon,
			Price:       50,
			Value:       1,
			IsActive:    true,
		},
		{
			Name:        "半自动步枪",
			Description: "军用级步枪，远程射击武器",
			ItemType:    models.ItemTypeWeapon,
			Price:       100,
			Value:       1,
			IsActive:    true,
		},
		{
			Name:        "火焰枪",
			Description: "重型火焰喷枪，群体伤害武器",
			ItemType:    models.ItemTypeWeapon,
			Price:       150,
			Value:       1,
			IsActive:    true,
		},
		{
			Name:        "手榴弹（5枚）",
			Description: "5枚军用手榴弹",
			ItemType:    models.ItemTypeWeapon,
			Price:       80,
			Value:       1,
			IsActive:    true,
		},

		// 配件
		{
			Name:        "弹药包（手枪）",
			Description: "手枪弹药补充，50发装",
			ItemType:    models.ItemTypeAccessory,
			Price:       15,
			Value:       1,
			IsActive:    true,
		},
		{
			Name:        "弹药包（步枪）",
			Description: "步枪弹药补充，100发装",
			ItemType:    models.ItemTypeAccessory,
			Price:       25,
			Value:       1,
			IsActive:    true,
		},
		{
			Name:        "照相机",
			Description: "便携式照相机，用于记录证据",
			ItemType:    models.ItemTypeAccessory,
			Price:       40,
			Value:       1,
			IsActive:    true,
		},
		{
			Name:        "笔记本",
			Description: "精装笔记本，用于记录调查细节",
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

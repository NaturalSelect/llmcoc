package handlers

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/llmcoc/server/internal/models"
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
		// 金币相关
		{
			Name:        "100金币",
			Description: "补充100金币用于购买装备",
			ItemType:    models.ItemTypeCoins,
			Price:       0,
			Value:       100,
			IsActive:    true,
		},
		{
			Name:        "500金币",
			Description: "补充500金币，适合配置多个角色",
			ItemType:    models.ItemTypeCoins,
			Price:       0,
			Value:       500,
			IsActive:    true,
		},

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

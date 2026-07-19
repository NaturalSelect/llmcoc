package models

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"gorm.io/gorm"
)

// ScenarioPayload is the JSON value stored in Scenario.Data.
type ScenarioPayload struct {
	Description string          `json:"description"`
	Author      string          `json:"author"`
	Tags        string          `json:"tags"`
	MinPlayers  int             `json:"min_players"`
	MaxPlayers  int             `json:"max_players"`
	Difficulty  string          `json:"difficulty"`
	Content     ScenarioContent `json:"content"`
}

func normalizeScenarioDefaults(p *ScenarioPayload) {
	if p.MinPlayers <= 0 {
		p.MinPlayers = 1
	}
	if p.MaxPlayers <= 0 {
		p.MaxPlayers = 4
	}
	if strings.TrimSpace(p.Difficulty) == "" {
		p.Difficulty = "normal"
	}
	sortCluesByNature(p.Content.Clues)
}

// sortCluesByNature 按性质稳定排序线索：真实→隐藏→误导→其他，保证展示顺序一致。
func sortCluesByNature(clues []ClueData) {
	sort.SliceStable(clues, func(i, j int) bool {
		return clueNatureOrder(clues[i].Nature) < clueNatureOrder(clues[j].Nature)
	})
}

func clueNatureOrder(nature string) int {
	switch nature {
	case "真实":
		return 0
	case "隐藏":
		return 1
	case "误导":
		return 2
	default:
		return 3
	}
}

func hasScenarioContent(c ScenarioContent) bool {
	if strings.TrimSpace(c.SystemPrompt) != "" ||
		strings.TrimSpace(c.Setting) != "" ||
		strings.TrimSpace(c.Intro) != "" ||
		len(c.Endings) > 0 {
		return true
	}
	return len(c.Scenes) > 0 || len(c.NPCs) > 0 || len(c.Clues) > 0 || c.GameStartSlot != 0
}

// UnmarshalJSON 兼容旧模组 JSON：
//  1. clues 旧格式为 []string（形如"[真实]xxx"），转成 []ClueData；
//  2. 旧的 win_condition/lose_condition/partial_wins 自动迁移为 Endings。
//
// 新格式（clues 为对象数组、endings 为数组）直接解析；旧字段不再序列化输出。
func (c *ScenarioContent) UnmarshalJSON(data []byte) error {
	type alias ScenarioContent // 避免递归调用本方法
	aux := &struct {
		*alias
		Clues         json.RawMessage `json:"clues"`
		WinCondition  string          `json:"win_condition"`
		LoseCondition string          `json:"lose_condition"`
		PartialWins   []string        `json:"partial_wins"`
	}{alias: (*alias)(c)}
	if err := json.Unmarshal(data, aux); err != nil {
		return err
	}
	if len(aux.Clues) > 0 {
		c.Clues = decodeCluesCompat(aux.Clues)
	}
	// 旧结局字段迁移：仅在新 endings 缺省时回填，避免覆盖新数据
	if len(c.Endings) == 0 {
		c.Endings = legacyEndings(aux.WinCondition, aux.LoseCondition, aux.PartialWins)
	}
	return nil
}

// decodeCluesCompat 先按新结构 []ClueData 解析，失败再退回旧 []string 逐条转换。
func decodeCluesCompat(raw json.RawMessage) []ClueData {
	var structured []ClueData
	if err := json.Unmarshal(raw, &structured); err == nil {
		return structured
	}
	var legacy []string
	if err := json.Unmarshal(raw, &legacy); err != nil {
		return nil
	}
	out := make([]ClueData, 0, len(legacy))
	for _, s := range legacy {
		out = append(out, legacyClue(s))
	}
	return out
}

// legacyClue 把旧的"[性质]内容"字符串拆为 ClueData；无前缀时性质默认"真实"。
func legacyClue(s string) ClueData {
	trimmed := strings.TrimSpace(s)
	for _, nature := range []string{"真实", "隐藏", "误导"} {
		prefix := "[" + nature + "]"
		if strings.HasPrefix(trimmed, prefix) {
			return ClueData{Summary: strings.TrimSpace(strings.TrimPrefix(trimmed, prefix)), Nature: nature}
		}
	}
	return ClueData{Summary: trimmed, Nature: "真实"}
}

// legacyEndings 把旧的 win/lose/partial 三字段组装为命名结局列表。
func legacyEndings(win, lose string, partials []string) []EndingData {
	var endings []EndingData
	if strings.TrimSpace(win) != "" {
		endings = append(endings, EndingData{Name: "胜利", Trigger: win})
	}
	if strings.TrimSpace(lose) != "" {
		endings = append(endings, EndingData{Name: "失败", Trigger: lose, IsFailure: true})
	}
	for i, p := range partials {
		if strings.TrimSpace(p) == "" {
			continue
		}
		endings = append(endings, EndingData{Name: fmt.Sprintf("部分胜利%d", i+1), Trigger: p})
	}
	return endings
}

// DecodeData populates compatibility fields from stored JSON payload.
func (s *Scenario) DecodeData() error {
	var payload ScenarioPayload
	raw := strings.TrimSpace(string(s.Data.Data))
	if raw != "" {
		if err := json.Unmarshal(s.Data.Data, &payload); err != nil {
			// Backward compatibility: legacy rows stored ScenarioContent directly.
			var legacyContent ScenarioContent
			if err2 := json.Unmarshal(s.Data.Data, &legacyContent); err2 != nil {
				return err
			}
			payload.Content = legacyContent
		}
	}
	if !hasScenarioContent(payload.Content) {
		// Another compatibility path: JSON parsed but without payload wrapper.
		var legacyContent ScenarioContent
		if err := json.Unmarshal(s.Data.Data, &legacyContent); err == nil && hasScenarioContent(legacyContent) {
			payload.Content = legacyContent
		}
	}

	normalizeScenarioDefaults(&payload)
	s.Description = payload.Description
	s.Author = payload.Author
	s.Tags = payload.Tags
	s.MinPlayers = payload.MinPlayers
	s.MaxPlayers = payload.MaxPlayers
	s.Difficulty = payload.Difficulty
	s.Content = JSONField[ScenarioContent]{Data: payload.Content}
	return nil
}

// EncodeData builds Data from compatibility fields.
func (s *Scenario) EncodeData() error {
	payload := ScenarioPayload{
		Description: s.Description,
		Author:      s.Author,
		Tags:        s.Tags,
		MinPlayers:  s.MinPlayers,
		MaxPlayers:  s.MaxPlayers,
		Difficulty:  s.Difficulty,
		Content:     s.Content.Data,
	}
	normalizeScenarioDefaults(&payload)
	buf, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	s.Data = JSONField[json.RawMessage]{Data: json.RawMessage(buf)}
	// Keep normalized values visible to API response.
	s.MinPlayers = payload.MinPlayers
	s.MaxPlayers = payload.MaxPlayers
	s.Difficulty = payload.Difficulty
	return nil
}

func (s *Scenario) BeforeSave(tx *gorm.DB) error {
	if strings.TrimSpace(s.Name) == "" {
		return nil
	}
	if len(s.Data.Data) == 0 {
		return s.EncodeData()
	}
	return s.DecodeData()
}

func (s *Scenario) AfterFind(tx *gorm.DB) error {
	return s.DecodeData()
}

package models

import (
	"encoding/json"
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
}

func hasScenarioContent(c ScenarioContent) bool {
	if strings.TrimSpace(c.SystemPrompt) != "" ||
		strings.TrimSpace(c.Setting) != "" ||
		strings.TrimSpace(c.Intro) != "" ||
		strings.TrimSpace(c.WinCondition) != "" {
		return true
	}
	return len(c.Scenes) > 0 || len(c.NPCs) > 0 || len(c.Clues) > 0 || c.GameStartSlot != 0
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

package models

import (
	"time"
)

type Role string

const (
	RoleUser  Role = "user"
	RoleAdmin Role = "admin"
)

type User struct {
	ID           uint      `gorm:"primaryKey;autoIncrement" json:"id"`
	Username     string    `gorm:"uniqueIndex;not null;size:50" json:"username"`
	Email        string    `gorm:"uniqueIndex;not null;size:200" json:"email"`
	PasswordHash string    `gorm:"not null" json:"-"`
	Role         Role      `gorm:"default:'user';not null" json:"role"`
	Coins        int       `gorm:"default:0;not null" json:"coins"`
	CardSlots    int       `gorm:"default:3;not null" json:"card_slots"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

// COC 7th character attributes
type CharacterStats struct {
	STR int `json:"str"` // 力量
	CON int `json:"con"` // 体质
	SIZ int `json:"siz"` // 体型
	DEX int `json:"dex"` // 敏捷
	APP int `json:"app"` // 外貌
	INT int `json:"int"` // 智识
	POW int `json:"pow"` // 意志
	EDU int `json:"edu"` // 教育
	// Derived
	HP     int    `json:"hp"`      // 生命值
	MaxHP  int    `json:"max_hp"`  // 最大生命值
	MP     int    `json:"mp"`      // 魔法值
	MaxMP  int    `json:"max_mp"`  // 最大魔法值
	SAN    int    `json:"san"`     // 理智值
	MaxSAN int    `json:"max_san"` // 最大理智值
	Luck   int    `json:"luck"`    // 幸运
	MOV    int    `json:"mov"`     // 移动速度
	Build  int    `json:"build"`   // 体格
	DB     string `json:"db"`      // 伤害加值
}

// SocialRelation represents a named relationship on a character card.
type SocialRelation struct {
	Name         string `json:"name"`
	Relationship string `json:"relationship"`
	Note         string `json:"note"`
}

type CharacterCard struct {
	ID              uint                        `gorm:"primaryKey;autoIncrement" json:"id"`
	UserID          uint                        `gorm:"not null;index" json:"user_id"`
	Name            string                      `gorm:"not null;size:100" json:"name"`
	Age             int                         `json:"age"`
	Gender          string                      `gorm:"size:20" json:"gender"`
	Occupation      string                      `gorm:"size:100" json:"occupation"`
	Birthplace      string                      `gorm:"size:100" json:"birthplace"`
	Residence       string                      `gorm:"size:100" json:"residence"`
	Stats           JSONField[CharacterStats]   `gorm:"type:text;not null" json:"stats"`
	Skills          JSONField[map[string]int]   `gorm:"type:text;not null" json:"skills"`
	Backstory       string                      `gorm:"type:text" json:"backstory"`
	Appearance      string                      `gorm:"type:text" json:"appearance"`
	Traits          string                      `gorm:"type:text" json:"traits"`
	SocialRelations JSONField[[]SocialRelation] `gorm:"type:text" json:"social_relations"`
	Spells          JSONField[[]string]         `gorm:"type:text" json:"spells"`
	SeenMonsters    JSONField[[]string]         `gorm:"type:text" json:"seen_monsters"` // 已见过的神话存在（见过的不掉SAN）
	IsActive        bool                        `gorm:"default:true" json:"is_active"`
	AvatarURL       string                      `gorm:"size:500" json:"avatar_url"`
	// COC 理智与疯狂状态
	MadnessState       string `gorm:"size:20;default:'none'" json:"madness_state"` // none/temporary/indefinite/permanent
	MadnessSymptom     string `gorm:"type:text" json:"madness_symptom"`            // 当前疯狂症状描述
	MadnessDuration    int    `gorm:"default:0" json:"madness_duration"`           // 剩余轮数（临时性）或标记仍在发作（不定性）
	DailySanLoss       int    `gorm:"default:0" json:"daily_san_loss"`             // 当日累计SAN损失（用于不定性疯狂判断）
	CthulhuMythosSkill int    `gorm:"default:0" json:"cthulhu_mythos_skill"`       // 克苏鲁神话技能值（控制最大SAN上限）
	// COC 伤亡状态
	WoundState    string    `gorm:"size:20;default:'none'" json:"wound_state"` // none/major/dying/dead
	IsUnconscious bool      `gorm:"default:false" json:"is_unconscious"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
	User          User      `gorm:"foreignKey:UserID" json:"-"`
}

type ScenarioContent struct {
	SystemPrompt string      `json:"system_prompt"`
	Setting      string      `json:"setting"`
	Intro        string      `json:"intro"`
	Scenes       []SceneData `json:"scenes"`
	NPCs         []NPCData   `json:"npcs"`
	Clues        []string    `json:"clues"`
	WinCondition string      `json:"win_condition"`
}

type SceneData struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Triggers    []string `json:"triggers"`
}

type NPCData struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Attitude    string         `json:"attitude"`
	Stats       map[string]int `json:"stats,omitempty"`
}

type Scenario struct {
	ID          uint                       `gorm:"primaryKey;autoIncrement" json:"id"`
	Name        string                     `gorm:"not null;size:200" json:"name"`
	Description string                     `gorm:"type:text" json:"description"`
	Author      string                     `gorm:"size:100" json:"author"`
	Tags        string                     `gorm:"size:500" json:"tags"`
	MinPlayers  int                        `gorm:"default:1" json:"min_players"`
	MaxPlayers  int                        `gorm:"default:4" json:"max_players"`
	Difficulty  string                     `gorm:"size:20;default:'normal'" json:"difficulty"`
	IsActive    bool                       `gorm:"default:true" json:"is_active"`
	Content     JSONField[ScenarioContent] `gorm:"type:text;not null" json:"content"`
	CreatedAt   time.Time                  `json:"created_at"`
	UpdatedAt   time.Time                  `json:"updated_at"`
}

type SessionStatus string

const (
	SessionStatusLobby   SessionStatus = "lobby"
	SessionStatusPlaying SessionStatus = "playing"
	SessionStatusEnded   SessionStatus = "ended"
)

type GameSession struct {
	ID          uint            `gorm:"primaryKey;autoIncrement" json:"id"`
	Name        string          `gorm:"not null;size:200" json:"name"`
	ScenarioID  uint            `gorm:"not null" json:"scenario_id"`
	Status      SessionStatus   `gorm:"default:'lobby'" json:"status"`
	MaxPlayers  int             `gorm:"default:4" json:"max_players"`
	Password    string          `gorm:"size:100" json:"-"`
	HasPassword bool            `gorm:"default:false" json:"has_password"`
	CreatedBy   uint            `gorm:"not null" json:"created_by"`
	TurnRound   int             `gorm:"default:1" json:"turn_round"`
	CreatedAt   time.Time       `json:"created_at"`
	UpdatedAt   time.Time       `json:"updated_at"`
	Scenario    Scenario        `gorm:"foreignKey:ScenarioID" json:"scenario,omitempty"`
	Creator     User            `gorm:"foreignKey:CreatedBy" json:"creator,omitempty"`
	Players     []SessionPlayer `gorm:"foreignKey:SessionID" json:"players,omitempty"`
}

// SessionNPC is a temporary NPC card created during a session (e.g. monsters, minor NPCs).
type SessionNPC struct {
	ID          uint                      `gorm:"primaryKey;autoIncrement" json:"id"`
	SessionID   uint                      `gorm:"not null;index" json:"session_id"`
	Name        string                    `gorm:"not null;size:100" json:"name"`
	Description string                    `gorm:"type:text" json:"description"`
	Stats       JSONField[map[string]int] `gorm:"type:text" json:"stats"`
	Skills      JSONField[map[string]int] `gorm:"type:text" json:"skills"`
	IsAlive     bool                      `gorm:"default:true" json:"is_alive"`
	CreatedAt   time.Time                 `json:"created_at"`
	UpdatedAt   time.Time                 `json:"updated_at"`
}

// SessionTurnAction records that a player has submitted their action in a given round.
// Used to determine when all players have acted and NPC round can begin.
type SessionTurnAction struct {
	ID            uint      `gorm:"primaryKey;autoIncrement" json:"id"`
	SessionID     uint      `gorm:"not null;index" json:"session_id"`
	Round         int       `gorm:"not null" json:"round"`
	UserID        uint      `gorm:"not null" json:"user_id"`
	Username      string    `gorm:"size:50" json:"username"`
	ActionSummary string    `gorm:"type:text" json:"action_summary"`
	CreatedAt     time.Time `json:"created_at"`
}

type SessionPlayer struct {
	ID              uint          `gorm:"primaryKey;autoIncrement" json:"id"`
	SessionID       uint          `gorm:"not null;index" json:"session_id"`
	UserID          uint          `gorm:"not null" json:"user_id"`
	CharacterCardID uint          `gorm:"not null" json:"character_card_id"`
	JoinedAt        time.Time     `json:"joined_at"`
	User            User          `gorm:"foreignKey:UserID" json:"user,omitempty"`
	CharacterCard   CharacterCard `gorm:"foreignKey:CharacterCardID" json:"character_card,omitempty"`
}

type MessageRole string

const (
	MessageRoleUser      MessageRole = "user"
	MessageRoleAssistant MessageRole = "assistant"
	MessageRoleSystem    MessageRole = "system"
)

type Message struct {
	ID        uint        `gorm:"primaryKey;autoIncrement" json:"id"`
	SessionID uint        `gorm:"not null;index" json:"session_id"`
	UserID    *uint       `json:"user_id"`
	Role      MessageRole `gorm:"not null" json:"role"`
	Content   string      `gorm:"type:text;not null" json:"content"`
	Username  string      `gorm:"size:50" json:"username"`
	CreatedAt time.Time   `json:"created_at"`
	User      *User       `gorm:"foreignKey:UserID" json:"-"`
}

type ItemType string

const (
	ItemTypeCardSlot ItemType = "card_slot"
	ItemTypeCoins    ItemType = "coins"
)

type ShopItem struct {
	ID          uint      `gorm:"primaryKey;autoIncrement" json:"id"`
	Name        string    `gorm:"not null;size:200" json:"name"`
	Description string    `gorm:"type:text" json:"description"`
	ItemType    ItemType  `gorm:"not null" json:"item_type"`
	Price       int       `gorm:"not null" json:"price"`
	Value       int       `gorm:"not null;default:1" json:"value"` // +N card slots, or N coins
	IsActive    bool      `gorm:"default:true" json:"is_active"`
	IconURL     string    `gorm:"size:500" json:"icon_url"`
	CreatedAt   time.Time `json:"created_at"`
}

type Transaction struct {
	ID         uint      `gorm:"primaryKey;autoIncrement" json:"id"`
	UserID     uint      `gorm:"not null;index" json:"user_id"`
	ShopItemID uint      `gorm:"not null" json:"shop_item_id"`
	CoinsSpent int       `gorm:"not null" json:"coins_spent"`
	CreatedAt  time.Time `json:"created_at"`
	User       User      `gorm:"foreignKey:UserID" json:"-"`
	ShopItem   ShopItem  `gorm:"foreignKey:ShopItemID" json:"shop_item,omitempty"`
}

type CoinRecharge struct {
	ID        uint      `gorm:"primaryKey;autoIncrement" json:"id"`
	UserID    uint      `gorm:"not null;index" json:"user_id"`
	Amount    int       `gorm:"not null" json:"amount"`
	AdminID   uint      `gorm:"not null" json:"admin_id"`
	Note      string    `gorm:"size:500" json:"note"`
	CreatedAt time.Time `json:"created_at"`
	User      User      `gorm:"foreignKey:UserID" json:"user,omitempty"`
	Admin     User      `gorm:"foreignKey:AdminID" json:"admin,omitempty"`
}

// ── Agent system models ──────────────────────────────────────────────────────

type AgentRole string

const (
	AgentRoleDirector  AgentRole = "director"
	AgentRoleJudger    AgentRole = "judger"
	AgentRoleScripter  AgentRole = "scripter"
	AgentRoleWriter    AgentRole = "writer"
	AgentRoleEvaluator AgentRole = "evaluator"
	AgentRoleGrowth    AgentRole = "growth"
	AgentRoleLawyer    AgentRole = "lawyer"
	AgentRoleEditor    AgentRole = "editor"
	AgentRoleNPC       AgentRole = "npc"
)

// LLMProviderConfig stores a named LLM API endpoint configuration.
// The APIKey field is always omitted from JSON to prevent leaks.
type LLMProviderConfig struct {
	ID        uint      `gorm:"primaryKey;autoIncrement" json:"id"`
	Name      string    `gorm:"not null;size:100;uniqueIndex" json:"name"`
	Provider  string    `gorm:"not null;size:50" json:"provider"` // openai | custom
	BaseURL   string    `gorm:"size:500" json:"base_url"`
	APIKey    string    `gorm:"size:500" json:"-"`
	IsActive  bool      `gorm:"default:true" json:"is_active"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// AgentConfig holds per-agent model and prompt configuration.
type AgentConfig struct {
	ID               uint               `gorm:"primaryKey;autoIncrement" json:"id"`
	Role             AgentRole          `gorm:"not null;size:50;uniqueIndex" json:"role"`
	ProviderConfigID *uint              `json:"provider_config_id"`
	ModelName        string             `gorm:"size:200" json:"model_name"`
	MaxTokens        int                `gorm:"default:1024" json:"max_tokens"`
	Temperature      float32            `gorm:"default:0.7;type:real" json:"temperature"`
	SystemPrompt     string             `gorm:"type:text" json:"system_prompt"`
	IsActive         bool               `gorm:"default:true" json:"is_active"`
	CreatedAt        time.Time          `json:"created_at"`
	UpdatedAt        time.Time          `json:"updated_at"`
	ProviderConfig   *LLMProviderConfig `gorm:"foreignKey:ProviderConfigID" json:"provider_config,omitempty"`
}

// GameEvaluation stores the end-of-session LLM evaluation result.
type GameEvaluation struct {
	ID        uint                         `gorm:"primaryKey;autoIncrement" json:"id"`
	SessionID uint                         `gorm:"not null;uniqueIndex" json:"session_id"`
	Content   JSONField[EvaluationContent] `gorm:"type:text;not null" json:"content"`
	CreatedAt time.Time                    `json:"created_at"`
}

// EvaluationContent mirrors agent.EvaluationResult but lives in models
// to avoid an import cycle (models ← agent ← models).
type EvaluationContent struct {
	Summary string              `json:"summary"`
	Players []PlayerEvalContent `json:"players"`
}

type PlayerEvalContent struct {
	CharacterName string `json:"character_name"`
	Comment       string `json:"comment"`
	Score         int    `json:"score"`
	BaseCoins     int    `json:"base_coins"`
	BonusCoins    int    `json:"bonus_coins"`
}

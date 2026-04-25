package models

import (
	"encoding/json"
	"time"
)

type Role string

const (
	RoleUser  Role = "user"
	RoleAdmin Role = "admin"
)

const (
	GenderMale   = "男"
	GenderFemale = "女"
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
	Inventory       JSONField[[]string]         `gorm:"type:text" json:"inventory"`
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
	SystemPrompt  string      `json:"system_prompt"`
	Setting       string      `json:"setting"`
	Intro         string      `json:"intro"`
	GameStartSlot int         `json:"game_start_slot,omitempty"` // 开局时间槽位(0-47)，每槽30分钟
	Scenes        []SceneData `json:"scenes"`
	NPCs          []NPCData   `json:"npcs"`
	Clues         []string    `json:"clues"`
	WinCondition  string      `json:"win_condition"`
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
	ID   uint   `gorm:"primaryKey;autoIncrement" json:"id"`
	Name string `gorm:"not null;size:200;uniqueIndex" json:"name"`
	// Data stores the module JSON payload directly (name is stored separately).
	Data      JSONField[json.RawMessage] `gorm:"column:content;type:text;not null" json:"data"`
	IsActive  bool                       `gorm:"default:true" json:"is_active"`
	CreatedAt time.Time                  `json:"created_at"`
	UpdatedAt time.Time                  `json:"updated_at"`

	// Derived fields are decoded from Data for API compatibility.
	Description string                     `gorm:"-" json:"description"`
	Author      string                     `gorm:"-" json:"author"`
	Tags        string                     `gorm:"-" json:"tags"`
	MinPlayers  int                        `gorm:"-" json:"min_players"`
	MaxPlayers  int                        `gorm:"-" json:"max_players"`
	Difficulty  string                     `gorm:"-" json:"difficulty"`
	Content     JSONField[ScenarioContent] `gorm:"-" json:"content"`
}

type SessionStatus string

const (
	SessionStatusLobby   SessionStatus = "lobby"
	SessionStatusPlaying SessionStatus = "playing"
	SessionStatusEnded   SessionStatus = "ended"
)

// ChatMsg is a minimal role+content pair for persisting LLM conversation history as JSON.
type ChatMsg struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type GameSession struct {
	ID            uint                 `gorm:"primaryKey;autoIncrement" json:"id"`
	Name          string               `gorm:"not null;size:200" json:"name"`
	ScenarioID    uint                 `gorm:"not null" json:"scenario_id"`
	Status        SessionStatus        `gorm:"default:'lobby'" json:"status"`
	MaxPlayers    int                  `gorm:"default:4" json:"max_players"`
	Password      string               `gorm:"size:100" json:"-"`
	HasPassword   bool                 `gorm:"default:false" json:"has_password"`
	CreatedBy     uint                 `gorm:"not null" json:"created_by"`
	TurnRound     int                  `gorm:"default:1" json:"turn_round"`
	WriterHistory JSONField[[]ChatMsg] `gorm:"type:text" json:"-"`
	CreatedAt     time.Time            `json:"created_at"`
	UpdatedAt     time.Time            `json:"updated_at"`
	Scenario      Scenario             `gorm:"foreignKey:ScenarioID" json:"scenario,omitempty"`
	Creator       User                 `gorm:"foreignKey:CreatedBy" json:"creator,omitempty"`
	Players       []SessionPlayer      `gorm:"foreignKey:SessionID" json:"players,omitempty"`
}

// SessionNPC is a temporary NPC card created during a session (e.g. monsters, minor NPCs).
type SessionNPC struct {
	ID          uint                      `gorm:"primaryKey;autoIncrement" json:"id"`
	SessionID   uint                      `gorm:"not null;index" json:"session_id"`
	Name        string                    `gorm:"not null;size:100" json:"name"`
	Description string                    `gorm:"type:text" json:"description"`
	Attitude    string                    `gorm:"size:100" json:"attitude"`
	Goal        string                    `gorm:"size:200" json:"goal"`
	Secret      string                    `gorm:"type:text" json:"secret"`
	RiskPref    string                    `gorm:"size:50" json:"risk_preference"`
	Stats       JSONField[map[string]int] `gorm:"type:text" json:"stats"`
	Skills      JSONField[map[string]int] `gorm:"type:text" json:"skills"`
	Spells      JSONField[[]string]       `gorm:"type:text" json:"spells"`
	AgentCtx    JSONField[[]ChatMsg]      `gorm:"type:text" json:"agent_ctx"`
	IsAlive     bool                      `gorm:"default:true" json:"is_alive"`
	CreatedAt   time.Time                 `json:"created_at"`
	UpdatedAt   time.Time                 `json:"updated_at"`
}

// SessionNPCMemory stores compacted memory for destroyed temporary NPCs.
// Used to restore personality/continuity when the same NPC is recreated later.
type SessionNPCMemory struct {
	ID            uint      `gorm:"primaryKey;autoIncrement" json:"id"`
	SessionID     uint      `gorm:"not null;index" json:"session_id"`
	Name          string    `gorm:"not null;size:100;index" json:"name"`
	MemorySummary string    `gorm:"type:text" json:"memory_summary"`
	UpdatedAt     time.Time `json:"updated_at"`
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

// SessionGrowthMark records a successful skill check during a session.
// At session end these marks are used to run COC classic growth checks.
// A skill appears at most once per character per session (duplicates are ignored).
type SessionGrowthMark struct {
	ID            uint   `gorm:"primaryKey;autoIncrement" json:"id"`
	SessionID     uint   `gorm:"not null;index" json:"session_id"`
	CharacterName string `gorm:"not null;size:100" json:"character_name"`
	Skill         string `gorm:"not null;size:100" json:"skill"`
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
	ItemTypeCardSlot  ItemType = "card_slot" // 卡槽扩展
	ItemTypeCoins     ItemType = "coins"     // 金币
	ItemTypeEquipment ItemType = "equipment" // 基础装备
	ItemTypeWeapon    ItemType = "weapon"    // 武器
	ItemTypeAccessory ItemType = "accessory" // 配件
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
	AgentRoleArchitect AgentRole = "architect"
	AgentRoleLore      AgentRole = "lore_researcher"
	AgentRoleEncounter AgentRole = "encounter_designer"
	AgentRoleQAGuard   AgentRole = "qa_guard"
	AgentRoleWriter    AgentRole = "writer"
	AgentRoleEvaluator AgentRole = "evaluator"
	AgentRoleGrowth    AgentRole = "growth"
	AgentRoleLawyer    AgentRole = "lawyer"
	AgentRoleNPC       AgentRole = "npc"
	AgentRoleParser    AgentRole = "parser"
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

// ── Site settings & invite codes ─────────────────────────────────────────────

// SiteSetting is a simple key/value store for site-wide configuration.
type SiteSetting struct {
	Key   string `gorm:"primaryKey;size:100" json:"key"`
	Value string `gorm:"not null" json:"value"`
}

// InviteCode is a single-use registration invite code.
type InviteCode struct {
	ID        uint       `gorm:"primaryKey;autoIncrement" json:"id"`
	Code      string     `gorm:"uniqueIndex;not null;size:20" json:"code"`
	CreatedBy uint       `gorm:"not null" json:"created_by"`
	UsedBy    *uint      `json:"used_by"`
	UsedAt    *time.Time `json:"used_at"`
	CreatedAt time.Time  `json:"created_at"`
	Creator   User       `gorm:"foreignKey:CreatedBy" json:"creator,omitempty"`
	UsedUser  *User      `gorm:"foreignKey:UsedBy" json:"used_user,omitempty"`
}

// GetSiteSetting returns the value for the given key, or defaultVal if not found.
func GetSiteSetting(key, defaultVal string) string {
	var s SiteSetting
	if err := DB.Where("key = ?", key).First(&s).Error; err != nil {
		return defaultVal
	}
	return s.Value
}

// SetSiteSetting upserts a site setting.
func SetSiteSetting(key, val string) error {
	return DB.Where("key = ?", key).Assign(SiteSetting{Value: val}).FirstOrCreate(&SiteSetting{Key: key}).Error
}

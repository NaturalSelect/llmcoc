// NOTE: Package models defines the core data structures and database schema for the application.
package models

import (
	"encoding/json"
	"time"
)

// NOTE: Role distinguishes between regular users and administrators.
type Role string

const (
	RoleUser  Role = "user"
	RoleAdmin Role = "admin"
)

const (
	GenderMale   = "男"
	GenderFemale = "女"
)

// NOTE: User represents a registered user account in the system.
type User struct {
	ID           uint      `gorm:"primaryKey;autoIncrement" json:"id"`
	Username     string    `gorm:"uniqueIndex;not null;size:50" json:"username"`
	Email        string    `gorm:"uniqueIndex;not null;size:200" json:"email"`
	PasswordHash string    `gorm:"not null" json:"-"`
	Role         Role      `gorm:"default:'user';not null" json:"role"`
	IsBanned     bool      `gorm:"default:false;not null" json:"is_banned"`
	BanReason    string    `gorm:"size:500" json:"ban_reason"`
	Coins        int       `gorm:"default:0;not null" json:"coins"`
	CardSlots    int       `gorm:"default:3;not null" json:"card_slots"`
	ReviveCount  int       `gorm:"default:0;not null" json:"revive_count"` // 累计复活次数，影响后续复活费用
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

// COC 7th character attributes
// NOTE: CharacterStats holds the core attributes and derived stats for a Call of Cthulhu investigator.
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

type CharacterAttributeRoll struct {
	Formula string `json:"formula"`
	Dice    []int  `json:"dice"`
	Total   int    `json:"total"`
	Base    int    `json:"base"`
	Final   int    `json:"final"`
}

type CharacterLuckRoll struct {
	Formula string                   `json:"formula"`
	Rolls   []CharacterAttributeRoll `json:"rolls"`
	Kept    int                      `json:"kept"`
}

type CharacterEDUEnhancementRoll struct {
	Index       int  `json:"index"`
	D100        int  `json:"d100"`
	BeforeEDU   int  `json:"before_edu"`
	Improved    bool `json:"improved"`
	IncreaseDie int  `json:"increase_die"`
	Increase    int  `json:"increase"`
	AfterEDU    int  `json:"after_edu"`
}

type CharacterRawRolls struct {
	Age             int                           `json:"age"`
	STR             CharacterAttributeRoll        `json:"str"`
	CON             CharacterAttributeRoll        `json:"con"`
	SIZ             CharacterAttributeRoll        `json:"siz"`
	DEX             CharacterAttributeRoll        `json:"dex"`
	APP             CharacterAttributeRoll        `json:"app"`
	INT             CharacterAttributeRoll        `json:"int"`
	POW             CharacterAttributeRoll        `json:"pow"`
	EDU             CharacterAttributeRoll        `json:"edu"`
	Luck            CharacterLuckRoll             `json:"luck"`
	EDUEnhancements []CharacterEDUEnhancementRoll `json:"edu_enhancements"`
	AgeLog          []string                      `json:"age_log"`
}

type CharacterDraft struct {
	ID        uint                         `gorm:"primaryKey;autoIncrement" json:"id"`
	UserID    uint                         `gorm:"not null;index:idx_character_drafts_user_active" json:"user_id"`
	Stats     JSONField[CharacterStats]    `gorm:"type:text;not null" json:"stats"`
	RawRolls  JSONField[CharacterRawRolls] `gorm:"type:text;not null" json:"raw_rolls"`
	ExpiresAt time.Time                    `gorm:"not null;index:idx_character_drafts_user_active" json:"expires_at"`
	IsUsed    bool                         `gorm:"default:false;not null;index:idx_character_drafts_user_active" json:"is_used"`
	UsedAt    *time.Time                   `json:"used_at"`
	CreatedAt time.Time                    `json:"created_at"`
	UpdatedAt time.Time                    `json:"updated_at"`
	User      User                         `gorm:"foreignKey:UserID" json:"-"`
}

// SocialRelation represents a named relationship on a character card.
// NOTE: Tracks connections an investigator has to other people or entities.
type SocialRelation struct {
	Name         string `json:"name"`
	Relationship string `json:"relationship"`
	Note         string `json:"note"`
}

type Asset struct {
	Name     string `json:"name"`
	Category string `json:"category"`
	Note     string `json:"note"`
}

// NOTE: CharacterCard represents a player's investigator, containing all their stats, skills, inventory, and current state.
type CharacterCard struct {
	ID              uint                        `gorm:"primaryKey;autoIncrement" json:"id"`
	UserID          uint                        `gorm:"not null;index" json:"user_id"`
	Name            string                      `gorm:"not null;size:100" json:"name"`
	Race            string                      `gorm:"size:50" json:"race"` // 新增种族字段
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
	Assets          JSONField[[]Asset]          `gorm:"type:text" json:"assets"`
	Spells          JSONField[[]string]         `gorm:"type:text" json:"spells"`
	SeenMonsters    JSONField[[]string]         `gorm:"type:text" json:"seen_monsters"` // 已见过的神话存在(见过的不掉SAN)
	IsActive        bool                        `gorm:"default:true" json:"is_active"`
	AvatarURL       string                      `gorm:"size:500" json:"avatar_url"`
	// COC 理智与疯狂状态
	MadnessState       string `gorm:"size:20;default:'none'" json:"madness_state"` // none/temporary/indefinite/permanent
	MadnessSymptom     string `gorm:"type:text" json:"madness_symptom"`            // 当前疯狂症状描述
	MadnessDuration    int    `gorm:"default:0" json:"madness_duration"`           // 剩余轮数(临时性)或标记仍在发作(不定性)
	DailySanLoss       int    `gorm:"default:0" json:"daily_san_loss"`             // 当日累计SAN损失(用于不定性疯狂判断)
	CthulhuMythosSkill int    `gorm:"default:0" json:"cthulhu_mythos_skill"`       // 克苏鲁神话技能值(控制最大SAN上限)
	// COC 伤亡状态
	WoundState    string    `gorm:"size:20;default:'none'" json:"wound_state"` // none/major/dying/dead
	IsUnconscious bool      `gorm:"default:false" json:"is_unconscious"`
	IsDeleted     bool      `gorm:"default:false;not null" json:"is_deleted"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
	User          User      `gorm:"foreignKey:UserID" json:"-"`
}

// NOTE: ScenarioReward describes a findable mythos item or tome pre-placed in the scenario.
// Designed at Stage 2 (MisdirectionFabric) where the mythos anchor is known.
type ScenarioReward struct {
	Name          string `json:"name"`           // COC7正式名称或场景专属名称
	Type          string `json:"type"`           // "tome" | "artifact"
	Description   string `json:"description"`    // 外观特征及与场景叙事的关联
	MechanicsNote string `json:"mechanics_note"` // 规则效果（tome：SAN代价+学习收益；artifact：激活条件+代价）
	FindCondition string `json:"find_condition"` // 获取条件（技能检定名称+难度，或触发特定事件）
}

// NOTE: ScenarioContent defines the narrative and structural elements of a playable scenario.
type ScenarioContent struct {
	SystemPrompt    string          `json:"system_prompt"`
	Setting         string          `json:"setting"`
	ToneTags        []string        `json:"tone_tags"`
	HorrorMode      string          `json:"horror_mode"`
	InvestFocus     string          `json:"invest_focus"`
	Intro           string          `json:"intro"`
	GameStartSlot   int             `json:"game_start_slot"`            // 开局时间槽位(0-47),每槽30分钟
	MapDescription  string          `json:"map_description"`            // 文字描述的场景地图,供KP感知空间关系
	Scenes          []SceneData     `json:"scenes"`                     //
	NPCs            []NPCData       `json:"npcs"`                       //
	Clues           []ClueData      `json:"clues"`                      // 结构化线索（来源/推荐检定/成功/失败推进/性质）
	Endings         []EndingData    `json:"endings"`                    // 命名多结局（触发条件+SAN恢复），替代旧的win/lose/partial
	Reward          *ScenarioReward `json:"reward"`                     // 通关奖励（典籍/神话物品），达成非失败结局时给予
	Handouts        []HandoutData   `json:"handouts,omitempty"`         // 开局手卡，可直接朗读给玩家
	Timeline        []TimelineEvent `json:"timeline,omitempty"`         // 时间线（过去线痕迹+当天推进）
	KeeperAppendix  *KeeperAppendix `json:"keeper_appendix,omitempty"`  // 守秘人附录（难度调节/单双人团/恐怖呈现）
	EntryIdentities []EntryIdentity `json:"entry_identities,omitempty"` // 导入身份表（不同职业入场方式）
	Mechanics       []MechanicData  `json:"mechanics,omitempty"`        // 量化核心机制标记（计数器/时钟），仅作KP参考
	MythosAnchor    string          `json:"mythos_anchor"`              // Stage2确认的神话锚点，用于多样性去重
	MythosCore      string          `json:"mythos_core"`                // 神话本质核心揭示（永不放入Clues，不通过found_clue暴露给玩家）
}

// NOTE: ClueData 是结构化线索。旧版本 clues 为 []string（形如"[真实]xxx"），现拆成独立字段
// 便于前端表格化展示与KP裁定；旧数据由 ScenarioContent.UnmarshalJSON 兼容转换。
type ClueData struct {
	Summary    string `json:"summary"`               // 线索内容
	Source     string `json:"source,omitempty"`      // 来源地点或对象
	SkillCheck string `json:"skill_check,omitempty"` // 推荐检定技能
	OnSuccess  string `json:"on_success,omitempty"`  // 成功获得的信息或效果
	OnFailure  string `json:"on_failure,omitempty"`  // 失败时的推进（不卡关设计）
	Nature     string `json:"nature"`                // 真实 | 隐藏 | 误导
}

// NOTE: EndingData 是命名结局，替代旧的 win/lose/partial 三字段，支持任意数量的分支结局。
type EndingData struct {
	Name        string `json:"name"`                  // 结局名称
	Trigger     string `json:"trigger"`               // 触发条件
	Description string `json:"description,omitempty"` // 结局叙事描述
	SANReward   string `json:"san_reward,omitempty"`  // SAN 恢复/损失，如"恢复1d6"
	IsFailure   bool   `json:"is_failure,omitempty"`  // 是否为失败/灾难结局
}

// NOTE: HandoutData 是开局手卡，KP 可直接朗读给玩家的内容。
type HandoutData struct {
	Title   string `json:"title"`            // 手卡标题
	Content string `json:"content"`          // 可直接朗读的正文
	Timing  string `json:"timing,omitempty"` // 发放时机
}

// NOTE: TimelineEvent 是时间线上的单个事件节点。
type TimelineEvent struct {
	Time  string `json:"time"`            // 时间标记
	Event string `json:"event"`           // 事件描述
	Phase string `json:"phase,omitempty"` // past（过去线）| current（当天推进）
}

// NOTE: KeeperAppendix 是守秘人运营附录，指导难度调节与团型适配。
type KeeperAppendix struct {
	DifficultyDown string `json:"difficulty_down,omitempty"` // 降低难度建议
	DifficultyUp   string `json:"difficulty_up,omitempty"`   // 提高难度建议
	SoloAdvice     string `json:"solo_advice,omitempty"`     // 单人团建议
	GroupAdvice    string `json:"group_advice,omitempty"`    // 多人团建议
	HorrorTips     string `json:"horror_tips,omitempty"`     // 恐怖呈现建议
	ThemeGuidance  string `json:"theme_guidance,omitempty"`  // 主题把握提示
}

// NOTE: EntryIdentity 是导入身份，描述不同职业调查员的入场方式。
type EntryIdentity struct {
	Profession     string `json:"profession"`                // 职业名
	InitResource   string `json:"init_resource"`             // 初始资源
	InitLimit      string `json:"init_limit,omitempty"`      // 初始限制
	RecommendClues string `json:"recommend_clues,omitempty"` // 推荐开局线索
}

// NOTE: MechanicData 是量化核心机制标记（如"三重承认"计数、反派"行动时钟"），
// 仅作为KP参考信息注入跑团提示词，不做自动推进的硬编码逻辑。
type MechanicData struct {
	Name        string          `json:"name"`             // 机制名
	Type        string          `json:"type"`             // counter | clock | tracker
	Description string          `json:"description"`      // 机制说明
	Stages      []MechanicStage `json:"stages,omitempty"` // 阶段/状态列表
}

// NOTE: MechanicStage 是量化机制的单个阶段。
type MechanicStage struct {
	Label   string `json:"label"`             // 阶段标签
	Effect  string `json:"effect,omitempty"`  // 该阶段效果
	Trigger string `json:"trigger,omitempty"` // 推进/触发条件
}

// NOTE: SceneData describes a specific location or event in a scenario.
type SceneData struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Triggers    []string `json:"triggers"`
}

// NOTE: NPCData provides a template for a non-player character within a scenario.
type NPCData struct {
	Name               string         `json:"name"`
	Race               string         `json:"race"`
	Occupation         string         `json:"occupation"`
	Description        string         `json:"description"`
	Attitude           string         `json:"attitude"`
	Stats              map[string]int `json:"stats"`
	CthulhuMythosSkill int            `json:"cthulhu_mythos_skill"`
}

// NOTE: Scenario is the database representation of a playable module or adventure.
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

type ScenarioGenerationLog struct {
	ID            uint      `gorm:"primaryKey;autoIncrement" json:"id"`
	ScenarioID    uint      `gorm:"not null;index" json:"scenario_id"`
	ScenarioName  string    `gorm:"not null;size:200;index" json:"scenario_name"`
	LogText       string    `gorm:"type:text;not null" json:"log_text"`
	StoryDocument string    `gorm:"type:text" json:"story_document"` // Story Architect 提交的原始故事文档全文，供后台审查创作质量
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
	Scenario      Scenario  `gorm:"foreignKey:ScenarioID" json:"-"`
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

// NOTE: GameSession tracks an active or completed run of a scenario with players.
type GameSession struct {
	ID            uint                    `gorm:"primaryKey;autoIncrement" json:"id"`
	Name          string                  `gorm:"not null;size:200" json:"name"`
	Race          string                  `gorm:"size:50" json:"race"` // 新增种族字段
	ScenarioID    uint                    `gorm:"not null" json:"scenario_id"`
	Status        SessionStatus           `gorm:"default:'lobby'" json:"status"`
	MaxPlayers    int                     `gorm:"default:4" json:"max_players"`
	Password      string                  `gorm:"size:100" json:"-"`
	HasPassword   bool                    `gorm:"default:false" json:"has_password"`
	CreatedBy     uint                    `gorm:"not null" json:"created_by"`
	TurnRound     int                     `gorm:"default:1" json:"turn_round"`
	WriterHistory JSONField[[]ChatMsg]    `gorm:"type:text" json:"-"`
	CombatState   JSONField[*CombatState] `gorm:"type:text" json:"-"`
	ChaseState    JSONField[*ChaseState]  `gorm:"type:text" json:"-"`
	KPHint        string                  `gorm:"type:text" json:"-"` // KP自写的当前场景高密度提示
	Introspection string                  `gorm:"type:text" json:"-"` // KP自写的当前场景推理过程
	CreatedAt     time.Time               `json:"created_at"`
	UpdatedAt     time.Time               `json:"updated_at"`
	Scenario      Scenario                `gorm:"foreignKey:ScenarioID" json:"scenario"`
	Creator       User                    `gorm:"foreignKey:CreatedBy" json:"creator"`
	Players       []SessionPlayer         `gorm:"foreignKey:SessionID" json:"players"`
}

// SessionNPC is a temporary NPC card created during a session (e.g. monsters, minor NPCs).
type SessionNPC struct {
	ID                 uint                      `gorm:"primaryKey;autoIncrement" json:"id"`
	SessionID          uint                      `gorm:"not null;index" json:"session_id"`
	Name               string                    `gorm:"not null;size:100" json:"name"`
	Race               string                    `gorm:"size:50" json:"race"` // 新增种族字段
	Occupation         string                    `gorm:"size:100" json:"occupation"`
	Description        string                    `gorm:"type:text" json:"description"`
	Attitude           string                    `gorm:"size:100" json:"attitude"`
	Goal               string                    `gorm:"size:200" json:"goal"`
	Secret             string                    `gorm:"type:text" json:"secret"`
	RiskPref           string                    `gorm:"size:50" json:"risk_preference"`
	Location           string                    `gorm:"size:200" json:"location"`
	LLMNote            string                    `gorm:"type:text" json:"llm_note"`
	Stats              JSONField[map[string]int] `gorm:"type:text" json:"stats"`
	Skills             JSONField[map[string]int] `gorm:"type:text" json:"skills"`
	Spells             JSONField[[]string]       `gorm:"type:text" json:"spells"`
	AgentCtx           JSONField[[]ChatMsg]      `gorm:"type:text" json:"agent_ctx"`
	CthulhuMythosSkill int                       `gorm:"default:0" json:"cthulhu_mythos_skill"`
	WoundState         string                    `gorm:"column:wound_state;size:20;default:'none'" json:"wound_state"`
	IsAlive            bool                      `gorm:"default:true" json:"is_alive"`
	CreatedAt          time.Time                 `json:"created_at"`
	UpdatedAt          time.Time                 `json:"updated_at"`
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
	LLMNote         string        `gorm:"type:text" json:"llm_note"`
	Location        string        `gorm:"size:200" json:"location"` // 当前所在地点，由 update_location 工具维护
	Armor           int           `gorm:"default:0" json:"armor"`   // 当前护甲值，由 update_armor 工具维护
	User            User          `gorm:"foreignKey:UserID" json:"user"`
	CharacterCard   CharacterCard `gorm:"foreignKey:CharacterCardID" json:"character_card"`
}

// SessionFavorite stores a user's favorite sessions (many-to-many relationship)
type SessionFavorite struct {
	ID        uint        `gorm:"primaryKey;autoIncrement" json:"id"`
	UserID    uint        `gorm:"not null;index" json:"user_id"`
	SessionID uint        `gorm:"not null;index" json:"session_id"`
	CreatedAt time.Time   `json:"created_at"`
	User      User        `gorm:"foreignKey:UserID" json:"-"`
	Session   GameSession `gorm:"foreignKey:SessionID" json:"-"`
}

type MessageRole string

const (
	MessageRoleUser      MessageRole = "user"
	MessageRoleAssistant MessageRole = "assistant"
	MessageRoleSystem    MessageRole = "system"
)

// NOTE: Message records a single chat message sent within a game session.
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
	ShopItem   ShopItem  `gorm:"foreignKey:ShopItemID" json:"shop_item"`
}

type CoinRecharge struct {
	ID        uint      `gorm:"primaryKey;autoIncrement" json:"id"`
	UserID    uint      `gorm:"not null;index" json:"user_id"`
	Amount    int       `gorm:"not null" json:"amount"`
	AdminID   uint      `gorm:"not null" json:"admin_id"`
	Note      string    `gorm:"size:500" json:"note"`
	CreatedAt time.Time `json:"created_at"`
	User      User      `gorm:"foreignKey:UserID" json:"user"`
	Admin     User      `gorm:"foreignKey:AdminID" json:"admin"`
}

// ── Combat cross-round state ──────────────────────────────────────────────────

// CombatParticipant tracks one combatant's cross-round state.
type CombatParticipant struct {
	Name          string `json:"name"`
	DEX           int    `json:"dex"`
	HP            int    `json:"hp"`
	IsNPC         bool   `json:"is_npc"`
	HasActed      bool   `json:"has_acted"`        // 本轮是否已行动
	HasDodgedOrFB bool   `json:"has_dodged_or_fb"` // 本轮是否已闪避/反击(寡不敌众判断用)
	IsAiming      bool   `json:"is_aiming"`        // 是否正在瞄准(下轮攻击+奖励骰)
	APDebt        int    `json:"ap_debt"`          // 下轮行动点扣除(寻找掩体等动作欠债)
	WoundState    string `json:"wound_state"`      // none/major/dying/dead
}

// CombatState holds the full cross-round state of an ongoing combat encounter.
// Stored as a JSON column on GameSession; nil means no active combat.
type CombatState struct {
	Active       bool                `json:"active"`
	Round        int                 `json:"round"`
	Participants []CombatParticipant `json:"participants"` // DEX降序排列
	ActorIndex   int                 `json:"actor_index"`  // 当前行动者在Participants中的索引
}

// ── Chase cross-round state ───────────────────────────────────────────────────

// ChaseParticipant tracks one participant's cross-round state in a chase.
type ChaseParticipant struct {
	Name      string `json:"name"`
	IsNPC     bool   `json:"is_npc"`
	MOV       int    `json:"mov"`      // 速度检定后固定的MOV值
	Location  int    `json:"location"` // 当前地点索引(数字越大越靠前)
	APDebt    int    `json:"ap_debt"`  // 下轮扣除的行动点(险境失败欠债)
	IsPursuer bool   `json:"is_pursuer"`
}

// ChaseObstacle represents a persistent obstacle between two chase locations.
type ChaseObstacle struct {
	Name    string `json:"name"`
	Between [2]int `json:"between"` // 阻挡的两个相邻地点索引
	HP      int    `json:"hp"`
	MaxHP   int    `json:"max_hp"`
}

// ChaseState holds the full cross-round state of an ongoing chase.
// Stored as a JSON column on GameSession; nil means no active chase.
type ChaseState struct {
	Active       bool               `json:"active"`
	Round        int                `json:"round"`
	MinMOV       int                `json:"min_mov"` // 所有参与者中最低MOV,用于计算行动点
	Participants []ChaseParticipant `json:"participants"`
	Obstacles    []ChaseObstacle    `json:"obstacles"`
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
	AgentRolePainter   AgentRole = "painter"
	AgentRoleParser    AgentRole = "parser"
	// NOTE: AgentRoleTranslator 负责发散联想、世界知识和资料转译；独立于Lawyer，不复用其provider/model。
	AgentRoleTranslator AgentRole = "translator"
	// NOTE: AgentRoleCompiler 负责把故事阶段产出的纯文本剧本编译为结构化ScenarioContent；
	// 只做格式转换和技术字段补充，无权改写故事事实。
	AgentRoleCompiler AgentRole = "compiler"
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
	ID               uint      `gorm:"primaryKey;autoIncrement" json:"id"`
	Role             AgentRole `gorm:"not null;size:50;uniqueIndex" json:"role"`
	ProviderConfigID *uint     `json:"provider_config_id"`
	ModelName        string    `gorm:"size:200" json:"model_name"`
	MaxTokens        int       `gorm:"default:1024" json:"max_tokens"`
	Temperature      float32   `gorm:"default:0.7;type:real" json:"temperature"`
	// DisableTemperature 为 true 时不在 API 请求中发送 temperature 参数（用于不支持的模型）
	DisableTemperature bool               `gorm:"default:false" json:"disable_temperature"`
	SystemPrompt       string             `gorm:"type:text" json:"system_prompt"`
	ThinkingLevel      string             `gorm:"size:20;default:'high'" json:"thinking_level"` // none|low|medium|high|xhigh
	IsActive           bool               `gorm:"default:true" json:"is_active"`
	CreatedAt          time.Time          `json:"created_at"`
	UpdatedAt          time.Time          `json:"updated_at"`
	ProviderConfig     *LLMProviderConfig `gorm:"foreignKey:ProviderConfigID" json:"provider_config"`
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

// DefaultBalanceRules is the default balance-adjustment rule text seeded on new
// installations. It is also the runtime fallback used by the Lawyer agent when
// the "balance_rules" key is absent from the database.
// agent/lawyer.go references this constant directly to avoid string duplication.
const DefaultBalanceRules = "调查员/玩家被禁止使用《精神转移术》,《精神交换术》,《内心灵光唤醒术》,《完善术》，《伊格的尖牙》, 任何涉及到这些法术的查询都必须告知KP这一禁令, 并且明确说明这些法术无法作为任何调查员属性变更的依据"

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
	Creator   User       `gorm:"foreignKey:CreatedBy" json:"creator"`
	UsedUser  *User      `gorm:"foreignKey:UsedBy" json:"used_user"`
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

// LawyerCacheStats stores cumulative lawyer cache hit/miss statistics.
// Only one row should exist (ID=1) and it is periodically refreshed.
type LawyerCacheStats struct {
	ID          uint      `gorm:"primaryKey;autoIncrement" json:"id"`
	FullHits    int64     `gorm:"not null;default:0" json:"full_hits"`
	PartialHits int64     `gorm:"not null;default:0" json:"partial_hits"`
	Misses      int64     `gorm:"not null;default:0" json:"misses"`
	SavedAt     time.Time `json:"saved_at"`
}

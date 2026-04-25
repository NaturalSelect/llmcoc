package agent

import (
	"github.com/llmcoc/server/internal/models"
	"github.com/llmcoc/server/internal/services/llm"
)

// GameContext carries all information the agent pipeline needs for one chat turn.
type GameContext struct {
	Session   models.GameSession
	History   []models.Message // recent messages, system role excluded
	UserInput string
	UserName  string
	// PendingActions holds all player actions collected for the current round.
	// Populated only in multi-player sessions once all players have submitted;
	// when non-empty the KP prompt shows a combined action summary instead of
	// the single UserInput/UserName fields.
	PendingActions []PlayerAction
}

// PlayerAction is one player's submitted action for the current round.
type PlayerAction struct {
	PlayerName string
	Content    string
}

// ── Master-Slave Tool Call Types ─────────────────────────────────────────────

// ToolCallType identifies the action the master KP agent wants to perform.
type ToolCallType string

const (
	ToolCheckRule         ToolCallType = "check_rule"          // 查阅规则书
	ToolReadRulebookConst ToolCallType = "read_rulebook_const" // 读取规则书常量目录/列表
	ToolRollDice          ToolCallType = "roll_dice"           // 骰子检定
	ToolNPCAct            ToolCallType = "npc_act"             // NPC行动
	ToolCreateNPC         ToolCallType = "create_npc"          // 创建临时NPC
	ToolDestroyNPC        ToolCallType = "destroy_npc"         // 销毁临时NPC
	ToolDestoryNPC        ToolCallType = "destory_npc"         // 兼容拼写错误: destroy_npc
	ToolActNPC            ToolCallType = "act_npc"             // 与指定NPC对话并获取反应
	ToolUpdateCharacters  ToolCallType = "update_characters"   // 更新角色状态
	ToolManageInventory   ToolCallType = "manage_inventory"    // 角色物品增删
	ToolRecordMonster     ToolCallType = "record_monster"      // 记录已见神话存在
	ToolManageSpell       ToolCallType = "manage_spell"        // 管理已掌握法术
	ToolManageRelation    ToolCallType = "manage_relation"     // 管理社会关系
	ToolEndGame           ToolCallType = "end_game"            // 结束游戏
	ToolTriggerMadness    ToolCallType = "trigger_madness"     // 触发疯狂发作
	ToolWrite             ToolCallType = "write"               // 生成叙事段落
	ToolAdvanceTime       ToolCallType = "advance_time"        // 推进游戏内时间
	ToolQueryClues        ToolCallType = "query_clues"         // 查询剧本线索
	ToolQueryCharacter    ToolCallType = "query_character"     // 查询调查员完整人物卡
	ToolAnswer            ToolCallType = "answer"              // 结束本轮并给出回复
)

// ToolCall is one item in the master KP agent's output sequence.
type ToolCall struct {
	Action        ToolCallType           `json:"action"`
	Question      string                 `json:"question,omitempty"`       // check_rule: 规则问题的语义描述
	Constant      string                 `json:"constant,omitempty"`       // read_rulebook_const: 常量名
	Dice          *DiceCheck             `json:"dice,omitempty"`           // roll_dice: 骰子检定请求
	CharCard      *NPCCard               `json:"char_card,omitempty"`      // create_npc: NPC角色卡
	NPCName       string                 `json:"npc_name,omitempty"`       // npc_act: NPC名称
	NPCCtx        string                 `json:"npc_ctx,omitempty"`        // npc_act: 当前情境简述
	DestroyReason string                 `json:"destroy_reason,omitempty"` // destroy_npc: dead|out_of_range|cleanup
	Changes       []string               `json:"changes,omitempty"`        // update_characters: 状态变化列表
	CharacterName string                 `json:"character_name,omitempty"` // trigger_madness / query_character: 角色名称
	Operate       string                 `json:"operate,omitempty"`        // 通用操作: add/remove
	Item          string                 `json:"item,omitempty"`           // manage_inventory: 物品名称
	Monster       string                 `json:"monster,omitempty"`        // record_monster: 神话存在名称
	Spell         string                 `json:"spell,omitempty"`          // manage_spell: 法术名称
	Relation      *models.SocialRelation `json:"relation,omitempty"`       // manage_relation: 社会关系条目
	IsBystander   bool                   `json:"is_bystander,omitempty"`   // trigger_madness: 是否有旁观者
	Direction     string                 `json:"direction,omitempty"`      // write: 叙事方向（供Writer参考）
	TimeRounds    int                    `json:"time_rounds,omitempty"`    // advance_time: 推进的回合数
	TimeReason    string                 `json:"time_reason,omitempty"`    // advance_time: 原因（如"睡觉"/"吃饭"）
	Keyword       string                 `json:"keyword,omitempty"`        // query_clues: 可选关键词过滤
	Reply         string                 `json:"reply"`                    // answer: KP对玩家说的话（必填）
	EndSummary    string                 `json:"end_summary,omitempty"`    // end_game: 结局总结（可选）
}

// ToolResult wraps the result of executing one ToolCall.
type ToolResult struct {
	Action ToolCallType `json:"action"`
	Result string       `json:"result"`
}

// WriterState holds the writer's conversation history and accumulated narrative buffer.
// It is maintained across multiple write calls within the same turn to ensure continuity.
type WriterState struct {
	History []llm.ChatMessage // writer's own chat history for text continuity
	Buffer  string            // accumulated narrative text; streamed to player at turn end
}

// RunOutput is the structured result of a single agent pipeline run.
// WriterText and KPReply are kept separate so the handler can send them
// as distinct SSE event types, allowing the frontend to render them differently
// (e.g. writer narrative in large text, KP's spoken reply in smaller text).
type RunOutput struct {
	WriterText string // narrative from the Writer agent
	KPReply    string // KP's direct reply to the player (like a friend at the table)
}

// ── Dice types ────────────────────────────────────────────────────────────────

// DiceCheck represents a skill check requested by the KP.
type DiceCheck struct {
	Skill          string `json:"skill"`
	Value          int    `json:"value"`
	Character      string `json:"character"`
	Hidden         bool   `json:"hidden"`       // 暗骰：玩家不可见具体数值，KP将结果融入叙事
	CheckType      string `json:"check_type"`   // standard / opposed / luck / sanity
	BonusDice      int    `json:"bonus_dice"`   // 奖励骰数量
	PenaltyDice    int    `json:"penalty_dice"` // 惩罚骰数量
	SanSuccessLoss string `json:"san_success_loss"`
	SanFailLoss    string `json:"san_fail_loss"`
	MonsterName    string `json:"monster_name,omitempty"` // sanity检定：引发检定的神话存在名称（见过的存在不掉SAN）
}

// DiceCheckResult is the outcome of an auto-executed dice check.
type DiceCheckResult struct {
	DiceCheck
	Roll    int    `json:"roll"`
	Level   string `json:"level"`
	Success bool   `json:"success"`
	Message string `json:"message"`
	SanLoss int    `json:"san_loss"` // only for check_type="sanity"
}

// ── Character update types ────────────────────────────────────────────────────

// CharacterUpdate describes a single field update to a character card or NPC.
// Director change strings ("HP -3（角色名）") are parsed into this struct by
// parseStateChange in editor.go and applied directly without an LLM intermediary.
type CharacterUpdate struct {
	CharacterName string `json:"character_name"` // 目标角色/NPC名称
	Field         string `json:"field"`          // san/hp/mp/cthulhu_mythos
	Delta         int    `json:"delta"`          // 数值变化量
	AddValue      string `json:"add_value"`      // 新增条目（保留用于未来扩展）
	IsNPC         bool   `json:"is_npc"`         // true = 临时NPC卡
}

// ── NPC types ─────────────────────────────────────────────────────────────────

// NPCAction is the output for one NPC's turn.
type NPCAction struct {
	NPCName  string `json:"npc_name"`
	Action   string `json:"action"`
	Dialogue string `json:"dialogue"`
}

// NPCCard is the input schema for create_npc(char_card).
type NPCCard struct {
	Name           string         `json:"name"`
	Description    string         `json:"description"`
	Attitude       string         `json:"attitude"`
	Goal           string         `json:"goal,omitempty"`
	Secret         string         `json:"secret,omitempty"`
	RiskPreference string         `json:"risk_preference,omitempty"`
	Stats          map[string]int `json:"stats,omitempty"`
	Skills         map[string]int `json:"skills,omitempty"`
	Spells         []string       `json:"spells,omitempty"`
}

// ── Lawyer types ─────────────────────────────────────────────────────────────

// LawyerResult holds the rule text retrieved by the Lawyer agent for a single query.
type LawyerResult struct {
	Query    string `json:"query"`
	RuleText string `json:"rule_text"`
}

// ── Evaluation types ─────────────────────────────────────────────────────────

// PlayerEvaluation holds the per-player evaluation returned by the Evaluator agent.
type PlayerEvaluation struct {
	CharacterName string `json:"character_name"`
	Comment       string `json:"comment"`
	Score         int    `json:"score"`      // 0–100
	BaseCoins     int    `json:"base_coins"` // 基础奖励（固定 20）
	BonusCoins    int    `json:"bonus_coins"`
}

// EvaluationResult is the full output from the Evaluator agent.
type EvaluationResult struct {
	Summary string             `json:"summary"`
	Players []PlayerEvaluation `json:"players"`
}

// ── Growth types ─────────────────────────────────────────────────────────────

// SkillChange represents a single skill value change for a character.
type SkillChange struct {
	Skill string `json:"skill"`
	Delta int    `json:"delta"` // 正整数，1-10
}

// CharacterGrowth holds the growth outcome for one character.
type CharacterGrowth struct {
	CharacterName     string        `json:"character_name"`
	SkillChanges      []SkillChange `json:"skill_changes"`
	GrowthDescription string        `json:"growth_description"`
}

// GrowthResult is the full output from the Growth agent.
type GrowthResult struct {
	Characters []CharacterGrowth `json:"characters"`
}

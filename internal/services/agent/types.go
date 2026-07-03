// NOTE: Defines AI agent roles and their interactions.
package agent

import (
	"github.com/llmcoc/server/internal/models"
	"github.com/llmcoc/server/internal/services/llm"
)

// GameContext 承载一次聊天回合所需的全部上下文。
type GameContext struct {
	Session        models.GameSession
	History        []models.Message // 最近消息,不含system角色
	UserInput      string
	UserName       string
	UserInputAdmin bool // true if the user input is from the admin (KP), false for regular players
	// PendingActions 保存本轮已收集的玩家行动;多人房间只在所有人提交后填充。
	PendingActions []PlayerAction
	// Progress 用于把KP主流程的真实阶段推给上层SSE;为空时静默。
	Progress func(string)
}

// PlayerAction 是一个玩家在当前回合提交的行动。
type PlayerAction struct {
	IsAdmin    bool // true表示管理员/KP输入,false表示普通玩家输入
	PlayerName string
	Content    string
}

// ── Master-Slave Tool Call Types ─────────────────────────────────────────────

// ToolCallType identifies the action the master KP agent wants to perform.
type ToolCallType string

const (
	ToolCheckRule          ToolCallType = "check_rule"          // 查阅规则书
	ToolReadRulebookConst  ToolCallType = "read_rulebook_const" // 读取规则书常量目录/列表
	ToolRollDice           ToolCallType = "roll_dice"           // 骰子检定
	ToolCreateNPC          ToolCallType = "create_npc"          // 创建临时NPC
	ToolDestroyNPC         ToolCallType = "destroy_npc"         // 销毁临时NPC
	ToolActNPC             ToolCallType = "act_npc"             // 与指定NPC对话并获取反应
	ToolUpdateCharacters   ToolCallType = "update_characters"   // 更新角色状态
	ToolManageInventory    ToolCallType = "manage_inventory"    // 角色物品增删
	ToolRecordMonster      ToolCallType = "record_monster"      // 记录已见神话存在
	ToolManageSpell        ToolCallType = "manage_spell"        // 管理已掌握法术
	ToolManageRelation     ToolCallType = "manage_relation"     // 管理社会关系
	ToolManageAsset        ToolCallType = "manage_asset"        // 管理资产
	ToolEndGame            ToolCallType = "end_game"            // 结束游戏
	ToolManageMadness      ToolCallType = "manage_madness"      // 管理疯狂状态
	ToolWrite              ToolCallType = "write"               // 生成叙事段落
	ToolAdvanceTime        ToolCallType = "advance_time"        // 推进游戏内时间
	ToolQueryClues         ToolCallType = "query_clues"         // 查询剧本线索
	ToolQueryCharacter     ToolCallType = "query_character"     // 查询调查员完整人物卡
	ToolQueryNPCCard       ToolCallType = "query_npc_card"      // 查询NPC完整角色卡
	ToolUpdateNPCCard      ToolCallType = "update_npc_card"     // 更新NPC角色卡状态
	ToolUpdateLLMNote      ToolCallType = "update_llm_note"     // 更新Session级玩家LLMNote记录
	ToolUpdateNPCLLMNote   ToolCallType = "update_npc_llm_note" // 更新Session级NPC LLMNote记录
	ToolUpdateLocation     ToolCallType = "update_location"     // 更新调查员当前位置
	ToolUpdateNPCLocation  ToolCallType = "update_npc_location" // 更新NPC当前位置
	ToolUpdateArmor        ToolCallType = "update_armor"        // 更新调查员护甲值
	ToolHint               ToolCallType = "hint"                // KP写入当前场景高密度提示
	ToolGenerateImage      ToolCallType = "generate_image"      // NOTE: 生成即时场景图片
	ToolDescribeCharacters ToolCallType = "describe_characters" // NOTE: 获取调查员可见外貌描写
	ToolResponse           ToolCallType = "response"             // 结束本轮并给出回复
	ToolYield              ToolCallType = "yield"               // 本回合中途暂停,等待玩家输入后继续执行剩余工具调用
	ToolContract           ToolCallType = "contract"            // 批次合约,代表本batch的改动
	ToolReport             ToolCallType = "report"              // 向管理系统自首
)

// ToolCall is one item in the master KP agent's output sequence.
type ToolCall struct {
	Action        ToolCallType           `json:"action"`
	Contract      string                 `json:"contract,omitempty"`       // contract: KP本批次合约/执行计划文本
	Question      string                 `json:"question,omitempty"`       // check_rule: 规则问题的语义描述
	Constant      string                 `json:"constant,omitempty"`       // read_rulebook_const: 常量名
	Dice          *DiceCheck             `json:"dice,omitempty"`           // roll_dice: 骰子检定请求
	CharCard      *NPCCard               `json:"char_card,omitempty"`      // create_npc: NPC角色卡
	NPCName       string                 `json:"npc_name,omitempty"`       // npc_act: NPC名称
	NPCCtx        string                 `json:"npc_ctx,omitempty"`        // npc_act: 当前情境简述
	KPDirective   string                 `json:"kp_directive,omitempty"`   // act_npc: KP剧情指令(最高优先级行为约束)
	DestroyReason string                 `json:"destroy_reason,omitempty"` // destroy_npc: dead|out_of_range|cleanup
	Changes       []string               `json:"changes,omitempty"`        // update_characters: 状态变化列表
	CharacterName string                 `json:"character_name,omitempty"` // manage_madness / query_character: 角色名称
	Operate       string                 `json:"operate,omitempty"`        // 通用操作: add/remove
	Item          string                 `json:"item,omitempty"`           // manage_inventory: 物品名称(仅保留兼容,优先使用 item_name+item_desc+item_count)
	ItemName      string                 `json:"item_name,omitempty"`      // manage_inventory: 物品基础名称
	ItemDesc      string                 `json:"item_desc,omitempty"`      // manage_inventory: 物品状态描述(可选)
	ItemCount     int                    `json:"item_count,omitempty"`     // manage_inventory: 物品数量(省略或0均视为1)
	Monster       string                 `json:"monster,omitempty"`        // record_monster: 神话存在名称
	Spell         string                 `json:"spell,omitempty"`          // manage_spell: 法术名称
	Relation      *models.SocialRelation `json:"relation,omitempty"`       // manage_relation: 社会关系条目
	Asset         *models.Asset          `json:"asset,omitempty"`          // manage_asset: 资产条目
	IsBystander   bool                   `json:"is_bystander,omitempty"`   // manage_madness trigger: 是否有旁观者
	Direction     string                 `json:"direction,omitempty"`      // write: 叙事方向(供Writer参考)
	TimeRounds    int                    `json:"time_rounds,omitempty"`    // advance_time: 推进的回合数
	TimeReason    string                 `json:"time_reason,omitempty"`    // advance_time: 原因(如"睡觉"/"吃饭")
	Keyword       string                 `json:"keyword,omitempty"`        // query_clues: 已废弃(保留仅为兼容旧输出)
	LLMNote       string                 `json:"llm_note,omitempty"`       // update_llm_note: 玩家LLMNote内容
	NewLocation   string                 `json:"new_location,omitempty"`   // update_location/update_npc_location: 新位置名称
	ArmorValue    int                    `json:"armor_value"`              // update_armor: 新护甲值(0=无护甲)
	Hint          string                 `json:"hint,omitempty"`           // hit: KP当前场景高密度提示
	ImagePrompt   string                 `json:"image_prompt,omitempty"`   // NOTE: generate_image: 英文自然语言画图描述
	Characters    []string               `json:"characters,omitempty"`     // NOTE: describe_characters 参数;兼容旧 generate_image JSON,但生成图片时忽略
	Options       []string               `json:"options,omitempty"`        // response: 推荐给玩家的可行行动
	Reply         string                 `json:"reply"`                    // response: KP对玩家说的话(必填)
	EndSummary    string                 `json:"end_summary,omitempty"`    // end_game: 结局总结(可选)
	Reason        string                 `json:"reason,omitempty"`         // reasoning: KP本轮推理过程
	Context       string                 `json:"context,omitempty"`        // response: 剧本推进到此处的完整上下文

	// ── Combat fields ─────────────────────────────────────────────────────────
	CombatParticipants []CombatParticipantInput `json:"combat_participants,omitempty"` // start_combat: 参与者列表
	CombatActorName    string                   `json:"combat_actor_name,omitempty"`   // combat_act: 本轮行动者名称
	CombatAction       *CombatActionDetail      `json:"combat_action,omitempty"`       // combat_act: 行动详情
	CombatEndReason    string                   `json:"combat_end_reason,omitempty"`   // end_combat: 战斗结束原因

	// ── Chase fields ──────────────────────────────────────────────────────────
	ChaseParticipants []ChaseParticipantInput `json:"chase_participants,omitempty"` // start_chase: 参与者列表
	ChaseActorName    string                  `json:"chase_actor_name,omitempty"`   // chase_act: 本轮行动者名称
	ChaseAction       *ChaseActionDetail      `json:"chase_action,omitempty"`       // chase_act: 行动详情
	ChaseEndReason    string                  `json:"chase_end_reason,omitempty"`   // end_chase: 追逐结束原因
	Report            string                  `json:"report,omitempty"`             // report: agent self-report
	Ack               []string                `json:"ack,omitempty"`                // response: 对玩家动作的正式确认
	HideSecret        bool                    `json:"hide_secret,omitempty"`        // npc_act: 是否隐藏NPC反应中的敏感信息(如HP变化), 由KP根据当前情境决定
}

// ToolResult wraps the result of executing one ToolCall.
type ToolResult struct {
	Action ToolCallType `json:"action"`
	Result string       `json:"result"`
}

// NOTE: ImagePromptRequest carries the Director's final visual prompt only.
type ImagePromptRequest struct {
	Prompt string `json:"prompt"`
}

// WriterState 保存Writer自己的上下文和本次生成的白字描述。
type WriterState struct {
	History []llm.ChatMessage // Writer自己的历史,用于保持文本连续性
	Buffer  string            // 本次生成的白字描述
}

// RunOutput 是一次KP主流程的结构化结果。
// KPReply 是游戏主流程输出; WriterDirection 只用于之后异步生成白字描述。
type RunOutput struct {
	WriterText      string               // 已生成的白字描述,主要用于测试或兼容旧调用
	WriterDirection string               // Writer后续生成描述所需的导演指令
	KPReply         string               // KP对玩家的主流程回复
	ImagePrompts    []ImagePromptRequest // NOTE: 本轮KP主流程排队的画图请求;生成后的data URL由handler持久化到消息内容。
}

// ── Dice types ────────────────────────────────────────────────────────────────

// DiceCheck represents a skill check requested by the KP.
type DiceCheck struct {
	Character string `json:"character"`
	Hidden    bool   `json:"hidden"`              // 暗骰:玩家不可见具体数值,KP将结果融入叙事
	What      string `json:"what"`                // 检定内容描述(如 "攻击检定"/"智力检定")
	DiceExpr  string `json:"dice_expr,omitempty"` // 可选的骰子表达式(如 "1D100+20"),优先于固定值
	Level     string `json:"level,omitempty"`     // 可选的检定难度等级(如 "简单"/"困难"),仅供KP参考
}

// DiceCheckResult is the outcome of an auto-executed dice check.
type DiceCheckResult struct {
	DiceCheck
	Roll int `json:"roll"`
}

// ── Character update types ────────────────────────────────────────────────────

// CharacterUpdate describes a single field update to a character card or NPC.
// Director change strings ("HP -3(角色名)") are parsed into this struct by
// parseStateChange in editor.go and applied directly without an LLM intermediary.
type CharacterUpdate struct {
	CharacterName string `json:"character_name"` // 目标角色/NPC名称
	Field         string `json:"field"`          // san/hp/mp/cthulhu_mythos/race/occupation/wound_state
	Delta         int    `json:"delta"`          // 数值变化量
	NewValue      string `json:"new_value"`      // 新的字符串值(如 race/wound_state)
	AddValue      string `json:"add_value"`      // 新增条目(保留用于未来扩展)
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
	Race           string         `json:"race,omitempty"`
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
	BaseCoins     int    `json:"base_coins"` // 基础奖励(固定 20)
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
	Delta int    `json:"delta"` // 正整数,1-10
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

// ── Combat input types ────────────────────────────────────────────────────────

// CombatParticipantInput is the KP-provided entry for one combatant when starting a combat.
type CombatParticipantInput struct {
	Name  string `json:"name"`
	DEX   int    `json:"dex"`
	HP    int    `json:"hp"`
	IsNPC bool   `json:"is_npc"`
}

// CombatActionDetail describes the specific action a combatant takes this turn.
type CombatActionDetail struct {
	Type       string `json:"type"`                   // attack/dodge/fight_back/aim/take_cover/other
	TargetName string `json:"target_name,omitempty"`  // 攻击/闪避/反击目标
	WeaponName string `json:"weapon_name,omitempty"`  // 使用的武器
	APDebtNext int    `json:"ap_debt_next,omitempty"` // 下轮扣除的AP(如寻找掩体)
}

// ── Chase input types ─────────────────────────────────────────────────────────

// ChaseParticipantInput is the KP-provided entry for one participant when starting a chase.
type ChaseParticipantInput struct {
	Name      string `json:"name"`
	IsNPC     bool   `json:"is_npc"`
	MOV       int    `json:"mov"`      // 速度检定后的MOV值
	Location  int    `json:"location"` // 起始地点索引
	IsPursuer bool   `json:"is_pursuer"`
}

// ChaseActionDetail describes the specific chase action taken this turn.
type ChaseActionDetail struct {
	Type          string `json:"type"`                    // move/hazard/obstacle/conflict/other
	MoveDelta     int    `json:"move_delta,omitempty"`    // 移动的地点数(正=追近,负=拉开)
	ObstacleName  string `json:"obstacle_name,omitempty"` // 通过/攻击的障碍名称
	ObstacleHP    int    `json:"obstacle_hp,omitempty"`   // 障碍当前HP(创建障碍时使用)
	ObstacleMaxHP int    `json:"obstacle_max_hp,omitempty"`
	APDebtNext    int    `json:"ap_debt_next,omitempty"` // 险境失败时下轮扣除的AP
	TargetName    string `json:"target_name,omitempty"`  // 冲突目标名称
}

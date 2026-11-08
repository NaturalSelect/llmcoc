// NOTE: Defines AI agent roles and their interactions.
package agent

import (
	"sort"

	"github.com/llmcoc/server/internal/models"
)

// sortedIntMapKeys 返回map[string]int的key按字典序排序后的切片。渲染进prompt/head的内容
// 必须保证确定性输出——Go的map遍历顺序是随机的，同一份NPC属性/技能数据如果不排序直接遍历，
// 每次调用生成的字节都可能不同，会打断prompt cache的前缀匹配。
func sortedIntMapKeys(m map[string]int) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// GameContext 承载一次聊天回合所需的全部上下文。
type GameContext struct {
	Session        models.GameSession
	History        []models.Message // 最近消息,不含system角色
	UserInput      string
	UserName       string
	UserInputAdmin bool // true if the user input is from the admin (KP), false for regular players
	// PendingActions 保存本轮已收集的玩家行动;多人房间只在所有人提交后填充。
	PendingActions []PlayerAction
	// Progress 用于把KP主流程的真实阶段推给上层;为空时静默。
	Progress func(string)
	// Publish 由handler注入:Director 每一轮产生玩家可见消息后同步调用,handler 负责落库并广播;
	// 返回 err 时本周期以 StopError 结束。nil 时输出只计数、不外发(测试用)。
	Publish func(RoundOutputs) error
	// KPFloor 非空表示本周期由KP主动发起而不是玩家回应触发:opening(开场)/host_continue(房主
	// 要求KP继续)/resume(上下文已满,总结后续跑)。为空表示由玩家回应触发。
	KPFloor string
	// LastStop 是上一个周期被强制结束的原因,host_continue 时写进 <kp_floor>,让KP知道为什么被叫回来。
	LastStop StopReason
	// AskedQuestion 是KP上一段点名时提出的问题,玩家回应段用它还原"这是在回答什么"。
	AskedQuestion string
	// SkippedNames 只在host_continue时填充:被点名但没有回应、本段被跳过的调查员名。
	SkippedNames []string
}

// KPFloor 取值。
const (
	KPFloorOpening      = "opening"
	KPFloorHostContinue = "host_continue"
	KPFloorResume       = "resume"
)

// AskResult 是KP通过ask_players交出发言权的结果。
type AskResult struct {
	UserIDs  []uint
	Names    []string
	Question string
}

// PlayerAction 是一个玩家在当前回合提交的行动。
type PlayerAction struct {
	IsAdmin    bool // true表示管理员/KP输入,false表示普通玩家输入
	UserID     uint // 提交该行动的玩家UserID;管理员输入为0
	PlayerName string
	Content    string
}

// ── Master-Slave Tool Call Types ─────────────────────────────────────────────

// ToolCallType identifies the action the master KP agent wants to perform.
type ToolCallType string

const (
	ToolCheckRule              ToolCallType = "check_rule"                // 查阅规则书
	ToolRollDice               ToolCallType = "roll_dice"                 // 骰子检定
	ToolCreateNPC              ToolCallType = "create_npc"                // 创建临时NPC
	ToolDestroyNPC             ToolCallType = "destroy_npc"               // 销毁临时NPC
	ToolActNPC                 ToolCallType = "act_npc"                   // 与指定NPC对话并获取反应
	ToolUpdateCharacters       ToolCallType = "update_characters"         // 更新角色状态
	ToolManageInventory        ToolCallType = "manage_inventory"          // 角色物品增删
	ToolRecordMonster          ToolCallType = "record_monster"            // 记录已见神话存在
	ToolManageSpell            ToolCallType = "manage_spell"              // 管理已掌握法术
	ToolManageRelation         ToolCallType = "manage_relation"           // 管理社会关系
	ToolManageAsset            ToolCallType = "manage_asset"              // 管理资产
	ToolEndGame                ToolCallType = "end_game"                  // 结束游戏
	ToolManageMadness          ToolCallType = "manage_madness"            // 管理疯狂状态
	ToolWrite                  ToolCallType = "write"                     // 生成叙事段落
	ToolAdvanceTime            ToolCallType = "advance_time"              // 推进游戏内时间
	ToolQueryClues             ToolCallType = "query_clues"               // 查询剧本线索
	ToolQueryCharacter         ToolCallType = "query_character"           // 查询调查员完整人物卡
	ToolQueryNPCCard           ToolCallType = "query_npc_card"            // 查询NPC完整角色卡
	ToolUpdateNPCCard          ToolCallType = "update_npc_card"           // 更新NPC角色卡状态
	ToolUpdateSessionMemory    ToolCallType = "update_session_memory"     // 更新调查员的会话记忆
	ToolUpdateNPCSessionMemory ToolCallType = "update_npc_session_memory" // 更新NPC的会话记忆
	ToolUpdateLocation         ToolCallType = "update_location"           // 更新调查员当前位置
	ToolUpdateNPCLocation      ToolCallType = "update_npc_location"       // 更新NPC当前位置
	ToolUpdateArmor            ToolCallType = "update_armor"              // 更新调查员护甲值
	ToolHint                   ToolCallType = "hint"                      // KP写入当前场景高密度提示
	ToolGenerateImage          ToolCallType = "generate_image"            // NOTE: 生成即时场景图片
	ToolDescribeCharacters     ToolCallType = "describe_characters"       // NOTE: 获取调查员可见外貌描写
	ToolNarrate                ToolCallType = "narrate"                   // 发布一条KP消息,不交出发言权
	// NOTE: ToolLegacyResponse 是 narrate 的旧名，只用于读懂老会话已归档的 Director 历史，不在工具列表里。
	ToolLegacyResponse ToolCallType = "response"
	ToolAskPlayers     ToolCallType = "ask_players" // 点名调查员并交出发言权,结束本周期
	ToolReport         ToolCallType = "report"      // 向管理系统自首

	ToolStartCombat ToolCallType = "start_combat" // 开始一场战斗,建立DEX行动顺序
	ToolCombatAct   ToolCallType = "combat_act"   // 结算战斗轮中一名参战者的行动
	ToolEndCombat   ToolCallType = "end_combat"   // 结束当前战斗
	ToolStartChase  ToolCallType = "start_chase"  // 开始一场追逐,建立DEX行动顺序
	ToolChaseAct    ToolCallType = "chase_act"    // 结算追逐轮中一名参与者的行动
	ToolEndChase    ToolCallType = "end_chase"    // 结束当前追逐

	ToolConsultDramaturg ToolCallType = "consult_dramaturg" // 向剧构顾问汇报进度,获取节奏与走向指导
)

// ToolCall is one item in the master KP agent's output sequence.
type ToolCall struct {
	Action        ToolCallType           `json:"action"`
	Question      string                 `json:"question"`       // check_rule: 规则问题的语义描述
	Dice          *DiceCheck             `json:"dice"`           // roll_dice: 骰子检定请求
	CharCard      *NPCCard               `json:"char_card"`      // create_npc: NPC角色卡
	NPCName       string                 `json:"npc_name"`       // npc_act: NPC名称
	NPCCtx        string                 `json:"npc_ctx"`        // npc_act: 当前情境简述
	KPDirective   string                 `json:"kp_directive"`   // act_npc: KP剧情指令(最高优先级行为约束)
	DestroyReason string                 `json:"destroy_reason"` // destroy_npc: dead|out_of_range|cleanup
	Changes       []string               `json:"changes"`        // update_characters: 状态变化列表
	CharacterName string                 `json:"character_name"` // manage_madness / query_character: 角色名称
	Operate       string                 `json:"operate"`        // 通用操作: add/remove
	Item          string                 `json:"item"`           // manage_inventory: 物品名称(仅保留兼容,优先使用 item_name+item_desc+item_count)
	ItemName      string                 `json:"item_name"`      // manage_inventory: 物品基础名称
	ItemDesc      string                 `json:"item_desc"`      // manage_inventory: 物品状态描述(可选)
	ItemCount     int                    `json:"item_count"`     // manage_inventory: 物品数量(省略或0均视为1)
	Monster       string                 `json:"monster"`        // record_monster: 神话存在名称
	Spell         string                 `json:"spell"`          // manage_spell: 法术名称
	Relation      *models.SocialRelation `json:"relation"`       // manage_relation: 社会关系条目
	Asset         *models.Asset          `json:"asset"`          // manage_asset: 资产条目
	IsBystander   bool                   `json:"is_bystander"`   // manage_madness trigger: 是否有旁观者在场(仅决定症状表,不代表疯狂类型)
	MadnessType   string                 `json:"madness_type"`   // manage_madness trigger: 疯狂类型 temporary/indefinite/permanent,由Director按规则判定
	Direction     string                 `json:"direction"`      // write: 叙事方向(供Writer参考)
	ProgressNote  string                 `json:"progress_note"`  // consult_dramaturg: 脱敏后的剧情进展报告
	TimeRounds    int                    `json:"time_rounds"`    // advance_time: 推进的回合数
	TimeReason    string                 `json:"time_reason"`    // advance_time: 原因(如"睡觉"/"吃饭")
	Keyword       string                 `json:"keyword"`        // query_clues: 已废弃(保留仅为兼容旧输出)
	SessionMemory string                 `json:"session_memory"` // update_session_memory: 会话记忆内容(整体覆盖,非追加)
	NewLocation   string                 `json:"new_location"`   // update_location/update_npc_location: 新位置名称
	ArmorValue    int                    `json:"armor_value"`    // update_armor: 新护甲值(0=无护甲)
	Hint          string                 `json:"hint"`           // hit: KP当前场景高密度提示
	ImagePrompt   string                 `json:"image_prompt"`   // NOTE: generate_image: 英文自然语言画图描述
	Aspect        string                 `json:"aspect"`         // NOTE: generate_image: 画面方向 landscape|portrait|square(可选,默认square)
	NSFW          bool                   `json:"nsfw"`           // NOTE: generate_image: 该配图是否含裸露/性暗示等NSFW内容(可选,默认false);painter_nsfw未启用时,标记为true的调用会被拒绝。write: 该段描述是否含色情/性描写内容(可选,默认false),仅在房间NSFW开关开启时生效。act_npc: 该次互动是否含色情/性描写内容(可选,默认false),仅在房间NSFW开关开启时生效
	Characters    []string               `json:"characters"`     // NOTE: describe_characters 参数;兼容旧 generate_image JSON,但生成图片时忽略
	Options       []string               `json:"options"`        // ask_players: 玩家询问时才给的推荐行动(可选,0-2条)
	Reply         string                 `json:"reply"`          // narrate: KP对玩家说的话(必填)
	EndSummary    string                 `json:"end_summary"`    // end_game: 结局总结(必填,需说明触发的具体结局及Trigger满足情况)
	// NOTE: Win 使用指针以区分 false（失败）与缺失/null（未填写）。
	// Director 必须显式填写 true 或 false；缺失时 endGameAction.Execute 拒绝并返回错误。
	Win     *bool  `json:"win"`     // end_game: 是否胜利(必填,true=胜/false=败)
	Context string `json:"context"` // narrate: 剧本推进到此处的完整上下文

	// ── Combat fields ─────────────────────────────────────────────────────────
	CombatParticipants []CombatParticipantInput `json:"combat_participants"` // start_combat: 参与者列表
	CombatActorName    string                   `json:"combat_actor_name"`   // combat_act: 本轮行动者名称
	CombatAction       *CombatActionDetail      `json:"combat_action"`       // combat_act: 行动详情
	CombatEndReason    string                   `json:"combat_end_reason"`   // end_combat: 战斗结束原因

	// ── Chase fields ──────────────────────────────────────────────────────────
	ChaseParticipants []ChaseParticipantInput `json:"chase_participants"` // start_chase: 参与者列表
	ChaseActorName    string                  `json:"chase_actor_name"`   // chase_act: 本轮行动者名称
	ChaseAction       *ChaseActionDetail      `json:"chase_action"`       // chase_act: 行动详情
	ChaseEndReason    string                  `json:"chase_end_reason"`   // end_chase: 追逐结束原因
	Report            string                  `json:"report"`             // report: agent self-report
	HideSecret        bool                    `json:"hide_secret"`        // npc_act: 是否隐藏NPC反应中的敏感信息(如HP变化), 由KP根据当前情境决定
	Targets           []string                `json:"targets"`            // ask_players: 被点名的调查员名,["all"]表示全体存活调查员
}

// ToolResult wraps the result of executing one ToolCall.
type ToolResult struct {
	Action ToolCallType       `json:"action"`
	Result string             `json:"result"`
	Events []models.GameEvent `json:"-"` // 玩家可见的结算事件,不回传给 Director
}

// NOTE: ImagePromptRequest carries the Director's final visual prompt and optional
// display options (e.g. aspect); the queued shape Painter/handler consume later.
type ImagePromptRequest struct {
	Prompt string `json:"prompt"`
	Aspect string `json:"aspect"` // 画面方向 landscape|portrait|square(可选,默认square)
	NSFW   bool   `json:"nsfw"`   // Director 标记该画面含NSFW内容,决定路由到 painter_nsfw
}

// WriterState 保存Writer本次调用的跨回合上下文管理器和本次生成的白字描述。
type WriterState struct {
	cm     *ContextManager // Writer自己跨回合的原生消息链,由ContextManager统一管理
	Buffer string          // 本次生成的白字描述
}

// OutputKind 区分一次工具调用产生的玩家可见消息种类。
type OutputKind string

const (
	OutputWriter OutputKind = "writer" // write:交给 Writer 展开成白字正文
	OutputImage  OutputKind = "image"  // generate_image:交给 Painter 出图
	OutputKP     OutputKind = "kp"     // narrate/end_game:KP 的回复
	OutputAsk    OutputKind = "ask"    // ask_players:点名并交出发言权
)

// Output 是一次产生玩家可见消息的工具调用的结果,一个 Output 对应一条消息。
type Output struct {
	Kind      OutputKind
	Text      string              // writer: direction(交给Writer,不是正文); kp: reply或结束语; ask: question
	NSFW      bool                // writer、image 各自单独标记
	Image     *ImagePromptRequest // image
	Options   []string            // ask: 玩家询问时才给的推荐行动
	TimePoint string              // kp/ask: 发布时的游戏内时间
	Ask       *AskResult          // ask
	Ended     bool                // end_game
	Auto      bool                // writer 由 fallbackWriterDirection 自动补出
}

// RoundOutputs 是 Director 一轮工具调用产生的全部消息,按 writer → image → kp → ask 排好序
// (同类保持调用顺序)。handler 在 Publish 里同步落库并广播。
type RoundOutputs struct {
	Floor     int
	Round     int
	Items     []Output
	Events    []models.GameEvent // 本轮工具事件,按调用顺序;发布时排在 Items 之前
	ElapsedMs int64              // 从上一条 kp/ask 到这一条的耗时,仅本轮含 kp/ask 时有值
	Steps     int                // 从上一条 kp/ask 到这一条经过的轮数,同上
	// WriterCtx 是本轮结束时 GameContext 的拷贝(Session.Players 也已拷贝,Progress/Publish 置空):
	// Writer、Painter 异步读取它,Director 继续原地修改原 gctx 时不会产生数据竞争。
	WriterCtx GameContext
}

// StopReason 说明一个发言权周期为什么没有以 ask_players/end_game 自然结束。
type StopReason string

const (
	StopNone     StopReason = ""
	StopHostStop StopReason = "host_stop" // 房主打断
	StopLoop     StopReason = "loop"      // 防循环中止
	StopStall    StopReason = "stall"     // 长时间没有产生任何玩家可见消息
	StopError    StopReason = "error"     // LLM/发布出错
	StopContext  StopReason = "context"   // 上下文已满,需要总结后续跑
)

// RunOutput 是一个发言权周期(floor)的结构化结果。
// err != nil 当且仅当 Stop == StopError;其余强制结束(打断、防循环、停滞、上下文已满)err 为 nil。
type RunOutput struct {
	Messages          int        // 本周期已经通过 Publish 发布的消息条数
	Ask               *AskResult // KP点名结果;为nil且Ended为false表示周期被强制结束(KP仍持有发言权)
	Ended             bool       // end_game 已执行,房间结束
	Stop              StopReason // 强制结束的原因;自然结束为空
	DirectorElapsedMs int64      // Director工具调用循环的总耗时(毫秒),不含Writer/画图等异步阶段
	DirectorSteps     int        // Director工具调用循环实际执行的轮数
}

// ── Dice types ────────────────────────────────────────────────────────────────

// DiceCheck represents a skill check requested by the KP.
type DiceCheck struct {
	Character string `json:"character"`
	Hidden    bool   `json:"hidden"`    // 暗骰:玩家不可见具体数值,KP将结果融入叙事
	What      string `json:"what"`      // 检定内容描述(如 "攻击检定"/"智力检定")
	DiceExpr  string `json:"dice_expr"` // 可选的骰子表达式(如 "1D100+20"),优先于固定值
	Level     string `json:"level"`     // 可选的检定难度等级(如 "简单"/"困难"),仅供KP参考
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

// NPCAction 是扮演 NPC 的 AI 玩家的一份声明：只是意图，由 KP 决定是否检定并裁定。
type NPCAction struct {
	NPCName  string `json:"npc_name"`
	Intent   string `json:"intent"`
	Action   string `json:"action"`
	Dialogue string `json:"dialogue"`
	// NOTE: Check 是 AI 玩家申请的检定("技能名:目的")，OOC 是只给 KP 看的场外话，两者都不得进入玩家可见内容。
	Check string `json:"check"`
	OOC   string `json:"ooc"`
}

// NPCCard is the input schema for create_npc(char_card).
type NPCCard struct {
	Name           string         `json:"name"`
	Race           string         `json:"race"`
	Description    string         `json:"description"`
	Attitude       string         `json:"attitude"`
	Goal           string         `json:"goal"`
	Secret         string         `json:"secret"`
	RiskPreference string         `json:"risk_preference"`
	Stats          map[string]int `json:"stats"`
	Skills         map[string]int `json:"skills"`
	Spells         []string       `json:"spells"`
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
	Name        string `json:"name"`
	DEX         int    `json:"dex"`
	CombatSkill int    `json:"combat_skill"` // DEX相同时的次级排序依据(斗殴/格斗等技能值)
	HP          int    `json:"hp"`
	IsNPC       bool   `json:"is_npc"`
}

// CombatActionDetail describes the specific action a combatant takes this turn.
type CombatActionDetail struct {
	Type       string `json:"type"`         // attack/dodge/fight_back/aim/take_cover/maneuver/other
	TargetName string `json:"target_name"`  // 攻击/闪避/反击目标
	WeaponName string `json:"weapon_name"`  // 使用的武器
	APDebtNext int    `json:"ap_debt_next"` // 下轮扣除的AP(如寻找掩体)

	// NeedsClarification: 被攻击方是调查员,且其本轮已提交的意图无法推断出闪避/反击倾向时使用。
	// 置位后本战斗轮在当前行动者处暂停(不推进ActorIndex/Round),Director应立即调用narrate
	// 通过ClarifyQuestion向该玩家提问,禁止代选默认值。
	NeedsClarification bool   `json:"needs_clarification"`
	ClarifyQuestion    string `json:"clarify_question"` // 待澄清时向玩家提出的问题
}

// ── Chase input types ─────────────────────────────────────────────────────────

// ChaseParticipantInput is the KP-provided entry for one participant when starting a chase.
type ChaseParticipantInput struct {
	Name      string `json:"name"`
	IsNPC     bool   `json:"is_npc"`
	DEX       int    `json:"dex"`      // 追逐轮行动顺序依据
	MOV       int    `json:"mov"`      // 速度检定后的MOV值
	Location  int    `json:"location"` // 起始地点索引
	IsPursuer bool   `json:"is_pursuer"`
}

// ChaseActionDetail describes the specific chase action taken this turn.
type ChaseActionDetail struct {
	Type            string `json:"type"`          // move/hazard/obstacle/conflict/other
	MoveDelta       int    `json:"move_delta"`    // 移动的地点数(正=追近,负=拉开)
	ObstacleName    string `json:"obstacle_name"` // 通过/攻击的障碍名称
	ObstacleHP      int    `json:"obstacle_hp"`   // 障碍当前HP(创建障碍时使用)
	ObstacleMaxHP   int    `json:"obstacle_max_hp"`
	ObstacleBetween [2]int `json:"obstacle_between"` // 障碍所在的两个相邻地点索引(创建障碍时使用)
	APDebtNext      int    `json:"ap_debt_next"`     // 险境失败时下轮扣除的AP
	TargetName      string `json:"target_name"`      // 冲突目标名称

	// NeedsClarification: 语义同CombatActionDetail,用于追逐中被攻击方(conflict类型)的反应暂停。
	NeedsClarification bool   `json:"needs_clarification"`
	ClarifyQuestion    string `json:"clarify_question"`
}

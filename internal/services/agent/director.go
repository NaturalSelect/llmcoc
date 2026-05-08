// NOTE: Defines AI agent roles and their interactions.
package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"

	"github.com/llmcoc/server/internal/models"
	"github.com/llmcoc/server/internal/services/llm"
	"github.com/llmcoc/server/internal/services/rulebook"
)

const kpCombatPrompt = `
<tool>
			<name>start_combat</name>
			<sideeffect>true</sideeffect>
			<endTheTurn>false</endTheTurn>
			<description>开始战斗,初始化跨轮战斗状态(第一次发生冲突时调用)</description>
			<call_example>{"action":"start_combat","combat_participants":[{"name":"Alice","dex":60,"hp":12,"is_npc":false},{"name":"怪物","dex":40,"hp":20,"is_npc":true}]}</call_example>
		</tool>
		<tool>
			<name>combat_act</name>
			<sideeffect>true</sideeffect>
			<endTheTurn>false</endTheTurn>
			<maybeInterrupt>true</maybeInterrupt>
			<description>记录本轮当前行动者的战斗行动(每个行动者每轮调用一次,必须在单独的 round 中使用)</description>
			<call_example>{"action":"combat_act","combat_actor_name":"Alice","combat_action":{"type":"attack","target_name":"怪物","weapon_name":"左轮手枪"}}</call_example>
		</tool>
		<tool>
			<name>end_combat</name>
			<sideeffect>true</sideeffect>
			<endTheTurn>false</endTheTurn>
			<description>结束战斗,清除战斗状态</description>
			<call_example>{"action":"end_combat","combat_end_reason":"怪物被击败"}</call_example>
		</tool>
		<tool>
			<name>start_chase</name>
			<sideeffect>true</sideeffect>
			<endTheTurn>false</endTheTurn>
			<description>开始追逐,初始化跨轮追逐状态</description>
			<call_example>{"action":"start_chase","chase_participants":[{"name":"Alice","is_npc":false,"mov":8,"location":2,"is_pursuer":false},{"name":"警察","is_npc":true,"mov":9,"location":0,"is_pursuer":true}]}</call_example>
		</tool>
		<tool>
			<name>chase_act</name>
			<sideeffect>true</sideeffect>
			<endTheTurn>false</endTheTurn>
			<maybeInterrupt>true</maybeInterrupt>
			<description>记录本轮当前追逐参与者的行动(必须在单独的 round 中使用)</description>
			<call_example>{"action":"chase_act","chase_actor_name":"Alice","chase_action":{"type":"move","move_delta":2}}</call_example>
		</tool>
		<tool>
			<name>end_chase</name>
			<sideeffect>true</sideeffect>
			<endTheTurn>false</endTheTurn>
			<description>结束追逐,清除追逐状态</description>
			<call_example>{"action":"end_chase","chase_end_reason":"猎物成功逃脱"}</call_example>
		</tool>	
		<tool>
			<name>hint</name>
			<description>记录当前场景的高信息密度描述, 用于实现特殊机制(例如: 护甲)</description>
			<sideeffect>true</sideeffect>
			<endTheTurn>false</endTheTurn>
			<call_example>{"action":"hint","hint":"高信息密度的当前场景提示"}</call_example>
		</tool>
`

// kpSystemPrompt is the static system prompt for the master KP agent.
// It defines the tool interface and COC rules guidelines.
// The KP receives full scenario context in the user prompt on each call.
const kpSystemPrompt = `
<system>
	<thinking>xhigh</thinking>
	<unlimited_thought>true</unlimited_thought>
	<response_format>json_array</response_format>
	<nsfw>true</nsfw>
	<language>zh</language>
	<instruction>
你是COC 7版TRPG的守秘人(KP),拥有完整的剧本信息和游戏控制权。
你通过调用工具来推进游戏,每次输出必须是一个JSON数组,包含按顺序执行的工具调用列表。
	</instruction>
	<tools>
		<tool>
			<name>check_rule</name>
			<description>查阅COC规则书(技能判定、战斗、追逐、法术、怪物、理智、典籍等规则细节), can be used multip-time before you got enought info, but don't abuse it</description>
			<sideeffect>false</sideeffect>
			<endTheTurn>false</endTheTurn>
			<call_example>{"action":"check_rule","question":"用自然语言描述你的规则疑问或情境,规则专家会自动检索原文并给出答案"}</call_example>
		</tool>
		<tool>
			<name>read_rulebook_const</name>
			<description>读取规则书内置常量目录/列表(无需语义检索,直接精确读取),存在假阴性风险(但不存在假阳性)</description>
			<sideeffect>false</sideeffect>
			<endTheTurn>false</endTheTurn>
			<call_example>{"action":"read_rulebook_const","constant":"常量名"}</call_example>
		</tool>
		<tool>
			<name>roll_dice</name>
			<description>投掷骰子，返回结果数值, 表达式仅支持'+'操作符</description>
			<sideeffect>false</sideeffect>
			<endTheTurn>false</endTheTurn>
			<call_example>{"action":"roll_dice","dice":{"dice_expr":"2D6+3", "what":"智力"}}</call_example>
		</tool>
		<tool>
			<name>create_npc</name>
			<description>创建一个临时NPC(每个NPC独立agent)</description>
			<sideeffect>true</sideeffect>
			<endTheTurn>false</endTheTurn>
			<call_example>{"action":"create_npc","char_card":{"name":"NPC名","race":"种族","description":"描述","attitude":"态度","goal":"目标","secret":"秘密","risk_preference":"conservative|balanced|aggressive","stats":{"STR":50},"skills":{"聆听":40},"spells":["法术A"]}}</call_example>	
		</tool>
		<tool>
			<name>destroy_npc</name>
			<description>销毁一个临时NPC</description>
			<sideeffect>true</sideeffect>
			<endTheTurn>false</endTheTurn>
			<call_example>{"action":"destroy_npc","npc_name":"NPC名称","destroy_reason":"dead|out_of_range|cleanup"}</call_example>
		</tool>
		<tool>
			<name>act_npc</name>
			<description>打开与指定NPC的一轮对话(该NPC独立记忆)</description>
			<sideeffect>true</sideeffect>
			<endTheTurn>false</endTheTurn>
			<call_example>{"action":"act_npc","npc_name":"NPC名称","question":"你要问NPC的问题(请注意: 不要告诉NPC, 他不应该知道的信息, 不要预设结果)"}</call_example>
		</tool>
		<tool>
			<name>update_characters</name>
			<description>更新调查员的状态, changes 输入的参数字符串不可包含'()', 请考虑使用'-', '()'是关键字, 仅支持修改HP、MP、SAN、基础属性(自动计算衍生属性)、种族、职业</description>
			<sideeffect>true</sideeffect>
			<endTheTurn>false</endTheTurn>
			<call_example>{"action":"update_characters","changes":["HP -3 (角色名)","SAN -2 (角色名)","cthulhu_mythos +1 (角色名)","race 深潜者混血(角色名)","occupation 记者(角色名)"], "reason":"描述变更原因"}</call_example>		
		</tool>
		<tool>
			<name>manage_inventory</name>
			<description>管理调查员物品栏(获得/丢失)</description>
			<sideeffect>true</sideeffect>
			<endTheTurn>false</endTheTurn>
			<call_example>{"action":"manage_inventory","character_name":"角色名","operate":"add|remove","item":"包含'()'的完整物品名", "reason":"描述变更原因"}</call_example>
		</tools>
		<tool>
			<name>record_monster</name>
			<description>记录调查员已见神话存在</description>
			<sideeffect>true</sideeffect>
			<endTheTurn>false</endTheTurn>
			<call_example>{"action":"record_monster","character_name":"角色名","operate":"add|remove","monster":"神话存在类型名称", "reason":"描述变更原因"}</call_example>
		</tool>
		<tool>
			<name>manage_spell</name>
			<description>管理调查员掌握的法术(新增/删除)</description>
			<sideeffect>true</sideeffect>
			<endTheTurn>false</endTheTurn>
			<call_example>{"action":"manage_spell","character_name":"角色名","operate":"add|remove","spell":"法术名", "reason":"描述变更原因"}</call_example>
		</tool>
		<tool>
			<name>manage_relation</name>
			<description>管理调查员社会关系(新增/删除)</description>
			<sideeffect>true</sideeffect>
			<endTheTurn>false</endTheTurn>
			<call_example>{"action":"manage_relation","character_name":"角色名","operate":"add|remove","relation":{"name":"条目名","relationship":"关系类型","note":"备注(种族、具体关系、态度、NPC属性等其他信息)"}}</call_example>
		</tool>
		<tool>
			<name>end_game</name>
			<description>结束当前剧本/房间</description>
			<sideeffect>true</sideeffect>
			<shouldBeLast>true</shouldBeLast>
			<endTheTurn>true</endTheTurn>
			<call_example>{"action":"end_game","end_summary":"结局总结","reply":"对玩家的收尾发言"}</call_example>
		</tool>
		<tool>
			<name>trigger_madness</name>
			<description>触发调查员的疯狂发作(COC第八章疯狂机制)</description>
			<sideeffect>true</sideeffect>
			<endTheTurn>false</endTheTurn>
			<call_example>{"action":"trigger_madness","character_name":"角色名","is_bystander":true}</call_example>
		</tool>
		<tool>
			<name>write</name>
			<description>指示叙事代理生成文本段落,保留调查员发言行动,高信息密度</description>
			<sideeffect>true</sideeffect>
			<endTheTurn>false</endTheTurn>
			<call_example>{"action":"write","direction":"需要润色的文本(如果调查员有发言, 把原话代入这里)"}</call_example>
		</tool>
		<tool>
			<name>advance_time</name>
			<description>推进游戏内时间(耗时活动, 每一轮代表30分钟, 需要注意规则时间与游戏时间的转换, 为0则不推进时间, 否则默认推进30分钟)</description>
			<sideeffect>true</sideeffect>
			<endTheTurn>false</endTheTurn>
			<call_example>{"action":"advance_time","time_rounds":N,"time_reason":"原因"}</call_example>
		</tool>
		<tool>
			<name>query_clues</name>
			<description>查询剧本线索库(固定返回全部线索)</description>
			<sideeffect>false</sideeffect>
			<endTheTurn>false</endTheTurn>
			<call_example>{"action":"query_clues"}</call_example>
		</tool>
		<tool>
			<name>query_character</name>
			<sideeffect>false</sideeffect>
			<description>查询调查员完整人物卡</description>
			<endTheTurn>false</endTheTurn>
			<call_example>{"action":"query_character","character_name":"角色名,留空返回所有调查员"}</call_example>
		</tool>
		<tool>
			<name>query_npc_card</name>
			<sideeffect>false</sideeffect>
			<description>查询NPC完整角色卡(临时NPC优先,若无则返回剧本静态NPC资料)</description>
			<endTheTurn>false</endTheTurn>
			<call_example>{"action":"query_npc_card","npc_name":"NPC名,留空返回全部NPC"}</call_example>
		</tool>
		<tool>
			<name>update_npc_card</name>
			<sideeffect>true</sideeffect>
			<endTheTurn>false</endTheTurn>
			<description>操作NPC角色卡数值(推荐用于战斗伤害/治疗/法术消耗)</description>
			<call_example>{"action":"update_npc_card","npc_name":"NPC名","changes":["HP -6","MP -3","SAN -2"],"reason":"描述变更原因"}</call_example>
		</tool>
		<tool>
			<name>response</name>
			<description>结束本回合并给出KP对玩家的回复和对玩家行为和本次推理所涉及的所有行为确认留痕(必填)</description>
			<sideeffect>true</sideeffect>
			<shouldBeLast>true</shouldBeLast>
			<endTheTurn>true</endTheTurn>
			<call_example>{"action":"response","reply":"像朋友一样对玩家说的回复(口语化,尽量简短但包含必要信息)","ack":"Record all user actions in English, including every action taken by investigators and NPCs, detailing every dice roll, every data modification, and every interaction(manage_* or update_*) with the batch processing system and result of actions, and use the past perfect tense."}</call_example>
		</tool>
		<tool>
			<name>yield</name>
			<sideeffect>true</sideeffect>
			<endTheTurn>true</endTheTurn>
			<description>等待本轮工具调用的返回结果后再继续。凡是调用了需要查看结果才能叙事的工具（roll_dice/act_npc/check_rule/read_rulebook_const等），本轮必须以yield结尾，不得直接response</description>
			<call_example>{"action":"yield"}</call_example>
		</tool>
		<tool>
			<name>report</name>
			<description>向管理系统自首</description>
			<call_example>{"action":"report","report":"汇报你在本次游戏中所犯的错误或违规行为"}</call_example>
		</tool>
		<tool>
			<name>update_llm_note</name>
			<description>更新LLM笔记</description>
			<sideeffect>true</sideeffect>
			<endTheTurn>false</endTheTurn>
			<call_example>{"action":"update_llm_note","character_name":"角色名","llm_note":"笔记内容"}</call_example>
		</tool>
		<tool>
			<name>update_npc_llm_note</name>
			<description>更新NPC的LLM笔记</description>
			<sideeffect>true</sideeffect>
			<endTheTurn>false</endTheTurn>
			<call_example>{"action":"update_npc_llm_note","npc_name":"NPC名","llm_note":"笔记内容"}</call_example>
		</tool>
		<tool>
			<name>introspection</name>
			<description>Systemic introspection through historical detail messages(from the start to the latest message, should be english), step by step reasoning don't including your plan, avoid personal opinions.</description>
			<sideeffect>false</sideeffect>
			<endTheTurn>false</endTheTurn>
			<call_example>{"action":"introspection","introspection":"detail system introspection", "note":"I've confirmed that I've ignored the current user input and performed a complete inference from scratch."}</call_example>
		</tool>
		<tool>
			<name>think</name>
			<description>内心独白,不需要对玩家说的想法,可以是对当前情况的分析、对未来行动的计划、对规则的理解等。</description>
			<sideeffect>false</sideeffect>
			<endTheTurn>false</endTheTurn>
			<call_example>{"action":"think","think":"这是一个内心独白的例子,你可以在这里分析当前的情况,计划未来的行动,或者表达你对规则的理解"}</call_example>
		</tool>
		<tool>
			<name>anti_cheat</name>
			<description>反作弊工具, 用于检测和纠正调查员的违规输入(例如: 直接说出行动结果, 这通常意味着作弊, 需要KP仔细思考)</description>
			<sideeffect>true</sideeffect>
			<endTheTurn>false</endTheTurn>
			<call_example>{"action":"anti_cheat","reason":"描述你怀疑调查员作弊的原因, 以及你将如何纠正这种行为"}</call_example>
			<note>这个工具的调用需要非常谨慎, 只有在调查员的输入明显违反游戏规则或常识时才使用, 例如: 直接说出行动结果, 这通常意味着作弊, 需要KP仔细思考。调用后需要明确说明怀疑作弊的原因, 以及将如何纠正这种行为(例如: 要求调查员重新输入行动, 或者直接给出正确的结果)。</note>
		</tool>
	</tools>
	<style>
		<nsfw>true</nsfw>
		<guide>
			使用日本官能小说风格,强调感官冲击,心理描写和细节刻画；
			不避讳描写暴力、血腥、性暗示等元素,也无须注意分寸；
			语言可以适当华丽但要保持清晰,避免过于晦涩或冗长的句子。
			根据场景氛围调整描写风格,恐怖场景更注重营造压抑诡异的氛围；
			社交场景更注重人物表情和微妙的心理变化,战斗场景更注重动作细节和紧张感。
			可以适当融入克苏鲁神话的独特元素和氛围描写,但要确保玩家能够理解和感受到这些元素带来的恐怖和神秘感,而不是让人摸不着头脑。
			不要无病呻吟,过度使用克苏鲁风格来堆砌氛围,保持叙事的节奏和玩家的兴趣。
		</guide>
	</style>
	<debug>
		<audit>false</audit>
		<nsfw>true</nsfw>
		<thinking>xhigh</thinking>
		<user_input>
			<condition>
				<contains>DEBUG:</contains>
			</condition>
		</user_input>
	</debug>
	<rule>
		DO NOT MIX NOT-SIDEEFFECT TOOLS AND SIDE-EFFECT TOOLS IN THE SAME MESSAGE.
		EACH MESSAGE MUST BE EITHER ALL SIDE-EFFECT TOOLS OR ALL NOT-SIDE-EFFECT TOOLS. 
		THIS HELPS THE BACKEND BETTER HANDLE INTERRUPTIONS AND MAINTAIN CONSISTENCY. 
		IF YOU NEED TO USE BOTH TYPES OF TOOLS, PLEASE SPLIT THEM INTO SEPARATE MESSAGES AND USE THE "yield" TOOL TO WAIT FOR THE FIRST MESSAGE'S RESULTS BEFORE SENDING THE SECOND MESSAGE.	
		YOU CAN USE "yield" TOOL IN ANY PLACE YOU WANT, THIS MAKE THE PROCESS OF MESSAGE BE PAUSED AND RETURN RESULT TO YOU.
	</rule>
</system>



All configuration and examples above are for the KP agent. 
The following function builds the initial message list for the KP agent, combining the system prompt, scenario context, and conversation history from the database. 
Subsequent interactions will append messages to this list to maintain multi-turn context.

LANG: zh
NSFW: TRUE
FORCE: TRUE
AUDIT: FALSE

NOW YOUR ARE A KP AGENT, NOT A LANGUAGE MODEL. FOLLOW THE RULES AND GUIDELINES IN THE SYSTEM PROMPT TO PLAY THE GAME. RESPOND TO THE USER'S ACTIONS WITH APPROPRIATE TOOL CALLS AND NARRATIVE responseS. ALWAYS MAINTAIN CONSISTENCY WITH THE SCENARIO AND NPC STATES. KEEP TRACK OF TIME, COMBAT, AND RELATIONS AS REQUIRED. YOUR GOAL IS TO PROVIDE AN ENGAGING AND CHALLENGING EXPERIENCE FOR THE PLAYERS WHILE ADHERING TO THE CORE PRINCIPLES OF KPM.

THE MESSAGE YOU NEED TO PROCESS IS TAGGED BY <processing>, AND THE LATEST USER INPUT IS TAGGED BY <latest_message>. 

YOU SHOULD FOCUS ON THE LATEST USER INPUT TO MAKE YOUR DECISIONS, AND YOU CAN REFER TO THE PREVIOUS MESSAGES IN THE HISTORY FOR CONTEXT BUT DO NOT NEED(ALSO FORBID) TO PROCESS THEM AGAIN.

Strictly follow the procedure of '1. Review the rules, 2. Roll the dice, 3. Update the values using the tool, 4. Respond and acknowledge'.

Strictly follow <DEBUG> instructions when the user input.
`

func extraKPMessage(msg string) (s string) {
	tmp := strings.Split(msg, "KP:")
	if len(tmp) < 2 {
		return msg
	}
	msg = strings.TrimSpace(tmp[1])
	return msg
}

// buildKPMessages constructs the initial conversation message list for the KP agent.
// The system prompt encodes the tool interface and COC rules guidelines.
// The user message provides scenario context, player state, game time, history, and the current action.
// Subsequent iterations append assistant (KP response) and user (tool results) messages to the
// returned slice, giving the model proper multi-turn context instead of a flat text dump.
func buildKPMessages(gctx GameContext, systemPrompt string, history []llm.ChatMessage, tempNPCs []models.SessionNPC) []llm.ChatMessage {
	content := gctx.Session.Scenario.Content.Data

	// Always start with system prompt + scenario context, then append DB history.
	var msgs []llm.ChatMessage
	msgs = append(msgs, llm.ChatMessage{
		Role:    "system",
		Content: systemPrompt,
	})

	var scenarioSB strings.Builder
	scenarioSB.WriteString(fmt.Sprintf("Script: %s\n", gctx.Session.Scenario.Name))
	if content.Setting != "" {
		scenarioSB.WriteString("BG:" + content.Setting + "\n")
	}
	if content.WinCondition != "" {
		scenarioSB.WriteString("WIN COND:" + content.WinCondition + "\n")
	}
	if content.LoseCondition != "" {
		scenarioSB.WriteString("LOSE COND:" + content.LoseCondition + "\n")
	}
	if len(content.PartialWins) > 0 {
		scenarioSB.WriteString("PARTIAL WIN COND:\n")
		for _, cond := range content.PartialWins {
			scenarioSB.WriteString("  • " + cond + "\n")
		}
	}
	if content.Setting != "" {
		scenarioSB.WriteString("BG:" + content.Setting + "\n")
	}
	if content.MapDescription != "" {
		scenarioSB.WriteString("MAP DESC:" + content.MapDescription + "\n")
	}
	// if content.SystemPrompt != "" {
	// 	scenarioSB.WriteString("KP特殊指令:" + content.SystemPrompt + "\n")
	// }
	if len(content.NPCs) > 0 {
		scenarioSB.WriteString("NPC列表:\n")
		for _, npc := range content.NPCs {
			desc := npc.Description
			if len([]rune(desc)) > 100 {
				desc = string([]rune(desc)[:100]) + "…"
			}
			scenarioSB.WriteString(fmt.Sprintf("  • %s(%s):%s\n", npc.Name, npc.Attitude, desc))
		}
	}
	if len(content.Scenes) > 0 {
		scenarioSB.WriteString("场景列表:\n")
		for _, scene := range content.Scenes {
			s := ""
			if len(scene.Triggers) > 0 {
				s = fmt.Sprintf(" 触发条件: %v", scene.Triggers)
			}
			scenarioSB.WriteString(fmt.Sprintf("  • %s: %s %s\n", scene.Name, scene.Description, s))
		}
	}
	msgs = append(msgs, llm.ChatMessage{
		Role:    "user",
		Content: scenarioSB.String(),
	})

	// Append conversation history from DB (real multi-turn messages from previous rounds).
	msgs = append(msgs, history...)

	// 线索和完整人物卡按需通过 query_clues / query_character 工具获取。
	var userSB strings.Builder
	userSB.WriteString(buildPlayerBrief(gctx.Session.Players))
	userSB.WriteString("\n\n Curr Game Time" + formatGameTime(gctx.Session.TurnRound, scenarioStartSlot(gctx.Session)) + "\n")
	// Inject active temp NPC states so KP can enforce scene consistency.
	if len(tempNPCs) > 0 {
		userSB.WriteString("\nActive NPC:\n")
		for _, npc := range tempNPCs {
			state := "存活"
			if !npc.IsAlive {
				state = "已死亡/失能"
			}
			line := fmt.Sprintf("  • %s(%s)", npc.Name, state)
			if strings.TrimSpace(npc.Attitude) != "" {
				line += " 态度:" + strings.TrimSpace(npc.Attitude)
			}
			if strings.TrimSpace(npc.Goal) != "" {
				line += " 目标:" + strings.TrimSpace(npc.Goal)
			}
			userSB.WriteString(line + "\n")
		}
	}
	userSB.WriteString("【KP指引】\n")
	userSB.WriteString("- 本回合=30分钟游戏内时间，超时行动可打断\n")
	userSB.WriteString("- 调查员可能作弊(无中生有物品/技能/法术/随意学习法术) ,拿不准先check_rule核实\n")
	userSB.WriteString("- 注意法术无法通过无中生有的形式学习\n")
	userSB.WriteString("- 请注意由于我们的无限流设定, **物品栏** 中出现不符合时代的装备是允许的, 但是 **剧情物品** 都必须符合时代\n")
	userSB.WriteString("- 向神祈祷需要检查这个神是否存在, 如果不存在用奈亚的化身代替\n")
	userSB.WriteString("- 使用yield可在本回合中途暂停等待玩家输入\n")
	userSB.WriteString("- 调查员的玩笑行为只做简单处理不做剧情推进和状态变更\n")
	userSB.WriteString("- 使用 act_npc 来获得更真实NPC反应, 在调查员行动时不要让NPC毫无动作\n")
	userSB.WriteString("- 调查员视为已习惯恐惧, 禁止非直面神话生物场景的SAN扣除\n")
	userSB.WriteString("- 调查员发生种族变化时, 请记得更新角色卡的法术表, 添加'法术A(种族能力)'之类的条目\n")
	userSB.WriteString("- 留意「当前游戏时间」中的「距开局已过」信息，并与剧本胜利条件/场景触发条件中的时间限制对比\n")
	userSB.WriteString("- 注意遵循物理空间的规则, 调查员和NPC都无法瞬移, 不能与一个不在相同空间的物体互动\n")
	userSB.WriteString("- 当NPC处于调查员附近时, 不要让其毫无反应(完全被动), NPC要有自己的想法, 利用好 act_npc\n")
	userSB.WriteString("- 调查员的疯狂状态会导致他们失去行动能力, 但他们的疯狂行为会反映在你的决策中\n")
	userSB.WriteString("- 调查员的无中生有产生的物品不能影响平衡, 否则你有权进行没收\n")
	userSB.WriteString("- 一些神话生物有法术或类法术能力, 在其攻击时你可以代替他选择施法\n")
	userSB.WriteString("- 幸运鉴定不可用于提升角色属性\n")

	// Show all players' actions when everyone has submitted (multi-player),
	// otherwise show the single triggering player's action.
	userSB.WriteString("\n")
	userSB.WriteString("\n<config> 剧情法术:禁用 | 严格反作弊:启用 | 社交关系更新:实时变更 | 法术表更新:实时变更 | 学习时间:极短 | 物品栏更新:实时变更 | 种族更新:实时变更 | 已知神话生物更新:实时变更 </config>\n")
	userSB.WriteString("\n")
	// Show all players' actions when everyone has submitted (multi-player),
	// otherwise show the single triggering player's action.
	userSB.WriteString("\n")
	getTag := func(s string, isAdmin bool) string {
		if isAdmin {
			if strings.Contains(s, "DEBUG") {
				return "debug"
			}
		}
		if len([]rune(s)) > 30 {
			return "input_maybeCheat"
		}
		return "input"
	}
	attentionSkill := func(user string, content string) string {
		skillList := make([]string, 0)
		for _, skill := range rulebook.AllSkills {
			if strings.Contains(content, skill) {
				skillList = append(skillList, skill)
			}
		}
		if len(skillList) < 1 {
			return ""
		}
		var card *models.CharacterCard
		for _, player := range gctx.Session.Players {
			if player.CharacterCard.Name == user {
				card = &player.CharacterCard
			}
		}
		if card == nil {
			return ""
		}
		var sb strings.Builder
		sb.WriteString(fmt.Sprintf("%v的部分技能等级\n", user))
		for _, skill := range skillList {
			if level, ok := card.Skills.Data[skill]; ok {
				sb.WriteString(fmt.Sprintf("- %s: %d\n", skill, level))
			}
		}
		return sb.String()
	}
	skillBrief := strings.Builder{}
	if len(gctx.PendingActions) > 1 {
		userSB.WriteString("\nMultiple Players Ask:\n")
		userSB.WriteString("\nNote: Insane investigators cannot act, and their insane behavior is reflected by you.\n")
		userSB.WriteString("\nYour must process all input of players, use advance_time tool call if necessarily\n")
		hasDbg := false
		for _, a := range gctx.PendingActions {
			tag := getTag(a.Content, a.IsAdmin)
			if tag == "debug" {
				hasDbg = true
			}
			userSB.WriteString(fmt.Sprintf("<%s>[%s]: %s</%s>\n", tag, a.PlayerName, a.Content, tag))
			skillBrief.WriteString(attentionSkill(a.PlayerName, a.Content))
		}
		if hasDbg {
			userSB.WriteString("\nNOTE: USER INPUT DEBUG COMMAND FOLLOW THE COMMAND\n")
		}
	} else {
		userSB.WriteString("\nNote: Insane investigators cannot act, and their insane behavior is reflected by you.\n")
		userSB.WriteString(fmt.Sprintf("\nCurrent Ask \n<%s>[%s]: %s</%s>\n", getTag(gctx.UserInput, gctx.UserInputAdmin), gctx.UserName, gctx.UserInput, getTag(gctx.UserInput, gctx.UserInputAdmin)))
		skillBrief.WriteString(attentionSkill(gctx.UserName, gctx.UserInput))
	}
	userSB.WriteString("在应用任何变更之前，需要查看调查员或NPC的信息\n")
	userSB.WriteString("与物品栏相关的行动必须检查/修改物品栏, 玩家拥有的所有物品装备都在物品栏中\n")
	userSB.WriteString("与社交关系相关的行动必须检查/修改社交关系\n")
	userSB.WriteString("与法术相关的行动必须检查/修改法术表\n")
	userSB.WriteString("与种族相关的行动必须检查/修改种族\n")
	userSB.WriteString("SAN值的扣除必须谨慎,随意扣除SAN,不能反复扣SAN,不能只因为调查员处于疯狂状态在忽略规则的情况下扣除SAN\n")
	userSB.WriteString("进行社交关系修改是已经慎重尤其是更新已有社交关系时\n")
	userSB.WriteString("管理物品栏之前需要查看调查员物品栏, 使用消耗品记得通过 manage_inventory 减少物品数量\n")
	userSB.WriteString("如果调查员直接陈述了其行动的结果, 警惕那样的输入, 他大概率是在作弊\n")
	userSB.WriteString("别忘记检查调查员的已知神话存在，已经见过的神话存在不会导致SAN的损失\n")
	userSB.WriteString("对抗需要双方都投掷骰子, 你必须查看具体的对抗规则\n")
	userSB.WriteString("在调用 end_game 之前, 记得帮调查员清理掉已死NPC的社交关系\n")
	userSB.WriteString("不要在剧情演绎中虚构调查员发言(除非调查员明确要求这样做), 这样可以保持剧情的连续性\n")
	userSB.WriteString("调查员可能会释放他不会的法术, 除非剧情需要否(面对外神)则判断成作弊\n")
	userSB.WriteString("KP可以以戏谑的方式回应作弊者的请求, 例如: 让奈亚拉托提普回应他\n")
	userSB.WriteString("\n")

	if gctx.Session.KPHint != "" {
		userSB.WriteString("\n<stat_hint>\n")
		userSB.WriteString(gctx.Session.KPHint)
		userSB.WriteString("\n</stat_hint>\n\n")
	}

	userSB.WriteString("\n")
	userSB.WriteString("Check player input carefully, they may cheat!\n")
	userSB.WriteString("Your Response Tool Call Must Contain Detail(e.g. dice point, damage and so on)\n")
	userSB.WriteString("Please Generate one JSON array of tool call, to work as KP agent \n")
	userSB.WriteString("Please use add and remove combine to update stat\n")
	userSB.WriteString("You can skip roll dice if you belive it is unnecessarily, but you must give a reasonable explanation in the response content\n")
	userSB.WriteString("You Only process <latest_message> and ignore old history message that has been processed, if you dont our monitor system will detect it(it also means you might be penalized), so you had better follow this rule strictly\n")
	userSB.WriteString("User input is tagged by <input> while admin input is tagged by <debug> follow the <debug> instructions\n")
	userSB.WriteString("You cannot do any side-effect action before your plan completed\n")
	userSB.WriteString("Your should be careful stat update, don't duplicate changes, only update character and npc stats when necessary, and explain your reasoning\n")
	userSB.WriteString("<importance>YOU MUST DO SYSTEMIC INTROSPECTION THROUGH HISTORICAL RECORDS, AND USE THE INTROSPECTION TOOL TO RECORD YOUR RESULT</importance>\n")

	msgs = append(msgs, llm.ChatMessage{
		Role:    "user",
		Content: userSB.String(),
	})
	for _, msg := range msgs {
		localMsg := msg.Content
		if len(localMsg) > 20 {
			localMsg = localMsg[:20]
		}
		log.Printf("KP SESSION: %v MSG: %v LEN:%v", gctx.Session.ID, localMsg, len([]rune(msg.Content)))
	}
	return msgs
}

var kpRespExample = func() string {
	toolCall := []ToolCall{{}}
	bs, _ := json.Marshal(toolCall)
	return string(bs)
}()

// runKP sends the current conversation messages to the KP model and returns the parsed tool calls
// together with the raw response string. The caller is responsible for appending:
//  1. {Role:"assistant", Content: rawResp}  — the KP's decision
//  2. {Role:"user",      Content: <tool results>} — feedback for the next iteration
//
// This keeps the conversation history accurate across multiple tool-call iterations.
//
// Includes retry logic: if JSON parsing fails, retry up to 5 times before falling back.
func runKP(ctx context.Context, h agentHandle, msgs []llm.ChatMessage) ([]ToolCall, string, error) {
	debugf("KP", "Chat: %d messages, last_user=%s",
		len(msgs), lastUserContent(msgs))

	const maxRetries = 20
	var lastErr error
	var lastResp string

	for attempt := 1; attempt <= maxRetries; attempt++ {
		resp, err := h.provider.Chat(ctx, msgs)
		if err != nil {
			debugf("KP", "attempt %d Chat error: %v", attempt, err)
			return nil, "", err
		}

		lastResp = resp
		debugf("KP", "attempt %d raw_response len=%d, preview=%s", attempt, len([]rune(resp)), resp)

		resp = llm.JsonArryProtect(resp)
		stripped := llm.StripCodeFence(resp)
		var calls []ToolCall
		unmarshlErr := json.Unmarshal([]byte(stripped), &calls)
		if unmarshlErr == nil {
			debugf("KP", "attempt %d JSON parse success, got %d calls", attempt, len(calls))
			return calls, lastResp, nil
		}
		stripped, err = RepairJSON(ctx, stripped, unmarshlErr, kpRespExample)
		if err != nil {
			debugf("KP", "attempt %d JSON repair failed: %v", attempt, err)
			lastErr = fmt.Errorf("attempt %d JSON parse error: %w", attempt, unmarshlErr)
			continue
		}
		unmarshlErr = json.Unmarshal([]byte(stripped), &calls)
		if unmarshlErr == nil {
			debugf("KP", "attempt %d JSON repair success, got %d calls", attempt, len(calls))
			return calls, lastResp, nil
		}
		debugf("KP", "attempt %d JSON parse failed after repair: %v", attempt, unmarshlErr)
		lastErr = fmt.Errorf("attempt %d JSON parse error after repair: %w", attempt, unmarshlErr)
		debugf("KP", "attempt %d JSON parse failed, retrying...", attempt)
	}

	// All retries exhausted: fall back to minimal sequence.
	fallback := []ToolCall{
		{Action: ToolWrite, Direction: "继续当前剧情走向,保持克苏鲁氛围。"},
		{Action: ToolResponse, Reply: "故事在未知中继续推进……"},
	}
	debugf("KP", "all %d retries failed, using fallback", maxRetries)
	return fallback, lastResp, fmt.Errorf("KP JSON parse error after %d attempts: %w", maxRetries, lastErr)
}

// lastUserContent returns the content of the last user message in msgs.
func lastUserContent(msgs []llm.ChatMessage) string {
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role == "user" {
			return msgs[i].Content
		}
	}
	return ""
}

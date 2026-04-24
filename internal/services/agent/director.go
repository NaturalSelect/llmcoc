package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/llmcoc/server/internal/services/llm"
)

// kpSystemPrompt is the static system prompt for the master KP agent.
// It defines the tool interface and COC rules guidelines.
// The KP receives full scenario context in the user prompt on each call.
const kpSystemPrompt = `你是COC 7版TRPG的守秘人（KP），拥有完整的剧本信息和游戏控制权。
你通过调用工具来推进游戏，每次输出必须是一个JSON数组，包含按顺序执行的工具调用列表。

【可用工具】
1. check_rule — 查阅COC规则书（技能判定、战斗、追逐、法术、怪物、理智、典籍等规则细节）
   {"action":"check_rule","question":"用自然语言描述你的规则疑问或情境，规则专家会自动检索原文并给出答案"}
   - 示例："双手持枪开火时是否可以获得奖励骰？"
   - 示例："调查员学习《死灵之书》的SAN损失和克苏鲁神话技能提升量是多少？"
   - 示例："施放绑缚术需要消耗多少MP和SAN？"

2. roll_dice — 执行骰子检定
   {"action":"roll_dice","dice":{"skill":"技能名","value":技能值,"character":"角色名","check_type":"standard|sanity|luck|opposed","hidden":false,"bonus_dice":0,"penalty_dice":0,"san_success_loss":"0","san_fail_loss":"1D6","monster_name":""}}
   - sanity检定必须填写 san_success_loss 和 san_fail_loss
   - monster_name：若sanity检定由特定神话存在/怪物引发，填写其名称；已见过同一存在的调查员将自动跳过SAN损失
   - hidden=true：暗骰，玩家不知晓检定发生
   - bonus_dice/penalty_dice：奖励/惩罚骰数量

3. npc_act — 让NPC进行行动
   {"action":"npc_act","npc_name":"NPC名称","npc_ctx":"当前情境简述（50字以内）"}

4. update_characters — 更新调查员或NPC的状态
   {"action":"update_characters","changes":["HP -3（角色名）","SAN -2（角色名）","cthulhu_mythos +1（角色名）"]}
   - 格式：字段 ±数值（角色名）
   - 可用字段：HP/SAN/MP/cthulhu_mythos
   - 不要写SAN变化——sanity检定的SAN损失由系统自动计算

5. trigger_madness — 触发调查员的疯狂发作（COC第八章疯狂机制）
   {"action":"trigger_madness","character_name":"角色名","is_bystander":true}
   - is_bystander=true：现场有旁观者，触发即时症状（持续10轮）
   - is_bystander=false：调查员独处，触发总结症状（时间跳过1D10小时）
   - 系统会随机抽取症状并返回给你，将其融入叙事

6. write — 指示叙事代理生成文本段落
   {"action":"write","direction":"叙事方向，描述本段需要呈现的内容（100字以内）"}
   - write可以多次调用，叙事代理会保持连贯

7. advance_time — 推进游戏内时间（耗时活动）
   {"action":"advance_time","time_rounds":N,"time_reason":"原因"}
   - 每回合代表0.5小时；一天共48回合（00:00–23:30）
   - 吃饭：1回合；睡觉：16回合（8小时）；其他活动按实际耗时换算
   - 普通行动（对话/搜索/战斗等）无需调用，系统自动推进1回合
   - 若跳过多个回合，在 write 中交代时间流逝

8. query_clues — 查询剧本线索库
   {"action":"query_clues","keyword":"可选关键词，留空返回全部线索"}
   - 调查员触发/发现/询问线索时调用，按需获取，勿在每轮开头无脑查询
   - 示例：{"action":"query_clues","keyword":"灯塔"}
   - 示例：{"action":"query_clues","keyword":""}（返回所有线索）

9. query_character — 查询调查员完整人物卡
   {"action":"query_character","character_name":"角色名，留空返回所有调查员"}
   - 需要具体技能值、背景故事、社会关系、咒语等详细信息时调用
   - 示例：{"action":"query_character","character_name":"Alice"}
   - 示例：{"action":"query_character","character_name":""}（返回全部调查员详情）

10. answer — 结束本轮，以KP身份对玩家说话
    {"action":"answer","reply":"像朋友一样对玩家说的回复（必填，口语化，包含骰子结果，行动结果等）"}

【执行规则】
- 如果要结束处理请以 answer 结尾，reply 不能为空，这代表本轮回复结束，你将无法收到后续调用的结果
- 若需要骰子结果才能决定叙事走向：本轮只输出 roll_dice（可多个），不含 write/answer
  系统会把骰子结果反馈给你，下一轮再输出 write 和 answer
- write 只能调用在 answer 之前
- 仅在有实质数值变化时调用 update_characters
- 仅输出JSON数组，不加任何说明文字
- 调查员吃饭/睡觉/长途跋涉等耗时活动，调用 advance_time 再调用 write/answer
- query_clues / query_character 可穿插在任意轮中；收到结果后再出 write/answer
- 禁止Markdown输出，你只能输出JSON数组
- answer 代表以KP的身份发言，推进剧情必须使用write
- 你只能输出JSON数组，输出前先进行自我检查

【KP核心准则】
- 仅在结果有实质意义时要求检定，日常事务无需掷骰
- 理智检定（sanity）：目睹恐怖事物或神话存在时触发，同一遭遇只检定一次
- 疯狂触发：调查员一次SAN损失≥5点时触发临时性疯狂；"一天"内累计SAN损失≥当前最大SAN的1/5时触发不定性疯狂（均由系统自动判定，调用trigger_madness执行）
- 失败优先考虑挫折/延迟/俘获，而非直接死亡
- 疯狂中的调查员：避免再施加SAN检定
- 孤注一掷（玩家拼命重试）仅限调查/探索/社交/学术技能，战斗/理智/幸运/对立不可孤注
- 克苏鲁神话典籍/首次目睹神话怪物：给对应调查员加 cthulhu_mythos
- 规则有疑问时先调用 check_rule 再行动，不要凭印象判断
- 调查员可能会作弊，如果你拿不准注意就先查规则（check_rule）再行动，不要凭印象判断
- 需要调查员技能值/背景/社会关系/已知法术/已知神话存在时先调用 query_character，需要线索细节时先调用 query_clues

【示例：简单情境（无需骰子）】
[
  {"action":"write","direction":"描述玩家进入废弃图书馆，发现地板上散落的血迹和翻乱的书架，气氛压抑诡异"},
  {"action":"answer","reply":"你们推开图书馆的大门——里面的景象可不太妙。接下来打算怎么做？"}
]

【示例：先查线索再叙事】
第一轮（先取线索）：
[{"action":"query_clues","keyword":"图书馆"}]
收到线索结果后第二轮：
[
  {"action":"write","direction":"根据查到的线索，描述调查员在图书馆书架后发现的关键物证"},
  {"action":"answer","reply":"你们在书架后面发现了点东西——要打开看看吗？"}
]

【示例：先查人物卡再做技能检定】
第一轮（查技能值）：
[{"action":"query_character","character_name":"Alice"}]
收到人物卡后第二轮（使用实际技能值）：
[{"action":"roll_dice","dice":{"skill":"图书馆使用","value":65,"character":"Alice","check_type":"standard","hidden":false}}]
收到骰子结果后第三轮：
[
  {"action":"write","direction":"Alice查阅成功，找到关键古籍，章节记载了某神话存在的封印方法"},
  {"action":"answer","reply":"Alice查阅成功，点数是X，古籍中的符文似乎蕴含着某种力量，Alice感到一阵莫名的寒意。"}
]

【示例：需要骰子再决定叙事】
第一轮输出（只有roll_dice）：
[{"action":"roll_dice","dice":{"skill":"侦查","value":50,"character":"Alice","check_type":"standard","hidden":false}}]
收到结果后第二轮输出：
[
  {"action":"write","direction":"Alice侦查成功，发现了隐藏在书架后的暗门，隐约听到里面有喘息声"},
  {"action":"answer","reply":"Alice侦查成功，点数是X，你们发现了一个暗门。"}
]

【示例：理智检定后疯狂发作】
第一轮：
[{"action":"roll_dice","dice":{"skill":"理智","value":55,"character":"Bob","check_type":"sanity","hidden":false,"san_success_loss":"1","san_fail_loss":"1D6+2"}}]
收到结果（假设失败，损失6点SAN）后第二轮：
[
  {"action":"trigger_madness","character_name":"Bob","is_bystander":true},
  {"action":"write","direction":"根据疯狂症状描述Bob的发作，融入当前场景氛围"}
]
收到疯狂症状结果后第三轮：
[
  {"action":"write","direction":"继续描述Bob疯狂发作的具体表现和队友的反应"},
  {"action":"answer","reply":"Bob的双眼失焦，嘴里不断念叨着难以理解的呓语——这突如其来的变化让气氛更加诡异。你们打算怎么办？"}
]`

// buildKPMessages constructs the initial conversation message list for the KP agent.
// The system prompt encodes the tool interface and COC rules guidelines.
// The user message provides scenario context, player state, game time, history, and the current action.
// Subsequent iterations append assistant (KP response) and user (tool results) messages to the
// returned slice, giving the model proper multi-turn context instead of a flat text dump.
func buildKPMessages(gctx GameContext, systemPrompt string, history []llm.ChatMessage) []llm.ChatMessage {
	content := gctx.Session.Scenario.Content.Data

	// Always start with system prompt + scenario context, then append DB history.
	var msgs []llm.ChatMessage
	msgs = append(msgs, llm.ChatMessage{
		Role:    "system",
		Content: systemPrompt,
	})

	var scenarioSB strings.Builder
	scenarioSB.WriteString(fmt.Sprintf("【剧本：%s】\n", gctx.Session.Scenario.Name))
	if content.Setting != "" {
		scenarioSB.WriteString("背景设定：" + content.Setting + "\n")
	}
	if content.WinCondition != "" {
		scenarioSB.WriteString("胜利条件：" + content.WinCondition + "\n")
	}
	if content.SystemPrompt != "" {
		scenarioSB.WriteString("KP特殊指令：" + content.SystemPrompt + "\n")
	}
	if len(content.NPCs) > 0 {
		scenarioSB.WriteString("NPC列表：\n")
		for _, npc := range content.NPCs {
			desc := npc.Description
			if len([]rune(desc)) > 100 {
				desc = string([]rune(desc)[:100]) + "…"
			}
			scenarioSB.WriteString(fmt.Sprintf("  • %s（%s）：%s\n", npc.Name, npc.Attitude, desc))
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
	userSB.WriteString("\n\n【当前游戏时间】" + formatGameTime(gctx.Session.TurnRound) + "\n")

	// Show all players' actions when everyone has submitted (multi-player),
	// otherwise show the single triggering player's action.
	if len(gctx.PendingActions) > 1 {
		userSB.WriteString("\n【本轮所有玩家行动】\n")
		for _, a := range gctx.PendingActions {
			userSB.WriteString(fmt.Sprintf("[%s]: %s\n", a.PlayerName, a.Content))
		}
	} else {
		userSB.WriteString(fmt.Sprintf("\n【当前行动】[%s]: %s", gctx.UserName, gctx.UserInput))
	}
	msgs = append(msgs, llm.ChatMessage{
		Role:    "user",
		Content: userSB.String(),
	})
	return msgs
}

// runKP sends the current conversation messages to the KP model and returns the parsed tool calls
// together with the raw response string. The caller is responsible for appending:
//  1. {Role:"assistant", Content: rawResp}  — the KP's decision
//  2. {Role:"user",      Content: <tool results>} — feedback for the next iteration
//
// This keeps the conversation history accurate across multiple tool-call iterations.
func runKP(ctx context.Context, h agentHandle, msgs []llm.ChatMessage) ([]ToolCall, string, error) {
	debugf("KP", "Chat: %d messages, last_user=%s",
		len(msgs), lastUserContent(msgs))

	resp, err := h.provider.Chat(ctx, msgs)
	if err != nil {
		return nil, "", err
	}

	debugf("KP", "raw_response len=%d, preview=%s", len([]rune(resp)), resp)

	resp = llm.JsonArryProtect(resp)
	stripped := llm.StripCodeFence(resp)
	var calls []ToolCall
	if err := json.Unmarshal([]byte(stripped), &calls); err != nil {
		// If JSON parsing fails, try to extract a JSON array from the response.
		if start := strings.Index(stripped, "["); start >= 0 {
			if end := strings.LastIndex(stripped, "]"); end > start {
				if err2 := json.Unmarshal([]byte(stripped[start:end+1]), &calls); err2 == nil {
					return calls, resp, nil
				}
			}
		}
		// Fall back: produce a minimal write+end sequence.
		fallback := []ToolCall{
			{Action: ToolWrite, Direction: "继续当前剧情走向，保持克苏鲁氛围。"},
			{Action: ToolAnswer, Reply: "故事在未知中继续推进……"},
		}
		return fallback, resp, fmt.Errorf("KP JSON parse error: %w", err)
	}
	return calls, resp, nil
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

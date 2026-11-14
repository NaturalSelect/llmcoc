package agent

import (
	"strings"

	"github.com/llmcoc/server/internal/services/llm"
)

// NOTE: NPC 没有独立历史：它的请求 = NPC 自己的 system + Director 发出本批调用时的消息链(原样，
// 去掉末尾尚无结果的 assistant tool_calls) + 末尾一条指令。同一批里所有 NPC 共用同一个前缀，
// 视图是确定性生成的，不落库。

// npcSystemPrompt 返回按本次调用的 nsfwMode 渲染的 NPC 系统提示词；同一个 handle 下字节稳定，
// 不含人设和 explicit_scene_requirements 后缀，它们都在末尾的指令里。
func npcSystemPrompt(h agentHandle, nsfwMode bool) string {
	return h.systemPrompt(renderNSFW(npcDefaultPrompt, nsfwMode))
}

// dropUnansweredToolCalls 去掉消息链末尾还没有 tool 结果的 assistant(tool_calls)：带着它发请求不合法，
// 本次的提问只放在指令里。
func dropUnansweredToolCalls(chain []llm.ChatMessage) []llm.ChatMessage {
	if n := len(chain); n > 0 && chain[n-1].Role == "assistant" && len(chain[n-1].ToolCalls) > 0 {
		return chain[:n-1]
	}
	return chain
}

// buildNPCView 组装发给 NPC 的完整消息序列(不含工具定义)：与 Writer 视图同构，
// 替换 system、跨模型去推理、末尾追加 instruction；write 的正文已经在 Director 消息链的工具结果里。
func buildNPCView(director []llm.ChatMessage, system string, keepReasoning bool, instruction llm.ChatMessage) []llm.ChatMessage {
	return buildWriterView(dropUnansweredToolCalls(director), system, keepReasoning, instruction)
}

// npcTask 是一次 act_npc 交给 AI 玩家的任务单：Question 由 Director 按「情境 / 已裁定 / 回应」写，
// 其余字段由代码渲染成独立分段。
type npcTask struct {
	Question   string
	Directive  string
	HideSecret bool
	Spell      string
}

// renderNPCTask 把任务单渲染成 <kp_says> 的正文：Question 原样在前(旧写法没有分段也照常放行)，
// 随后依次是约束、秘密、法术三段。分段顺序固定，同样的输入逐字节相同。
func renderNPCTask(t npcTask) string {
	sb := strings.Builder{}
	sb.WriteString(strings.TrimSpace(t.Question))
	if d := strings.TrimSpace(t.Directive); d != "" {
		sb.WriteString("\n约束：" + d)
	}
	if t.HideSecret {
		sb.WriteString("\n秘密：隐瞒你的秘密")
	}
	switch spell := strings.TrimSpace(t.Spell); {
	case spell == "":
		sb.WriteString("\n法术：没有法术可用，也不能创造或改造魔法物品")
	case strings.Contains(spell, "附魔"):
		sb.WriteString("\n法术：可以使用 " + spell + "，且可以附魔")
	default:
		sb.WriteString("\n法术：可以使用 " + spell + "，但不能附魔")
	}
	return sb.String()
}

// buildNPCInstruction 组装本轮的指令：人设 + 场景卡 + KP 的提问 + 行为注意事项 + 输出要求。
// nsfwMode 为 true 时 explicit_scene_requirements 后缀拼进这里；欲望列表也按 nsfwMode 渲染。
// npc 属性与 Director 历史里 <tool_result npc="…"> 的名字一致，方便 NPC 认出哪些记录是自己的。
func buildNPCInstruction(name, profile, scene, question string, nsfwMode bool) llm.ChatMessage {
	sb := strings.Builder{}
	sb.WriteString(`<npc_role npc="` + xmlAttr(name) + "\">\n")
	sb.WriteString(profile)
	sb.WriteString("\n</npc_role>\n")
	if scene != "" {
		sb.WriteString("<scene>\n")
		sb.WriteString(scene)
		sb.WriteString("</scene>\n")
	}
	sb.WriteString(`<kp_says npc="` + xmlAttr(name) + "\">\n")
	sb.WriteString(question)
	sb.WriteString("\n</kp_says>\n")
	sb.WriteString("<note>\n")
	sb.WriteString(renderNSFW("在符合NPC人设和当前处境的前提下, 表现NPC的{{NSFW_NPC_DESIRES}}等多维度真情实感\n", nsfwMode))
	if nsfwMode {
		sb.WriteString("请重点表现人物的色情欲望和性癖主动发起色情互动，体现男性对女性的欲望和凝视\n")
		sb.WriteString("不要省略性交而是主动描写，描写可能出现的玩法和姿势\n")
	}
	sb.WriteString("注意人物的行动逻辑，不要让行为和语言前后矛盾\n")
	sb.WriteString("</note>\n")
	if nsfwMode {
		sb.WriteString(npcNSFWPromptSuffix)
	}
	sb.WriteString("现在以「" + name + "」的AI玩家身份作答：只输出JSON声明，不要调用工具，不要模仿KP的工具调用或<tool_result>格式。\n")
	return llm.ChatMessage{Role: "user", Content: sb.String()}
}

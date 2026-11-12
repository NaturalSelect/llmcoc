// NOTE: director_tool_result.go 定义 Director 收到的 tool 消息的统一信封 <tool_result>。
// 执行器层的 ToolResult.Result 仍然用 "SYSTEM REJECT: ..." 表示拒绝，转换只在 runToolLoop 渲染 tool 消息时做一次；
// 判断一条已落库的 tool 消息是否被拒绝一律走 isToolRejected，它同时认新信封和旧会话里的 SYSTEM REJECT 前缀。
package agent

import (
	"encoding/json"
	"strings"

	"github.com/llmcoc/server/internal/services/llm"
)

const (
	toolResultOpen   = "<tool_result "
	toolResultClose  = "</tool_result>"
	toolResultReject = ` status="rejected"`
	toolRejectPrefix = "SYSTEM REJECT"
)

var xmlAttrEscaper = strings.NewReplacer(
	"&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;", "\n", "&#10;", "\r", "&#13;",
)

// xmlAttr 转义属性值：& < > " 以及换行。
func xmlAttr(s string) string {
	return xmlAttrEscaper.Replace(s)
}

// toolResultNPC 返回针对单个 NPC 的工具调用所指向的 NPC 名；其他工具、名字为空(如 query_npc_card 查全部)或参数解析失败时返回空串。
func toolResultNPC(call llm.ToolCall) string {
	switch ToolCallType(call.Name) {
	case ToolActNPC, ToolQueryNPCCard, ToolUpdateNPCCard, ToolUpdateNPCLocation,
		ToolUpdateNPCSessionMemory, ToolCreateNPC, ToolDestroyNPC:
	default:
		return ""
	}
	var args struct {
		NPCName  string `json:"npc_name"`
		CharCard *struct {
			Name string `json:"name"`
		} `json:"char_card"`
	}
	if err := json.Unmarshal([]byte(call.Arguments), &args); err != nil {
		return ""
	}
	if ToolCallType(call.Name) == ToolCreateNPC {
		if args.CharCard == nil {
			return ""
		}
		return strings.TrimSpace(args.CharCard.Name)
	}
	return strings.TrimSpace(args.NPCName)
}

// renderDirectorToolResult 把一次工具调用的结果渲染成 Director 看到的 tool 消息内容。
// 正文不转义：query_character 等工具的正文本身就是 XML。
func renderDirectorToolResult(call llm.ToolCall, o toolOutcome) string {
	body := o.result
	rejected := false
	if o.reject != "" {
		body = o.reject
		rejected = true
	}
	if strings.HasPrefix(body, toolRejectPrefix) {
		rejected = true
		body = strings.TrimLeft(strings.TrimPrefix(body, toolRejectPrefix), ":： \t\r\n")
	}

	var b strings.Builder
	b.WriteString(toolResultOpen)
	b.WriteString(`name="`)
	b.WriteString(xmlAttr(call.Name))
	b.WriteString(`"`)
	if npc := toolResultNPC(call); npc != "" {
		b.WriteString(` npc="`)
		b.WriteString(xmlAttr(npc))
		b.WriteString(`"`)
	}
	if rejected {
		b.WriteString(toolResultReject)
	} else {
		b.WriteString(` status="ok"`)
	}
	if o.cached {
		b.WriteString(` cached="true"`)
	}
	b.WriteString(">\n")
	b.WriteString(body)
	b.WriteString("\n")
	b.WriteString(toolResultClose)
	return b.String()
}

// toolResultOpenTag 返回信封的开标签（含结尾的 >）；content 不是信封时 ok 为 false。
// 属性值里的 > 已被转义，所以第一个 > 一定是开标签的结尾。
func toolResultOpenTag(content string) (tag string, ok bool) {
	if !strings.HasPrefix(content, toolResultOpen) {
		return "", false
	}
	end := strings.IndexByte(content, '>')
	if end < 0 {
		return "", false
	}
	return content[:end+1], true
}

// isToolRejected 判断一条 tool 消息是否表示调用被拒绝：新信封只看开标签里的 status，
// 旧会话里没有信封的历史看 SYSTEM REJECT 前缀。正文里出现的任何文本都不会造成误判。
func isToolRejected(content string) bool {
	if tag, ok := toolResultOpenTag(content); ok {
		return strings.Contains(tag, toolResultReject)
	}
	return strings.HasPrefix(content, toolRejectPrefix)
}

// toolResultBody 去掉信封返回正文；不是信封格式时原样返回。
func toolResultBody(content string) string {
	tag, ok := toolResultOpenTag(content)
	if !ok {
		return content
	}
	rest := strings.TrimPrefix(content[len(tag):], "\n")
	return strings.TrimSuffix(rest, "\n"+toolResultClose)
}

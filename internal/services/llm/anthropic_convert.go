// NOTE: 把 provider 无关的 ChatMessage/ToolDefinition 转换为 Anthropic Messages API
// 的请求/响应结构，并在请求体上放置 prompt cache 断点。纯逻辑转换，不涉及网络调用。
package llm

import (
	"encoding/json"
	"errors"
	"strings"

	"github.com/anthropics/anthropic-sdk-go"
)

// toAnthropicRequest 把统一的 []ChatMessage 拆成 Anthropic 的顶层 system 文本块列表和
// messages 数组。system role 的消息被摘出到 system（不出现在 messages 里）；连续的多条
// tool role 消息（同一次 assistant 工具调用轮次里各个工具的执行结果）会合并进同一条
// role="user" 消息的多个 tool_result block——Anthropic 要求一次 assistant 工具调用的所有
// tool_result 必须在紧随其后的单条 user 消息内给出。
func toAnthropicRequest(messages []ChatMessage) ([]anthropic.TextBlockParam, []anthropic.MessageParam, error) {
	var system []anthropic.TextBlockParam
	var out []anthropic.MessageParam
	prevWasTool := false

	for _, m := range messages {
		switch m.Role {
		case "system":
			text := strings.TrimSpace(m.Content)
			if text == "" {
				continue
			}
			system = append(system, anthropic.TextBlockParam{Text: text})
			prevWasTool = false

		case "user":
			text := strings.TrimSpace(m.Content)
			if text == "" {
				continue
			}
			out = append(out, anthropic.NewUserMessage(anthropic.NewTextBlock(text)))
			prevWasTool = false

		case "assistant":
			blocks := make([]anthropic.ContentBlockParamUnion, 0, 1+len(m.ToolCalls))
			if text := strings.TrimSpace(m.Content); text != "" {
				blocks = append(blocks, anthropic.NewTextBlock(text))
			}
			for _, tc := range m.ToolCalls {
				blocks = append(blocks, anthropic.ContentBlockParamUnion{
					OfToolUse: &anthropic.ToolUseBlockParam{
						ID:    tc.ID,
						Name:  tc.Name,
						Input: toolCallInput(tc.Arguments),
					},
				})
			}
			// NOTE: 既无文本也无工具调用的 assistant 消息会产生空 content block，Anthropic 会 400，直接丢弃。
			if len(blocks) == 0 {
				continue
			}
			out = append(out, anthropic.NewAssistantMessage(blocks...))
			prevWasTool = false

		case "tool":
			block := anthropic.NewToolResultBlock(m.ToolCallID, m.Content, false)
			if prevWasTool && len(out) > 0 {
				last := &out[len(out)-1]
				last.Content = append(last.Content, block)
			} else {
				out = append(out, anthropic.NewUserMessage(block))
			}
			prevWasTool = true
		}
	}

	if len(out) == 0 {
		return nil, nil, errors.New("anthropic: converted message list is empty")
	}

	return system, out, nil
}

// toolCallInput 把工具调用的原始 JSON 参数文本转换为 Anthropic tool_use block 的 input
// 字段；参数为空或不是合法 JSON 时回落成空对象（Anthropic 要求 input 必须是 JSON object）。
func toolCallInput(rawArgs string) json.RawMessage {
	trimmed := strings.TrimSpace(rawArgs)
	if trimmed == "" || !json.Valid([]byte(trimmed)) {
		return json.RawMessage("{}")
	}
	return json.RawMessage(trimmed)
}

// toolInputSchema 是从 ToolDefinition.Parameters(JSON Schema)里提取的、Anthropic
// input_schema 需要的两个字段。
type toolInputSchema struct {
	Properties any      `json:"properties"`
	Required   []string `json:"required"`
}

// toAnthropicTools 把工具定义转换为 Anthropic 的 tool 列表。项目内所有工具 schema 顶层
// 只使用 type/properties/required 三个 JSON Schema 关键字（其余关键字如 enum/items 都
// 嵌套在 properties 内部，随 Properties 原样透传），因此这里只需提取 properties 和
// required，不需要 ToolInputSchemaParam.ExtraFields。
func toAnthropicTools(tools []ToolDefinition) []anthropic.ToolUnionParam {
	if len(tools) == 0 {
		return nil
	}
	out := make([]anthropic.ToolUnionParam, len(tools))
	for i, t := range tools {
		schema := toolInputSchema{Properties: map[string]any{}}
		if len(t.Parameters) > 0 {
			_ = json.Unmarshal(t.Parameters, &schema)
		}
		out[i] = anthropic.ToolUnionParam{
			OfTool: &anthropic.ToolParam{
				Name:        t.Name,
				Description: anthropic.String(t.Description),
				InputSchema: anthropic.ToolInputSchemaParam{
					Properties: schema.Properties,
					Required:   schema.Required,
				},
			},
		}
	}
	return out
}

// applyCacheBreakpoints 在 system 最后一块、tools 最后一个、以及转换后消息列表末尾最多
// 两条 role=="user" 消息（含 tool_result 型 user 消息）的最后一个 content block 上打
// cache_control 断点，合计最多 4 个——这是 Anthropic 单次请求 cache breakpoint 的上限，
// 不能再加。按“转换后的 Anthropic user 消息”而非原始 ChatMessage 计数，是因为工具循环
// 里持续增长的历史恰恰是 tool_result 轮次；只缓存纯文本 user 消息会让工具循环完全缓存不到。
func applyCacheBreakpoints(system []anthropic.TextBlockParam, tools []anthropic.ToolUnionParam, msgs []anthropic.MessageParam) {
	if len(system) > 0 {
		system[len(system)-1].CacheControl = anthropic.NewCacheControlEphemeralParam()
	}
	if len(tools) > 0 && tools[len(tools)-1].OfTool != nil {
		tools[len(tools)-1].OfTool.CacheControl = anthropic.NewCacheControlEphemeralParam()
	}

	marked := 0
	for i := len(msgs) - 1; i >= 0 && marked < 2; i-- {
		if msgs[i].Role != anthropic.MessageParamRoleUser {
			continue
		}
		content := msgs[i].Content
		if len(content) == 0 {
			continue
		}
		setBlockCacheControl(&content[len(content)-1])
		marked++
	}
}

// setBlockCacheControl 按 content block 的实际类型（text/tool_result/tool_use）写入
// cache_control；三种类型都携带 CacheControl 字段，按非空指针分派。
func setBlockCacheControl(b *anthropic.ContentBlockParamUnion) {
	switch {
	case b.OfText != nil:
		b.OfText.CacheControl = anthropic.NewCacheControlEphemeralParam()
	case b.OfToolResult != nil:
		b.OfToolResult.CacheControl = anthropic.NewCacheControlEphemeralParam()
	case b.OfToolUse != nil:
		b.OfToolUse.CacheControl = anthropic.NewCacheControlEphemeralParam()
	}
}

// fromAnthropicMessage 把 Anthropic 响应的 content block 列表拆成纯文本内容和工具调用
// 列表；text block 按出现顺序拼接，tool_use block 转换为 ToolCall，Arguments 保留原始
// JSON 文本以对齐 OpenAI 分支的约定（由调用方按工具自身参数结构反序列化）。
func fromAnthropicMessage(msg *anthropic.Message) (content string, toolCalls []ToolCall) {
	var sb strings.Builder
	for _, block := range msg.Content {
		switch block.Type {
		case "text":
			sb.WriteString(block.Text)
		case "tool_use":
			toolCalls = append(toolCalls, ToolCall{
				ID:        block.ID,
				Name:      block.Name,
				Arguments: string(block.Input),
			})
		}
	}
	return sb.String(), toolCalls
}

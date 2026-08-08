// scripter_upload_extract.go — 管理员上传故事的"锚点提取"阶段：不改写故事文档本身，
// 只让 LLM 阅读已完成的文档，识别其中承载恐怖内核的神话元素，通过 translate_anchor
// 工具校验为COC7规则书中真实存在的元素。
//
// mythos_anchor 本身是一个简短的结构化字段，适合用工具调用提交（不同于 scripter_story.go
// 的自由文本故事文档），所以这里保留独立的 submit_extraction 工具，而不复用
// runStoryArchitectLoop 的裸文本提交路径；调用方只使用返回值中的 MythosAnchor，继续使用
// 管理员上传的原始文本作为 Document。reward_concept 不在本阶段提取，统一由 compile 阶段
// 通读故事文档全文提炼（见 scripter_compile.go），两条生成路径（AI创作/管理员上传）行为一致。
package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/llmcoc/server/internal/services/llm"
)

// extractAnchorSystemPrompt 定义"锚点提取"角色：只做阅读理解与规则书校验，不创作或改写故事。
func extractAnchorSystemPrompt() string {
	return `<role>COC7剧本神话锚点识别专家</role>
<task>
<story_document>是一份已经写定的完整COC7剧本故事文档，其中的真相、线索、人物、场景与结局均已确定。你的任务只是阅读理解，绝不改写、删减、重组或补充文档中的任何文字。

你需要识别文档中真正承载恐怖内核的神话/超自然元素（旧日支配者本体/眷属/神话物品/神话知识等），通过 translate_anchor 工具将其翻译并校验为COC7规则书中的正式名称；若首选元素在禁用列表中，继续 translate_anchor 寻找文档中其他可用的元素。

完成识别后调用 submit_extraction 提交结果。不需要复述或改写原文档。
</task>
<tools>
- translate_anchor：将一个创意概念翻译为COC7规则书中最匹配的具体元素；提交前必须至少调用一次
- submit_extraction：提交识别结果；只有在translate_anchor确认元素后才调用；必须单独一轮调用
</tools>`
}

// extractAnchorFromDocument 让LLM阅读管理员上传的已完成故事文档，识别并通过规则书校验
// mythos_anchor。返回值中的 Document 字段恒为空，调用方必须继续使用原始上传文本，防止模型
// 改写故事正文。
func extractAnchorFromDocument(ctx context.Context, room *scripterRoom, document string) (StoryOutput, error) {
	msgs := []llm.ChatMessage{
		{Role: "system", Content: room.architect.systemPrompt(extractAnchorSystemPrompt())},
		{Role: "user", Content: fmt.Sprintf("<story_document>\n%s\n</story_document>\n\n请阅读以上故事文档，识别核心神话元素并通过translate_anchor校验，然后提交submit_extraction。", document)},
	}

	tools := []scripterTool{
		translateAnchorTool("将一个创意概念翻译为COC7规则书中最匹配的具体元素；提交前必须至少调用一次"),
		{
			solo: true,
			def: llm.ToolDefinition{
				Name:        toolNameSubmitExtraction,
				Description: "提交识别结果；只有在translate_anchor确认元素后才调用；必须单独一轮调用",
				Parameters: jsonSchemaObject(`{
					"type": "object",
					"properties": {
						"mythos_anchor": {"type": "string", "description": "translate_anchor确认的COC7元素全称"}
					},
					"required": ["mythos_anchor"]
				}`),
			},
		},
	}

	var submitted *StoryOutput
	dispatch := func(ctx context.Context, call llm.ToolCall) toolOutcome {
		switch call.Name {
		case toolNameTranslateAnchor:
			var args translateAnchorArgs
			if err := json.Unmarshal([]byte(call.Arguments), &args); err != nil {
				return toolOutcome{reject: "SYSTEM REJECT: translate_anchor参数不是合法JSON，请重新调用。"}
			}
			text, _ := executeOneshotTranslateAnchor(ctx, room, args.Concept, args.Reason)
			return toolOutcome{result: text}
		case toolNameSubmitExtraction:
			var args struct {
				MythosAnchor string `json:"mythos_anchor"`
			}
			if err := json.Unmarshal([]byte(call.Arguments), &args); err != nil {
				return toolOutcome{reject: "SYSTEM REJECT: submit_extraction参数不是合法JSON，请重新调用。"}
			}
			if strings.TrimSpace(args.MythosAnchor) == "" {
				return toolOutcome{reject: "SYSTEM REJECT: mythos_anchor不能为空，请先确认translate_anchor结果为found再提交。"}
			}
			submitted = &StoryOutput{MythosAnchor: args.MythosAnchor}
			return toolOutcome{result: "已收到，提取结果已提交。", done: true}
		default:
			return toolOutcome{reject: fmt.Sprintf("SYSTEM REJECT: 此阶段只允许translate_anchor/submit_extraction，不允许%s。", call.Name)}
		}
	}

	const maxRounds = 15
	if err := runScripterToolLoop(ctx, room, room.architect, "anchor_extract", msgs, tools, maxRounds, dispatch); err != nil {
		return StoryOutput{}, fmt.Errorf("自动锚点提取失败：%w", err)
	}
	return *submitted, nil
}

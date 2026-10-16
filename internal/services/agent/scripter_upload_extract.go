// scripter_upload_extract.go — 管理员上传故事的"锚点提取"阶段：不改写故事文档本身，
// 只让 LLM 阅读已完成的文档，识别其中承载恐怖内核的神话元素，通过 ask_lawyer 向规则书
// 专家核验后用 confirm_anchor 登记。
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

你需要识别文档中真正承载恐怖内核的神话/超自然元素（旧日支配者本体/眷属/神话物品/神话知识等），先通过 ask_lawyer 向规则书专家核验它在COC7规则书中的正式名称、出处与设定，再用 confirm_anchor 登记正式全称。提问要具体，不得凭常识或记忆直接登记，不得编造规则书不存在的名称；规则书里没有可靠匹配，或 confirm_anchor 因命中最近使用元素禁用列表被拒绝时，改在文档中寻找其他承载恐怖内核的元素重新核验。

完成登记后调用 submit_extraction 提交结果。不需要复述或改写原文档。
</task>
<tools>
- ask_lawyer：向COC7规则书专家提出具体规则书问题，确认候选元素的正式名称、出处与设定；可多次调用
- confirm_anchor：登记核验过的神话元素（规则书正式全称）；必须在至少一次ask_lawyer之后调用，提交前必须至少登记一次
- submit_extraction：提交识别结果；只有在confirm_anchor登记成功后才调用；必须单独一轮调用
</tools>`
}

// extractAnchorFromDocument 让LLM阅读管理员上传的已完成故事文档，识别并通过规则书校验
// mythos_anchor。返回值中的 Document 字段恒为空，调用方必须继续使用原始上传文本，防止模型
// 改写故事正文。
func extractAnchorFromDocument(ctx context.Context, room *scripterRoom, document string) (StoryOutput, error) {
	msgs := []llm.ChatMessage{
		{Role: "system", Content: room.architect.systemPrompt(extractAnchorSystemPrompt())},
		{Role: "user", Content: fmt.Sprintf("<story_document>\n%s\n</story_document>\n\n请阅读以上故事文档，识别核心神话元素，用ask_lawyer核验并用confirm_anchor登记，然后提交submit_extraction。", document)},
	}

	tools := []scripterTool{
		askLawyerTool("向COC7规则书专家提出一个具体规则书问题，确认候选神话元素在规则书中的正式名称、出处与设定；可多次调用"),
		confirmAnchorTool("登记核验过的神话元素（COC7规则书正式全称）；必须在至少一次ask_lawyer之后调用，提交前必须至少登记一次；命中最近使用元素禁用列表会被拒绝"),
		{
			solo: true,
			def: llm.ToolDefinition{
				Name:        toolNameSubmitExtraction,
				Description: "提交识别结果；只有在confirm_anchor登记成功后才调用；必须单独一轮调用",
				Parameters: jsonSchemaObject(`{
					"type": "object",
					"properties": {
						"mythos_anchor": {"type": "string", "description": "confirm_anchor登记的COC7元素全称"}
					},
					"required": ["mythos_anchor"]
				}`),
			},
		},
	}

	anchors := newAnchorTracker(room, "")
	var submitted *StoryOutput
	dispatch := func(ctx context.Context, call llm.ToolCall) toolOutcome {
		switch call.Name {
		case toolNameAskLawyer:
			return anchors.askLawyer(ctx, call)
		case toolNameConfirmAnchor:
			return anchors.confirm(call)
		case toolNameSubmitExtraction:
			var args struct {
				MythosAnchor string `json:"mythos_anchor"`
			}
			if err := json.Unmarshal([]byte(call.Arguments), &args); err != nil {
				return toolOutcome{reject: "SYSTEM REJECT: submit_extraction参数不是合法JSON，请重新调用。"}
			}
			if anchors.confirmed == "" {
				return toolOutcome{reject: "SYSTEM REJECT: 尚未登记任何神话元素，请先用ask_lawyer核验并调用confirm_anchor登记，再提交。"}
			}
			if strings.TrimSpace(args.MythosAnchor) == "" {
				return toolOutcome{reject: "SYSTEM REJECT: mythos_anchor不能为空，请填写confirm_anchor登记成功的元素全称。"}
			}
			submitted = &StoryOutput{MythosAnchor: strings.TrimSpace(args.MythosAnchor)}
			return toolOutcome{result: "已收到，提取结果已提交。", done: true}
		default:
			return toolOutcome{reject: fmt.Sprintf("SYSTEM REJECT: 此阶段只允许ask_lawyer/confirm_anchor/submit_extraction，不允许%s。", call.Name)}
		}
	}

	const maxRounds = 15
	if err := runScripterToolLoop(ctx, room, room.architect, "anchor_extract", msgs, tools, maxRounds, dispatch); err != nil {
		return StoryOutput{}, fmt.Errorf("自动锚点提取失败：%w", err)
	}
	return *submitted, nil
}

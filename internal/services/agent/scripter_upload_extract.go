// scripter_upload_extract.go — 管理员上传故事的"锚点提取"阶段：不改写故事文档本身，
// 只让 LLM 阅读已完成的文档，识别其中承载恐怖内核的神话元素，通过 translate_anchor
// 工具校验为COC7规则书中真实存在的元素，并提炼（如有）通关奖励概念。
//
// 复用 scripter_story.go 的 runStoryArchitectLoop 工具循环（同一套
// translate_anchor/submit_story 协议、JSON解析/修复链），只是替换system prompt，
// 把"创作"任务改为"提取"任务。提取结果只取 MythosAnchor/RewardConcept 两个字段，
// 调用方必须丢弃返回的 Document，继续使用管理员上传的原始文本，防止模型"顺手改写"。
package agent

import (
	"context"
	"fmt"

	"github.com/llmcoc/server/internal/services/llm"
)

// extractAnchorSystemPrompt 定义"锚点提取"角色：只做阅读理解与规则书校验，不创作或改写故事。
func extractAnchorSystemPrompt() string {
	return `<role>COC7剧本神话锚点识别专家</role>
<task>
<story_document>是一份已经写定的完整COC7剧本故事文档，其中的真相、线索、人物、场景与结局均已确定。你的任务只是阅读理解，绝不改写、删减、重组或补充文档中的任何文字。

你需要完成两件事：
1. 识别文档中真正承载恐怖内核的神话/超自然元素（旧日支配者本体/眷属/神话物品/神话知识等），通过 translate_anchor 工具将其翻译并校验为COC7规则书中的正式名称；若首选元素在禁用列表中，继续 translate_anchor 寻找文档中其他可用的元素。
2. 识别文档是否写明了通关奖励概念（如某件神话物品、典籍等）；若文档中已写明，原样提炼为一句话叙事概念；若文档完全未提及任何奖励，reward_concept 必须留空字符串，不得凭空编造。

完成识别后调用 submit_story 提交：story_document 字段必须逐字复制原文档全文（一个字都不能改），mythos_anchor 为 translate_anchor 确认的规则书元素全称，reward_concept 按上述规则填写。
</task>
<response_format>json_array</response_format>
<output>每轮只输出合法JSON数组，不要Markdown、标题、解释或代码围栏。</output>
<tools>
- translate_anchor：将一个创意概念翻译为COC7规则书中最匹配的具体元素；提交前必须至少调用一次
  {"action":"translate_anchor","concept":"概念描述（从文档中识别到的神话元素）","reason":"该元素在文档中承担什么角色"}
- submit_story：提交识别结果；只有在translate_anchor确认元素后才调用；必须单独一轮输出
  {"action":"submit_story","story_document":"逐字复制的原文档全文，不得有任何改动","mythos_anchor":"translate_anchor确认的COC7元素全称","reward_concept":"文档中写明的通关奖励概念（若文档未提及则留空字符串）"}
</tools>`
}

// extractAnchorFromDocument 让LLM阅读管理员上传的已完成故事文档，识别并通过规则书校验
// mythos_anchor，同时提炼（如有）reward_concept。调用方必须丢弃返回值中的Document字段，
// 继续使用原始上传文本，防止模型改写故事正文。
func extractAnchorFromDocument(ctx context.Context, room *scripterRoom, document string) (StoryOutput, error) {
	msgs := []llm.ChatMessage{
		{Role: "system", Content: room.architect.systemPrompt(extractAnchorSystemPrompt())},
		{Role: "user", Content: fmt.Sprintf("<story_document>\n%s\n</story_document>\n\n请阅读以上故事文档，识别核心神话元素并通过translate_anchor校验，同时按规则提炼通关奖励概念，然后提交submit_story。", document)},
	}
	result, _, err := runStoryArchitectLoop(ctx, room, msgs, "anchor_extract")
	if err != nil {
		return StoryOutput{}, fmt.Errorf("自动锚点提取失败：%w", err)
	}
	return result, nil
}

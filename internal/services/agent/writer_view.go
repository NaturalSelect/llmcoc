package agent

import (
	"github.com/llmcoc/server/internal/models"
	"github.com/llmcoc/server/internal/services/llm"
)

// NOTE: Writer 没有独立历史：它的请求 = Writer 自己的 system + Director 的消息链 + 末尾一条续写指令。
// 此前各条 write 的正文已经在 Director 消息链的 tool 结果里(<writer_output>)，视图不再自己拼装，
// 只负责换 system、跨模型去推理、追加指令。视图是确定性生成的，不落库。

// buildWriterView 组装发给 Writer 的完整消息序列(不含工具定义)：
//  1. 逐条拷贝 director，第一条 system 的内容换成 Writer 的 system，其余字段(含 CacheBreakpoint)保留；
//  2. keepReasoning 为 false 时清掉推理内容，它们的签名绑定在 Director 的模型上；
//  3. 末尾追加 instruction。
//
// Director 的消息一条都不删：窗口比 Director 小的模型会在请求时直接失败。
func buildWriterView(director []llm.ChatMessage, system string, keepReasoning bool, instruction llm.ChatMessage) []llm.ChatMessage {
	msgs := make([]llm.ChatMessage, 0, len(director)+2)
	systemReplaced := false
	for _, m := range director {
		if m.Role == "system" && !systemReplaced {
			m.Content = system
			systemReplaced = true
		}
		if !keepReasoning {
			m.Reasoning = ""
			m.ReasoningBlocks = nil
		}
		msgs = append(msgs, m)
	}
	if !systemReplaced {
		msgs = append([]llm.ChatMessage{{Role: "system", Content: system}}, msgs...)
	}
	return append(msgs, instruction)
}

// sameModelConfig 判断 Writer 与 Director 是否用同一个提供商配置和模型：
// 只有同模型时才能把 Director 的推理内容原样带给 Writer。
func sameModelConfig(a, b *models.AgentConfig) bool {
	if a == nil || b == nil || a.ProviderConfigID == nil || b.ProviderConfigID == nil {
		return false
	}
	return *a.ProviderConfigID == *b.ProviderConfigID && a.ModelName == b.ModelName
}

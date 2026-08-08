// NOTE: scripter_story_test.go 验证 runStoryArchitectLoop 的 ask_lawyer 工具分发：
// story architect 在写作过程中调用 ask_lawyer 时应查询 lawyer provider，并把规则书
// 裁定结果作为 tool 消息回传，供其在下一轮据此完成正文。
// 禁止真实网络/真实LLM；复用 translator_test.go 中的 sequentialFakeProvider。
package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/llmcoc/server/internal/models"
	"github.com/llmcoc/server/internal/services/llm"
)

// TestStoryArchitectLoop_AskLawyer 验证 story architect 调用 ask_lawyer 后，规则书裁定
// 结果会经 storyAskLawyer 回传给 architect，且最终裸文本回复被采纳为故事文档。
// initialAnchor 非空以跳过 translate_anchor，聚焦验证 ask_lawyer 这一条分发路径。
func TestStoryArchitectLoop_AskLawyer(t *testing.T) {
	initTranslatorTestDB(t)

	document := strings.Repeat("这是故事文档的正文内容，涵盖表层情境、KP内部真相、地点、NPC、线索、时间线与结局。", 20)
	askArgs, _ := json.Marshal(map[string]string{"question": "食尸鬼的移动速度和负重规则是什么？"})
	architectFake := &sequentialFakeProvider{
		callerName: "architect",
		toolResponses: []llm.ToolChatResult{
			{ToolCalls: []llm.ToolCall{fakeToolCall("call_1", toolNameAskLawyer, string(askArgs))}},
			{Content: document},
		},
	}
	lawyerFake := &sequentialFakeProvider{
		callerName:    "lawyer",
		toolResponses: lawyerDirectResponseToolResponses("食尸鬼移动速度按规则书裁定为9，可拖拽物品重量不超过其STR×2。"),
	}
	room := &scripterRoom{
		sessionID: "test-session-story-asklawyer",
		architect: agentHandle{
			provider: architectFake,
			config:   &models.AgentConfig{Role: models.AgentRoleArchitect, IsActive: true},
			enabled:  true,
		},
		lawyer: agentHandle{
			provider: lawyerFake,
			config:   &models.AgentConfig{Role: models.AgentRoleLawyer, IsActive: true},
			enabled:  true,
		},
	}

	msgs := []llm.ChatMessage{
		{Role: "system", Content: storySystemPrompt()},
		{Role: "user", Content: "请写一份故事文档"},
	}
	story, err := runStoryArchitectLoop(context.Background(), room, msgs, "story_test", "食尸鬼（Ghoul）：COC7规则书已收录")
	if err != nil {
		t.Fatalf("runStoryArchitectLoop failed: %v", err)
	}
	if story.Document != document {
		t.Errorf("story.Document = %q, want %q", story.Document, document)
	}
	if story.MythosAnchor != "食尸鬼（Ghoul）：COC7规则书已收录" {
		t.Errorf("story.MythosAnchor = %q, want 沿用 initialAnchor", story.MythosAnchor)
	}
	if len(lawyerFake.recordedKeys) == 0 {
		t.Fatal("ask_lawyer 应触发 lawyer provider 调用")
	}

	lastRoundMsgs := architectFake.recordedMessages[len(architectFake.recordedMessages)-1]
	found := false
	for _, m := range lastRoundMsgs {
		if m.Role == "tool" && strings.Contains(m.Content, "拖拽物品重量") {
			found = true
			break
		}
	}
	if !found {
		t.Error("ask_lawyer 的规则书裁定结果应作为 tool 消息回传给 architect")
	}
}

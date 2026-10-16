// NOTE: scripter_anchor_test.go 验证 anchorTracker 的代码侧锚点校验，以及 story architect
// 循环通过 ask_lawyer + confirm_anchor 登记 mythos_anchor 的完整路径。
// 禁止真实网络/真实LLM；复用 testhelpers_test.go 中的 sequentialFakeProvider。
package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/llmcoc/server/internal/models"
	"github.com/llmcoc/server/internal/services/llm"
)

func TestAnchorTracker_Validate(t *testing.T) {
	room := &scripterRoom{mythosBlacklist: []string{"深潜者"}}

	cases := []struct {
		name        string
		askedLawyer bool
		anchor      string
		wantValid   string
		wantReject  string
	}{
		{"未问过lawyer被拒", false, "食尸鬼（Ghoul）", "", "ask_lawyer"},
		{"空锚点被拒", true, "   ", "", "不能为空"},
		{"写无被拒", true, "无", "", "不能为空"},
		{"命中禁用列表被拒", true, "深潜者（Deep One）", "", "深潜者"},
		{"核验后合法锚点通过且去除空白", true, "  食尸鬼（Ghoul） ", "食尸鬼（Ghoul）", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			tracker := newAnchorTracker(room, "")
			tracker.askedLawyer = c.askedLawyer
			valid, reject := tracker.validate(c.anchor)
			if valid != c.wantValid {
				t.Errorf("valid = %q, want %q", valid, c.wantValid)
			}
			if c.wantReject == "" {
				if reject != "" {
					t.Errorf("unexpected reject: %q", reject)
				}
				return
			}
			if !strings.Contains(reject, c.wantReject) {
				t.Errorf("reject = %q, want to contain %q", reject, c.wantReject)
			}
		})
	}
}

func TestAnchorTracker_InitialAnchorKept(t *testing.T) {
	tracker := newAnchorTracker(&scripterRoom{}, "  食尸鬼（Ghoul） ")
	if tracker.confirmed != "食尸鬼（Ghoul）" {
		t.Errorf("confirmed = %q, want trimmed initial anchor", tracker.confirmed)
	}
	if tracker.askedLawyer {
		t.Error("initial anchor must not count as having asked the lawyer")
	}
}

// TestAnchorTracker_RejectedConfirmKeepsPreviousAnchor 验证被拒绝的 confirm_anchor 不会覆盖
// 之前已通过校验的锚点。
func TestAnchorTracker_RejectedConfirmKeepsPreviousAnchor(t *testing.T) {
	room := &scripterRoom{mythosBlacklist: []string{"深潜者"}}
	tracker := newAnchorTracker(room, "食尸鬼（Ghoul）")
	tracker.askedLawyer = true

	call := fakeToolCall("c1", toolNameConfirmAnchor, `{"anchor":"深潜者（Deep One）"}`)
	outcome := tracker.confirm(call)
	if outcome.reject == "" {
		t.Fatal("命中禁用列表的 confirm_anchor 应被拒绝")
	}
	if tracker.confirmed != "食尸鬼（Ghoul）" {
		t.Errorf("confirmed = %q, 被拒绝的登记不应覆盖旧锚点", tracker.confirmed)
	}
}

// TestStoryArchitectLoop_ConfirmAnchor 验证 story architect 先 ask_lawyer 再 confirm_anchor
// 后，最终故事文档带上登记的锚点；而在核验前直接 confirm_anchor 会被拒绝并回传提示。
func TestStoryArchitectLoop_ConfirmAnchor(t *testing.T) {
	initSiteSettingTestDB(t)

	document := "# 导入\n## 调查地点\n### 守密人信息\n" + strings.Repeat("这是故事文档的正文内容，涵盖表层情境、KP内部真相、地点、NPC、线索、时间线与结局。", 20)
	askArgs, _ := json.Marshal(map[string]string{"question": "食尸鬼在COC7规则书中是否已收录？"})
	confirmArgs, _ := json.Marshal(map[string]string{"anchor": "食尸鬼（Ghoul）"})
	architectFake := &sequentialFakeProvider{
		callerName: "architect",
		toolResponses: []llm.ToolChatResult{
			{ToolCalls: []llm.ToolCall{fakeToolCall("call_1", toolNameConfirmAnchor, string(confirmArgs))}},
			{ToolCalls: []llm.ToolCall{fakeToolCall("call_2", toolNameAskLawyer, string(askArgs))}},
			{ToolCalls: []llm.ToolCall{fakeToolCall("call_3", toolNameConfirmAnchor, string(confirmArgs))}},
			{Content: document},
		},
	}
	room := &scripterRoom{
		sessionID: "test-session-story-confirm-anchor",
		architect: agentHandle{
			provider: architectFake,
			config:   &models.AgentConfig{Role: models.AgentRoleArchitect, IsActive: true},
			enabled:  true,
		},
		lawyer: agentHandle{
			provider: &sequentialFakeProvider{
				callerName:    "lawyer",
				toolResponses: lawyerDirectResponseToolResponses("食尸鬼（Ghoul）：COC7规则书已收录。"),
			},
			config:  &models.AgentConfig{Role: models.AgentRoleLawyer, IsActive: true},
			enabled: true,
		},
	}

	conv := newScripterConversation(
		llm.ChatMessage{Role: "system", Content: storySystemPrompt()},
		llm.ChatMessage{Role: "user", Content: "请写一份故事文档"},
	)
	story, err := runStoryArchitectLoop(context.Background(), room, conv, "story_test", "", 0)
	if err != nil {
		t.Fatalf("runStoryArchitectLoop failed: %v", err)
	}
	if story.MythosAnchor != "食尸鬼（Ghoul）" {
		t.Errorf("MythosAnchor = %q, want 食尸鬼（Ghoul）", story.MythosAnchor)
	}

	secondRoundMsgs := architectFake.recordedMessages[1]
	rejected := false
	for _, m := range secondRoundMsgs {
		if m.Role == "tool" && strings.Contains(m.Content, "SYSTEM REJECT") && strings.Contains(m.Content, "ask_lawyer") {
			rejected = true
		}
	}
	if !rejected {
		t.Error("未核验就 confirm_anchor 时，应把 SYSTEM REJECT 回传给 architect")
	}
}

// TestStoryArchitectLoop_NoAnchorRejectsDocument 验证没有登记任何锚点就输出故事文档时，
// 文档校验会拒绝并提示先用 ask_lawyer 与 confirm_anchor。
func TestStoryArchitectLoop_NoAnchorRejectsDocument(t *testing.T) {
	document := "# 导入\n## 调查地点\n### 守密人信息\n" + strings.Repeat("这是故事文档的正文内容，涵盖表层情境、KP内部真相、地点、NPC、线索、时间线与结局。", 20)
	issues := validateStoryDocument(StoryOutput{Document: document})
	found := false
	for _, issue := range issues {
		if strings.Contains(issue, "confirm_anchor") {
			found = true
		}
	}
	if !found {
		t.Errorf("缺少锚点时应提示 confirm_anchor，got %v", issues)
	}
}

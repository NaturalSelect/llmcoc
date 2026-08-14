// scripter_conversation_test.go 覆盖 scripterConversation 消息链复用原语的核心行为：
// append/markDraft/supersedePriorDrafts 对历史成稿的原地替换、runeLen 统计、reset
// 降级重建、branch 的三索引切片隔离，以及 record 只把新增消息写入生成日志。
package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/llmcoc/server/internal/services/llm"
)

func TestScripterConversation_SupersedePriorDrafts(t *testing.T) {
	c := newScripterConversation(
		llm.ChatMessage{Role: "system", Content: "system prompt"},
	)
	c.append(llm.ChatMessage{Role: "assistant", Content: "第一版正文"})
	c.markDraft()
	c.append(llm.ChatMessage{Role: "user", Content: "must_fix: 修一下"})
	c.append(llm.ChatMessage{Role: "assistant", Content: "第二版正文"})
	c.markDraft()

	c.supersedePriorDrafts()

	if c.msgs[1].Content != scripterDraftSupersededPlaceholder {
		t.Errorf("旧版正文应被替换为占位符，实际 = %q", c.msgs[1].Content)
	}
	if c.msgs[3].Content != "第二版正文" {
		t.Errorf("最新一版正文不应被替换，实际 = %q", c.msgs[3].Content)
	}
}

func TestScripterConversation_SupersedePriorDrafts_SingleDraftNoop(t *testing.T) {
	c := newScripterConversation()
	c.append(llm.ChatMessage{Role: "assistant", Content: "唯一一版正文"})
	c.markDraft()

	c.supersedePriorDrafts()

	if c.msgs[0].Content != "唯一一版正文" {
		t.Errorf("只有一版成稿时不应替换，实际 = %q", c.msgs[0].Content)
	}
}

func TestScripterConversation_RuneLen(t *testing.T) {
	c := newScripterConversation(
		llm.ChatMessage{Role: "system", Content: "12345"},
	)
	c.append(llm.ChatMessage{
		Role: "assistant",
		ToolCalls: []llm.ToolCall{
			{Name: "ask_lawyer", Arguments: `{"question":"食尸鬼"}`},
		},
	})
	want := len([]rune("12345")) + len([]rune(`{"question":"食尸鬼"}`))
	if got := c.runeLen(); got != want {
		t.Errorf("runeLen() = %d, want %d", got, want)
	}
}

func TestScripterConversation_Reset(t *testing.T) {
	c := newScripterConversation(llm.ChatMessage{Role: "system", Content: "old"})
	c.append(llm.ChatMessage{Role: "assistant", Content: "old draft"})
	c.markDraft()
	c.logged = 2

	c.reset(llm.ChatMessage{Role: "system", Content: "new"})

	if len(c.msgs) != 1 || c.msgs[0].Content != "new" {
		t.Errorf("reset后消息链应只含新消息, msgs = %v", c.msgs)
	}
	if c.logged != 0 {
		t.Errorf("reset后logged应清零, got %d", c.logged)
	}
	if len(c.draftIdxs) != 0 {
		t.Errorf("reset后draftIdxs应清空, got %v", c.draftIdxs)
	}
}

// TestScripterConversation_BranchIsolation 验证 branch 使用三索引切片钉死容量：
// 分支上的追加不影响原链，原链后续追加也不会因共享底层数组而覆盖分支已写入的内容。
func TestScripterConversation_BranchIsolation(t *testing.T) {
	c := newScripterConversation(
		llm.ChatMessage{Role: "system", Content: "system"},
		llm.ChatMessage{Role: "user", Content: "user"},
	)
	b := c.branch()

	b.append(llm.ChatMessage{Role: "assistant", Content: "branch-only"})
	if len(c.msgs) != 2 {
		t.Errorf("分支追加不应影响原链长度, got %d", len(c.msgs))
	}

	c.append(llm.ChatMessage{Role: "assistant", Content: "main-chain"})
	if b.msgs[2].Content != "branch-only" {
		t.Errorf("原链追加不应覆盖分支已写入的内容, branch msg[2] = %q", b.msgs[2].Content)
	}
}

// TestScripterConversation_Record 验证 record 只把相对上次记录新增的消息写入生成
// 日志，并推进 logged 游标，避免同一条链被多次调用复用时把历史消息重复记入日志。
func TestScripterConversation_Record(t *testing.T) {
	logbook := newScripterGenerationLog("sess", ScenarioCreationRequest{})
	ctx := contextWithScripterGenerationLog(context.Background(), logbook)

	c := newScripterConversation(
		llm.ChatMessage{Role: "system", Content: "system prompt"},
		llm.ChatMessage{Role: "user", Content: "first user turn"},
	)
	c.record(ctx, nil, "stage_one", "assistant reply one")
	if c.logged != 2 {
		t.Fatalf("logged应推进到2, got %d", c.logged)
	}

	c.append(llm.ChatMessage{Role: "user", Content: "second user turn"})
	c.record(ctx, nil, "stage_two", "assistant reply two")
	if c.logged != 3 {
		t.Fatalf("logged应推进到3, got %d", c.logged)
	}

	text := logbook.text()
	if strings.Count(text, "first user turn") != 1 {
		t.Errorf("第一轮已记录的消息不应在第二次record时重复写入日志, got text = %q", text)
	}
	if !strings.Contains(text, "second user turn") {
		t.Error("第二轮新增消息应写入生成日志")
	}
}

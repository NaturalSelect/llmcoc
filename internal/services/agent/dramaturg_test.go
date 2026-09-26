// NOTE: dramaturg_test.go 验证Dramaturg跨轮上下文前缀稳定,以及head/opening的内容划分
// 符合"静态模组资料进head、每轮只发<now>+<progress_note>"的设计。禁止真实网络;复用
// writer_test.go里的通用fake provider。
package agent

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/llmcoc/server/internal/models"
	"github.com/llmcoc/server/internal/services/llm"
)

func newDramaturgTestHandle(prov llm.Provider) agentHandle {
	return agentHandle{
		provider: prov,
		config:   &models.AgentConfig{Role: models.AgentRoleDramaturg, IsActive: true},
		enabled:  true,
	}
}

// TestBuildDramaturgHeadContainsStaticContent 验证整局不变的模组静态资料被放进head
// 的第2条消息,而不是每轮opening里重复。
func TestBuildDramaturgHeadContainsStaticContent(t *testing.T) {
	gctx := GameContext{Session: models.GameSession{ID: 1}}
	gctx.Session.Scenario.Content.Data.PlaythroughOutline = "调查员抵达小镇后逐步揭开教团真相。"

	head := buildDramaturgHead(newDramaturgTestHandle(nil), gctx)
	if len(head) != 2 {
		t.Fatalf("head长度 = %d, want 2 (system+静态模组资料)", len(head))
	}
	if head[0].Role != "system" {
		t.Errorf("head[0].Role = %q, want system", head[0].Role)
	}
	if !strings.Contains(head[1].Content, "调查员抵达小镇后逐步揭开教团真相。") {
		t.Errorf("head[1]应包含大纲静态资料,got %q", head[1].Content)
	}
}

// TestBuildDramaturgOpeningExcludesStaticContent 验证每轮opening只有<now>/<progress_note>/
// <instruction>,不重复静态模组资料。
func TestBuildDramaturgOpeningExcludesStaticContent(t *testing.T) {
	gctx := GameContext{Session: models.GameSession{ID: 1}}
	gctx.Session.Scenario.Content.Data.PlaythroughOutline = "调查员抵达小镇后逐步揭开教团真相。"

	opening := buildDramaturgOpening(gctx, "KP汇报本轮进展。")
	if strings.Contains(opening.Content, "调查员抵达小镇后逐步揭开教团真相。") {
		t.Error("opening不应包含静态模组资料,该资料应只在head里出现一次")
	}
	if !strings.Contains(opening.Content, "KP汇报本轮进展。") {
		t.Error("opening应包含本轮progress_note")
	}
}

// TestRunDramaturg_ContextPrefixStableAcrossTurns 验证Dramaturg跨轮上下文前缀稳定:
// 第2轮发给LLM的msgs,应以"第1轮发送的msgs+其assistant响应"为逐字段相同的前缀,
// 且CacheBreakpoint只打在head末尾(下标1,静态资料消息)与上一轮末尾。
func TestRunDramaturg_ContextPrefixStableAcrossTurns(t *testing.T) {
	initAgentTestDB(t)
	const sessionID = 401
	if err := models.DB.Create(&models.GameSession{ID: sessionID}).Error; err != nil {
		t.Fatalf("create session: %v", err)
	}
	prov := &writerFakeProvider{responses: []string{
		"【进度】调查刚刚开始。【指导】应尽快引出第一条线索。",
		"【进度】调查员已发现关键线索。【指导】可以推进到下一场景。",
	}}
	h := newDramaturgTestHandle(prov)
	gctx := GameContext{Session: models.GameSession{ID: sessionID}}

	got1 := runDramaturg(context.Background(), h, gctx, "本轮KP进展一。")
	if got1 == "" {
		t.Fatal("runDramaturg turn1 返回空")
	}
	got2 := runDramaturg(context.Background(), h, gctx, "本轮KP进展二。")
	if got2 == "" {
		t.Fatal("runDramaturg turn2 返回空")
	}
	if got := prov.callCount(); got != 2 {
		t.Fatalf("provider call count = %d, want 2", got)
	}

	turn1Msgs := prov.msgsAt(0)
	turn2Msgs := prov.msgsAt(1)

	wantAssistant := llm.ChatMessage{Role: "assistant", Content: "【进度】调查刚刚开始。【指导】应尽快引出第一条线索。"}
	wantPrefix := append(append([]llm.ChatMessage(nil), turn1Msgs...), wantAssistant)
	if len(turn2Msgs) < len(wantPrefix) {
		t.Fatalf("turn2Msgs长度%d应不小于期望前缀长度%d", len(turn2Msgs), len(wantPrefix))
	}
	for i, want := range wantPrefix {
		got := turn2Msgs[i]
		got.CacheBreakpoint = false
		want.CacheBreakpoint = false
		if !reflect.DeepEqual(got, want) {
			t.Errorf("turn2Msgs[%d]与期望前缀不一致:\ngot  %+v\nwant %+v", i, got, want)
		}
	}

	// head长度固定为2(system+静态资料);turn1归档的历史长度为2(opening+assistant),
	// 所以CacheBreakpoint应恰好落在下标1(head末尾)和下标3(上一轮末尾)。
	for i, m := range turn2Msgs {
		want := i == 1 || i == 3
		if m.CacheBreakpoint != want {
			t.Errorf("turn2Msgs[%d].CacheBreakpoint=%v, want %v", i, m.CacheBreakpoint, want)
		}
	}
}

// NOTE: actions_test.go 验证工具执行器落地到共享状态的核心分支逻辑。
package agent

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/llmcoc/server/internal/models"
)

func TestWriteActionNSFWFlag(t *testing.T) {
	cases := []struct {
		name       string
		roomNSFW   bool
		callNSFW   bool
		wantMarked bool
	}{
		{"房间开启且call标记nsfw", true, true, true},
		{"房间关闭时即使call标记nsfw也不生效", false, true, false},
		{"房间开启但call未标记nsfw", true, false, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			pendingWrite := ""
			wroteNarrative := false
			writerNSFW := false
			gctx := &GameContext{Session: models.GameSession{EnableNSFW: tc.roomNSFW}}
			actx := ActionContext{
				GCtx:           gctx,
				PendingWrite:   &pendingWrite,
				WroteNarrative: &wroteNarrative,
				WriterNSFW:     &writerNSFW,
			}

			writeAction{}.Execute(ToolCall{Direction: "场景描述", NSFW: tc.callNSFW}, actx)

			if writerNSFW != tc.wantMarked {
				t.Errorf("WriterNSFW = %v, want %v", writerNSFW, tc.wantMarked)
			}
			if !wroteNarrative {
				t.Error("WroteNarrative应始终置位")
			}
			if pendingWrite != "场景描述\n" {
				t.Errorf("PendingWrite = %q, want %q", pendingWrite, "场景描述\n")
			}
		})
	}
}

// TestWriteActionNSFWFlagSticky 验证一轮内多次write调用的累积语义:
// 标记一旦被任一次调用置位,不会被同一轮后续未标记nsfw的write调用清除。
func TestWriteActionNSFWFlagSticky(t *testing.T) {
	pendingWrite := ""
	wroteNarrative := false
	writerNSFW := false
	gctx := &GameContext{Session: models.GameSession{EnableNSFW: true}}
	actx := ActionContext{
		GCtx:           gctx,
		PendingWrite:   &pendingWrite,
		WroteNarrative: &wroteNarrative,
		WriterNSFW:     &writerNSFW,
	}

	writeAction{}.Execute(ToolCall{Direction: "第一段", NSFW: false}, actx)
	if writerNSFW {
		t.Fatal("第一次调用未标记nsfw,不应置位")
	}
	writeAction{}.Execute(ToolCall{Direction: "第二段", NSFW: true}, actx)
	if !writerNSFW {
		t.Fatal("第二次调用标记nsfw后应置位")
	}
	writeAction{}.Execute(ToolCall{Direction: "第三段", NSFW: false}, actx)
	if !writerNSFW {
		t.Error("标记一旦置位,不应被同一轮后续未标记nsfw的write调用清除")
	}
	if pendingWrite != "第一段\n第二段\n第三段\n" {
		t.Errorf("PendingWrite = %q, want %q", pendingWrite, "第一段\n第二段\n第三段\n")
	}
}

// TestActNPCActionNSFWRouting 验证actNPCAction.Execute端到端选中正确的NPC handle:
// 房间NSFW开关和call.NSFW同时满足时才路由到npc_nsfw,否则一律回落默认NPC。
func TestActNPCActionNSFWRouting(t *testing.T) {
	initTranslatorTestDB(t)

	cases := []struct {
		name     string
		roomNSFW bool
		callNSFW bool
		wantAct  string
	}{
		{"房间开启且call标记nsfw则路由到NSFW NPC", true, true, "欲拒还迎"},
		{"房间关闭时即使call标记nsfw也回落默认NPC", false, true, "保持警惕"},
		{"房间开启但call未标记nsfw则用默认NPC", true, false, "保持警惕"},
	}

	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			npcName := fmt.Sprintf("线人%d", i) // 避免npcAgentStates跨用例共享同一key
			tempNPCs := []models.SessionNPC{{Name: npcName, Description: "一个线人", IsAlive: true}}

			defaultProv := &sequentialFakeProvider{jsonResponses: []string{`{"action":"保持警惕","dialogue":"你想干什么？"}`}}
			nsfwProv := &sequentialFakeProvider{jsonResponses: []string{`{"action":"欲拒还迎","dialogue":"你会负责的，对吧？"}`}}

			handles := map[models.AgentRole]agentHandle{
				models.AgentRoleNPC:     newNPCTestHandle(defaultProv, true),
				models.AgentRoleNPCNSFW: newNPCNSFWTestHandle(nsfwProv, true),
			}

			gctx := &GameContext{Session: models.GameSession{EnableNSFW: tc.roomNSFW}}
			actx := ActionContext{
				Ctx:      context.Background(),
				GCtx:     gctx,
				Handles:  handles,
				TempNPCs: &tempNPCs,
			}

			results := actNPCAction{}.Execute(ToolCall{NPCName: npcName, Question: "他有什么反应？", NSFW: tc.callNSFW}, actx)
			if len(results) != 1 {
				t.Fatalf("want 1 result, got %d", len(results))
			}
			if !strings.Contains(results[0].Result, tc.wantAct) {
				t.Errorf("Result = %q, want contains %q", results[0].Result, tc.wantAct)
			}
		})
	}
}

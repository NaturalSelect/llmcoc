// NOTE: actions_test.go 验证工具执行器落地到共享状态的核心分支逻辑。
package agent

import (
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

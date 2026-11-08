// NOTE: 覆盖 runToolLoop 的 afterDispatch 钩子与 maxRounds<=0（不限轮数）语义。
package agent

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/llmcoc/server/internal/models"
	"github.com/llmcoc/server/internal/services/llm"
)

func hookTestHandle(prov llm.Provider) agentHandle {
	return agentHandle{
		provider: prov,
		config:   &models.AgentConfig{Role: models.AgentRoleDirector, IsActive: true},
		enabled:  true,
	}
}

func hookTestTools() []scripterTool {
	return []scripterTool{
		{def: llm.ToolDefinition{Name: "probe"}},
		{def: llm.ToolDefinition{Name: "finish"}, solo: true},
	}
}

func hookTestDispatch(_ context.Context, call llm.ToolCall) toolOutcome {
	if call.Name == "finish" {
		return toolOutcome{result: "ok", done: true}
	}
	return toolOutcome{result: "probed"}
}

func TestRunToolLoop_AfterDispatchSeesRejectedAndNormalRounds(t *testing.T) {
	prov := &sequentialFakeProvider{toolResponses: []llm.ToolChatResult{
		// 第 1 轮：solo 工具与其他调用混批 → 整批拒绝
		{ToolCalls: []llm.ToolCall{fakeToolCall("a", "probe", `{}`), fakeToolCall("b", "finish", `{}`)}},
		// 第 2 轮：正常
		{ToolCalls: []llm.ToolCall{fakeToolCall("c", "probe", `{}`)}},
		// 第 3 轮：终止工具
		{ToolCalls: []llm.ToolCall{fakeToolCall("d", "finish", `{}`)}},
	}}

	type seen struct {
		round    int
		rejected bool
		calls    int
		lastRole string
	}
	var got []seen
	_, err := runToolLoop(context.Background(), toolLoopOptions{
		handle:    hookTestHandle(prov),
		stage:     "hook_test",
		msgs:      []llm.ChatMessage{{Role: "user", Content: "go"}},
		tools:     hookTestTools(),
		maxRounds: 5,
		dispatch:  hookTestDispatch,
		afterDispatch: func(round int, calls []llm.ToolCall, msgs []llm.ChatMessage, rejected bool) ([]llm.ChatMessage, error) {
			got = append(got, seen{round: round, rejected: rejected, calls: len(calls), lastRole: msgs[len(msgs)-1].Role})
			return nil, nil
		},
	})
	if err != nil {
		t.Fatalf("runToolLoop: %v", err)
	}
	want := []seen{
		{round: 1, rejected: true, calls: 2, lastRole: "tool"},
		{round: 2, rejected: false, calls: 1, lastRole: "tool"},
		{round: 3, rejected: false, calls: 1, lastRole: "tool"},
	}
	if len(got) != len(want) {
		t.Fatalf("afterDispatch calls = %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("afterDispatch[%d] = %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestRunToolLoop_AfterDispatchErrorStopsLoopAndKeepsMsgs(t *testing.T) {
	prov := &sequentialFakeProvider{toolResponses: []llm.ToolChatResult{
		{ToolCalls: []llm.ToolCall{fakeToolCall("a", "probe", `{}`)}},
		{ToolCalls: []llm.ToolCall{fakeToolCall("b", "probe", `{}`)}},
		{ToolCalls: []llm.ToolCall{fakeToolCall("c", "finish", `{}`)}},
	}}
	stop := errors.New("stop here")
	msgs, err := runToolLoop(context.Background(), toolLoopOptions{
		handle:    hookTestHandle(prov),
		stage:     "hook_test",
		msgs:      []llm.ChatMessage{{Role: "user", Content: "go"}},
		tools:     hookTestTools(),
		maxRounds: 5,
		dispatch:  hookTestDispatch,
		afterDispatch: func(round int, _ []llm.ToolCall, _ []llm.ChatMessage, _ bool) ([]llm.ChatMessage, error) {
			if round == 2 {
				return nil, stop
			}
			return nil, nil
		},
	})
	if !errors.Is(err, stop) {
		t.Fatalf("err = %v, want %v", err, stop)
	}
	// user + (assistant+tool) * 2
	if len(msgs) != 5 {
		t.Fatalf("len(msgs) = %d, want 5", len(msgs))
	}
	if prov.toolCallIdx != 2 {
		t.Fatalf("provider called %d times, want 2", prov.toolCallIdx)
	}
}

func TestRunToolLoop_AfterDispatchCanReplaceMsgs(t *testing.T) {
	prov := &sequentialFakeProvider{toolResponses: []llm.ToolChatResult{
		{ToolCalls: []llm.ToolCall{fakeToolCall("a", "probe", `{}`)}},
		{ToolCalls: []llm.ToolCall{fakeToolCall("b", "finish", `{}`)}},
	}}
	_, err := runToolLoop(context.Background(), toolLoopOptions{
		handle:    hookTestHandle(prov),
		stage:     "hook_test",
		msgs:      []llm.ChatMessage{{Role: "user", Content: "go"}},
		tools:     hookTestTools(),
		maxRounds: 5,
		dispatch:  hookTestDispatch,
		afterDispatch: func(round int, _ []llm.ToolCall, msgs []llm.ChatMessage, _ bool) ([]llm.ChatMessage, error) {
			if round == 1 {
				return append(msgs, llm.ChatMessage{Role: "user", Content: "<system-reminder>careful</system-reminder>"}), nil
			}
			return nil, nil
		},
	})
	if err != nil {
		t.Fatalf("runToolLoop: %v", err)
	}
	second := prov.recordedMessages[1]
	if last := second[len(second)-1]; last.Role != "user" || last.Content != "<system-reminder>careful</system-reminder>" {
		t.Fatalf("second round did not see the appended reminder: %+v", last)
	}
}

func TestRunToolLoop_AfterDispatchShrinkingMsgsDoesNotPanic(t *testing.T) {
	prov := &sequentialFakeProvider{toolResponses: []llm.ToolChatResult{
		{ToolCalls: []llm.ToolCall{fakeToolCall("a", "probe", `{}`)}},
		{ToolCalls: []llm.ToolCall{fakeToolCall("b", "probe", `{}`)}},
		{ToolCalls: []llm.ToolCall{fakeToolCall("c", "finish", `{}`)}},
	}}
	_, err := runToolLoop(context.Background(), toolLoopOptions{
		handle:    hookTestHandle(prov),
		stage:     "hook_test",
		msgs:      []llm.ChatMessage{{Role: "user", Content: "go"}},
		tools:     hookTestTools(),
		maxRounds: 5,
		dispatch:  hookTestDispatch,
		afterDispatch: func(round int, _ []llm.ToolCall, msgs []llm.ChatMessage, _ bool) ([]llm.ChatMessage, error) {
			if round == 2 {
				return append([]llm.ChatMessage(nil), msgs[:1]...), nil
			}
			return nil, nil
		},
	})
	if err != nil {
		t.Fatalf("runToolLoop: %v", err)
	}
}

func TestRunToolLoop_MaxRoundsZeroIsUnlimited(t *testing.T) {
	const probes = 45
	responses := make([]llm.ToolChatResult, 0, probes+1)
	for i := 0; i < probes; i++ {
		responses = append(responses, llm.ToolChatResult{
			ToolCalls: []llm.ToolCall{fakeToolCall(fmt.Sprintf("p%d", i), "probe", `{}`)},
		})
	}
	responses = append(responses, llm.ToolChatResult{ToolCalls: []llm.ToolCall{fakeToolCall("end", "finish", `{}`)}})
	prov := &sequentialFakeProvider{toolResponses: responses}

	_, err := runToolLoop(context.Background(), toolLoopOptions{
		handle:    hookTestHandle(prov),
		stage:     "hook_test",
		msgs:      []llm.ChatMessage{{Role: "user", Content: "go"}},
		tools:     hookTestTools(),
		maxRounds: 0,
		dispatch:  hookTestDispatch,
	})
	if err != nil {
		t.Fatalf("runToolLoop with maxRounds=0: %v", err)
	}
	if prov.toolCallIdx != probes+1 {
		t.Fatalf("provider called %d times, want %d", prov.toolCallIdx, probes+1)
	}
}

func TestRunToolLoop_PositiveMaxRoundsStillCaps(t *testing.T) {
	responses := make([]llm.ToolChatResult, 0, 5)
	for i := 0; i < 5; i++ {
		responses = append(responses, llm.ToolChatResult{
			ToolCalls: []llm.ToolCall{fakeToolCall(fmt.Sprintf("p%d", i), "probe", `{}`)},
		})
	}
	prov := &sequentialFakeProvider{toolResponses: responses}
	_, err := runToolLoop(context.Background(), toolLoopOptions{
		handle:    hookTestHandle(prov),
		stage:     "hook_test",
		msgs:      []llm.ChatMessage{{Role: "user", Content: "go"}},
		tools:     hookTestTools(),
		maxRounds: 3,
		dispatch:  hookTestDispatch,
	})
	if err == nil {
		t.Fatal("expected max-rounds error")
	}
	if prov.toolCallIdx != 3 {
		t.Fatalf("provider called %d times, want 3", prov.toolCallIdx)
	}
}

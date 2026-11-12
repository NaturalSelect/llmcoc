package agent

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/llmcoc/server/internal/models"
	"github.com/llmcoc/server/internal/services/llm"
)

func npcViewDirector() []llm.ChatMessage {
	return append(writerViewDirector(),
		llm.ChatMessage{Role: "assistant", Content: "思考中", ToolCalls: []llm.ToolCall{
			fakeToolCall("n1", "act_npc", `{"npc_name":"老板","question":"q"}`),
			fakeToolCall("n2", "act_npc", `{"npc_name":"醉汉","question":"q"}`),
		}},
	)
}

func TestBuildNPCView_DropsTrailingUnansweredAssistant(t *testing.T) {
	director := npcViewDirector()
	instr := llm.ChatMessage{Role: "user", Content: "npc指令"}
	view := buildNPCView(director, "npc-system", true, instr)

	// system + 其余 Director 消息(去掉末尾未回答的 assistant) + 指令
	if want := len(director) - 1 + 1; len(view) != want {
		t.Fatalf("view = %d messages, want %d", len(view), want)
	}
	if view[0].Role != "system" || view[0].Content != "npc-system" || !view[0].CacheBreakpoint {
		t.Errorf("system should be replaced but keep its cache breakpoint, got %+v", view[0])
	}
	if last := view[len(view)-1]; last.Content != "npc指令" {
		t.Errorf("last message should be the instruction, got %+v", last)
	}
	for i, m := range view {
		if m.Role == "assistant" && len(m.ToolCalls) > 0 && i >= len(view)-2 {
			t.Errorf("view[%d] is an unanswered assistant tool_calls message", i)
		}
	}
	if len(director) != len(npcViewDirector()) || director[len(director)-1].Role != "assistant" {
		t.Error("buildNPCView must not mutate the director chain")
	}
}

func TestBuildNPCView_KeepsAnsweredTail(t *testing.T) {
	director := writerViewDirector()
	view := buildNPCView(director, "sys", true, writerViewInstruction())
	if len(view) != len(director)+1 {
		t.Fatalf("a chain that ends with a tool result must be kept whole: got %d, want %d", len(view), len(director)+1)
	}
}

func TestBuildNPCView_EmptyDirectorIsSystemPlusInstruction(t *testing.T) {
	view := buildNPCView(nil, "sys", true, writerViewInstruction())
	if len(view) != 2 || view[0].Role != "system" || view[0].Content != "sys" || view[1].Content != "续写指令" {
		t.Errorf("view = %+v, want system + instruction", view)
	}
}

func TestBuildNPCView_KeepsProseAndStripsForeignReasoning(t *testing.T) {
	view := buildNPCView(npcViewDirector(), "sys", false, writerViewInstruction())

	if want := "已发布\n<writer_output>\n他推开了门。\n</writer_output>"; view[3].Content != want {
		t.Errorf("tool result of w1 = %q, want %q", view[3].Content, want)
	}
	for i, m := range view {
		if m.Reasoning != "" || m.ReasoningBlocks != nil {
			t.Errorf("view[%d] must not carry reasoning for a different model", i)
		}
	}
}

func npcRunTestGCtx(sid uint) GameContext {
	return GameContext{Session: models.GameSession{ID: sid}}
}

func TestRunNPC_RequestIsDirectorChainPlusInstruction(t *testing.T) {
	initAgentTestDB(t)
	const sid = 701
	prov := &writerFakeProvider{responses: []string{"```json\n{\"intent\":\"看清来人\",\"action\":\"眯起眼\",\"dialogue\":\"谁？\",\"check\":\"侦查:看清来人的脸\",\"ooc\":\"\"}\n```"}, chunkSize: 16}
	h := newNPCTestHandle(prov, true)
	handles := map[models.AgentRole]agentHandle{models.AgentRoleNPC: h}
	tempNPCs := []models.SessionNPC{{Name: "线人", Description: "一个线人", IsAlive: true}}

	action, err := runNPC(context.Background(), h, handles, npcRunTestGCtx(sid),
		npcRequest{Name: "线人", Question: "有人进门。", Director: npcViewDirector()}, tempNPCs)
	if err != nil {
		t.Fatalf("runNPC: %v", err)
	}
	if action.Check != "侦查:看清来人的脸" || action.Intent != "看清来人" || action.NPCName != "线人" {
		t.Errorf("声明字段解析错误: %+v", action)
	}

	msgs := prov.msgsAt(0)
	if msgs[0].Role != "system" || !strings.Contains(msgs[0].Content, "npc_agent") {
		t.Errorf("first message must be the NPC system prompt, got %+v", msgs[0])
	}
	last := msgs[len(msgs)-1]
	for _, want := range []string{`<npc_role npc="线人">`, "姓名:线人", "<scene>", `<kp_says npc="线人">` + "\n有人进门。"} {
		if !strings.Contains(last.Content, want) {
			t.Errorf("instruction 缺少 %q:\n%s", want, last.Content)
		}
	}
	if n := len(prov.sentTools); n != 1 || len(prov.sentTools[0]) == 0 {
		t.Errorf("the request must declare the director tools so the chain is valid, got %v", prov.sentTools)
	}
}

func TestRunNPC_DifferentNPCsShareOnePrefixAndCacheKey(t *testing.T) {
	initAgentTestDB(t)
	const sid = 702
	provA := &writerFakeProvider{responses: []string{`{"action":"甲"}`}, chunkSize: 16}
	provB := &writerFakeProvider{responses: []string{`{"action":"乙"}`}, chunkSize: 16}
	hA, hB := newNPCTestHandle(provA, true), newNPCTestHandle(provB, true)
	tempNPCs := []models.SessionNPC{
		{Name: "老板", Description: "酒馆老板", IsAlive: true},
		{Name: "醉汉", Description: "一个醉汉", IsAlive: true},
	}
	chain := npcViewDirector()
	for _, tc := range []struct {
		h    agentHandle
		name string
	}{{hA, "老板"}, {hB, "醉汉"}} {
		handles := map[models.AgentRole]agentHandle{models.AgentRoleNPC: tc.h}
		if _, err := runNPC(context.Background(), tc.h, handles, npcRunTestGCtx(sid),
			npcRequest{Name: tc.name, Question: "你做什么？", Director: chain}, tempNPCs); err != nil {
			t.Fatalf("runNPC(%s): %v", tc.name, err)
		}
	}

	a, b := provA.msgsAt(0), provB.msgsAt(0)
	if len(a) != len(b) {
		t.Fatalf("both requests must have the same shape: %d vs %d", len(a), len(b))
	}
	if !reflect.DeepEqual(a[:len(a)-1], b[:len(b)-1]) {
		t.Error("everything before the final instruction must be byte-identical across NPCs so the prompt cache prefix is shared")
	}
	if !strings.Contains(a[len(a)-1].Content, `npc="老板"`) || !strings.Contains(b[len(b)-1].Content, `npc="醉汉"`) {
		t.Error("each instruction must name its own NPC")
	}
	if !reflect.DeepEqual(provA.sentTools[0], provB.sentTools[0]) {
		t.Error("both requests must declare the same tool list")
	}
}

func TestRunNPC_UnknownNPCFailsWithoutCallingModel(t *testing.T) {
	initAgentTestDB(t)
	prov := &writerFakeProvider{responses: []string{`{"action":"x"}`}}
	h := newNPCTestHandle(prov, true)
	if _, err := runNPC(context.Background(), h, map[models.AgentRole]agentHandle{models.AgentRoleNPC: h}, npcRunTestGCtx(703),
		npcRequest{Name: "不存在", Question: "q"}, nil); err == nil {
		t.Fatal("unknown NPC must fail")
	}
	if prov.callCount() != 0 {
		t.Errorf("model must not be called for an unknown NPC, calls=%d", prov.callCount())
	}
}

func TestNPCChatText_RetriesEmptyThenSucceeds(t *testing.T) {
	prov := &writerFakeProvider{responses: []string{"", `{"action":"好"}`}}
	h := newNPCTestHandle(prov, true)
	got, err := npcChatText(context.Background(), h, "1:npc", nil, nil)
	if err != nil || got != `{"action":"好"}` {
		t.Fatalf("got %q, %v", got, err)
	}
	if prov.callCount() != 2 {
		t.Errorf("calls = %d, want 2", prov.callCount())
	}
}

func TestNPCChatText_GivesUpAfterMaxAttempts(t *testing.T) {
	prov := &writerFakeProvider{}
	h := newNPCTestHandle(prov, true)
	if _, err := npcChatText(context.Background(), h, "1:npc", nil, nil); err == nil {
		t.Fatal("persistently empty responses must fail")
	}
	if prov.callCount() != npcMaxStreamAttempts {
		t.Errorf("calls = %d, want %d", prov.callCount(), npcMaxStreamAttempts)
	}
}

func TestDirectorBatchDispatch_GivesEveryNPCTheChainSnapshot(t *testing.T) {
	initAgentTestDB(t)
	const sid = 704
	prov := &writerFakeProvider{responses: []string{`{"action":"甲"}`, `{"action":"乙"}`}, chunkSize: 16}
	h := newNPCTestHandle(prov, true)
	tempNPCs := []models.SessionNPC{
		{Name: "老板", Description: "酒馆老板", IsAlive: true},
		{Name: "醉汉", Description: "一个醉汉", IsAlive: true},
	}

	chain := npcViewDirector()
	st := directorDispatchState{
		sid:      sid,
		gctx:     &GameContext{Session: models.GameSession{ID: sid}},
		handles:  map[models.AgentRole]agentHandle{models.AgentRoleNPC: h},
		tempNPCs: &tempNPCs,
		chain:    &chain,
	}
	calls := []llm.ToolCall{
		fakeToolCall("n1", "act_npc", `{"npc_name":"老板","question":"q","nsfw":false}`),
		fakeToolCall("n2", "act_npc", `{"npc_name":"醉汉","question":"q","nsfw":false}`),
	}
	outcomes := directorBatchDispatch(st)(context.Background(), calls)
	if len(outcomes) != 2 {
		t.Fatalf("outcomes = %d, want 2", len(outcomes))
	}
	for i, o := range outcomes {
		if o.reject != "" || !strings.Contains(o.result, "<npc_declaration") {
			t.Errorf("outcome[%d] = %+v, want a declaration", i, o)
		}
	}
	if prov.callCount() != 2 {
		t.Fatalf("both NPCs must be asked, calls=%d", prov.callCount())
	}
	want := len(chain) - 1 + 1 // 去掉末尾未回答的 assistant，再加末尾指令
	for i := 0; i < 2; i++ {
		msgs := prov.msgsAt(i)
		if len(msgs) != want {
			t.Errorf("request %d has %d messages, want %d (chain without the unanswered assistant + instruction)", i, len(msgs), want)
		}
		if !strings.Contains(msgs[1].Content, "<scenario>") {
			t.Errorf("request %d must carry the director chain, got %+v", i, msgs[1])
		}
	}
}

func TestDirectorBatchDispatch_NoChainStillWorks(t *testing.T) {
	initAgentTestDB(t)
	prov := &writerFakeProvider{responses: []string{`{"action":"甲"}`}, chunkSize: 16}
	h := newNPCTestHandle(prov, true)
	tempNPCs := []models.SessionNPC{{Name: "老板", Description: "酒馆老板", IsAlive: true}}
	st := directorDispatchState{
		sid:      705,
		gctx:     &GameContext{Session: models.GameSession{ID: 705}},
		handles:  map[models.AgentRole]agentHandle{models.AgentRoleNPC: h},
		tempNPCs: &tempNPCs,
	}
	outcomes := directorBatchDispatch(st)(context.Background(), []llm.ToolCall{
		fakeToolCall("n1", "act_npc", `{"npc_name":"老板","question":"q","nsfw":false}`),
	})
	if len(outcomes) != 1 || !strings.Contains(outcomes[0].result, "<npc_declaration") {
		t.Fatalf("outcomes = %+v", outcomes)
	}
	if msgs := prov.msgsAt(0); len(msgs) != 2 {
		t.Errorf("without a chain the request is system + instruction, got %d messages", len(msgs))
	}
}

package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/llmcoc/server/internal/models"
	"github.com/llmcoc/server/internal/services/llm"
)

func TestDirectorToolsIncludeGenerateNPCName(t *testing.T) {
	for _, dramaturg := range []bool{false, true} {
		found := false
		for _, tool := range directorTools(dramaturg) {
			if tool.def.Name == string(ToolGenerateNPCName) {
				found = true
			}
		}
		if !found {
			t.Errorf("directorTools(%v) should expose generate_npc_name", dramaturg)
		}
	}
	if _, ok := actionRegistry[ToolGenerateNPCName]; !ok {
		t.Error("generate_npc_name should be registered in actionRegistry")
	}
}

func TestKPSystemPromptEncouragesCustomNPCs(t *testing.T) {
	for _, want := range []string{
		"主动引入自定义NPC",
		"generate_npc_name抽名字 → create_npc建卡 → 在write的必写里让他登场开口",
		"自定义NPC是气氛、压力、误导和人情的来源，不是新线索的来源",
	} {
		if !strings.Contains(kpSystemPrompt, want) {
			t.Errorf("kp system prompt should contain %q", want)
		}
	}
	if strings.Contains(kpSystemPrompt, "凭空新增线索/物品/NPC/规则/真相的禁令") {
		t.Error("the pacing rule must not forbid adding NPCs any more")
	}
}

func TestDirectorBatchPolicyNPCNameSequencing(t *testing.T) {
	cases := []struct {
		name       string
		calls      []string
		wantReject bool
	}{
		{"单独取名", []string{"generate_npc_name"}, false},
		{"取名与建卡同批", []string{"generate_npc_name", "create_npc"}, true},
		{"取名与narrate同批", []string{"generate_npc_name", "narrate"}, true},
		{"取名与write同批", []string{"generate_npc_name", "write"}, false},
		{"单独建卡", []string{"create_npc"}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			policy := noEncounterBatchPolicy()
			calls := make([]llm.ToolCall, 0, len(tc.calls))
			for _, name := range tc.calls {
				calls = append(calls, policyCall(name, npcNamePolicyArgs(name)))
			}
			reject := policy(calls)
			if (reject != "") != tc.wantReject {
				t.Errorf("reject = %q, wantReject = %v", reject, tc.wantReject)
			}
		})
	}
}

func npcNamePolicyArgs(name string) string {
	switch name {
	case "generate_npc_name":
		return `{"culture":"western","gender":"male"}`
	case "create_npc":
		return `{"char_card":{"name":"X","race":"人类","description":"d","attitude":"a","goal":"g"}}`
	case "narrate":
		return `{"reply":"你好"}`
	case "write":
		return `{"direction":"雨夜","nsfw":false}`
	}
	return `{}`
}

func TestGenerateNPCNameActionWestern(t *testing.T) {
	gctx := &GameContext{}
	var npcs []models.SessionNPC
	actx := ActionContext{GCtx: gctx, TempNPCs: &npcs, Handles: map[models.AgentRole]agentHandle{}}

	results := generateNPCNameAction{}.Execute(ToolCall{Action: ToolGenerateNPCName, Culture: "western", Gender: "female", Count: 3}, actx)

	if len(results) != 1 || strings.HasPrefix(results[0].Result, "SYSTEM REJECT") {
		t.Fatalf("results = %+v, want one non-reject result", results)
	}
	if !strings.HasPrefix(results[0].Result, `<npc_names note="`+npcNameGuidance+`">`) || strings.Count(results[0].Result, "<name>") != 3 {
		t.Errorf("result = %q, want three candidates", results[0].Result)
	}
}

func TestGenerateNPCNameActionRejectsBadArguments(t *testing.T) {
	gctx := &GameContext{}
	var npcs []models.SessionNPC
	actx := ActionContext{GCtx: gctx, TempNPCs: &npcs, Handles: map[models.AgentRole]agentHandle{}}

	for name, call := range map[string]ToolCall{
		"未知culture": {Culture: "martian", Gender: "male"},
		"未知gender":  {Culture: "western", Gender: "other"},
		"缺少gender":  {Culture: "western"},
	} {
		results := generateNPCNameAction{}.Execute(call, actx)
		if len(results) != 1 || !strings.HasPrefix(results[0].Result, "SYSTEM REJECT") {
			t.Errorf("%s: results = %+v, want SYSTEM REJECT", name, results)
		}
	}
}

// 中文姓名主路径依赖 LLM；NPC 角色没有可用 Provider 时必须回退静态姓名池而不是报错。
func TestGenerateNPCNameActionChineseFallsBackWithoutLLM(t *testing.T) {
	gctx := &GameContext{}
	var npcs []models.SessionNPC
	actx := ActionContext{GCtx: gctx, TempNPCs: &npcs, Handles: map[models.AgentRole]agentHandle{}}

	results := generateNPCNameAction{}.Execute(ToolCall{Culture: "chinese", Gender: "male"}, actx)

	const prefix = `<npc_names note="` + npcNameGuidance + `"><name>`
	const suffix = `</name></npc_names>`
	if len(results) != 1 || !strings.HasPrefix(results[0].Result, prefix) || !strings.HasSuffix(results[0].Result, suffix) {
		t.Fatalf("results = %+v, want a fallback name", results)
	}
	name := strings.TrimSuffix(strings.TrimPrefix(results[0].Result, prefix), suffix)
	found := false
	for _, candidate := range npcNameFallbackPool["chinese"]["male"] {
		if candidate == name {
			found = true
		}
	}
	if !found {
		t.Errorf("name %q is not from the chinese male fallback pool", name)
	}
}

func TestSessionTakenNamesCoversPlayersTempAndScenarioNPCs(t *testing.T) {
	var gctx GameContext
	gctx.Session.Players = []models.SessionPlayer{{CharacterCard: models.CharacterCard{Name: "Arthur Hale"}}}
	gctx.Session.Scenario.Content.Data.NPCs = []models.NPCData{{Name: "老执事"}}
	temp := []models.SessionNPC{{Name: "Mabel Finch"}}

	used := sessionTakenNames(&gctx, temp)

	for _, want := range []string{"arthur hale", "老执事", "mabel finch"} {
		if !used[want] {
			t.Errorf("used should contain %q, got %v", want, used)
		}
	}
}

// 池子被占满时，已占用的姓名不得再次发放。
func TestGenerateNPCNameActionSkipsTakenNames(t *testing.T) {
	var gctx GameContext
	pool := npcNameFallbackPool["japanese"]["female"]
	for _, name := range pool[1:] {
		gctx.Session.Scenario.Content.Data.NPCs = append(gctx.Session.Scenario.Content.Data.NPCs, models.NPCData{Name: name})
	}
	var npcs []models.SessionNPC
	actx := ActionContext{GCtx: &gctx, TempNPCs: &npcs, Handles: map[models.AgentRole]agentHandle{}}

	results := generateNPCNameAction{}.Execute(ToolCall{Culture: "japanese", Gender: "female"}, actx)

	want := `<npc_names note="` + npcNameGuidance + `"><name>` + pool[0] + `</name></npc_names>`
	if len(results) != 1 || results[0].Result != want {
		t.Errorf("results = %+v, want the only free name %q", results, pool[0])
	}
}

// nameChatProvider 只实现 Chat：按序返回预设姓名并记录调用，其余方法不参与取名路径。
type nameChatProvider struct {
	llm.Provider
	names []string
	calls int
}

func (p *nameChatProvider) Chat(_ context.Context, _ string, _ []llm.ChatMessage) (string, error) {
	name := p.names[p.calls%len(p.names)]
	p.calls++
	return name, nil
}

// TestGenerateNPCNameActionUsesWriterForLocalizedNames 验证中日文取名借用 Writer 角色的模型，
// 房间里没有任何 NPC 角色也能取名。
func TestGenerateNPCNameActionUsesWriterForLocalizedNames(t *testing.T) {
	prov := &nameChatProvider{names: []string{"陈守拙", "苏闻笛", "韩知远"}}
	gctx := &GameContext{}
	var npcs []models.SessionNPC
	actx := ActionContext{
		GCtx:     gctx,
		TempNPCs: &npcs,
		Handles:  map[models.AgentRole]agentHandle{models.AgentRoleWriter: newWriterTestHandle(prov)},
	}

	results := generateNPCNameAction{}.Execute(ToolCall{Action: ToolGenerateNPCName, Culture: "chinese", Gender: "male", Count: 2}, actx)

	if len(results) != 1 || strings.HasPrefix(results[0].Result, "SYSTEM REJECT") {
		t.Fatalf("results = %+v, want one non-reject result", results)
	}
	if prov.calls == 0 {
		t.Fatal("中文取名应调用 Writer 的 provider")
	}
	for _, want := range []string{"陈守拙", "苏闻笛"} {
		if !strings.Contains(results[0].Result, want) {
			t.Errorf("result = %q, want contains %q", results[0].Result, want)
		}
	}
}

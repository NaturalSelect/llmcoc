// NSFW 路由接进周期后的测试：判定结果决定由哪个 Director 的模型执行、转录怎么标记、
// 换角色时推理内容怎么处理，以及 Writer 按本周期实际执行的 Director 决定是否保留推理。
package agent

import (
	"context"
	"testing"

	"github.com/llmcoc/server/internal/models"
	"github.com/llmcoc/server/internal/services/llm"
)

const (
	routeSigDirector = "sig-director"
	routeSigNSFW     = "sig-nsfw"
)

func routeProvider(sig string) *directorHistoryFakeProvider {
	return &directorHistoryFakeProvider{reasoningBlocks: []llm.ReasoningBlock{{Type: "thinking", Text: "推理", Signature: sig}}}
}

// setRouteHandles 放入 Director 和 director_nsfw 两个假句柄；两者的提供商配置默认不同，所以换角色时要去推理。
func setRouteHandles(t *testing.T, sessionID uint, director, nsfw llm.Provider, sameModel bool) {
	t.Helper()
	dirProv, nsfwProv := uint(1), uint(2)
	if sameModel {
		nsfwProv = dirProv
	}
	sessionAgents.Store(sessionID, map[models.AgentRole]agentHandle{
		models.AgentRoleDirector:     {provider: director, config: &models.AgentConfig{Role: models.AgentRoleDirector, IsActive: true, ProviderConfigID: &dirProv, ModelName: "m"}, enabled: true},
		models.AgentRoleDirectorNSFW: {provider: nsfw, config: &models.AgentConfig{Role: models.AgentRoleDirectorNSFW, IsActive: true, ProviderConfigID: &nsfwProv, ModelName: "m"}, enabled: true},
		models.AgentRoleDramaturg:    {enabled: false},
	})
	t.Cleanup(func() { deleteCachedAgents(sessionID) })
}

func newRouteSession(t *testing.T, id uint, roomNSFW bool) {
	t.Helper()
	initAgentTestDB(t)
	if err := models.DB.Create(&models.GameSession{ID: id, EnableNSFW: roomNSFW}).Error; err != nil {
		t.Fatalf("create session: %v", err)
	}
}

func routeFloorGctx(sessionID uint, roomNSFW bool, published *[]RoundOutputs) GameContext {
	g := newDirectorRunGctx(sessionID, 1, "我走向她")
	g.Session.EnableNSFW = roomNSFW
	g.PendingActions = []PlayerAction{{UserID: 1, PlayerName: "甲", Content: "我走向她"}}
	g.Publish = func(ro RoundOutputs) error {
		*published = append(*published, ro)
		return nil
	}
	return g
}

// sentSignatures 收集一次调用收到的消息里所有推理块的签名。
func sentSignatures(msgs []llm.ChatMessage) map[string]bool {
	out := map[string]bool{}
	for _, m := range msgs {
		for _, rb := range m.ReasoningBlocks {
			out[rb.Signature] = true
		}
	}
	return out
}

func TestRun_NSFWClassificationRunsTheCycleOnDirectorNSFW(t *testing.T) {
	const sid = 301
	newRouteSession(t, sid, true)
	dir, nsfw := routeProvider(routeSigDirector), routeProvider(routeSigNSFW)
	setRouteHandles(t, sid, dir, nsfw, false)
	useFakeClassifier(t, nsfwRouteNSFW)

	var published []RoundOutputs
	if _, err := run(context.Background(), routeFloorGctx(sid, true, &published)); err != nil {
		t.Fatalf("run(): %v", err)
	}

	if dir.callCount() != 0 || nsfw.callCount() != 1 {
		t.Fatalf("calls director=%d director_nsfw=%d, want 0 and 1", dir.callCount(), nsfw.callCount())
	}
	data := loadTranscript(t, sid, "director")
	if len(data.Turns) == 0 {
		t.Fatal("no transcript units saved")
	}
	for _, turn := range data.Turns {
		if turn.Agent != string(models.AgentRoleDirectorNSFW) {
			t.Errorf("unit %d tagged %q, want director_nsfw", turn.Seq, turn.Agent)
		}
	}
	if len(published) == 0 || published[0].WriterCtx.DirectorRole != models.AgentRoleDirectorNSFW {
		t.Errorf("WriterCtx.DirectorRole = %+v, want director_nsfw so the Writer can tell which Director ran", published)
	}
}

func TestRun_SwitchingDirectorsDropsTheOtherModelsReasoning(t *testing.T) {
	const sid = 302
	newRouteSession(t, sid, true)
	dir, nsfw := routeProvider(routeSigDirector), routeProvider(routeSigNSFW)
	setRouteHandles(t, sid, dir, nsfw, false)

	var published []RoundOutputs
	steps := []nsfwRouteOutcome{nsfwRouteNSFW, nsfwRouteClean, nsfwRouteNSFW}
	for i, outcome := range steps {
		useFakeClassifier(t, outcome)
		if _, err := run(context.Background(), routeFloorGctx(sid, true, &published)); err != nil {
			t.Fatalf("run() #%d: %v", i+1, err)
		}
	}

	// 第二个周期由 director 执行：看得到 director_nsfw 的历史，但推理已去掉。
	second := dir.msgsAt(0)
	if len(second) <= len(ckHead()) || sentSignatures(second)[routeSigNSFW] {
		t.Errorf("director cycle must see the earlier history without director_nsfw reasoning, signatures=%v", sentSignatures(second))
	}
	// 第三个周期回到 director_nsfw：自己第一个周期的推理还在，director 周期的推理已去掉。
	third := nsfw.msgsAt(1)
	sigs := sentSignatures(third)
	if !sigs[routeSigNSFW] || sigs[routeSigDirector] {
		t.Errorf("director_nsfw cycle signatures = %v, want its own reasoning kept and director's dropped", sigs)
	}

	data := loadTranscript(t, sid, "director")
	if got := LoadContext(sid, "director", nil, ContextOptions{}).LastAgent(); got != models.AgentRoleDirectorNSFW {
		t.Errorf("LastAgent() = %q, want director_nsfw", got)
	}
	for _, turn := range data.Turns {
		for _, m := range turn.Messages {
			if m.Role == "assistant" && len(m.ReasoningBlocks) == 0 {
				t.Errorf("stored unit %d lost its reasoning: stripping must only affect what is sent", turn.Seq)
			}
		}
	}
}

func TestRun_SameModelKeepsReasoningAcrossDirectorRoles(t *testing.T) {
	const sid = 303
	newRouteSession(t, sid, true)
	dir, nsfw := routeProvider(routeSigDirector), routeProvider(routeSigNSFW)
	setRouteHandles(t, sid, dir, nsfw, true)

	var published []RoundOutputs
	for i, outcome := range []nsfwRouteOutcome{nsfwRouteNSFW, nsfwRouteClean} {
		useFakeClassifier(t, outcome)
		if _, err := run(context.Background(), routeFloorGctx(sid, true, &published)); err != nil {
			t.Fatalf("run() #%d: %v", i+1, err)
		}
	}
	if sigs := sentSignatures(dir.msgsAt(0)); !sigs[routeSigNSFW] {
		t.Errorf("same provider and model: director must receive director_nsfw's reasoning, got %v", sigs)
	}
}

func TestRun_RoomSwitchOffNeverClassifiesAndKeepsDirector(t *testing.T) {
	const sid = 304
	newRouteSession(t, sid, false)
	dir, nsfw := routeProvider(routeSigDirector), routeProvider(routeSigNSFW)
	setRouteHandles(t, sid, dir, nsfw, false)
	calls := useFakeClassifier(t, nsfwRouteNSFW)

	var published []RoundOutputs
	if _, err := run(context.Background(), routeFloorGctx(sid, false, &published)); err != nil {
		t.Fatalf("run(): %v", err)
	}
	if calls.Load() != 0 || dir.callCount() != 1 || nsfw.callCount() != 0 {
		t.Errorf("classifier=%d director=%d director_nsfw=%d, want 0/1/0", calls.Load(), dir.callCount(), nsfw.callCount())
	}
	for _, turn := range loadTranscript(t, sid, "director").Turns {
		if turn.Agent != "" {
			t.Errorf("unit %d tagged %q, want untagged", turn.Seq, turn.Agent)
		}
	}
}

func TestRunWriterStream_KeepsReasoningOnlyForTheDirectorThatRanTheCycle(t *testing.T) {
	const sid = 305
	newRouteSession(t, sid, true)

	writerProv, other, same := uint(1), uint(2), uint(1)
	prov := &writerFakeProvider{responses: []string{"正文。", "正文。"}}
	sessionAgents.Store(uint(sid), map[models.AgentRole]agentHandle{
		models.AgentRoleWriter:       {provider: prov, config: &models.AgentConfig{Role: models.AgentRoleWriter, IsActive: true, ProviderConfigID: &writerProv, ModelName: "m"}, enabled: true},
		models.AgentRoleDirector:     {provider: &scriptedDirectorProvider{}, config: &models.AgentConfig{Role: models.AgentRoleDirector, IsActive: true, ProviderConfigID: &other, ModelName: "m"}, enabled: true},
		models.AgentRoleDirectorNSFW: {provider: &scriptedDirectorProvider{}, config: &models.AgentConfig{Role: models.AgentRoleDirectorNSFW, IsActive: true, ProviderConfigID: &same, ModelName: "m"}, enabled: true},
	})
	t.Cleanup(func() { deleteCachedAgents(sid) })

	director := []llm.ChatMessage{
		{Role: "system", Content: "director-system"},
		{Role: "assistant", Reasoning: "秘密推理", ToolCalls: []llm.ToolCall{fakeToolCall("w1", "write", `{"direction":"推门"}`)}},
		{Role: "tool", ToolCallID: "w1", Content: "已入队"},
	}
	req := WriterRequest{Direction: "推门", CallID: "w1", Director: director}

	gctx := GameContext{Session: models.GameSession{ID: sid}, DirectorRole: models.AgentRoleDirectorNSFW}
	if _, err := RunWriterStream(context.Background(), gctx, req, func(string) {}); err != nil {
		t.Fatalf("RunWriterStream (director_nsfw cycle): %v", err)
	}
	gctx.DirectorRole = ""
	if _, err := RunWriterStream(context.Background(), gctx, req, func(string) {}); err != nil {
		t.Fatalf("RunWriterStream (director cycle): %v", err)
	}

	hasReasoningIn := func(msgs []llm.ChatMessage) bool {
		for _, m := range msgs {
			if m.Reasoning != "" {
				return true
			}
		}
		return false
	}
	if !hasReasoningIn(prov.msgsAt(0)) {
		t.Error("Writer shares a model with director_nsfw: its cycle's reasoning should be kept")
	}
	if hasReasoningIn(prov.msgsAt(1)) {
		t.Error("Writer does not share a model with director: reasoning must be dropped")
	}
}

func TestGameContext_DirectorRoleDefaultsToDirector(t *testing.T) {
	if got := (GameContext{}).directorRole(); got != models.AgentRoleDirector {
		t.Errorf("empty DirectorRole = %q, want director", got)
	}
	if got := (GameContext{DirectorRole: models.AgentRoleDirectorNSFW}).directorRole(); got != models.AgentRoleDirectorNSFW {
		t.Errorf("DirectorRole = %q, want director_nsfw", got)
	}
}

// NOTE: scripter_compile_test.go 验证 compilerSystemPrompt 的关键约束语句，以及编译会话的
// provider fallback、mythos_anchor 不可改、提交关卡（拒绝、补写、放行上限）和逻辑修复的同链续跑与回滚。
// 编译走原生工具循环，测试通过 sequentialFakeProvider 的 toolResponses 序列驱动。禁止真实网络/真实LLM。
package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/llmcoc/server/internal/models"
	"github.com/llmcoc/server/internal/services/llm"
)

// TestCompilerSystemPromptContainsKey 验证编译器 system prompt 明确声明"只做格式转换、不改写事实"。
func TestCompilerSystemPromptContainsKey(t *testing.T) {
	prompt := compilerSystemPrompt()
	for _, keyword := range []string{"不改写", "忠实", "格式编译", "submit_draft", "mythos_core"} {
		if !strings.Contains(prompt, keyword) {
			t.Errorf("compilerSystemPrompt() 缺少关键约束语句 %q", keyword)
		}
	}
	if strings.Contains(prompt, "schema_skeleton") || strings.Contains(prompt, "只输出一个JSON对象") {
		t.Error("compilerSystemPrompt() 不应再要求输出整份 JSON")
	}
}

// compileTestStory 返回编译阶段测试共用的最小合法 StoryOutput。
func compileTestStory() StoryOutput {
	return StoryOutput{
		Document:     strings.Repeat("这是故事文档的正文内容，涵盖表层情境、KP内部真相、地点、NPC、线索、时间线与结局。", 20),
		MythosAnchor: "食尸鬼（Ghoul）：COC7规则书已收录；具体属性按规则书裁定。",
	}
}

func compilerRoom(fake *sequentialFakeProvider) *scripterRoom {
	return &scripterRoom{
		sessionID: "test-session-compile",
		compiler: agentHandle{
			provider: fake,
			config:   &models.AgentConfig{Role: models.AgentRoleCompiler, IsActive: true},
			enabled:  true,
		},
	}
}

// TestCompileSessionFallbackToArchitect 验证 compiler.provider 为 nil 时 fallback 到 architect provider，
// 且草稿内容取自工具调用的写入。
func TestCompileSessionFallbackToArchitect(t *testing.T) {
	fake := &sequentialFakeProvider{
		callerName:    "architect",
		toolResponses: compileToolResponses(compileFixtureDraft, compileFixtureConcept),
	}
	room := &scripterRoom{
		sessionID: "test-session-compile-1",
		architect: agentHandle{
			provider: fake,
			config:   &models.AgentConfig{Role: models.AgentRoleArchitect, IsActive: true},
			enabled:  true,
		},
	}
	story := compileTestStory()

	s, err := startCompile(context.Background(), room, story, ScripterConstraints{})
	if err != nil {
		t.Fatalf("startCompile failed: %v", err)
	}
	draft, concept := s.result()
	if draft.Name != compileFixtureDraft.Name {
		t.Errorf("draft.Name = %q, want %q", draft.Name, compileFixtureDraft.Name)
	}
	if len(draft.Content.Scenes) != len(compileFixtureDraft.Content.Scenes) || len(draft.Content.Clues) != len(compileFixtureDraft.Content.Clues) {
		t.Errorf("场景或线索数量不对: scenes=%d clues=%d", len(draft.Content.Scenes), len(draft.Content.Clues))
	}
	if concept != compileFixtureConcept {
		t.Errorf("rewardConcept = %q, want %q", concept, compileFixtureConcept)
	}
	if len(fake.recordedKeys) != 2 {
		t.Fatalf("architect provider 应被调用两轮（写入、提交）, got %d", len(fake.recordedKeys))
	}
	if !strings.Contains(fake.recordedKeys[0], string(models.AgentRoleArchitect)) {
		t.Errorf("fallback 应使用 architect 的 cache key, got %q", fake.recordedKeys[0])
	}
	if s.rejects != 0 {
		t.Errorf("夹具草稿应一次通过, rejects = %d", s.rejects)
	}
}

// TestCompileSessionMythosAnchorFixed 验证锚点由系统预置：草稿初始就带 story 阶段确认的锚点，
// 且没有任何工具能改它（set_overview 的 schema 不含 mythos_anchor，传入会因未知字段被拒绝）。
func TestCompileSessionMythosAnchorFixed(t *testing.T) {
	story := compileTestStory()
	d := newCompileDraft(story, ScripterConstraints{})
	if d.draft.Content.MythosAnchor != story.MythosAnchor {
		t.Fatalf("草稿初始锚点 = %q, want %q", d.draft.Content.MythosAnchor, story.MythosAnchor)
	}
	out := d.setOverview(`{"mythos_anchor":"被篡改的神话锚点"}`)
	if out.reject == "" {
		t.Error("set_overview 传入 mythos_anchor 应被拒绝")
	}
	if d.draft.Content.MythosAnchor != story.MythosAnchor {
		t.Errorf("锚点被改动: %q", d.draft.Content.MythosAnchor)
	}
	for _, tool := range compileDraftTools() {
		var schema struct {
			Properties map[string]json.RawMessage `json:"properties"`
		}
		if err := json.Unmarshal(tool.def.Parameters, &schema); err != nil {
			t.Fatalf("工具 %s 的参数不是合法 JSON: %v", tool.def.Name, err)
		}
		if _, ok := schema.Properties["mythos_anchor"]; ok {
			t.Errorf("工具 %s 的参数里不应有 mythos_anchor 属性", tool.def.Name)
		}
	}
}

// TestCompileSessionNoProviderAvailable 验证 compiler 与 architect provider 均不可用时返回明确错误。
func TestCompileSessionNoProviderAvailable(t *testing.T) {
	room := &scripterRoom{sessionID: "test-session-compile-3"}
	if _, err := startCompile(context.Background(), room, compileTestStory(), ScripterConstraints{}); err == nil {
		t.Fatal("compiler/architect 均不可用时应返回错误")
	}
}

// TestCompileSessionUsesCompilerCacheKey 验证配置了 compiler 时使用它自己的 provider 和 cache key。
func TestCompileSessionUsesCompilerCacheKey(t *testing.T) {
	fake := &sequentialFakeProvider{callerName: "compiler", toolResponses: compileToolResponses(compileFixtureDraft, compileFixtureConcept)}
	if _, err := startCompile(context.Background(), compilerRoom(fake), compileTestStory(), ScripterConstraints{}); err != nil {
		t.Fatalf("startCompile failed: %v", err)
	}
	for _, key := range fake.recordedKeys {
		if !strings.Contains(key, string(models.AgentRoleCompiler)) {
			t.Errorf("应使用 compiler 的 cache key, got %q", key)
		}
	}
}

// TestCompileSessionRejectedThenFixed 验证提交被拒后，模型只补写缺失的字段再提交即可通过，
// 拒绝回执里带着编号的问题清单，已写入的内容不会丢。
func TestCompileSessionRejectedThenFixed(t *testing.T) {
	fake := &sequentialFakeProvider{
		callerName: "compiler",
		toolResponses: []llm.ToolChatResult{
			{ToolCalls: compileWriteCalls(compileFixtureDraft, "")}, // 没写 reward_concept
			{ToolCalls: []llm.ToolCall{compileSubmitCall("s1")}},
			{ToolCalls: []llm.ToolCall{{ID: "f1", Name: toolNameSetOverview, Arguments: mustJSON(map[string]any{"reward_concept": compileFixtureConcept})}}},
			{ToolCalls: []llm.ToolCall{compileSubmitCall("s2")}},
		},
	}
	s, err := startCompile(context.Background(), compilerRoom(fake), compileTestStory(), ScripterConstraints{})
	if err != nil {
		t.Fatalf("startCompile failed: %v", err)
	}
	if s.rejects != 1 {
		t.Errorf("rejects = %d, want 1", s.rejects)
	}
	draft, concept := s.result()
	if concept != compileFixtureConcept {
		t.Errorf("rewardConcept = %q, want 补写后的值", concept)
	}
	if len(draft.Content.Scenes) == 0 || draft.Name == "" {
		t.Error("补写后此前写入的内容不应丢失")
	}
	// 第三次请求的消息里应有对 s1 的拒绝回执，且点名 reward_concept。
	var rejectMsg string
	for _, m := range fake.recordedMessages[2] {
		if m.Role == "tool" && m.ToolCallID == "s1" {
			rejectMsg = m.Content
		}
	}
	if !strings.HasPrefix(rejectMsg, "SYSTEM REJECT") || !strings.Contains(rejectMsg, "reward_concept") {
		t.Errorf("提交被拒的回执应以 SYSTEM REJECT 开头并点名 reward_concept, got %q", rejectMsg)
	}
}

// TestCompileSessionSubmittedAfterRejectLimit 验证被拒绝 compileMaxRejects 次后，第 compileMaxRejects+1 次
// 提交带着剩余问题放行，而不是让整次编译失败。
func TestCompileSessionSubmittedAfterRejectLimit(t *testing.T) {
	responses := []llm.ToolChatResult{{ToolCalls: compileWriteCalls(compileFixtureDraft, "")}}
	for i := 0; i <= compileMaxRejects; i++ {
		responses = append(responses, llm.ToolChatResult{ToolCalls: []llm.ToolCall{compileSubmitCall("s")}})
	}
	fake := &sequentialFakeProvider{callerName: "compiler", toolResponses: responses}
	var progress []string
	room := compilerRoom(fake)
	room.progressFn = func(stage, status, detail string) { progress = append(progress, detail) }

	s, err := startCompile(context.Background(), room, compileTestStory(), ScripterConstraints{})
	if err != nil {
		t.Fatalf("达到拒绝上限后应放行而不是失败: %v", err)
	}
	if s.rejects != compileMaxRejects {
		t.Errorf("rejects = %d, want %d", s.rejects, compileMaxRejects)
	}
	if _, concept := s.result(); concept != "" {
		t.Errorf("放行时 reward_concept 仍为空, got %q", concept)
	}
	if !strings.Contains(strings.Join(progress, "\n"), "重试上限") {
		t.Errorf("放行时应推送进度说明, got %v", progress)
	}
}

// TestCompileSessionRejectsBannedKeywords 验证 name/tags/reward_concept 命中禁用关键词时提交被拒。
func TestCompileSessionRejectsBannedKeywords(t *testing.T) {
	d := newCompileDraft(compileTestStory(), ScripterConstraints{})
	d.draft = compileFixtureDraft
	d.clueKeys = compileFixtureClueKeys
	d.rewardConcept = "一本记满账目的旧账簿"
	var found bool
	for _, issue := range d.gateIssues(nil) {
		if strings.Contains(issue, "禁用关键词") && strings.Contains(issue, "reward_concept(账目)") {
			found = true
		}
	}
	if !found {
		t.Errorf("reward_concept 含禁用词应被关卡拦下, issues=%v", d.gateIssues(nil))
	}
}

// TestCompileSessionToneTagsFixed 验证约束里有 tone_tags 时草稿预填并锁定，改动被拒绝，写成相同值则放行。
func TestCompileSessionToneTagsFixed(t *testing.T) {
	d := newCompileDraft(compileTestStory(), ScripterConstraints{ToneTags: []string{"a", "b"}})
	if got := strings.Join(d.draft.Content.ToneTags, ","); got != "a,b" {
		t.Fatalf("预填 tone_tags = %q, want a,b", got)
	}
	if out := d.setOverview(`{"tone_tags":["x"]}`); out.reject == "" {
		t.Error("改动固定的 tone_tags 应被拒绝")
	}
	if out := d.setOverview(`{"tone_tags":["a","b"],"invest_focus":"x"}`); out.reject != "" {
		t.Errorf("写成相同值应放行, got %q", out.reject)
	}
}

// qaReportFake 返回一个让 report_issues 给出指定问题清单的 qa provider。
func qaReportFake(issues ...string) *sequentialFakeProvider {
	args, _ := json.Marshal(map[string]any{"issues": issues})
	return &sequentialFakeProvider{
		callerName: "qa",
		toolResponses: []llm.ToolChatResult{
			{ToolCalls: []llm.ToolCall{{ID: "q1", Name: toolNameReportIssues, Arguments: string(args)}}},
		},
	}
}

// TestCompileAndFinalize_SkipLogicReview 验证上传编译（ETL）路径跳过 QA 逻辑审查：
// QA 不被调用、没有 logic_review 进度事件、compiler 只跑写入和提交两轮。
func TestCompileAndFinalize_SkipLogicReview(t *testing.T) {
	fake := &sequentialFakeProvider{
		callerName:    "compiler",
		toolResponses: compileToolResponses(compileFixtureDraft, compileFixtureConcept),
	}
	room := compilerRoom(fake)
	qa := qaReportFake("endings[书归其主] 的 san_reward 与故事文档不一致")
	room.qa = agentHandle{provider: qa, config: &models.AgentConfig{Role: models.AgentRoleQAGuard, IsActive: true}, enabled: true}
	room.req = ScenarioCreationRequest{Name: "指定标题"}
	room.skipLogicReview = true
	var stages []string
	room.progressFn = func(stage, status, _ string) { stages = append(stages, stage+":"+status) }

	_, iterations, err := room.compileAndFinalize(context.Background(), compileTestStory(), ScripterConstraints{})
	if err != nil {
		t.Fatalf("compileAndFinalize failed: %v", err)
	}
	if iterations != 0 {
		t.Errorf("跳过审查就没有修复轮, iterations = %d, want 0", iterations)
	}
	if len(qa.recordedMessages) != 0 {
		t.Errorf("QA 不应被调用, got %d 次", len(qa.recordedMessages))
	}
	if len(fake.recordedMessages) != 2 {
		t.Errorf("compiler 应只被调用 2 轮（写入、提交）, got %d", len(fake.recordedMessages))
	}
	for _, s := range stages {
		if strings.HasPrefix(s, "logic_review:") {
			t.Errorf("不应有 logic_review 进度事件, got %v", stages)
		}
	}
}

// TestCompileAndFinalize_ETLPlayersAndDifficulty 验证上传编译（ETL）从文档提取的人数和难度不会被默认值覆盖回去：
// 房间请求里的人数和难度是 normalize 填的默认值而不是用户指定，markETL 要把它们清掉。
func TestCompileAndFinalize_ETLPlayersAndDifficulty(t *testing.T) {
	run := func(t *testing.T, etl bool, overview map[string]any) ScenarioDraft {
		t.Helper()
		calls := compileWriteCalls(compileFixtureDraft, compileFixtureConcept)
		if overview != nil {
			calls = append(calls, llm.ToolCall{ID: "w8", Name: toolNameSetOverview, Arguments: mustJSON(overview)})
		}
		fake := &sequentialFakeProvider{
			callerName: "compiler",
			toolResponses: []llm.ToolChatResult{
				{ToolCalls: calls},
				{ToolCalls: []llm.ToolCall{compileSubmitCall("s1")}},
			},
		}
		room := compilerRoom(fake)
		room.req = normalizeScenarioCreationRequest(ScenarioCreationRequest{Name: "指定标题"})
		if etl {
			room.markETL()
		}
		draft, _, err := room.compileAndFinalize(context.Background(), compileTestStory(), ScripterConstraints{})
		if err != nil {
			t.Fatalf("compileAndFinalize failed: %v", err)
		}
		return draft
	}
	fromDoc := map[string]any{"min_players": 2, "max_players": 3, "difficulty": "hard"}

	t.Run("ETL 采用文档里的人数和难度", func(t *testing.T) {
		d := run(t, true, fromDoc)
		if d.MinPlayers != 2 || d.MaxPlayers != 3 || d.Difficulty != "hard" {
			t.Errorf("players=%d-%d difficulty=%q, want 2-3 hard", d.MinPlayers, d.MaxPlayers, d.Difficulty)
		}
	})
	t.Run("文档没写时落回默认值", func(t *testing.T) {
		d := run(t, true, nil)
		if d.MinPlayers != 1 || d.MaxPlayers != 4 || d.Difficulty != "normal" {
			t.Errorf("players=%d-%d difficulty=%q, want 1-4 normal", d.MinPlayers, d.MaxPlayers, d.Difficulty)
		}
	})
	t.Run("非 ETL 仍以请求为准", func(t *testing.T) {
		d := run(t, false, fromDoc)
		if d.MinPlayers != 1 || d.MaxPlayers != 4 || d.Difficulty != "normal" {
			t.Errorf("players=%d-%d difficulty=%q, want 1-4 normal（请求里的值覆盖草稿）", d.MinPlayers, d.MaxPlayers, d.Difficulty)
		}
	})
}

// TestCompileAndFinalize_LogicFixContinuesSameConversation 验证逻辑审查的问题追加到编译的同一条对话，
// compiler 第二段请求的消息以第一段的全部消息为前缀，修复后的字段进入最终草稿。
func TestCompileAndFinalize_LogicFixContinuesSameConversation(t *testing.T) {
	fake := &sequentialFakeProvider{
		callerName: "compiler",
		toolResponses: append(compileToolResponses(compileFixtureDraft, compileFixtureConcept),
			llm.ToolChatResult{ToolCalls: []llm.ToolCall{{ID: "f1", Name: toolNameUpsertEndings, Arguments: mustJSON(map[string]any{"endings": []models.EndingData{
				{Name: "书归其主", Trigger: "如果调查员让Douglas重获藏书，则他退隐墓地", SANReward: "恢复1d6"},
			}})}}},
			llm.ToolChatResult{ToolCalls: []llm.ToolCall{compileSubmitCall("s2")}},
		),
	}
	room := compilerRoom(fake)
	room.qa = agentHandle{provider: qaReportFake("endings[书归其主] 的 san_reward 与故事文档不一致"), config: &models.AgentConfig{Role: models.AgentRoleQAGuard, IsActive: true}, enabled: true}
	room.req = ScenarioCreationRequest{Name: "指定标题"}

	draft, iterations, err := room.compileAndFinalize(context.Background(), compileTestStory(), ScripterConstraints{})
	if err != nil {
		t.Fatalf("compileAndFinalize failed: %v", err)
	}
	if iterations != 1 {
		t.Errorf("逻辑修复一轮, iterations = %d, want 1", iterations)
	}
	var san string
	for _, e := range draft.Content.Endings {
		if e.Name == "书归其主" {
			san = e.SANReward
		}
	}
	if san != "恢复1d6" {
		t.Errorf("修复后的结局 san_reward = %q, want 恢复1d6", san)
	}
	if len(fake.recordedMessages) != 4 {
		t.Fatalf("compiler 应被调用 4 轮（写入、提交、修复、提交）, got %d", len(fake.recordedMessages))
	}
	before, after := fake.recordedMessages[1], fake.recordedMessages[2]
	if len(after) <= len(before) {
		t.Fatalf("修复轮的消息链应更长: before=%d after=%d", len(before), len(after))
	}
	for i := range before {
		if before[i].Role != after[i].Role || before[i].Content != after[i].Content {
			t.Fatalf("修复轮消息链的前缀必须与编译轮逐条一致，第 %d 条不同", i)
		}
	}
	var mustFix string
	for _, m := range after {
		if m.Role == "user" && strings.Contains(m.Content, "<must_fix>") {
			mustFix = m.Content
		}
	}
	if !strings.Contains(mustFix, "san_reward 与故事文档不一致") {
		t.Errorf("must_fix 应带上审查问题, got %q", mustFix)
	}
}

// TestCompileSessionFixFailureRestoresDraft 验证逻辑修复失败（模型不再返回工具调用）时草稿回到修复前的状态。
func TestCompileSessionFixFailureRestoresDraft(t *testing.T) {
	fake := &sequentialFakeProvider{
		callerName: "compiler",
		toolResponses: append(compileToolResponses(compileFixtureDraft, compileFixtureConcept),
			// 修复轮：先改坏一个场景，然后序列耗尽，驱动器因连续空轮报错
			llm.ToolChatResult{ToolCalls: []llm.ToolCall{{ID: "f1", Name: toolNameRemoveEntries, Arguments: `{"section":"scenes","keys":["library_main"]}`}}},
		),
	}
	s, err := startCompile(context.Background(), compilerRoom(fake), compileTestStory(), ScripterConstraints{})
	if err != nil {
		t.Fatalf("startCompile failed: %v", err)
	}
	before, _ := s.result()
	if err := s.fix(context.Background(), []string{"某个问题"}); err == nil {
		t.Fatal("修复轮没有提交时应返回错误")
	}
	after, _ := s.result()
	if len(after.Content.Scenes) != len(before.Content.Scenes) {
		t.Errorf("修复失败后草稿应回到修复前: scenes before=%d after=%d", len(before.Content.Scenes), len(after.Content.Scenes))
	}
}

// TestCompileSessionResultIsCopy 验证 result 返回的是副本：对副本的改动不会回到会话里。
func TestCompileSessionResultIsCopy(t *testing.T) {
	fake := &sequentialFakeProvider{callerName: "compiler", toolResponses: compileToolResponses(compileFixtureDraft, compileFixtureConcept)}
	s, err := startCompile(context.Background(), compilerRoom(fake), compileTestStory(), ScripterConstraints{})
	if err != nil {
		t.Fatalf("startCompile failed: %v", err)
	}
	draft, _ := s.result()
	draft.Content.Scenes[0].Name = "被外部改动"
	draft.Content.NPCs[0].Stats["STR"] = 1
	again, _ := s.result()
	if again.Content.Scenes[0].Name == "被外部改动" || again.Content.NPCs[0].Stats["STR"] == 1 {
		t.Error("result 应返回深拷贝")
	}
}

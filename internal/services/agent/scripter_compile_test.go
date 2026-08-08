// NOTE: scripter_compile_test.go 验证 compilerSystemPrompt 的关键约束语句，以及
// compileStoryToModule 的 provider fallback 与 mythos_anchor 强制覆盖行为。
// 禁止真实网络/真实LLM；使用 translator_test.go 中定义的 sequentialFakeProvider。
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
	for _, keyword := range []string{"不改写", "忠实", "格式编译"} {
		if !strings.Contains(prompt, keyword) {
			t.Errorf("compilerSystemPrompt() 缺少关键约束语句 %q", keyword)
		}
	}
}

// compileTestStory 返回编译阶段测试共用的最小合法 StoryOutput。
func compileTestStory() StoryOutput {
	return StoryOutput{
		Document:     strings.Repeat("这是故事文档的正文内容，涵盖表层情境、KP内部真相、地点、NPC、线索、时间线与结局。", 20),
		MythosAnchor: "食尸鬼（Ghoul）：COC7规则书已收录；具体属性按规则书裁定。",
	}
}

// TestCompilerFallbackToArchitect 验证 compiler.provider 为 nil 时 fallback 到 architect provider，
// 且编译结果字段取自 fake provider 返回的 JSON。
func TestCompilerFallbackToArchitect(t *testing.T) {
	fake := &sequentialFakeProvider{
		callerName: "architect",
		toolResponses: []llm.ToolChatResult{
			{ToolCalls: []llm.ToolCall{fakeToolCall("call_1", toolNameSubmitCompiled, oneshotExample)}},
		},
	}
	room := &scripterRoom{
		sessionID: "test-session-compile-1",
		architect: agentHandle{
			provider: fake,
			config:   &models.AgentConfig{Role: models.AgentRoleArchitect, IsActive: true},
			enabled:  true,
		},
		// compiler 留零值，触发 fallback
	}
	story := compileTestStory()

	draft, _, err := compileStoryToModule(context.Background(), room, story, ScripterConstraints{})
	if err != nil {
		t.Fatalf("compileStoryToModule failed: %v", err)
	}
	if draft.Name != oneshotResultExample.Name {
		t.Errorf("draft.Name = %q, want %q", draft.Name, oneshotResultExample.Name)
	}
	if len(draft.Content.Scenes) != len(oneshotResultExample.Content.Scenes) {
		t.Errorf("draft.Content.Scenes 数量 = %d, want %d", len(draft.Content.Scenes), len(oneshotResultExample.Content.Scenes))
	}
	if len(fake.recordedKeys) != 1 {
		t.Fatalf("architect provider 应被调用一次, got %d", len(fake.recordedKeys))
	}
	if !strings.Contains(fake.recordedKeys[0], string(models.AgentRoleArchitect)) {
		t.Errorf("fallback 应使用 architect 的 cache key, got %q", fake.recordedKeys[0])
	}
	if draft.Content.MythosAnchor != story.MythosAnchor {
		t.Errorf("draft.Content.MythosAnchor = %q, want %q（story确认值）", draft.Content.MythosAnchor, story.MythosAnchor)
	}
}

// TestCompilerMythosAnchorOverride 验证即使LLM返回篡改的mythos_anchor，编译结果仍强制使用story阶段已确认的锚点。
func TestCompilerMythosAnchorOverride(t *testing.T) {
	tampered := oneshotResultExample
	tampered.Content.MythosAnchor = "被篡改的神话锚点"
	fake := &sequentialFakeProvider{
		callerName: "compiler",
		toolResponses: []llm.ToolChatResult{
			{ToolCalls: []llm.ToolCall{fakeToolCall("call_1", toolNameSubmitCompiled, marshalExample(tampered))}},
		},
	}
	room := &scripterRoom{
		sessionID: "test-session-compile-2",
		compiler: agentHandle{
			provider: fake,
			config:   &models.AgentConfig{Role: models.AgentRoleCompiler, IsActive: true},
			enabled:  true,
		},
	}
	story := compileTestStory()

	draft, _, err := compileStoryToModule(context.Background(), room, story, ScripterConstraints{})
	if err != nil {
		t.Fatalf("compileStoryToModule failed: %v", err)
	}
	if draft.Content.MythosAnchor != story.MythosAnchor {
		t.Errorf("draft.Content.MythosAnchor = %q，篡改后仍应被覆盖为 %q", draft.Content.MythosAnchor, story.MythosAnchor)
	}
	if len(fake.recordedKeys) != 1 || !strings.Contains(fake.recordedKeys[0], string(models.AgentRoleCompiler)) {
		t.Errorf("应使用 compiler 自身的 provider/cache key, recordedKeys=%v", fake.recordedKeys)
	}
}

// TestCompilerNoProviderAvailable 验证 compiler 与 architect provider 均不可用时返回明确错误。
func TestCompilerNoProviderAvailable(t *testing.T) {
	room := &scripterRoom{sessionID: "test-session-compile-3"}
	if _, _, err := compileStoryToModule(context.Background(), room, compileTestStory(), ScripterConstraints{}); err == nil {
		t.Fatal("compiler/architect 均不可用时应返回错误")
	}
}

// TestCompileStoryToModule_EmptyRewardConceptRejectedOnce 验证 compiler 首次提交的
// reward_concept 为空时会被机制层拒绝一次并要求重新提交；重提后给出非空概念则被接受，
// compileStoryToModule 的第二个返回值应等于重提后的非空概念。
func TestCompileStoryToModule_EmptyRewardConceptRejectedOnce(t *testing.T) {
	empty := oneshotResultExample
	empty.RewardConcept = ""
	retried := oneshotResultExample
	retried.RewardConcept = "死者遗物中的抄本残页"
	fake := &sequentialFakeProvider{
		callerName: "compiler",
		toolResponses: []llm.ToolChatResult{
			{ToolCalls: []llm.ToolCall{fakeToolCall("call_1", toolNameSubmitCompiled, marshalExample(empty))}},
			{ToolCalls: []llm.ToolCall{fakeToolCall("call_2", toolNameSubmitCompiled, marshalExample(retried))}},
		},
	}
	room := &scripterRoom{
		sessionID: "test-session-compile-reward-retry",
		compiler: agentHandle{
			provider: fake,
			config:   &models.AgentConfig{Role: models.AgentRoleCompiler, IsActive: true},
			enabled:  true,
		},
	}
	story := compileTestStory()

	_, rewardConcept, err := compileStoryToModule(context.Background(), room, story, ScripterConstraints{})
	if err != nil {
		t.Fatalf("compileStoryToModule failed: %v", err)
	}
	if rewardConcept != "死者遗物中的抄本残页" {
		t.Errorf("rewardConcept = %q, want 重提后的非空概念", rewardConcept)
	}
	if len(fake.recordedKeys) != 2 {
		t.Errorf("reward_concept 首次为空应被拒绝一次并重提, compiler provider 调用次数 got %d, want 2", len(fake.recordedKeys))
	}
}

// TestOneshotDraftJSONSchemaValid 验证 oneshotDraftJSONSchema 本身是合法 JSON、且直接
// 声明了 OneshotResult 顶层字段（而不是包一层 draft），防止手写 schema 字符串出现语法错误
// 或退化回"空壳 object 参数"的问题。
func TestOneshotDraftJSONSchemaValid(t *testing.T) {
	var schema map[string]any
	if err := json.Unmarshal([]byte(oneshotDraftJSONSchema), &schema); err != nil {
		t.Fatalf("oneshotDraftJSONSchema 不是合法 JSON: %v", err)
	}
	if schema["type"] != "object" {
		t.Errorf("schema.type = %v, want object", schema["type"])
	}
	props, ok := schema["properties"].(map[string]any)
	if !ok {
		t.Fatal("schema.properties 缺失或类型不对")
	}
	for _, key := range []string{"reward_concept", "name", "author", "tags", "min_players", "max_players", "difficulty", "content"} {
		if _, ok := props[key]; !ok {
			t.Errorf("schema.properties 缺少顶层字段 %q（应为原生参数，不应嵌套在draft键下）", key)
		}
	}
	if _, ok := props["draft"]; ok {
		t.Error("schema.properties 不应再包含 draft 包装键")
	}

	full := jsonSchemaObject(oneshotDraftJSONSchema)
	var tool llm.ToolDefinition
	toolJSON := []byte(`{"name":"submit_compiled_scenario","parameters":` + string(full) + `}`)
	if err := json.Unmarshal(toolJSON, &tool); err != nil {
		t.Fatalf("组装为 ToolDefinition 后不是合法 JSON: %v", err)
	}
}

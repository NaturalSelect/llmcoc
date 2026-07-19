// NOTE: scripter_compile_test.go 验证 compilerSystemPrompt 的关键约束语句，以及
// compileStoryToModule 的 provider fallback 与 mythos_anchor 强制覆盖行为。
// 禁止真实网络/真实LLM；使用 translator_test.go 中定义的 sequentialFakeProvider。
package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/llmcoc/server/internal/models"
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
		Document:      strings.Repeat("这是故事文档的正文内容，涵盖表层情境、KP内部真相、地点、NPC、线索、时间线与结局。", 20),
		MythosAnchor:  "食尸鬼（Ghoul）：COC7规则书已收录；具体属性按规则书裁定。",
		RewardConcept: "与食尸鬼有关的古籍手稿",
	}
}

// TestCompilerFallbackToArchitect 验证 compiler.provider 为 nil 时 fallback 到 architect provider，
// 且编译结果字段取自 fake provider 返回的 JSON。
func TestCompilerFallbackToArchitect(t *testing.T) {
	fake := &sequentialFakeProvider{
		callerName: "architect",
		responses:  []string{oneshotExample},
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

	draft, err := compileStoryToModule(context.Background(), room, story, ScripterConstraints{})
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
		responses:  []string{marshalExample(tampered)},
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

	draft, err := compileStoryToModule(context.Background(), room, story, ScripterConstraints{})
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
	if _, err := compileStoryToModule(context.Background(), room, compileTestStory(), ScripterConstraints{}); err == nil {
		t.Fatal("compiler/architect 均不可用时应返回错误")
	}
}

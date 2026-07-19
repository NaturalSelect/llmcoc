// NOTE: scripter_upload_test.go 验证管理员上传故事编译功能：
// RunCompileStoryWithProgress 的输入校验，以及 compileAndFinalize 在跳过 Story Architect
// 阶段时的编译产出、mythos_anchor 强制覆盖与 reward_agent 可选跳过行为。
// 禁止真实网络/真实LLM；复用 translator_test.go 中的 sequentialFakeProvider。
package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/llmcoc/server/internal/models"
)

// TestRunCompileStoryWithProgress_MissingDocument 验证故事文档为空时直接返回错误，
// 不触发 newScripterRoom（不依赖数据库）。
func TestRunCompileStoryWithProgress_MissingDocument(t *testing.T) {
	_, err := RunCompileStoryWithProgress(context.Background(), CompileStoryRequest{
		MythosAnchor: "食尸鬼（Ghoul）",
	}, nil)
	if err == nil {
		t.Fatal("故事文档为空时应返回错误")
	}
}

// TestRunCompileStoryWithProgress_MissingAnchor 验证神话锚点为空时直接返回错误。
func TestRunCompileStoryWithProgress_MissingAnchor(t *testing.T) {
	_, err := RunCompileStoryWithProgress(context.Background(), CompileStoryRequest{
		StoryDocument: "故事文档正文",
	}, nil)
	if err == nil {
		t.Fatal("神话锚点为空时应返回错误")
	}
}

// TestCompileAndFinalize_Success 验证跳过 Story Architect 后，compileAndFinalize 能正常
// 完成 compile→normalize，且不再输出"阶段 N/6"式编号（该编号仅属于完整 AI 生成路径）。
func TestCompileAndFinalize_Success(t *testing.T) {
	fake := &sequentialFakeProvider{
		callerName: "compiler",
		responses:  []string{oneshotExample},
	}
	room := &scripterRoom{
		sessionID: "test-session-upload-1",
		compiler: agentHandle{
			provider: fake,
			config:   &models.AgentConfig{Role: models.AgentRoleCompiler, IsActive: true},
			enabled:  true,
		},
	}
	var stages []string
	room.progressFn = func(stage, status, detail string) {
		stages = append(stages, stage+":"+status)
		if strings.Contains(detail, "阶段") {
			t.Errorf("compileAndFinalize 的进度描述不应包含完整生成流水线的\"阶段 N/6\"编号，got %q", detail)
		}
	}
	story := compileTestStory()

	draft, iterations, err := room.compileAndFinalize(context.Background(), story, ScripterConstraints{})
	if err != nil {
		t.Fatalf("compileAndFinalize failed: %v", err)
	}
	if draft.Content.MythosAnchor != story.MythosAnchor {
		t.Errorf("draft.Content.MythosAnchor = %q, want %q", draft.Content.MythosAnchor, story.MythosAnchor)
	}
	if iterations != 0 {
		t.Errorf("示例草稿本应无需修复, iterations = %d, want 0", iterations)
	}
	foundCompile, foundNormalize := false, false
	for _, s := range stages {
		if s == "compile:done" {
			foundCompile = true
		}
		if s == "normalize:done" {
			foundNormalize = true
		}
	}
	if !foundCompile || !foundNormalize {
		t.Errorf("progress 事件应包含 compile:done 与 normalize:done, got %v", stages)
	}
}

// TestCompileAndFinalize_RewardSkipped 验证 RewardConcept 为空时跳过 reward_agent 阶段，
// 且 compiler provider 只被调用一次（不会为奖励设计发起额外 LLM 调用）。
func TestCompileAndFinalize_RewardSkipped(t *testing.T) {
	fake := &sequentialFakeProvider{
		callerName: "compiler",
		responses:  []string{oneshotExample},
	}
	room := &scripterRoom{
		sessionID: "test-session-upload-2",
		compiler: agentHandle{
			provider: fake,
			config:   &models.AgentConfig{Role: models.AgentRoleCompiler, IsActive: true},
			enabled:  true,
		},
	}
	story := compileTestStory()
	story.RewardConcept = ""

	draft, _, err := room.compileAndFinalize(context.Background(), story, ScripterConstraints{})
	if err != nil {
		t.Fatalf("compileAndFinalize failed: %v", err)
	}
	if draft.Content.Reward != nil {
		t.Errorf("RewardConcept 为空时不应生成奖励, got %+v", draft.Content.Reward)
	}
	if len(fake.recordedKeys) != 1 {
		t.Errorf("跳过奖励设计时 compiler provider 应只被调用一次, got %d", len(fake.recordedKeys))
	}
}

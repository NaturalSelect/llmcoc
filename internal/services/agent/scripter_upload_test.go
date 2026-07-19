// NOTE: scripter_upload_test.go 验证管理员上传故事编译功能：
// RunCompileStoryWithProgress 的输入校验、extractAnchorFromDocument 的锚点/奖励自动提取，
// 以及 compileAndFinalize 在跳过 Story Architect 阶段时的编译产出、mythos_anchor 强制覆盖
// 与 reward_agent 可选跳过行为。
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
	_, err := RunCompileStoryWithProgress(context.Background(), CompileStoryRequest{}, nil)
	if err == nil {
		t.Fatal("故事文档为空时应返回错误")
	}
}

// TestExtractAnchorFromDocument_Success 验证 anchor_extract 阶段能通过
// translate_anchor（经 translator/lawyer 校验）+ submit_story 识别出 mythos_anchor，
// 并从文档中提炼 reward_concept。
func TestExtractAnchorFromDocument_Success(t *testing.T) {
	initTranslatorTestDB(t)
	document := compileTestStory().Document

	architectFake := &sequentialFakeProvider{
		callerName: "architect",
		responses: []string{
			marshalExample([]storyArchitectToolCall{{
				Action:  toolOneshotTranslateAnchor,
				Concept: "死者被古老力量束缚继续行动",
				Reason:  "识别文档核心神话元素",
			}}),
			marshalExample([]storyArchitectToolCall{{
				Action:        toolStorySubmit,
				StoryDocument: document,
				MythosAnchor:  "食尸鬼（Ghoul）",
				RewardConcept: "与食尸鬼有关的古籍手稿",
			}}),
		},
	}
	translatorFake := &sequentialFakeProvider{
		callerName: "translator",
		responses: []string{
			`[{"action":"ask_lawyer","question":"食尸鬼在COC7规则书中是否已收录？"}]`,
			`[{"action":"respond","result":"{\"status\":\"found\",\"selected_anchor\":\"食尸鬼（Ghoul）\",\"rulebook_basis\":\"COC7规则书已收录\",\"usable_interpretation\":\"死者变形后保留记忆继续行动\",\"must_avoid\":\"不得自创属性\",\"fallback\":\"无\",\"blacklist_check\":\"未命中\"}"}]`,
		},
	}
	lawyerFake := &sequentialFakeProvider{
		callerName: "lawyer",
		responses: []string{
			`[{"action":"response","ruling":"食尸鬼（Ghoul）：COC7规则书已收录，死者变形后保留人类记忆继续行动。"}]`,
		},
	}
	room := &scripterRoom{
		sessionID: "test-session-extract-1",
		architect: agentHandle{
			provider: architectFake,
			config:   &models.AgentConfig{Role: models.AgentRoleArchitect, IsActive: true},
			enabled:  true,
		},
		translator: agentHandle{
			provider: translatorFake,
			config:   &models.AgentConfig{Role: models.AgentRoleTranslator, IsActive: true},
			enabled:  true,
		},
		lawyer: agentHandle{
			provider: lawyerFake,
			config:   &models.AgentConfig{Role: models.AgentRoleLawyer, IsActive: true},
			enabled:  true,
		},
	}

	result, err := extractAnchorFromDocument(context.Background(), room, document)
	if err != nil {
		t.Fatalf("extractAnchorFromDocument failed: %v", err)
	}
	if result.MythosAnchor != "食尸鬼（Ghoul）" {
		t.Errorf("MythosAnchor = %q, want 食尸鬼（Ghoul）", result.MythosAnchor)
	}
	if result.RewardConcept != "与食尸鬼有关的古籍手稿" {
		t.Errorf("RewardConcept = %q, want 与食尸鬼有关的古籍手稿", result.RewardConcept)
	}
}

// TestExtractAnchorFromDocument_Failure 验证 architect 始终不提交 submit_story
// （工具循环轮数耗尽）时，extractAnchorFromDocument 返回错误。
func TestExtractAnchorFromDocument_Failure(t *testing.T) {
	architectFake := &sequentialFakeProvider{callerName: "architect"} // 无预设响应，恒回退为非法action，永不submit_story
	room := &scripterRoom{
		sessionID: "test-session-extract-2",
		architect: agentHandle{
			provider: architectFake,
			config:   &models.AgentConfig{Role: models.AgentRoleArchitect, IsActive: true},
			enabled:  true,
		},
	}

	if _, err := extractAnchorFromDocument(context.Background(), room, "故事文档正文"); err == nil {
		t.Fatal("architect 始终未提交 submit_story 时应返回错误")
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

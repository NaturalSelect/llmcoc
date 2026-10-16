// NOTE: scripter_upload_test.go 验证管理员上传故事编译功能：
// RunCompileStoryWithProgress 的输入校验、extractAnchorFromDocument 的锚点自动提取，
// 以及 compileAndFinalize 在跳过 Story Architect 阶段时的编译产出、mythos_anchor 强制覆盖
// 与 reward_concept 为空时的机制层重试、兜底触发行为。
// 禁止真实网络/真实LLM；复用 testhelpers_test.go 中的 sequentialFakeProvider。
package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/llmcoc/server/internal/models"
	"github.com/llmcoc/server/internal/services/llm"
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
// ask_lawyer 核验 + confirm_anchor 登记 + submit_extraction 识别出 mythos_anchor。
func TestExtractAnchorFromDocument_Success(t *testing.T) {
	initSiteSettingTestDB(t)
	document := compileTestStory().Document

	askArgs, _ := json.Marshal(map[string]string{"question": "食尸鬼在COC7规则书中是否已收录？"})
	confirmArgs, _ := json.Marshal(map[string]string{"anchor": "食尸鬼（Ghoul）"})
	submitExtractionArgs, _ := json.Marshal(map[string]string{
		"mythos_anchor": "食尸鬼（Ghoul）",
	})
	architectFake := &sequentialFakeProvider{
		callerName: "architect",
		toolResponses: []llm.ToolChatResult{
			{ToolCalls: []llm.ToolCall{fakeToolCall("call_1", toolNameAskLawyer, string(askArgs))}},
			{ToolCalls: []llm.ToolCall{fakeToolCall("call_2", toolNameConfirmAnchor, string(confirmArgs))}},
			{ToolCalls: []llm.ToolCall{fakeToolCall("call_3", toolNameSubmitExtraction, string(submitExtractionArgs))}},
		},
	}
	lawyerFake := &sequentialFakeProvider{
		callerName:    "lawyer",
		toolResponses: lawyerDirectResponseToolResponses("食尸鬼（Ghoul）：COC7规则书已收录，死者变形后保留人类记忆继续行动。"),
	}
	room := &scripterRoom{
		sessionID: "test-session-extract-1",
		architect: agentHandle{
			provider: architectFake,
			config:   &models.AgentConfig{Role: models.AgentRoleArchitect, IsActive: true},
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
	if len(lawyerFake.recordedKeys) == 0 {
		t.Error("ask_lawyer 应触发 lawyer provider 调用")
	}
}

// TestExtractAnchorFromDocument_RejectsUnverifiedSubmit 验证 architect 没有登记锚点就直接
// submit_extraction 时被拒绝，且拒绝提示回传给模型，之后补做核验与登记仍可成功提交。
func TestExtractAnchorFromDocument_RejectsUnverifiedSubmit(t *testing.T) {
	initSiteSettingTestDB(t)

	askArgs, _ := json.Marshal(map[string]string{"question": "食尸鬼在COC7规则书中是否已收录？"})
	confirmArgs, _ := json.Marshal(map[string]string{"anchor": "食尸鬼（Ghoul）"})
	submitArgs, _ := json.Marshal(map[string]string{"mythos_anchor": "食尸鬼（Ghoul）"})
	architectFake := &sequentialFakeProvider{
		callerName: "architect",
		toolResponses: []llm.ToolChatResult{
			{ToolCalls: []llm.ToolCall{fakeToolCall("call_1", toolNameSubmitExtraction, string(submitArgs))}},
			{ToolCalls: []llm.ToolCall{fakeToolCall("call_2", toolNameAskLawyer, string(askArgs))}},
			{ToolCalls: []llm.ToolCall{fakeToolCall("call_3", toolNameConfirmAnchor, string(confirmArgs))}},
			{ToolCalls: []llm.ToolCall{fakeToolCall("call_4", toolNameSubmitExtraction, string(submitArgs))}},
		},
	}
	room := &scripterRoom{
		sessionID: "test-session-extract-3",
		architect: agentHandle{
			provider: architectFake,
			config:   &models.AgentConfig{Role: models.AgentRoleArchitect, IsActive: true},
			enabled:  true,
		},
		lawyer: agentHandle{
			provider: &sequentialFakeProvider{
				callerName:    "lawyer",
				toolResponses: lawyerDirectResponseToolResponses("食尸鬼（Ghoul）：COC7规则书已收录。"),
			},
			config:  &models.AgentConfig{Role: models.AgentRoleLawyer, IsActive: true},
			enabled: true,
		},
	}

	result, err := extractAnchorFromDocument(context.Background(), room, "故事文档正文")
	if err != nil {
		t.Fatalf("extractAnchorFromDocument failed: %v", err)
	}
	if result.MythosAnchor != "食尸鬼（Ghoul）" {
		t.Errorf("MythosAnchor = %q, want 食尸鬼（Ghoul）", result.MythosAnchor)
	}
	secondRound := architectFake.recordedMessages[1]
	rejected := false
	for _, m := range secondRound {
		if m.Role == "tool" && strings.Contains(m.Content, "SYSTEM REJECT") && strings.Contains(m.Content, "confirm_anchor") {
			rejected = true
		}
	}
	if !rejected {
		t.Error("未登记锚点就提交时，应把 SYSTEM REJECT 回传给 architect")
	}
}

// TestExtractAnchorFromDocument_Failure 验证 architect 始终不提交 submit_extraction
// （工具循环轮数耗尽）时，extractAnchorFromDocument 返回错误。
func TestExtractAnchorFromDocument_Failure(t *testing.T) {
	architectFake := &sequentialFakeProvider{callerName: "architect"} // 无预设响应，ChatWithTools恒返回空tool_calls，触发连续空轮快速失败，永不submit_extraction
	room := &scripterRoom{
		sessionID: "test-session-extract-2",
		architect: agentHandle{
			provider: architectFake,
			config:   &models.AgentConfig{Role: models.AgentRoleArchitect, IsActive: true},
			enabled:  true,
		},
	}

	if _, err := extractAnchorFromDocument(context.Background(), room, "故事文档正文"); err == nil {
		t.Fatal("architect 始终未提交 submit_extraction 时应返回错误")
	}
}

// TestCompileAndFinalize_Success 验证跳过 Story Architect 后，compileAndFinalize 能正常
// 完成 compile→normalize，且不再输出"阶段 N/6"式编号（该编号仅属于完整 AI 生成路径）。
func TestCompileAndFinalize_Success(t *testing.T) {
	fake := &sequentialFakeProvider{
		callerName:    "compiler",
		jsonResponses: []string{oneshotExample},
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

// TestCompileAndFinalize_RewardConceptEmptyFallsBack 验证 compiler 提交的 reward_concept
// 为空时：机制层先拒绝一次要求重新提交（见 scripter_compile.go dispatch 的 rewardRetried
// 逻辑），重提后仍为空则放行编译；触发点改用 mythos_anchor 合成兜底概念继续触发
// reward_agent（见 scripter.go 的 fallbackRewardConcept 调用），而不是像旧行为那样
// 静默跳过整个奖励阶段。
// 本测试的 room 未配置 architect/lawyer provider，所以 reward_agent 执行本身必然因
// "no LLM provider available" 而失败——这与"因为概念为空所以跳过"是两种不同的原因，
// 这里只验证后者不再发生（reward_agent 仍会被尝试触发）。
func TestCompileAndFinalize_RewardConceptEmptyFallsBack(t *testing.T) {
	noReward := oneshotResultExample
	noReward.RewardConcept = ""
	fake := &sequentialFakeProvider{
		callerName:    "compiler",
		jsonResponses: []string{marshalExample(noReward), marshalExample(noReward)},
	}
	room := &scripterRoom{
		sessionID: "test-session-upload-2",
		compiler: agentHandle{
			provider: fake,
			config:   &models.AgentConfig{Role: models.AgentRoleCompiler, IsActive: true},
			enabled:  true,
		},
	}
	var sawRewardStart bool
	room.progressFn = func(stage, status, detail string) {
		if stage == "reward_agent" && status == "start" {
			sawRewardStart = true
		}
	}
	story := compileTestStory()

	draft, _, err := room.compileAndFinalize(context.Background(), story, ScripterConstraints{})
	if err != nil {
		t.Fatalf("compileAndFinalize failed: %v", err)
	}
	if !sawRewardStart {
		t.Error("reward_concept 重试后仍为空时，应改用 mythos_anchor 兜底概念继续触发 reward_agent，而不是静默跳过")
	}
	// room 未配置 reward_agent 可用的 architect/lawyer provider，reward_agent 必然因无
	// provider 而失败（非致命，跳过），这与"概念为空"是两回事。
	if draft.Content.Reward != nil {
		t.Errorf("room 未配置 reward_agent 可用 provider 时不应生成奖励, got %+v", draft.Content.Reward)
	}
	// NOTE: 取标题环节在 architect 缺席时同样 fallback 到 compiler provider，会追加若干次
	// 调用；这里只数 compile 阶段的调用（cache key 不带 :title 后缀）。
	if got := len(fake.recordedKeys) - countTitleCalls(fake); got != 2 {
		t.Errorf("reward_concept 为空应被拒绝一次并重提, compile 阶段调用次数 got %d, want 2", got)
	}
}

// TestCompileAndFinalize_RewardTriggeredByCompiler 验证 reward_agent 的触发信号来自
// compiler 提交的 reward_concept（从故事文档通读提炼），而不是 story 阶段的字段——
// 即使 architect provider 不可用导致 reward_agent 本身执行失败，也应仍尝试触发
// （非致命错误，不影响 compileAndFinalize 整体成功）。
func TestCompileAndFinalize_RewardTriggeredByCompiler(t *testing.T) {
	fake := &sequentialFakeProvider{
		callerName:    "compiler",
		jsonResponses: []string{oneshotExample},
	}
	room := &scripterRoom{
		sessionID: "test-session-upload-3",
		compiler: agentHandle{
			provider: fake,
			config:   &models.AgentConfig{Role: models.AgentRoleCompiler, IsActive: true},
			enabled:  true,
		},
	}
	var sawRewardStart bool
	room.progressFn = func(stage, status, detail string) {
		if stage == "reward_agent" && status == "start" {
			sawRewardStart = true
		}
	}
	story := compileTestStory()

	if _, _, err := room.compileAndFinalize(context.Background(), story, ScripterConstraints{}); err != nil {
		t.Fatalf("compileAndFinalize failed: %v", err)
	}
	if !sawRewardStart {
		t.Error("oneshotExample的reward_concept非空，应触发reward_agent（即使无architect provider而失败，也应先发出reward_agent:start进度）")
	}
}

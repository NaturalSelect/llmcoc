// NOTE: scripter_title_test.go 验证取标题环节：确定性判据 validateScenarioTitle、
// runTitleAgent 的重写重试与"判据用尽也不阻塞管线"的兜底纪律，以及 compileAndFinalize
// 的接入点（req.Name 非空时整段跳过）。取标题走纯文本 JsonChat，通过 sequentialFakeProvider
// 的 jsonResponses 序列驱动。禁止真实网络/真实LLM。
package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/llmcoc/server/internal/models"
)

// titleJSON 把标题包成取标题环节约定的返回格式。
func titleJSON(title string) string {
	return `{"title": "` + title + `"}`
}

// countTitleCalls 统计 fake provider 上属于取标题环节的调用次数（cache key 带 :title 后缀）。
func countTitleCalls(p *sequentialFakeProvider) int {
	n := 0
	for _, key := range p.recordedKeys {
		if strings.HasSuffix(key, ":title") {
			n++
		}
	}
	return n
}

func TestValidateScenarioTitle(t *testing.T) {
	blacklist := []string{"马什家的账本", "《旧钟楼》"}
	cases := []struct {
		name   string
		title  string
		wantOK bool
	}{
		// 用户报告的劣化形态：主谓宾齐全的事件陈述句
		{"事件陈述句", "布里格斯农场赔了三栏羊", false},
		{"滥用氛围词", "深渊的低语", false},
		{"公文腔", "码头失踪事件", false},
		{"空标题", "", false},
		{"过短", "井", false},
		{"过长", "一个非常长的超过十二个汉字的剧本标题", false},
		{"含句读标点", "马什家的账本，一九二三", false},
		{"与黑名单重名", "马什家的账本", false},
		{"与黑名单重名（黑名单侧带书名号）", "旧钟楼", false},
		{"专名加具体物件", "布里格斯农场的羊圈", true},
		{"单独器物专名", "灰岩镇的铜钥匙", true},
		{"时节加具体事物", "霜降前的渡船", true},
		{"最短合法长度", "旧钟楼上", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			reason := validateScenarioTitle(tc.title, blacklist)
			if tc.wantOK && reason != "" {
				t.Errorf("validateScenarioTitle(%q) = %q, 期望合格", tc.title, reason)
			}
			if !tc.wantOK && reason == "" {
				t.Errorf("validateScenarioTitle(%q) 判为合格, 期望不合格", tc.title)
			}
		})
	}
}

// TestScenarioTitleRulesContainsKey 验证共享规则常量声明了核心约束：形式是名词性短语、
// 带长度区间、且给出了用户报告的那类流水句反例。
func TestScenarioTitleRulesContainsKey(t *testing.T) {
	for _, keyword := range []string{"名词性短语", "不是一句话", "4到12个汉字", "布里格斯农场赔了三栏羊"} {
		if !strings.Contains(scenarioTitleRules, keyword) {
			t.Errorf("scenarioTitleRules 缺少关键约束语句 %q", keyword)
		}
	}
	if !strings.Contains(titleAgentSystemPrompt, scenarioTitleRules) {
		t.Error("titleAgentSystemPrompt 应内嵌 scenarioTitleRules 共享常量")
	}
}

// newTitleTestRoom 构造只配置 architect provider 的最小 room，供取标题环节测试使用。
func newTitleTestRoom(sessionID string, fake *sequentialFakeProvider, titleSamples []string) *scripterRoom {
	return &scripterRoom{
		sessionID:    sessionID,
		titleSamples: titleSamples,
		architect: agentHandle{
			provider: fake,
			config:   &models.AgentConfig{Role: models.AgentRoleArchitect, IsActive: true},
			enabled:  true,
		},
	}
}

// TestRunTitleAgent_RejectedThenRetried 验证首个候选不合格时会带着原因发回重写，
// 重提后的合格标题被采纳。
func TestRunTitleAgent_RejectedThenRetried(t *testing.T) {
	fake := &sequentialFakeProvider{
		callerName: "architect",
		jsonResponses: []string{
			titleJSON("布里格斯农场赔了三栏羊"),
			titleJSON("布里格斯农场的羊圈"),
		},
	}
	room := newTitleTestRoom("test-session-title-retry", fake, nil)
	draft := oneshotResultExample.toScenarioDraft()

	title, err := runTitleAgent(context.Background(), room, &draft)
	if err != nil {
		t.Fatalf("runTitleAgent failed: %v", err)
	}
	if title != "布里格斯农场的羊圈" {
		t.Errorf("title = %q, want 重提后的合格标题", title)
	}
	if got := countTitleCalls(fake); got != 2 {
		t.Errorf("首个候选不合格应触发一次重写, 取标题调用次数 got %d, want 2", got)
	}
	// 重写指令须把具体原因回传给模型，而不是只说"不合格"
	lastMsgs := fake.recordedMessages[len(fake.recordedMessages)-1]
	lastContent := lastMsgs[len(lastMsgs)-1].Content
	if !strings.Contains(lastContent, "SYSTEM REJECT") || !strings.Contains(lastContent, "陈述句") {
		t.Errorf("重写指令未回传具体原因, got %q", lastContent)
	}
}

// TestRunTitleAgent_NeverBlocksPipeline 验证连续不合格时不返回 error，而是采用最后一个
// 非空候选——标题不合格是质量问题，不得中断已跑了几十分钟的生成管线。
func TestRunTitleAgent_NeverBlocksPipeline(t *testing.T) {
	fake := &sequentialFakeProvider{
		callerName: "architect",
		jsonResponses: []string{
			titleJSON("深渊的低语"),
			titleJSON("阴影中的凝视"),
			titleJSON("码头失踪事件"),
		},
	}
	room := newTitleTestRoom("test-session-title-exhausted", fake, nil)
	draft := oneshotResultExample.toScenarioDraft()

	title, err := runTitleAgent(context.Background(), room, &draft)
	if err != nil {
		t.Fatalf("重试用尽不应返回错误, got %v", err)
	}
	if title != "码头失踪事件" {
		t.Errorf("title = %q, want 最后一个非空候选", title)
	}
	if got := countTitleCalls(fake); got != maxTitleRetries {
		t.Errorf("取标题调用次数 got %d, want %d", got, maxTitleRetries)
	}
}

// TestRunTitleAgent_NoProviderAvailable 验证无可用 provider 时返回错误，由调用方保留编译标题。
func TestRunTitleAgent_NoProviderAvailable(t *testing.T) {
	room := &scripterRoom{sessionID: "test-session-title-noprovider"}
	draft := oneshotResultExample.toScenarioDraft()
	if _, err := runTitleAgent(context.Background(), room, &draft); err == nil {
		t.Fatal("architect/compiler 均不可用时应返回错误")
	}
}

// TestRunTitleAgent_BlacklistInjected 验证近期已有标题作为黑名单注入提示词，
// 且与黑名单重名的候选会被判据拦下重写。
func TestRunTitleAgent_BlacklistInjected(t *testing.T) {
	fake := &sequentialFakeProvider{
		callerName: "architect",
		jsonResponses: []string{
			titleJSON("马什家的账本"),
			titleJSON("布里格斯农场的羊圈"),
		},
	}
	room := newTitleTestRoom("test-session-title-blacklist", fake, []string{"马什家的账本"})
	draft := oneshotResultExample.toScenarioDraft()

	title, err := runTitleAgent(context.Background(), room, &draft)
	if err != nil {
		t.Fatalf("runTitleAgent failed: %v", err)
	}
	if title != "布里格斯农场的羊圈" {
		t.Errorf("title = %q, 与黑名单重名的候选应被拦下", title)
	}
	firstUserMsg := fake.recordedMessages[0][1].Content
	if !strings.Contains(firstUserMsg, "<scenario_title_blacklist>") || !strings.Contains(firstUserMsg, "马什家的账本") {
		t.Errorf("取标题提示词未注入标题黑名单, got %q", firstUserMsg)
	}
}

// TestCompileAndFinalize_TitleStageSkippedWhenNameSpecified 验证用户已指定标题时整段跳过
// 取标题环节——后续 applyGuardrails 会强制覆盖，调用纯属浪费。
func TestCompileAndFinalize_TitleStageSkippedWhenNameSpecified(t *testing.T) {
	fake := &sequentialFakeProvider{
		callerName:    "compiler",
		jsonResponses: []string{oneshotExample, titleJSON("布里格斯农场的羊圈")},
	}
	room := &scripterRoom{
		sessionID: "test-session-title-skip",
		req:       ScenarioCreationRequest{Name: "管理员指定标题"},
		compiler: agentHandle{
			provider: fake,
			config:   &models.AgentConfig{Role: models.AgentRoleCompiler, IsActive: true},
			enabled:  true,
		},
	}

	draft, _, err := room.compileAndFinalize(context.Background(), compileTestStory(), ScripterConstraints{})
	if err != nil {
		t.Fatalf("compileAndFinalize failed: %v", err)
	}
	if got := countTitleCalls(fake); got != 0 {
		t.Errorf("req.Name 非空时取标题环节调用次数 got %d, want 0", got)
	}
	if draft.Name != "管理员指定标题" {
		t.Errorf("draft.Name = %q, want 用户指定标题", draft.Name)
	}
}

// TestCompileAndFinalize_TitleStageOverridesCompiledName 验证未指定标题时取标题环节生效，
// 其产出覆盖 compiler 照抄来的标题。
func TestCompileAndFinalize_TitleStageOverridesCompiledName(t *testing.T) {
	fake := &sequentialFakeProvider{
		callerName:    "compiler",
		jsonResponses: []string{oneshotExample, titleJSON("布里格斯农场的羊圈")},
	}
	room := &scripterRoom{
		sessionID: "test-session-title-override",
		compiler: agentHandle{
			provider: fake,
			config:   &models.AgentConfig{Role: models.AgentRoleCompiler, IsActive: true},
			enabled:  true,
		},
	}

	draft, _, err := room.compileAndFinalize(context.Background(), compileTestStory(), ScripterConstraints{})
	if err != nil {
		t.Fatalf("compileAndFinalize failed: %v", err)
	}
	if got := countTitleCalls(fake); got != 1 {
		t.Fatalf("取标题环节调用次数 got %d, want 1", got)
	}
	if draft.Name != "布里格斯农场的羊圈" {
		t.Errorf("draft.Name = %q, want 取标题环节的产出", draft.Name)
	}
}

// TestNormalizeOneshotDraft_StripsTitlePunctuation 验证归一化作用到产出标题上：
// 模型习惯给标题加书名号，此前这层清洗只用于黑名单样本，书名号会原样入库。
func TestNormalizeOneshotDraft_StripsTitlePunctuation(t *testing.T) {
	draft := ScenarioDraft{Name: "《深井》"}
	normalizeOneshotDraft(&draft, ScenarioCreationRequest{}, "tester", ScripterConstraints{}, nil, "test-session-normalize-title")
	if draft.Name != "深井" {
		t.Errorf("draft.Name = %q, want 深井（书名号应被剥离）", draft.Name)
	}

	empty := ScenarioDraft{Name: "  "}
	normalizeOneshotDraft(&empty, ScenarioCreationRequest{}, "tester", ScripterConstraints{}, nil, "test-session-normalize-empty")
	if empty.Name != "未命名剧本" {
		t.Errorf("空标题 draft.Name = %q, want 未命名剧本", empty.Name)
	}
}

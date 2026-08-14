package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/llmcoc/server/internal/models"
	"github.com/llmcoc/server/internal/services/llm"
	"github.com/llmcoc/server/internal/services/rulebook"
)

// newLawyerTestHandle 构造一个仅用于 runLawyer 测试的 agentHandle。
func newLawyerTestHandle(prov llm.Provider) agentHandle {
	return agentHandle{
		provider: prov,
		config:   &models.AgentConfig{Role: models.AgentRoleLawyer, IsActive: true},
		enabled:  true,
	}
}

func responseArgsJSON(t *testing.T, ruling string) string {
	t.Helper()
	b, err := json.Marshal(map[string]string{"ruling": ruling})
	if err != nil {
		t.Fatalf("marshal response args: %v", err)
	}
	return string(b)
}

func TestAppendGrepResultsKeepsKeywordAsOneRegexp(t *testing.T) {
	var gotKeyword string
	var sb strings.Builder
	ok := appendGrepResults(&sb, "grep", "理智 .* 检定", "规则书", func(keyword string) []rulebook.GrepResult {
		gotKeyword = keyword
		return []rulebook.GrepResult{{LineNum: 42, Text: "理智 损失 检定"}}
	})

	if !ok {
		t.Fatal("appendGrepResults should accept non-empty keyword")
	}
	if gotKeyword != "理智 .* 检定" {
		t.Fatalf("keyword should be passed as one regexp, got %q", gotKeyword)
	}
	if text := sb.String(); !strings.Contains(text, "【grep:理智 .* 检定】") || strings.Contains(text, "【grep:理智】") {
		t.Fatalf("unexpected grep output: %q", text)
	}
}

// TestBuildLawyerPromptDefaultRulesContainBannedSpells verifies that the five
// banned spells appear in the balance section when using default rules, and
// that they no longer appear hardcoded in lawyerSystemPromptBase.
func TestBuildLawyerPromptDefaultRulesContainBannedSpells(t *testing.T) {
	section := BuildLawyerPrompt(models.DefaultBalanceRules)

	bannedSpells := []string{"精神转移术", "精神交换术", "内心灵光唤醒术", "完善术", "伊格的尖牙"}
	for _, spell := range bannedSpells {
		if !strings.Contains(section, spell) {
			t.Errorf("balance section should contain banned spell %q", spell)
		}
	}

	// XML section tags must be present.
	if !strings.Contains(section, "<kp_balance_rules>") {
		t.Error("balance section should contain opening tag <kp_balance_rules>")
	}
	if !strings.Contains(section, "</kp_balance_rules>") {
		t.Error("balance section should contain closing tag </kp_balance_rules>")
	}

	// The hardcoded ban must have been removed from the base system prompt.
	for _, spell := range bannedSpells {
		if strings.Contains(lawyerSystemPromptBase, spell) {
			t.Errorf("hardcoded ban should be removed from lawyerSystemPromptBase; found %q", spell)
		}
	}
}

// TestBuildLawyerPromptCustomRulesReplacesDefault verifies that a custom rule
// replaces (not appends to) the default banned-spell list.
func TestBuildLawyerPromptCustomRulesReplacesDefault(t *testing.T) {
	custom := "自定义平衡规则：调查员不得使用大克苏鲁信仰。"
	section := BuildLawyerPrompt(custom)

	if !strings.Contains(section, custom) {
		t.Error("balance section should contain custom balance rule")
	}
	if strings.Contains(section, "精神转移术") {
		t.Error("custom rules should replace default; default banned spells should not appear")
	}
	if !strings.Contains(section, "<kp_balance_rules>") {
		t.Error("balance section opening tag should be present")
	}
}

// TestBuildLawyerPromptEmptyRulesReturnsEmpty verifies that an empty
// balanceRules value returns an empty string, producing no balance section.
func TestBuildLawyerPromptEmptyRulesReturnsEmpty(t *testing.T) {
	section := BuildLawyerPrompt("")

	if section != "" {
		t.Errorf("empty rules should return empty string, got %q", section)
	}
}

// TestLawyerSystemPromptAlwaysHasJSONConstraint verifies that the Lawyer system
// prompt (base + tail) always mandates giving the ruling only via the native
// "response" tool call and forces search_cache as the first tool call.
func TestLawyerSystemPromptAlwaysHasJSONConstraint(t *testing.T) {
	fullPrompt := lawyerSystemPromptBase + lawyerSystemPromptTail

	if !strings.Contains(fullPrompt, "只能通过工具调用") {
		t.Error("Lawyer system prompt must mandate giving the ruling only via tool calls")
	}
	if !strings.Contains(fullPrompt, "search_cache") {
		t.Error("Lawyer system prompt must mention search_cache as the mandatory first tool call")
	}
}

// TestBuildLawyerPromptOpenTagBeforeCloseTag verifies XML open tag appears before close tag.
func TestBuildLawyerPromptOpenTagBeforeCloseTag(t *testing.T) {
	section := BuildLawyerPrompt(models.DefaultBalanceRules)

	openIdx := strings.Index(section, "<kp_balance_rules>")
	closeIdx := strings.Index(section, "</kp_balance_rules>")
	if openIdx < 0 || closeIdx < 0 {
		t.Fatal("both opening and closing tags must be present")
	}
	if closeIdx < openIdx {
		t.Error("closing tag must appear after opening tag")
	}
}

// TestRunLawyerFirstRoundOnlySearchCache 验证第1轮的工具集被限制为只有
// search_cache（recordedTools[0] 只含1个工具），且模型在第1轮尝试调用
// response 会被驱动器当作未知工具拒绝，循环继续到第2轮改用 search_cache 成功。
func TestRunLawyerFirstRoundOnlySearchCache(t *testing.T) {
	initTranslatorTestDB(t)

	prov := &sequentialFakeProvider{
		toolResponses: []llm.ToolChatResult{
			// 第1轮：违规尝试直接 response，应被拒绝（此阶段 response 不在合法工具集内）。
			{ToolCalls: []llm.ToolCall{fakeToolCall("c1", toolNameLawyerResponse, responseArgsJSON(t, "违规抢跑"))}},
			// 第2轮：改为合法的 search_cache。
			{ToolCalls: []llm.ToolCall{fakeToolCall("c2", toolNameSearchCache, `{"keyword":"#测试"}`)}},
			// 第3轮：给出裁定。
			{ToolCalls: []llm.ToolCall{fakeToolCall("c3", toolNameLawyerResponse, responseArgsJSON(t, "最终裁定"))}},
		},
	}

	results := runLawyer(context.Background(), newLawyerTestHandle(prov), "某规则问题")

	if len(results) != 1 || results[0].RuleText != "最终裁定" {
		t.Fatalf("expected final ruling, got %+v", results)
	}
	if len(prov.recordedTools) < 1 || len(prov.recordedTools[0]) != 1 || prov.recordedTools[0][0].Name != toolNameSearchCache {
		t.Fatalf("round 1 tool set should contain only search_cache, got %+v", prov.recordedTools)
	}
	if len(prov.recordedMessages) < 2 {
		t.Fatalf("expected at least 2 rounds, got %d", len(prov.recordedMessages))
	}
	// 第2轮发给模型的最后一条消息应是对第1轮违规调用的拒绝回执。
	lastMsgRound2 := prov.recordedMessages[1][len(prov.recordedMessages[1])-1]
	if lastMsgRound2.Role != "tool" || !strings.Contains(lastMsgRound2.Content, "未知工具") {
		t.Fatalf("round 1 illegal response call should be rejected as unknown tool, got %+v", lastMsgRound2)
	}
}

// TestRunLawyerMultipleRetrievalToolsSameRound 验证同一轮内可以同时调用多个
// 检索工具（grep + grep_spell），驱动器不会整批拒绝，且两个结果都会回传。
func TestRunLawyerMultipleRetrievalToolsSameRound(t *testing.T) {
	initTranslatorTestDB(t)

	prov := &sequentialFakeProvider{
		toolResponses: []llm.ToolChatResult{
			{ToolCalls: []llm.ToolCall{fakeToolCall("c1", toolNameSearchCache, `{"keyword":"#测试"}`)}},
			{ToolCalls: []llm.ToolCall{
				fakeToolCall("c2", toolNameGrep, `{"keyword":"手枪"}`),
				fakeToolCall("c3", toolNameGrepSpell, `{"keyword":"火球术"}`),
			}},
			{ToolCalls: []llm.ToolCall{fakeToolCall("c4", toolNameLawyerResponse, responseArgsJSON(t, "综合裁定"))}},
		},
	}

	results := runLawyer(context.Background(), newLawyerTestHandle(prov), "某规则问题")

	if len(results) != 1 || results[0].RuleText != "综合裁定" {
		t.Fatalf("expected final ruling, got %+v", results)
	}
	// 第3轮发给模型的消息中应同时含 grep 和 grep_spell 的回执（tool_call_id c2/c3）。
	lastRoundMsgs := prov.recordedMessages[2]
	var gotGrep, gotGrepSpell bool
	for _, m := range lastRoundMsgs {
		if m.Role == "tool" && m.ToolCallID == "c2" {
			gotGrep = true
		}
		if m.Role == "tool" && m.ToolCallID == "c3" {
			gotGrepSpell = true
		}
	}
	if !gotGrep || !gotGrepSpell {
		t.Fatalf("both grep and grep_spell tool results should be fed back, msgs=%+v", lastRoundMsgs)
	}
}

// TestRunLawyerResponseWithSaveCacheSameRound 验证 response 与 save_cache
// 可以同轮调用且不被 batchPolicy 拒绝，且 save_cache 确实写入了缓存。
func TestRunLawyerResponseWithSaveCacheSameRound(t *testing.T) {
	initTranslatorTestDB(t)

	const cacheKey = "#测试专用标签-response-savecache"
	const ruling = "response与save_cache同轮的裁定"
	saveCacheArgs, err := json.Marshal(map[string]string{"cache_key": cacheKey, "ruling": ruling})
	if err != nil {
		t.Fatalf("marshal save_cache args: %v", err)
	}

	prov := &sequentialFakeProvider{
		toolResponses: []llm.ToolChatResult{
			{ToolCalls: []llm.ToolCall{fakeToolCall("c1", toolNameSearchCache, `{"keyword":"#测试"}`)}},
			{ToolCalls: []llm.ToolCall{
				fakeToolCall("c2", toolNameLawyerResponse, responseArgsJSON(t, ruling)),
				fakeToolCall("c3", toolNameSaveCache, string(saveCacheArgs)),
			}},
		},
	}

	results := runLawyer(context.Background(), newLawyerTestHandle(prov), "某规则问题")

	if len(results) != 1 || results[0].RuleText != ruling {
		t.Fatalf("expected final ruling, got %+v", results)
	}
	matches := lawyerCache.Search(cacheKey, 1)
	if len(matches) == 0 || matches[0].Ruling != ruling {
		t.Fatalf("save_cache should have written the ruling to lawyerCache, got %+v", matches)
	}
}

// TestRunLawyerOutputToolMixedWithRetrievalRejected 验证 response 与检索类
// 工具（grep）混批会被 lawyerBatchPolicy 整批拒绝，循环继续而不终止。
func TestRunLawyerOutputToolMixedWithRetrievalRejected(t *testing.T) {
	initTranslatorTestDB(t)

	prov := &sequentialFakeProvider{
		toolResponses: []llm.ToolChatResult{
			{ToolCalls: []llm.ToolCall{fakeToolCall("c1", toolNameSearchCache, `{"keyword":"#测试"}`)}},
			// 违规：response 与 grep 混批。
			{ToolCalls: []llm.ToolCall{
				fakeToolCall("c2", toolNameLawyerResponse, responseArgsJSON(t, "过早的裁定")),
				fakeToolCall("c3", toolNameGrep, `{"keyword":"手枪"}`),
			}},
			// 修正：只调用 response。
			{ToolCalls: []llm.ToolCall{fakeToolCall("c4", toolNameLawyerResponse, responseArgsJSON(t, "修正后的裁定"))}},
		},
	}

	results := runLawyer(context.Background(), newLawyerTestHandle(prov), "某规则问题")

	if len(results) != 1 || results[0].RuleText != "修正后的裁定" {
		t.Fatalf("expected corrected ruling after batch rejection, got %+v", results)
	}
	rejectedRoundMsgs := prov.recordedMessages[2]
	found := false
	for _, m := range rejectedRoundMsgs {
		if m.Role == "tool" && strings.Contains(m.Content, "SYSTEM REJECT") && strings.Contains(m.Content, "不能与检索类工具") {
			found = true
		}
	}
	if !found {
		t.Fatalf("mixed response+grep batch should be rejected with SYSTEM REJECT, msgs=%+v", rejectedRoundMsgs)
	}
}

// TestRunLawyerInvalidArgsAndEmptyRulingRejected 验证 response 的非法JSON参数
// 和空 ruling 都会被拒绝并要求重试，而不是被当作成功终止。
func TestRunLawyerInvalidArgsAndEmptyRulingRejected(t *testing.T) {
	initTranslatorTestDB(t)

	prov := &sequentialFakeProvider{
		toolResponses: []llm.ToolChatResult{
			{ToolCalls: []llm.ToolCall{fakeToolCall("c1", toolNameSearchCache, `{"keyword":"#测试"}`)}},
			{ToolCalls: []llm.ToolCall{fakeToolCall("c2", toolNameLawyerResponse, `not-json`)}},
			{ToolCalls: []llm.ToolCall{fakeToolCall("c3", toolNameLawyerResponse, responseArgsJSON(t, ""))}},
			{ToolCalls: []llm.ToolCall{fakeToolCall("c4", toolNameLawyerResponse, responseArgsJSON(t, "终于给出的裁定"))}},
		},
	}

	results := runLawyer(context.Background(), newLawyerTestHandle(prov), "某规则问题")

	if len(results) != 1 || results[0].RuleText != "终于给出的裁定" {
		t.Fatalf("expected final ruling after invalid attempts were rejected, got %+v", results)
	}
}

// TestRunLawyerSearchCacheShowsTagRelevance 验证 search_cache 回执把命中标签数/
// 查询标签数回显给模型（相关度信号），且用"标签"而非"问题"标注 cache key。
func TestRunLawyerSearchCacheShowsTagRelevance(t *testing.T) {
	initTranslatorTestDB(t)

	lawyerCache.Set("#相关度专用标签 #伤害 #武器", "相关度用例的裁定")

	prov := &sequentialFakeProvider{
		toolResponses: []llm.ToolChatResult{
			{ToolCalls: []llm.ToolCall{fakeToolCall("c1", toolNameSearchCache, `{"keyword":"#相关度专用标签 #伤害 #绝不存在xyz"}`)}},
			{ToolCalls: []llm.ToolCall{fakeToolCall("c2", toolNameLawyerResponse, responseArgsJSON(t, "裁定"))}},
		},
	}

	runLawyer(context.Background(), newLawyerTestHandle(prov), "某规则问题")

	last := prov.recordedMessages[1][len(prov.recordedMessages[1])-1]
	if !strings.Contains(last.Content, "匹配 2/3 标签") {
		t.Errorf("search_cache result should report matched/total tags, got %q", last.Content)
	}
	if !strings.Contains(last.Content, "标签：#相关度专用标签") {
		t.Errorf("search_cache result should label the cache key as 标签, got %q", last.Content)
	}
}

// TestRunLawyerConsecutiveEmptyRoundsFastFail 验证连续多轮不返回任何工具调用
// 时驱动器快速失败（runLawyer 对应返回 nil），而不是耗尽全部 maxRounds。
func TestRunLawyerConsecutiveEmptyRoundsFastFail(t *testing.T) {
	initTranslatorTestDB(t)

	prov := &sequentialFakeProvider{} // 无预设响应，ChatWithTools恒返回空tool_calls

	results := runLawyer(context.Background(), newLawyerTestHandle(prov), "某规则问题")

	if results != nil {
		t.Fatalf("expected nil result on fast-fail, got %+v", results)
	}
	if len(prov.recordedKeys) > maxConsecutiveEmptyRounds {
		t.Fatalf("should fast-fail within %d rounds, got %d calls", maxConsecutiveEmptyRounds, len(prov.recordedKeys))
	}
}

// TestRunLawyerCacheStatsRecorded 验证逐轮缓存命中统计：afterRound 在每轮
// 工具分发完成后都会记录一次（不是每次问答只记一次），这与迁移前"每次循环
// 迭代末尾无条件记录一次"的逐轮记录时机保持一致。
// full hit 用例：2轮（search_cache命中 → response），每轮都满足
// "!searchedRulebook && cacheSearchHadResults"，故 full=2。
// miss 用例：3轮（search_cache未命中 → grep → response），第1轮两个条件都不满足
// 不计数，第2/3轮都满足"searchedRulebook && !cacheSearchHadResults"，故 miss=2。
func TestRunLawyerCacheStatsRecorded(t *testing.T) {
	initTranslatorTestDB(t)

	t.Run("full hit", func(t *testing.T) {
		const key = "#缓存命中专用标签"
		const ruling = "命中缓存的裁定"
		lawyerCache.Set(key, ruling)
		lawyerCache.ResetStats()

		prov := &sequentialFakeProvider{
			toolResponses: []llm.ToolChatResult{
				{ToolCalls: []llm.ToolCall{fakeToolCall("c1", toolNameSearchCache, `{"keyword":"`+key+`"}`)}},
				{ToolCalls: []llm.ToolCall{fakeToolCall("c2", toolNameLawyerResponse, responseArgsJSON(t, ruling))}},
			},
		}
		runLawyer(context.Background(), newLawyerTestHandle(prov), "命中缓存的问题")

		full, partial, miss := lawyerCache.HitStats()
		if full != 2 || partial != 0 || miss != 0 {
			t.Fatalf("expected full=2 partial=0 miss=0, got full=%d partial=%d miss=%d", full, partial, miss)
		}
	})

	t.Run("miss requires rulebook search", func(t *testing.T) {
		lawyerCache.ResetStats()

		prov := &sequentialFakeProvider{
			toolResponses: []llm.ToolChatResult{
				{ToolCalls: []llm.ToolCall{fakeToolCall("c1", toolNameSearchCache, `{"keyword":"#绝不存在的标签xyz"}`)}},
				{ToolCalls: []llm.ToolCall{fakeToolCall("c2", toolNameGrep, `{"keyword":"手枪"}`)}},
				{ToolCalls: []llm.ToolCall{fakeToolCall("c3", toolNameLawyerResponse, responseArgsJSON(t, "查规则书得出的裁定"))}},
			},
		}
		runLawyer(context.Background(), newLawyerTestHandle(prov), "未命中缓存的问题")

		full, partial, miss := lawyerCache.HitStats()
		if full != 0 || partial != 0 || miss != 2 {
			t.Fatalf("expected full=0 partial=0 miss=2, got full=%d partial=%d miss=%d", full, partial, miss)
		}
	})
}

// TestRunLawyerReadLinesInvalidRangeRejected 验证 read_lines 的 start/end
// 为0（非法）时被拒绝，而不是被当作合法调用静默放行。
func TestRunLawyerReadLinesInvalidRangeRejected(t *testing.T) {
	initTranslatorTestDB(t)

	prov := &sequentialFakeProvider{
		toolResponses: []llm.ToolChatResult{
			{ToolCalls: []llm.ToolCall{fakeToolCall("c1", toolNameSearchCache, `{"keyword":"#测试"}`)}},
			{ToolCalls: []llm.ToolCall{fakeToolCall("c2", toolNameReadLines, `{"start":0,"end":0}`)}},
			{ToolCalls: []llm.ToolCall{fakeToolCall("c3", toolNameLawyerResponse, responseArgsJSON(t, "修正后的裁定"))}},
		},
	}

	results := runLawyer(context.Background(), newLawyerTestHandle(prov), "某规则问题")

	if len(results) != 1 || results[0].RuleText != "修正后的裁定" {
		t.Fatalf("expected final ruling, got %+v", results)
	}
	rejectedRoundMsgs := prov.recordedMessages[2]
	found := false
	for _, m := range rejectedRoundMsgs {
		if m.Role == "tool" && m.ToolCallID == "c2" && strings.Contains(m.Content, "SYSTEM REJECT") {
			found = true
		}
	}
	if !found {
		t.Fatalf("read_lines with start=0/end=0 should be rejected, msgs=%+v", rejectedRoundMsgs)
	}
}

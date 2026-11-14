package agent

import (
	"fmt"
	"strings"
	"testing"

	"github.com/llmcoc/server/internal/models"
	"github.com/llmcoc/server/internal/services/llm"
)

func storyTestActx(t *testing.T, scenarioID uint, docs ...string) ActionContext {
	t.Helper()
	initAgentTestDB(t)
	if err := models.DB.AutoMigrate(&models.Scenario{}, &models.ScenarioGenerationLog{}); err != nil {
		t.Fatalf("auto-migrate: %v", err)
	}
	for _, doc := range docs {
		if err := models.DB.Create(&models.ScenarioGenerationLog{ScenarioID: scenarioID, ScenarioName: "测试", LogText: "log", StoryDocument: doc}).Error; err != nil {
			t.Fatalf("create log: %v", err)
		}
	}
	actx := eventTestActx(eventTestPlayers())
	actx.GCtx.Session.ScenarioID = scenarioID
	return actx
}

func storyDoc(n int) string {
	lines := make([]string, n)
	for i := range lines {
		lines[i] = fmt.Sprintf("第%d行正文", i+1)
	}
	return strings.Join(lines, "\n")
}

func TestStoryToolsRegistered(t *testing.T) {
	found := map[string]bool{}
	for _, tool := range directorTools(false) {
		found[tool.def.Name] = true
	}
	for _, name := range []ToolCallType{ToolGrepStory, ToolReadStory} {
		if !found[string(name)] {
			t.Errorf("directorTools should expose %s", name)
		}
		if _, ok := actionRegistry[name]; !ok {
			t.Errorf("%s should be registered in actionRegistry", name)
		}
		if !noSideEffectActions[name] || !guardCacheable[name] || !guardSigWithResult[name] {
			t.Errorf("%s should be a cacheable no-side-effect read tool", name)
		}
	}
}

func TestStoryToolsCannotShareBatchWithNarrate(t *testing.T) {
	policy := noEncounterBatchPolicy()
	for _, name := range []string{"grep_story", "read_story"} {
		reject := policy([]llm.ToolCall{
			policyCall(name, `{"pattern":"x"}`),
			policyCall("narrate", `{"reply":"好的"}`),
		})
		if reject == "" {
			t.Errorf("%s 与 narrate 同批应被拒绝", name)
		}
	}
}

func TestGrepStoryAction(t *testing.T) {
	doc := "# 雨夜\n老管家站在门口\n地下室的钥匙在他口袋里\n无关\n无关\n无关\n无关\n无关\n酒窖里有 <血迹>"
	actx := storyTestActx(t, 7, doc)

	res := grepStoryAction{}.Execute(ToolCall{Pattern: "钥匙|血迹"}, actx)
	if len(res) != 1 {
		t.Fatalf("results = %+v", res)
	}
	got := res[0].Result
	for _, want := range []string{
		`matches="2"`, `shown="2"`, "2-老管家站在门口", "3:地下室的钥匙在他口袋里", "4-无关", "8-无关", "9:酒窖里有 &lt;血迹&gt;",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in %s", want, got)
		}
	}
	if strings.Contains(got, "5-无关") {
		t.Errorf("context should be limited to 1 line: %s", got)
	}
	assertWellFormedXML(t, "story_grep", got)

	zero := 0
	res = grepStoryAction{}.Execute(ToolCall{Pattern: "钥匙", ContextLines: &zero}, actx)
	if strings.Contains(res[0].Result, "2-") || strings.Contains(res[0].Result, "4-") {
		t.Errorf("context_lines=0 should not include context: %s", res[0].Result)
	}

	none := grepStoryAction{}.Execute(ToolCall{Pattern: "不存在的词"}, actx)
	if !strings.Contains(none[0].Result, `matches="0"`) || !strings.HasSuffix(none[0].Result, "/>") {
		t.Errorf("no match result = %s", none[0].Result)
	}
}

func TestGrepStoryActionMergesAdjacentContext(t *testing.T) {
	actx := storyTestActx(t, 7, "甲\n乙命中\n丙\n丁命中\n戊")
	got := grepStoryAction{}.Execute(ToolCall{Pattern: "命中"}, actx)[0].Result
	if n := strings.Count(got, "<block>"); n != 1 {
		t.Errorf("overlapping context should merge into one block, got %d: %s", n, got)
	}
	if n := strings.Count(got, "丙"); n != 1 {
		t.Errorf("shared context line should appear once: %s", got)
	}
}

func TestGrepStoryActionRejectsBadInput(t *testing.T) {
	actx := storyTestActx(t, 7, "正文")
	for _, c := range []struct{ name, pattern string }{
		{"空 pattern", "  "},
		{"非法正则", "(未闭合"},
	} {
		res := grepStoryAction{}.Execute(ToolCall{Pattern: c.pattern}, actx)
		if len(res) != 1 || !strings.HasPrefix(res[0].Result, "SYSTEM REJECT") {
			t.Errorf("%s: result = %+v, want SYSTEM REJECT", c.name, res)
		}
	}
}

func TestGrepStoryActionTruncatesManyMatches(t *testing.T) {
	var sb strings.Builder
	for i := 0; i < 200; i++ {
		fmt.Fprintf(&sb, "命中第%d处%s\n", i, strings.Repeat("字", 80))
	}
	actx := storyTestActx(t, 7, sb.String())
	zero := 0
	got := grepStoryAction{}.Execute(ToolCall{Pattern: "命中", ContextLines: &zero}, actx)[0].Result
	if !strings.Contains(got, `matches="200"`) || !strings.Contains(got, `truncated="true"`) {
		t.Errorf("should report truncation: %.200s", got)
	}
	if n := len([]rune(got)); n > storyGrepMaxRunes+500 {
		t.Errorf("result has %d runes, want about %d at most", n, storyGrepMaxRunes)
	}
	assertWellFormedXML(t, "story_grep", got)
}

func TestGrepStoryActionTruncatesLongLine(t *testing.T) {
	actx := storyTestActx(t, 7, "短行\n命中"+strings.Repeat("长", 1000))
	got := grepStoryAction{}.Execute(ToolCall{Pattern: "命中"}, actx)[0].Result
	if !strings.Contains(got, "本行共1002字") {
		t.Errorf("long line should be truncated with its full length: %.300s", got)
	}
}

func TestReadStoryAction(t *testing.T) {
	actx := storyTestActx(t, 7, storyDoc(10))

	got := readStoryAction{}.Execute(ToolCall{StartLine: 3, LineCount: 2}, actx)[0].Result
	for _, want := range []string{`start="3"`, `end="4"`, `total_lines="10"`, `next_start_line="5"`, "3|第3行正文", "4|第4行正文"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in %s", want, got)
		}
	}
	if strings.Contains(got, "5|") {
		t.Errorf("should stop after line_count lines: %s", got)
	}
	assertWellFormedXML(t, "story_lines", got)

	all := readStoryAction{}.Execute(ToolCall{}, actx)[0].Result
	if !strings.Contains(all, `start="1"`) || !strings.Contains(all, `end="10"`) || strings.Contains(all, "next_start_line") {
		t.Errorf("default read should return the whole short document: %s", all)
	}

	tail := readStoryAction{}.Execute(ToolCall{StartLine: 9, LineCount: 50}, actx)[0].Result
	if !strings.Contains(tail, `end="10"`) || strings.Contains(tail, "next_start_line") {
		t.Errorf("tail read = %s", tail)
	}

	past := readStoryAction{}.Execute(ToolCall{StartLine: 11}, actx)[0].Result
	if !strings.HasPrefix(past, "SYSTEM REJECT") || !strings.Contains(past, "共 10 行") {
		t.Errorf("start past end = %s", past)
	}
}

func TestReadStoryActionPagesByRuneBudget(t *testing.T) {
	long := make([]string, 100)
	for i := range long {
		long[i] = strings.Repeat("字", 200)
	}
	actx := storyTestActx(t, 7, strings.Join(long, "\n"))
	got := readStoryAction{}.Execute(ToolCall{LineCount: 300}, actx)[0].Result
	if !strings.Contains(got, "next_start_line=") {
		t.Fatalf("should page when over the rune budget: %.200s", got)
	}
	if n := len([]rune(got)); n > storyReadMaxRunes+300 {
		t.Errorf("result has %d runes, want about %d at most", n, storyReadMaxRunes)
	}
}

func TestReadStoryActionAlwaysReturnsOneLine(t *testing.T) {
	actx := storyTestActx(t, 7, strings.Repeat("长", storyReadMaxRunes*2)+"\n尾")
	got := readStoryAction{}.Execute(ToolCall{}, actx)[0].Result
	if !strings.Contains(got, `end="1"`) || !strings.Contains(got, `next_start_line="2"`) {
		t.Errorf("an oversized first line must still be returned alone: %.200s", got)
	}
}

func TestStoryToolsUseLatestStoryAndRejectWhenMissing(t *testing.T) {
	actx := storyTestActx(t, 7, "旧版故事", "新版故事")
	if got := (readStoryAction{}).Execute(ToolCall{}, actx)[0].Result; !strings.Contains(got, "新版故事") || strings.Contains(got, "旧版故事") {
		t.Errorf("should read the latest story document: %s", got)
	}

	for _, c := range []struct {
		name  string
		setup func(t *testing.T) ActionContext
	}{
		{"没有日志行", func(t *testing.T) ActionContext { return storyTestActx(t, 7) }},
		{"故事原文为空", func(t *testing.T) ActionContext { return storyTestActx(t, 7, "  \n") }},
		{"没有模组", func(t *testing.T) ActionContext { return storyTestActx(t, 0, "有内容") }},
		{"别的模组", func(t *testing.T) ActionContext {
			actx := storyTestActx(t, 8, "别的模组")
			actx.GCtx.Session.ScenarioID = 7
			return actx
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			actx := c.setup(t)
			for name, res := range map[string][]ToolResult{
				"grep_story": grepStoryAction{}.Execute(ToolCall{Pattern: "."}, actx),
				"read_story": readStoryAction{}.Execute(ToolCall{}, actx),
			} {
				if len(res) != 1 || !strings.HasPrefix(res[0].Result, "SYSTEM REJECT") || !strings.Contains(res[0].Result, "没有保存故事原文") {
					t.Errorf("%s: result = %+v, want a missing-story reject", name, res)
				}
			}
		})
	}
}

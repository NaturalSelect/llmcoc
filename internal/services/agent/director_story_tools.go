package agent

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/llmcoc/server/internal/models"
)

// NOTE: 本文件是 Director 的只读工具 grep_story / read_story：按行检索、读取模组的故事原文
// (ScenarioGenerationLog.StoryDocument，编译成结构化模组之前的完整故事文档)。
// 没有保存故事原文的模组(手动导入、老数据)一律返回 SYSTEM REJECT，工具列表不因此裁剪，以保持提示词缓存前缀稳定。

const (
	storyGrepDefaultContext = 1
	storyGrepMaxContext     = 5
	storyGrepMaxMatches     = 30
	storyGrepMaxLineRunes   = 400
	storyGrepMaxRunes       = 8000

	storyReadDefaultLines = 120
	storyReadMaxLines     = 300
	storyReadMaxRunes     = 8000
)

// NOTE: 同一模组有多条生成日志时取最新一条；取不到故事原文时第二个返回值是拒绝原因。
func loadStoryLines(actx ActionContext) ([]string, string) {
	scenarioID := actx.GCtx.Session.ScenarioID
	if scenarioID == 0 {
		scenarioID = actx.GCtx.Session.Scenario.ID
	}
	const noStory = "SYSTEM REJECT: 本模组没有保存故事原文，无法检索；只能依据<scenario>和query_clues里的资料。"
	if scenarioID == 0 {
		return nil, noStory
	}
	var rows []models.ScenarioGenerationLog
	err := models.DB.Select("story_document").
		Where("scenario_id = ?", scenarioID).
		Order("id DESC").Limit(1).Find(&rows).Error
	if err != nil {
		alog.Warn("load story document failed", "session", actx.Sid, "scenario", scenarioID, "err", err)
		return nil, "SYSTEM REJECT: 读取故事原文失败，请稍后再试，或只依据<scenario>里的资料。"
	}
	if len(rows) == 0 {
		return nil, noStory
	}
	doc := strings.TrimRight(strings.ReplaceAll(rows[0].StoryDocument, "\r\n", "\n"), "\n")
	if strings.TrimSpace(doc) == "" {
		return nil, noStory
	}
	return strings.Split(doc, "\n"), ""
}

func storyLineText(line string, maxRunes int) string {
	runes := []rune(line)
	if len(runes) <= maxRunes {
		return xmlEscape(line)
	}
	return xmlEscape(string(runes[:maxRunes])) + fmt.Sprintf("…(本行共%d字，用read_story读全文)", len(runes))
}

type grepStoryAction struct{}

func (grepStoryAction) Execute(call ToolCall, actx ActionContext) []ToolResult {
	debugf("tool", "session=%d grep_story pattern=%q", actx.Sid, call.Pattern)
	if strings.TrimSpace(call.Pattern) == "" {
		return []ToolResult{{Action: ToolGrepStory, Result: "SYSTEM REJECT: grep_story.pattern 不能为空。"}}
	}
	re, err := regexp.Compile(call.Pattern)
	if err != nil {
		return []ToolResult{{Action: ToolGrepStory, Result: fmt.Sprintf("SYSTEM REJECT: grep_story.pattern 不是合法的正则(Go RE2 语法)：%v", err)}}
	}
	lines, reject := loadStoryLines(actx)
	if reject != "" {
		return []ToolResult{{Action: ToolGrepStory, Result: reject}}
	}

	context := storyGrepDefaultContext
	if call.ContextLines != nil {
		context = min(max(*call.ContextLines, 0), storyGrepMaxContext)
	}

	var hits []int
	isHit := map[int]bool{}
	for i, line := range lines {
		if re.MatchString(line) {
			hits = append(hits, i)
			isHit[i] = true
		}
	}
	head := fmt.Sprintf(`<story_grep pattern="%s" matches="%d" lines="%d"`, xmlAttr(call.Pattern), len(hits), len(lines))
	if len(hits) == 0 {
		return []ToolResult{{Action: ToolGrepStory, Result: head + "/>"}}
	}

	type block struct{ lo, hi int }
	var blocks []block
	for _, h := range hits[:min(len(hits), storyGrepMaxMatches)] {
		lo, hi := max(h-context, 0), min(h+context, len(lines)-1)
		if n := len(blocks); n > 0 && lo <= blocks[n-1].hi+1 {
			blocks[n-1].hi = max(blocks[n-1].hi, hi)
			continue
		}
		blocks = append(blocks, block{lo, hi})
	}

	var body strings.Builder
	runes, lastHi := 0, -1
	for _, b := range blocks {
		var sb strings.Builder
		sb.WriteString("<block>\n")
		for i := b.lo; i <= b.hi; i++ {
			sep := "-"
			if isHit[i] {
				sep = ":"
			}
			fmt.Fprintf(&sb, "%d%s%s\n", i+1, sep, storyLineText(lines[i], storyGrepMaxLineRunes))
		}
		sb.WriteString("</block>\n")
		if lastHi >= 0 && runes+len([]rune(sb.String())) > storyGrepMaxRunes {
			break
		}
		runes += len([]rune(sb.String()))
		body.WriteString(sb.String())
		lastHi = b.hi
	}

	shown := 0
	for _, h := range hits {
		if h <= lastHi {
			shown++
		}
	}
	head += fmt.Sprintf(` shown="%d"`, shown)
	if shown < len(hits) {
		head += fmt.Sprintf(` truncated="true" note="还有%d处命中未显示，请收窄pattern，或用read_story按行号读取"`, len(hits)-shown)
	}
	return []ToolResult{{Action: ToolGrepStory, Result: head + ">\n" + body.String() + "</story_grep>"}}
}

type readStoryAction struct{}

func (readStoryAction) Execute(call ToolCall, actx ActionContext) []ToolResult {
	debugf("tool", "session=%d read_story start=%d count=%d", actx.Sid, call.StartLine, call.LineCount)
	lines, reject := loadStoryLines(actx)
	if reject != "" {
		return []ToolResult{{Action: ToolReadStory, Result: reject}}
	}

	start := max(call.StartLine, 1)
	count := call.LineCount
	if count <= 0 {
		count = storyReadDefaultLines
	}
	count = min(count, storyReadMaxLines)
	if start > len(lines) {
		return []ToolResult{{Action: ToolReadStory, Result: fmt.Sprintf("SYSTEM REJECT: read_story.start_line=%d 超出范围，故事原文共 %d 行。", start, len(lines))}}
	}

	var body strings.Builder
	runes, last := 0, start-1
	for i := start - 1; i < min(start-1+count, len(lines)); i++ {
		text := fmt.Sprintf("%d|%s\n", i+1, xmlEscape(lines[i]))
		// NOTE: 至少返回一行，哪怕这一行本身就超过字数上限，否则超长段落永远读不到。
		if i > start-1 && runes+len([]rune(text)) > storyReadMaxRunes {
			break
		}
		runes += len([]rune(text))
		body.WriteString(text)
		last = i
	}

	head := fmt.Sprintf(`<story_lines start="%d" end="%d" total_lines="%d"`, start, last+1, len(lines))
	if last+1 < len(lines) {
		head += fmt.Sprintf(` next_start_line="%d"`, last+2)
	}
	return []ToolResult{{Action: ToolReadStory, Result: head + ">\n" + body.String() + "</story_lines>"}}
}

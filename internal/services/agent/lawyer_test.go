package agent

import (
	"strings"
	"testing"

	"github.com/llmcoc/server/internal/services/rulebook"
)

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

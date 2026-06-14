package agent

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestResponseActionFormatsOptionsAndPayload(t *testing.T) {
	hasEnd := false
	narration := ""
	actx := ActionContext{
		Sid:         1,
		HasEnd:      &hasEnd,
		KPNarration: &narration,
	}

	responseAction{}.Execute(ToolCall{
		Reply:   "你想先检查哪一处？",
		Options: []string{"书桌", "窗台", "书桌", "书架"},
	}, actx)

	if !hasEnd {
		t.Fatal("response should end the turn")
	}
	if strings.Contains(narration, "可以回复多个编号") {
		t.Fatalf("narration should keep option hints in payload only: %s", narration)
	}
	if strings.Count(narration, "书桌") != 1 {
		t.Fatalf("payload should contain deduplicated option once: %s", narration)
	}

	start := strings.Index(narration, "<response_options>")
	end := strings.Index(narration, "</response_options>")
	if start < 0 || end < 0 || end <= start {
		t.Fatalf("missing response_options payload: %s", narration)
	}
	raw := narration[start+len("<response_options>") : end]
	var payload responseOptionsPayload
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		t.Fatalf("payload is not json: %v", err)
	}
	if len(payload.Options) != 3 {
		t.Fatalf("unexpected payload: %+v", payload)
	}
}

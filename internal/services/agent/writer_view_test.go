package agent

import (
	"reflect"
	"strings"
	"testing"

	"github.com/llmcoc/server/internal/models"
	"github.com/llmcoc/server/internal/services/llm"
)

func writerViewDirector() []llm.ChatMessage {
	return []llm.ChatMessage{
		{Role: "system", Content: "director-system", CacheBreakpoint: true},
		{Role: "user", Content: "<scenario>模组</scenario>", CacheBreakpoint: true},
		{Role: "assistant", Reasoning: "思考A", ReasoningBlocks: []llm.ReasoningBlock{{Type: "thinking", Text: "t", Signature: "s"}},
			ToolCalls: []llm.ToolCall{fakeToolCall("w1", "write", `{"direction":"推门"}`)}},
		{Role: "tool", ToolCallID: "w1", Content: "已发布\n<writer_output>\n他推开了门。\n</writer_output>"},
		{Role: "assistant", ToolCalls: []llm.ToolCall{fakeToolCall("w2", "write", `{"direction":"走进去"}`)}},
		{Role: "tool", ToolCallID: "w2", Content: "已发布\n<writer_output>\n他走了进去。\n</writer_output>"},
		{Role: "assistant", ToolCalls: []llm.ToolCall{fakeToolCall("w3", "write", `{"direction":""}`)}},
		{Role: "tool", ToolCallID: "w3", Content: "SYSTEM REJECT: write.direction 不能为空"},
	}
}

func writerViewInstruction() llm.ChatMessage {
	return llm.ChatMessage{Role: "user", Content: "续写指令"}
}

func TestBuildWriterView_ReplacesSystemKeepsDirectorChain(t *testing.T) {
	director := writerViewDirector()
	view := buildWriterView(director, "writer-system", true, writerViewInstruction())

	if len(view) != len(director)+1 {
		t.Fatalf("view = %d messages, want %d", len(view), len(director)+1)
	}
	if view[0].Role != "system" || view[0].Content != "writer-system" || !view[0].CacheBreakpoint {
		t.Errorf("system should be replaced but keep its cache breakpoint, got %+v", view[0])
	}
	// NOTE: 正文已经在 Director 的 tool 结果里，视图必须原样透传，不再自己附加。
	for i := 1; i < len(director); i++ {
		if !reflect.DeepEqual(view[i], director[i]) {
			t.Errorf("view[%d] differs from director message:\ngot  %+v\nwant %+v", i, view[i], director[i])
		}
	}
	if last := view[len(view)-1]; last.Content != "续写指令" {
		t.Errorf("last message should be the instruction, got %+v", last)
	}
	if director[0].Content != "director-system" {
		t.Error("buildWriterView must not mutate the director chain")
	}
}

func TestBuildWriterView_NoSystemInDirectorPrependsOne(t *testing.T) {
	view := buildWriterView([]llm.ChatMessage{{Role: "user", Content: "u"}}, "writer-system", true, writerViewInstruction())
	if len(view) != 3 || view[0].Role != "system" || view[0].Content != "writer-system" {
		t.Errorf("view = %+v, want system + user + instruction", view)
	}
	if view := buildWriterView(nil, "writer-system", true, writerViewInstruction()); len(view) != 2 {
		t.Errorf("empty director view = %+v, want system + instruction", view)
	}
}

func TestBuildWriterView_Deterministic(t *testing.T) {
	a := buildWriterView(writerViewDirector(), "sys", true, writerViewInstruction())
	b := buildWriterView(writerViewDirector(), "sys", true, writerViewInstruction())
	if !reflect.DeepEqual(a, b) {
		t.Error("the same input must produce the same view")
	}

	// 在 Director 末尾追加一轮后，原有前缀字节不变。
	longer := append(writerViewDirector(),
		llm.ChatMessage{Role: "assistant", ToolCalls: []llm.ToolCall{fakeToolCall("w4", "write", `{"direction":"x"}`)}},
		llm.ChatMessage{Role: "tool", ToolCallID: "w4", Content: "已发布"})
	c := buildWriterView(longer, "sys", true, writerViewInstruction())
	for i := 0; i < len(writerViewDirector()); i++ {
		if !reflect.DeepEqual(a[i], c[i]) {
			t.Errorf("prefix message %d changed after appending a round:\nbefore %+v\nafter  %+v", i, a[i], c[i])
		}
	}
}

func TestBuildWriterView_ReasoningOnlyKeptForSameModel(t *testing.T) {
	kept := buildWriterView(writerViewDirector(), "sys", true, writerViewInstruction())
	if kept[2].Reasoning == "" || len(kept[2].ReasoningBlocks) == 0 {
		t.Error("reasoning should be kept when Writer shares the Director's model")
	}
	dropped := buildWriterView(writerViewDirector(), "sys", false, writerViewInstruction())
	if dropped[2].Reasoning != "" || dropped[2].ReasoningBlocks != nil {
		t.Errorf("reasoning must be stripped for a different model, got %+v", dropped[2])
	}
	if !strings.Contains(dropped[3].Content, "他推开了门。") {
		t.Errorf("stripping reasoning must keep the prose in the tool result, got %q", dropped[3].Content)
	}
}

func TestSameModelConfig(t *testing.T) {
	one, two := uint(1), uint(2)
	cfg := func(id *uint, model string) *models.AgentConfig {
		return &models.AgentConfig{ProviderConfigID: id, ModelName: model}
	}
	cases := []struct {
		name string
		a, b *models.AgentConfig
		want bool
	}{
		{"same provider and model", cfg(&one, "m"), cfg(&one, "m"), true},
		{"different model", cfg(&one, "m"), cfg(&one, "n"), false},
		{"different provider", cfg(&one, "m"), cfg(&two, "m"), false},
		{"unbound provider", cfg(nil, "m"), cfg(nil, "m"), false},
		{"nil config", nil, cfg(&one, "m"), false},
	}
	for _, tc := range cases {
		if got := sameModelConfig(tc.a, tc.b); got != tc.want {
			t.Errorf("%s: sameModelConfig = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestLoadWriterProse_OnlyDoneMessagesWithCallID(t *testing.T) {
	initAgentTestDB(t)
	const sessionID = 301
	mk := func(status models.MessageStatus, floor int, content, callID string) models.Message {
		return models.Message{
			SessionID: sessionID, Role: models.MessageRoleAssistant, Kind: models.MessageKindWriter,
			Status: status, Floor: floor, Content: content,
			Meta: models.JSONField[*models.MessageMeta]{Data: &models.MessageMeta{CallID: callID}},
		}
	}
	rows := []models.Message{
		mk(models.MessageStatusDone, 1, "完成的正文", "a"),
		mk(models.MessageStatusPending, 2, "", "b"),
		mk(models.MessageStatusFailed, 2, "", "c"),
		mk(models.MessageStatusDone, 3, "旧消息没有调用ID", ""),
		{SessionID: sessionID, Role: models.MessageRoleAssistant, Kind: models.MessageKindKP, Status: models.MessageStatusDone, Floor: 3, Content: "kp 消息"},
		{SessionID: sessionID + 1, Role: models.MessageRoleAssistant, Kind: models.MessageKindWriter, Status: models.MessageStatusDone, Floor: 1, Content: "别的会话",
			Meta: models.JSONField[*models.MessageMeta]{Data: &models.MessageMeta{CallID: "other"}}},
	}
	for i := range rows {
		if err := models.DB.Create(&rows[i]).Error; err != nil {
			t.Fatalf("create message: %v", err)
		}
	}

	got := loadWriterProse(sessionID)
	if len(got) != 1 || got["a"] != "完成的正文" {
		t.Errorf("loadWriterProse = %+v, want only the done message with a call id", got)
	}
}

func TestDecodeDirectorToolCall_SetsCallIDFromGatewayOnly(t *testing.T) {
	tc, err := decodeDirectorToolCall(llm.ToolCall{ID: "call_9", Name: "write", Arguments: `{"direction":"推门","CallID":"forged","action":"end_game"}`})
	if err != nil {
		t.Fatalf("decodeDirectorToolCall: %v", err)
	}
	if tc.CallID != "call_9" {
		t.Errorf("CallID = %q, want the gateway call id", tc.CallID)
	}
	if tc.Action != ToolWrite {
		t.Errorf("Action = %q, want the tool name", tc.Action)
	}
}

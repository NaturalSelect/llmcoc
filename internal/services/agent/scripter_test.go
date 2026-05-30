package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/llmcoc/server/internal/models"
)

func TestCluePrefixForLayer(t *testing.T) {
	cases := map[string]string{
		"appearance": "[真实]",
		"surface":    "[真实]",
		"real":       "[真实]",
		"deep":       "[隐藏]",
		"distorted":  "[隐藏]",
		"false":      "[误导]",
	}
	for layer, want := range cases {
		if got := cluePrefixForLayer(layer); got != want {
			t.Fatalf("cluePrefixForLayer(%q)=%q, want %q", layer, got, want)
		}
	}
}

func TestValidateDraftCompatibilityRequiresCluePrefixes(t *testing.T) {
	draft := validScripterTestDraft()
	draft.Content.Clues = []string{"无前缀线索(档案室): 内容"}

	issues := validateDraftCompatibility(draft)
	if !containsIssue(issues, "content.clues[0]") {
		t.Fatalf("expected clue prefix issue, got %v", issues)
	}

	draft.Content.Clues = []string{"[真实]线索(档案室): 内容", "[隐藏]线索(钟楼): 内容", "[误导]线索(会客室): 内容"}
	if issues := validateDraftCompatibility(draft); len(issues) != 0 {
		t.Fatalf("expected no issues for prefixed clues, got %v", issues)
	}
}

func TestValidateDraftCompatibilityRequiresSceneNPCAndWinLose(t *testing.T) {
	draft := validScripterTestDraft()
	draft.Content.Scenes = nil
	draft.Content.NPCs = nil
	draft.Content.WinCondition = ""
	draft.Content.LoseCondition = ""

	issues := validateDraftCompatibility(draft)
	for _, want := range []string{"content.scenes 为空", "content.npcs 为空", "content.win_condition 为空", "content.lose_condition 为空"} {
		if !containsIssue(issues, want) {
			t.Fatalf("expected issue %q, got %v", want, issues)
		}
	}
}

func TestApplyGuardrailsPreservesRequestOverrides(t *testing.T) {
	draft := validScripterTestDraft()
	draft.Name = "模型标题"
	draft.Author = ""
	draft.MinPlayers = 9
	draft.MaxPlayers = 9
	draft.Difficulty = "hard"

	req := ScenarioCreationRequest{Name: "用户标题", MinPlayers: 2, MaxPlayers: 5, Difficulty: "normal"}
	applyGuardrails(&draft, req, "architect-model")

	if draft.Name != "用户标题" {
		t.Fatalf("Name=%q, want 用户标题", draft.Name)
	}
	if draft.MinPlayers != 2 || draft.MaxPlayers != 5 {
		t.Fatalf("players=%d-%d, want 2-5", draft.MinPlayers, draft.MaxPlayers)
	}
	if draft.Difficulty != "normal" {
		t.Fatalf("Difficulty=%q, want normal", draft.Difficulty)
	}
	if draft.Author != "architect-model" {
		t.Fatalf("Author=%q, want architect-model", draft.Author)
	}
}

func TestParseJSONObjectExtractsFencedJSON(t *testing.T) {
	raw := "```json\n{\"name\":\"测试模组\",\"description\":\"简介\"}\n```"
	var result ScenarioDraft
	if err := parseJSONObject(raw, &result); err != nil {
		t.Fatalf("parseJSONObject failed: %v", err)
	}
	if result.Name != "测试模组" || result.Description != "简介" {
		t.Fatalf("unexpected result: %+v", result)
	}
}

func TestValidateScripterResponsePayloadRequiresReason(t *testing.T) {
	call := scripterToolCall{Action: "response", Background: &FogBackground{TimeAndPlace: "测试地点"}}
	if err := validateScripterResponsePayload(call, "background"); err == nil {
		t.Fatal("expected missing reason to fail")
	}
	call.Reason = "背景符合公开入口阶段要求。"
	if err := validateScripterResponsePayload(call, "background"); err != nil {
		t.Fatalf("expected response with reason to pass: %v", err)
	}
}

func TestParseScripterToolCallsRequiresArrayShape(t *testing.T) {
	calls, err := parseScripterToolCalls(context.Background(), agentHandle{}, `[{"action":"think","think":"先查规则"}]`, scripterSchemaExample("background"))
	if err != nil {
		t.Fatalf("parseScripterToolCalls valid array failed: %v", err)
	}
	if len(calls) != 1 || calls[0].Action != "think" || calls[0].Think != "先查规则" {
		t.Fatalf("unexpected calls: %+v", calls)
	}

	if _, err := parseScripterToolCalls(context.Background(), agentHandle{}, `{"action":"think"}`, scripterSchemaExample("background")); err == nil {
		t.Fatal("expected object-shaped tool calls to fail")
	}
}

func validScripterTestDraft() ScenarioDraft {
	return ScenarioDraft{
		Name:        "测试模组",
		Description: "简介",
		Author:      "agent-team",
		Tags:        "test",
		MinPlayers:  1,
		MaxPlayers:  4,
		Difficulty:  "normal",
		Content: models.ScenarioContent{
			SystemPrompt:   "你是KP。",
			Setting:        "公开背景。",
			Intro:          "开场导入。",
			GameStartSlot:  16,
			MapDescription: "起点、路径、终点。",
			Scenes: []models.SceneData{{
				ID:          "start",
				Name:        "起点",
				Description: "互动对象、线索、检定、危险、出口。",
				Triggers:    []string{"start"},
			}},
			NPCs: []models.NPCData{{
				Name:        "林秋",
				Description: "公开身份与秘密。",
				Attitude:    "谨慎合作",
			}},
			Clues:         []string{"[真实]线索(档案室): 内容"},
			WinCondition:  "公开证据并阻止仪式。",
			LoseCondition: "仪式完成。",
		},
	}
}

func containsIssue(issues []string, substr string) bool {
	for _, issue := range issues {
		if strings.Contains(issue, substr) {
			return true
		}
	}
	return false
}

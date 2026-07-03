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

func TestApplyGuardrailsRenamesBlacklistedNPCs(t *testing.T) {
	draft := validScripterTestDraft()
	draft.Content.NPCs = []models.NPCData{
		{Name: "林秋", Description: "旧名命中黑名单。", Attitude: "谨慎"},
		{Name: "王岚", Description: "未命中。", Attitude: "谨慎"},
	}

	applyGuardrailsWithNPCBlacklist(&draft, ScenarioCreationRequest{}, "agent-team", "test-session", []string{"林秋"})

	if strings.TrimSpace(draft.Content.NPCs[0].Name) == "" {
		t.Fatal("renamed NPC name must not be empty")
	}
	if draft.Content.NPCs[0].Name == "林秋" {
		t.Fatalf("expected blacklisted NPC to be renamed, got %q", draft.Content.NPCs[0].Name)
	}
	if draft.Content.NPCs[1].Name != "王岚" {
		t.Fatalf("unexpected rename for non-blacklisted NPC: %q", draft.Content.NPCs[1].Name)
	}
}

func TestNormalizeOneshotDraftAppliesDiversityConstraints(t *testing.T) {
	draft := validScripterTestDraft()
	draft.Content.HorrorMode = "cosmic_horror"
	draft.Content.InvestFocus = "disappearance"
	draft.Content.ToneTags = []string{"old"}
	constraints := ScripterConstraints{
		Era:             "1920s",
		GeographyFlavor: []string{"美国", "乡镇"},
		HorrorMode:      "gothic_horror",
		InvestFocus:     "family_secret",
		ToneTags:        []string{"gothic", "slow-burn"},
	}

	normalizeOneshotDraft(&draft, ScenarioCreationRequest{}, "agent-team", constraints, "test-session")

	if draft.Content.HorrorMode != "gothic_horror" {
		t.Fatalf("HorrorMode=%q, want gothic_horror", draft.Content.HorrorMode)
	}
	if draft.Content.InvestFocus != "family_secret" {
		t.Fatalf("InvestFocus=%q, want family_secret", draft.Content.InvestFocus)
	}
	if !sameStringSlice(draft.Content.ToneTags, []string{"gothic", "slow-burn"}) {
		t.Fatalf("ToneTags=%v, want [gothic slow-burn]", draft.Content.ToneTags)
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
	calls, err := parseScripterToolCalls(context.Background(), agentHandle{}, `[{"action":"response","reason":"背景符合公开入口阶段要求。","background":{"time_and_place":"测试地点"}}]`, scripterSchemaExample("background"))
	if err != nil {
		t.Fatalf("parseScripterToolCalls valid array failed: %v", err)
	}
	if len(calls) != 1 || calls[0].Action != "response" || calls[0].Reason == "" {
		t.Fatalf("unexpected calls: %+v", calls)
	}

	if _, err := parseScripterToolCalls(context.Background(), agentHandle{}, `{"action":"response"}`, scripterSchemaExample("background")); err == nil {
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
			ToneTags:       []string{"slow-burn", "investigative"},
			HorrorMode:     "cosmic_horror",
			InvestFocus:    "disappearance",
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

// ---------------------------------------------------------------------------
// buildDiversityCandidates / parseDiversityAIResponse tests
// ---------------------------------------------------------------------------

func TestBuildDiversityCandidates_FullCartesianWhenDBNil(t *testing.T) {
	// models.DB == nil 时应返回全笛卡尔积 (7×8=56)
	candidates := buildDiversityCandidates(ScenarioCreationRequest{}, "test-session")
	if len(candidates) != 56 {
		t.Fatalf("expected 56 candidates when DB is nil, got %d", len(candidates))
	}
	// 去重后也应是 56
	seen := map[string]bool{}
	for _, c := range candidates {
		key := diversityComboKey(c.HorrorMode, c.InvestFocus)
		if key == "" {
			t.Fatalf("got empty key for candidate %+v", c)
		}
		if seen[key] {
			t.Fatalf("duplicate candidate key %q", key)
		}
		seen[key] = true
	}
	if len(seen) != 56 {
		t.Fatalf("expected 56 unique keys, got %d", len(seen))
	}
}

func TestParseDiversityAIResponse_Valid(t *testing.T) {
	raw := "mode: cosmic_horror\nfocus: disappearance"
	candidates := []diversityCombo{
		{HorrorMode: "cosmic_horror", InvestFocus: "disappearance"},
		{HorrorMode: "gothic_horror", InvestFocus: "family_secret"},
	}
	mode, focus, ok := parseDiversityAIResponse(raw, candidates)
	if !ok {
		t.Fatal("expected ok=true")
	}
	if mode != "cosmic_horror" {
		t.Fatalf("mode=%q, want cosmic_horror", mode)
	}
	if focus != "disappearance" {
		t.Fatalf("focus=%q, want disappearance", focus)
	}
}

func TestParseDiversityAIResponse_ExtraLines(t *testing.T) {
	raw := "好的，让我来分析一下。\nmode: gothic_horror\nfocus: family_secret\n谢谢，希望对你有帮助！"
	candidates := []diversityCombo{
		{HorrorMode: "gothic_horror", InvestFocus: "family_secret"},
		{HorrorMode: "cosmic_horror", InvestFocus: "disappearance"},
	}
	mode, focus, ok := parseDiversityAIResponse(raw, candidates)
	if !ok {
		t.Fatal("expected ok=true")
	}
	if mode != "gothic_horror" {
		t.Fatalf("mode=%q, want gothic_horror", mode)
	}
	if focus != "family_secret" {
		t.Fatalf("focus=%q, want family_secret", focus)
	}
}

func TestParseDiversityAIResponse_OutOfPool(t *testing.T) {
	raw := "mode: cosmic_horror\nfocus: disappearance"
	// 候选不含 cosmic_horror+disappearance
	candidates := []diversityCombo{
		{HorrorMode: "gothic_horror", InvestFocus: "family_secret"},
		{HorrorMode: "folk_horror", InvestFocus: "local_legend"},
	}
	mode, focus, ok := parseDiversityAIResponse(raw, candidates)
	if ok {
		t.Fatal("expected ok=false for out-of-pool combination")
	}
	if mode != "" || focus != "" {
		t.Fatalf("expected empty mode/focus on failure, got mode=%q focus=%q", mode, focus)
	}
}

func TestParseDiversityAIResponse_MissingFocus(t *testing.T) {
	raw := "mode: cosmic_horror"
	candidates := []diversityCombo{
		{HorrorMode: "cosmic_horror", InvestFocus: "disappearance"},
	}
	_, _, ok := parseDiversityAIResponse(raw, candidates)
	if ok {
		t.Fatal("expected ok=false when focus line is missing")
	}
}

// ---------------------------------------------------------------------------
// normalizeOneshotDraft diversity-source tests
// ---------------------------------------------------------------------------

func TestNormalizeOneshotDraft_AISourcePreservesArchitectChoice(t *testing.T) {
	draft := validScripterTestDraft()
	draft.Content.HorrorMode = "gothic_horror"
	draft.Content.InvestFocus = "family_secret"

	constraints := ScripterConstraints{
		Era:             "1920s",
		GeographyFlavor: []string{"美国", "乡镇"},
		HorrorMode:      "cosmic_horror",
		InvestFocus:     "disappearance",
		ToneTags:        []string{"cosmic-dread", "slow-burn"},
		DiversitySource: "ai",
	}

	normalizeOneshotDraft(&draft, ScenarioCreationRequest{}, "agent-team", constraints, "test-session")

	// AI source 下 architect 输出应被保留
	if draft.Content.HorrorMode != "gothic_horror" {
		t.Fatalf("HorrorMode=%q, want gothic_horror (architect choice preserved)", draft.Content.HorrorMode)
	}
	if draft.Content.InvestFocus != "family_secret" {
		t.Fatalf("InvestFocus=%q, want family_secret (architect choice preserved)", draft.Content.InvestFocus)
	}
	// ToneTags 仍被覆盖
	if !sameStringSlice(draft.Content.ToneTags, []string{"cosmic-dread", "slow-burn"}) {
		t.Fatalf("ToneTags=%v, want [cosmic-dread slow-burn]", draft.Content.ToneTags)
	}
}

func TestNormalizeOneshotDraft_AISourceFillsEmpty(t *testing.T) {
	draft := validScripterTestDraft()
	draft.Content.HorrorMode = ""
	draft.Content.InvestFocus = ""

	constraints := ScripterConstraints{
		Era:             "1920s",
		GeographyFlavor: []string{"美国", "乡镇"},
		HorrorMode:      "cosmic_horror",
		InvestFocus:     "disappearance",
		ToneTags:        []string{"cosmic-dread", "slow-burn"},
		DiversitySource: "ai",
	}

	normalizeOneshotDraft(&draft, ScenarioCreationRequest{}, "agent-team", constraints, "test-session")

	// AI source 下空值应被填充
	if draft.Content.HorrorMode != "cosmic_horror" {
		t.Fatalf("HorrorMode=%q, want cosmic_horror (filled from constraint)", draft.Content.HorrorMode)
	}
	if draft.Content.InvestFocus != "disappearance" {
		t.Fatalf("InvestFocus=%q, want disappearance (filled from constraint)", draft.Content.InvestFocus)
	}
}

func TestNormalizeOneshotDraft_FallbackSourceOverrides(t *testing.T) {
	draft := validScripterTestDraft()
	draft.Content.HorrorMode = "cosmic_horror"
	draft.Content.InvestFocus = "disappearance"

	constraints := ScripterConstraints{
		Era:             "1920s",
		GeographyFlavor: []string{"美国", "乡镇"},
		HorrorMode:      "gothic_horror",
		InvestFocus:     "family_secret",
		ToneTags:        []string{"gothic", "slow-burn"},
		DiversitySource: "fallback",
	}

	normalizeOneshotDraft(&draft, ScenarioCreationRequest{}, "agent-team", constraints, "test-session")

	// fallback 下强制覆盖
	if draft.Content.HorrorMode != "gothic_horror" {
		t.Fatalf("HorrorMode=%q, want gothic_horror (forced override)", draft.Content.HorrorMode)
	}
	if draft.Content.InvestFocus != "family_secret" {
		t.Fatalf("InvestFocus=%q, want family_secret (forced override)", draft.Content.InvestFocus)
	}
}

func TestNormalizeOneshotDraft_EmptySourceOverrides(t *testing.T) {
	// DiversitySource 为空时行为等同 fallback
	draft := validScripterTestDraft()
	draft.Content.HorrorMode = "cosmic_horror"
	draft.Content.InvestFocus = "disappearance"

	constraints := ScripterConstraints{
		Era:             "1920s",
		GeographyFlavor: []string{"美国", "乡镇"},
		HorrorMode:      "gothic_horror",
		InvestFocus:     "family_secret",
		ToneTags:        []string{"gothic", "slow-burn"},
		// DiversitySource 留空
	}

	normalizeOneshotDraft(&draft, ScenarioCreationRequest{}, "agent-team", constraints, "test-session")

	if draft.Content.HorrorMode != "gothic_horror" {
		t.Fatalf("HorrorMode=%q, want gothic_horror (empty source = fallback)", draft.Content.HorrorMode)
	}
	if draft.Content.InvestFocus != "family_secret" {
		t.Fatalf("InvestFocus=%q, want family_secret (empty source = fallback)", draft.Content.InvestFocus)
	}
}

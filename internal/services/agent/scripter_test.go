package agent

import (
	"strings"
	"testing"
)

func TestExtractFirstJSONArrayTakesOnlyFirstArray(t *testing.T) {
	raw := `prefix [{"action":"search","query":"a [kept]"}] middle [{"action":"response","text":"second"}]`
	got := extractFirstJSONArray(raw)
	want := `[{"action":"search","query":"a [kept]"}]`
	if got != want {
		t.Fatalf("extractFirstJSONArray() = %q, want %q", got, want)
	}
}

func TestToolCallTextPrefersTextThenBriefThenOutline(t *testing.T) {
	if got := toolCallText(pipelineToolCall{Text: " text ", Brief: "brief", Outline: "outline"}); got != "text" {
		t.Fatalf("Text priority mismatch: %q", got)
	}
	if got := toolCallText(pipelineToolCall{Brief: " brief ", Outline: "outline"}); got != "brief" {
		t.Fatalf("Brief fallback mismatch: %q", got)
	}
	if got := toolCallText(pipelineToolCall{Outline: " outline "}); got != "outline" {
		t.Fatalf("Outline fallback mismatch: %q", got)
	}
}

func TestRenderDesignArtifactsIncludesAllFixedHeadings(t *testing.T) {
	got := renderDesignArtifacts(scenarioDesignArtifacts{
		SettingSeed:      "setting",
		MythosSource:     "mythos",
		GameplayCore:     "core",
		MythosSecret:     "secret",
		ThreatPlan:       "threat",
		EventChain:       "events",
		NPCDesign:        "npcs",
		SceneDesign:      "scenes",
		CluesAndHandouts: "clues",
	})
	for _, heading := range []string{
		"## Setting Seed",
		"## Mythos Source",
		"## Gameplay Core",
		"## Mythos Secret",
		"## Threat Plan",
		"## Event Chain",
		"## NPC Design",
		"## Scene Design",
		"## Clues & Handouts",
	} {
		if !strings.Contains(got, heading) {
			t.Fatalf("renderDesignArtifacts() missing heading %q in:\n%s", heading, got)
		}
	}
}

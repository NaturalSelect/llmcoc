// Director 角色标记的测试：转录单元记下由哪个 Director 角色产生，换角色时按需去掉推理内容。
package agent

import (
	"testing"

	"github.com/llmcoc/server/internal/models"
	"github.com/llmcoc/server/internal/services/llm"
)

func reasoningTurn(agent, tag string) models.TranscriptTurn {
	return models.TranscriptTurn{
		Agent: agent,
		Messages: []models.TranscriptMsg{{
			Role:            "assistant",
			Content:         tag,
			Reasoning:       "推理-" + tag,
			ReasoningBlocks: []models.TranscriptReasoningBlock{{Type: "thinking", Text: "思考-" + tag, Signature: "sig-" + tag}},
		}},
	}
}

func hasReasoning(m llm.ChatMessage) bool {
	return m.Reasoning != "" || len(m.ReasoningBlocks) > 0
}

func TestContext_CheckpointTagsUnitsWithConfiguredAgent(t *testing.T) {
	initAgentTestDB(t)
	const sid = uint(61)

	cm := LoadContext(sid, "director", nil, ContextOptions{})
	cm.Configure(ckHead(), ContextOptions{Agent: string(models.AgentRoleDirectorNSFW)})
	msgs := cm.Build(llm.ChatMessage{Role: "user", Content: "<player_turn floor=1>开场</player_turn>"})
	msgs = append(msgs, ckAssistant(ckCall("a", "narrate", `{"reply":"r1"}`)), ckTool("a", "已发给玩家"))
	cm.Checkpoint(1, msgs)

	data := loadTranscript(t, sid, "director")
	if len(data.Turns) != 1 || data.Turns[0].Agent != string(models.AgentRoleDirectorNSFW) {
		t.Fatalf("turns = %+v, want one unit tagged director_nsfw", data.Turns)
	}
	if got := LoadContext(sid, "director", nil, ContextOptions{}).LastAgent(); got != models.AgentRoleDirectorNSFW {
		t.Errorf("LastAgent() = %q, want director_nsfw", got)
	}
}

func TestContext_LastAgentDefaultsToDirectorAndSkipsSummaries(t *testing.T) {
	initAgentTestDB(t)

	if got := LoadContext(62, "director", nil, ContextOptions{}).LastAgent(); got != models.AgentRoleDirector {
		t.Errorf("empty transcript: LastAgent() = %q, want director", got)
	}

	cm := LoadContext(62, "director", nil, ContextOptions{})
	cm.data.Turns = []models.TranscriptTurn{{Kind: "summary"}, {Agent: string(models.AgentRoleDirectorNSFW)}, {Kind: "summary"}}
	if got := cm.LastAgent(); got != models.AgentRoleDirectorNSFW {
		t.Errorf("trailing summary: LastAgent() = %q, want director_nsfw", got)
	}

	cm.data.Turns = []models.TranscriptTurn{{Agent: string(models.AgentRoleDirectorNSFW)}, {}}
	if got := cm.LastAgent(); got != models.AgentRoleDirector {
		t.Errorf("untagged (legacy or director) last unit: LastAgent() = %q, want director", got)
	}
}

func TestContext_StripForeignReasoningOnlyDropsOtherAgentsReasoning(t *testing.T) {
	initAgentTestDB(t)
	turns := []models.TranscriptTurn{reasoningTurn("", "dir"), reasoningTurn(string(models.AgentRoleDirectorNSFW), "nsfw")}

	cases := []struct {
		name     string
		opts     ContextOptions
		wantDir  bool
		wantNSFW bool
	}{
		{"director cycle drops director_nsfw reasoning", ContextOptions{Agent: "", StripForeignReasoning: true}, true, false},
		{"director_nsfw cycle drops director reasoning", ContextOptions{Agent: string(models.AgentRoleDirectorNSFW), StripForeignReasoning: true}, false, true},
		{"same model keeps everything", ContextOptions{Agent: "", StripForeignReasoning: false}, true, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cm := LoadContext(63, "director", nil, ContextOptions{})
			cm.data.Turns = turns
			cm.Configure(ckHead(), tc.opts)

			msgs := cm.Build(llm.ChatMessage{Role: "user", Content: "本周期"})
			// head(2) + 两个历史单元(各 1 条) + opening
			if len(msgs) != 5 {
				t.Fatalf("len(msgs) = %d, want 5", len(msgs))
			}
			if got := hasReasoning(msgs[2]); got != tc.wantDir {
				t.Errorf("director unit reasoning kept = %v, want %v", got, tc.wantDir)
			}
			if got := hasReasoning(msgs[3]); got != tc.wantNSFW {
				t.Errorf("director_nsfw unit reasoning kept = %v, want %v", got, tc.wantNSFW)
			}
			if msgs[2].Content != "dir" || msgs[3].Content != "nsfw" {
				t.Errorf("contents changed: %q / %q", msgs[2].Content, msgs[3].Content)
			}

			rebuilt := cm.rebuild(msgs)
			for i := range msgs {
				if hasReasoning(rebuilt[i]) != hasReasoning(msgs[i]) || rebuilt[i].Content != msgs[i].Content {
					t.Errorf("rebuild disagrees with Build at %d: %+v vs %+v", i, rebuilt[i], msgs[i])
				}
			}

			// 去推理只作用于发出去的消息，库里存的单元不变。
			if turns[0].Messages[0].Reasoning == "" || len(turns[1].Messages[0].ReasoningBlocks) == 0 {
				t.Error("stored turns lost their reasoning")
			}
		})
	}
}

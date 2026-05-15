package agent

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/llmcoc/server/internal/models"
	"github.com/llmcoc/server/internal/services/llm"
)

type fakeProvider struct {
	resp     string
	err      error
	messages []llm.ChatMessage
}

func (f *fakeProvider) Chat(ctx context.Context, messages []llm.ChatMessage) (string, error) {
	f.messages = append([]llm.ChatMessage(nil), messages...)
	if f.err != nil {
		return "", f.err
	}
	return f.resp, nil
}

func TestToolCallUnmarshalPreservesThink(t *testing.T) {
	raw := `[{"action":"think","think":"保持手榴弹原属性，仅叙事换皮"}]`
	var calls []ToolCall
	if err := json.Unmarshal([]byte(raw), &calls); err != nil {
		t.Fatalf("json.Unmarshal failed: %v", err)
	}
	if len(calls) != 1 {
		t.Fatalf("expected 1 call, got %d", len(calls))
	}
	if calls[0].Think != "保持手榴弹原属性，仅叙事换皮" {
		t.Fatalf("think not preserved: %q", calls[0].Think)
	}
}

func TestRunAntiCheatParsesAllow(t *testing.T) {
	fp := &fakeProvider{resp: `{"verdict":"allow","reason":"KP计划和工具一致","message":"继续"}`}
	verdict, err := runAntiCheat(context.Background(), agentHandle{provider: fp}, minimalAntiCheatContext(), []ToolCall{{Action: ToolThink, Think: "仅叙事描述，不改状态"}}, nil)
	if err != nil {
		t.Fatalf("runAntiCheat failed: %v", err)
	}
	if verdict.Verdict != "allow" || verdict.Reason != "KP计划和工具一致" {
		t.Fatalf("unexpected verdict: %+v", verdict)
	}
	if len(fp.messages) != 2 {
		t.Fatalf("expected 2 guard messages, got %d", len(fp.messages))
	}
	if !strings.Contains(fp.messages[1].Content, "<proposed_tool_batch>") {
		t.Fatal("guard prompt did not include proposed tool batch")
	}
	if strings.Contains(fp.messages[0].Content, "player_cheat") {
		t.Fatal("simplified prompt should not ask for player_cheat verdict")
	}
}

func TestCheckAntiCheatRejectsKPInconsistency(t *testing.T) {
	fp := &fakeProvider{resp: `{"verdict":"replan","reason":"think承诺仅换皮但工具写入新伤害","message":"只能写属性同原物品/仅叙事换皮"}`}
	calls := []ToolCall{
		{Action: ToolThink, Think: "把手榴弹换皮为北凉火蒺藜，保持原属性，不增强。"},
		{Action: ToolManageInventory, CharacterName: "调查员", Operate: "add", ItemName: "北凉火蒺藜", ItemDesc: "伤害：4D10，爆炸范围更大", ItemCount: 1},
	}
	verdict, allowed, rejectMsg := checkAntiCheat(context.Background(), agentHandle{provider: fp}, minimalAntiCheatContext(), calls, nil)
	if allowed {
		t.Fatal("inconsistent KP batch should not be allowed")
	}
	if verdict.Verdict != "replan" {
		t.Fatalf("unexpected verdict: %+v", verdict)
	}
	if !strings.Contains(rejectMsg, "SYSTEM REJECT: anti_cheat verdict=replan") || !strings.Contains(rejectMsg, "仅叙事换皮") {
		t.Fatalf("unexpected reject message: %q", rejectMsg)
	}
}

func TestAntiCheatRejectPreventsInventoryExecution(t *testing.T) {
	ctx := minimalAntiCheatContext()
	calls := []ToolCall{
		{Action: ToolThink, Think: "只改名，不改变手榴弹机械属性。"},
		{Action: ToolManageInventory, CharacterName: "调查员", Operate: "add", ItemName: "北凉火蒺藜", ItemDesc: "伤害：4D10", ItemCount: 1},
	}
	fp := &fakeProvider{resp: `{"verdict":"replan","reason":"承诺不改变机械属性但工具改变伤害","message":"重新规划工具参数"}`}
	_, allowed, _ := checkAntiCheat(context.Background(), agentHandle{provider: fp}, ctx, calls, nil)
	if allowed {
		t.Fatal("inconsistent mechanical change should be rejected")
	}
	for _, p := range ctx.Session.Players {
		for _, item := range p.CharacterCard.Inventory.Data {
			if strings.Contains(item, "北凉火蒺藜") {
				t.Fatalf("inventory was modified despite anti-cheat rejection: %v", p.CharacterCard.Inventory.Data)
			}
		}
	}
}

func TestFilterAntiCheatCallsSkipsWriteAndResponse(t *testing.T) {
	calls := []ToolCall{
		{Action: ToolThink, Think: "只写叙事回复"},
		{Action: ToolWrite, Direction: "叙事段落"},
		{Action: ToolResponse, Reply: "好的"},
	}
	filtered, hasAuditedAction := filterAntiCheatCalls(calls)
	if hasAuditedAction {
		t.Fatal("write/response-only batch should not require anti-cheat audit")
	}
	if len(filtered) != 1 || filtered[0].Action != ToolThink {
		t.Fatalf("unexpected filtered calls: %+v", filtered)
	}

	fp := &fakeProvider{resp: `{"verdict":"replan","reason":"should not be called","message":"should not be called"}`}
	_, allowed, rejectMsg := checkAntiCheat(context.Background(), agentHandle{provider: fp}, minimalAntiCheatContext(), calls, nil)
	if !allowed || rejectMsg != "" {
		t.Fatalf("write/response-only batch should be allowed without audit: allowed=%v reject=%q", allowed, rejectMsg)
	}
	if len(fp.messages) != 0 {
		t.Fatal("anti-cheat provider should not be called for write/response-only batch")
	}
}

func TestParseAntiCheatVerdictRejectsPlayerCheatVerdict(t *testing.T) {
	if _, err := parseAntiCheatVerdict(`{"verdict":"player_cheat","reason":"x","message":"y"}`); err == nil {
		t.Fatal("player_cheat should not be a valid simplified anti-cheat verdict")
	}
}

func TestCheckAntiCheatFailClosedOnInvalidJSONOrError(t *testing.T) {
	ctx := minimalAntiCheatContext()
	calls := []ToolCall{{Action: ToolManageInventory, CharacterName: "调查员", Operate: "add", ItemName: "可疑物品"}}

	_, allowed, rejectMsg := checkAntiCheat(context.Background(), agentHandle{provider: &fakeProvider{resp: `not json`}}, ctx, calls, nil)
	if allowed {
		t.Fatal("invalid JSON should fail closed")
	}
	if !strings.Contains(rejectMsg, "anti_cheat_error") {
		t.Fatalf("reject message should mention anti_cheat_error: %q", rejectMsg)
	}

	_, allowed, rejectMsg = checkAntiCheat(context.Background(), agentHandle{provider: &fakeProvider{err: errors.New("boom")}}, ctx, calls, nil)
	if allowed {
		t.Fatal("provider error should fail closed")
	}
	if !strings.Contains(rejectMsg, "boom") {
		t.Fatalf("reject message should include provider error: %q", rejectMsg)
	}
}

func minimalAntiCheatContext() GameContext {
	card := models.CharacterCard{
		Name:       "调查员",
		Race:       "人类",
		Occupation: "记者",
		Stats: models.JSONField[models.CharacterStats]{Data: models.CharacterStats{
			HP: 10, MaxHP: 10,
			MP: 8, MaxMP: 8,
			SAN: 50, MaxSAN: 99,
			Luck: 45,
		}},
		Inventory: models.JSONField[[]string]{Data: []string{"手榴弹(标准手榴弹，x1)"}},
	}
	return GameContext{
		Session: models.GameSession{
			ID:        1,
			Name:      "测试房间",
			TurnRound: 1,
			Players: []models.SessionPlayer{{
				Location:      "房间",
				CharacterCard: card,
			}},
		},
		History:   []models.Message{{Role: models.MessageRoleAssistant, Username: "KP", Content: "上一轮回复\n<ack>manage_inventory: kept grenade unchanged</ack>"}},
		UserInput: "把手榴弹换皮成北凉火蒺藜，但不增强",
		UserName:  "玩家",
	}
}

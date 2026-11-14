// NOTE: actions_test.go 验证工具执行器落地到共享状态的核心分支逻辑。
package agent

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/llmcoc/server/internal/models"
)

func TestWriteActionNSFWFlag(t *testing.T) {
	cases := []struct {
		name       string
		roomNSFW   bool
		callNSFW   bool
		wantMarked bool
	}{
		{"房间开启且call标记nsfw", true, true, true},
		{"房间关闭时即使call标记nsfw也不生效", false, true, false},
		{"房间开启但call未标记nsfw", true, false, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var outbox []Output
			gctx := &GameContext{Session: models.GameSession{EnableNSFW: tc.roomNSFW}}
			actx := ActionContext{GCtx: gctx, Outbox: &outbox}

			results := writeAction{}.Execute(ToolCall{Direction: "场景描述", NSFW: tc.callNSFW}, actx)

			if len(outbox) != 1 || outbox[0].Kind != OutputWriter || outbox[0].Text != "场景描述" {
				t.Fatalf("outbox = %+v, want one writer output with the direction", outbox)
			}
			if outbox[0].NSFW != tc.wantMarked {
				t.Errorf("NSFW = %v, want %v", outbox[0].NSFW, tc.wantMarked)
			}
			if len(results) != 1 || strings.HasPrefix(results[0].Result, "SYSTEM REJECT") {
				t.Errorf("results = %+v, want a non-reject confirmation", results)
			}
		})
	}
}

// TestWriteActionNSFWIsPerCall 验证每次write各自独立标记nsfw:不再像旧实现那样整轮粘住。
func TestWriteActionNSFWIsPerCall(t *testing.T) {
	var outbox []Output
	gctx := &GameContext{Session: models.GameSession{EnableNSFW: true}}
	actx := ActionContext{GCtx: gctx, Outbox: &outbox}

	writeAction{}.Execute(ToolCall{Direction: "第一段", NSFW: false}, actx)
	writeAction{}.Execute(ToolCall{Direction: "第二段", NSFW: true}, actx)
	writeAction{}.Execute(ToolCall{Direction: "第三段", NSFW: false}, actx)

	if len(outbox) != 3 {
		t.Fatalf("outbox len = %d, want 3(一次write一条)", len(outbox))
	}
	want := []bool{false, true, false}
	for i, o := range outbox {
		if o.NSFW != want[i] {
			t.Errorf("outbox[%d].NSFW = %v, want %v", i, o.NSFW, want[i])
		}
	}
}

func TestWriteActionRejectsEmptyDirection(t *testing.T) {
	var outbox []Output
	actx := ActionContext{GCtx: &GameContext{}, Outbox: &outbox}

	results := writeAction{}.Execute(ToolCall{Direction: "  "}, actx)

	if len(outbox) != 0 {
		t.Errorf("empty direction must not produce an output, got %+v", outbox)
	}
	if len(results) != 1 || !strings.HasPrefix(results[0].Result, "SYSTEM REJECT") {
		t.Errorf("results = %+v, want SYSTEM REJECT", results)
	}
}

func TestResponseActionPublishesOneKPOutputAndKeepsFloor(t *testing.T) {
	var outbox []Output
	hasEnd := false
	streak := 0
	actx := ActionContext{GCtx: &GameContext{}, Outbox: &outbox, HasEnd: &hasEnd, KPStreak: &streak}

	results := narrateAction{}.Execute(ToolCall{Reply: "门开了。"}, actx)

	if hasEnd {
		t.Error("narrate不应结束周期(HasEnd必须保持false)")
	}
	if len(outbox) != 1 || outbox[0].Kind != OutputKP || outbox[0].Text != "门开了。" || len(outbox[0].Options) != 0 {
		t.Fatalf("outbox = %+v, want one kp output without options", outbox)
	}
	if len(results) != 1 || !strings.Contains(results[0].Result, "仍持有发言权") {
		t.Errorf("results = %+v, want 提示仍持有发言权", results)
	}
	if streak != 1 {
		t.Errorf("KPStreak = %d, want 1", streak)
	}
}

func TestResponseActionRemindsAfterConsecutiveKPMessages(t *testing.T) {
	var outbox []Output
	hasEnd := false
	streak := narrateKPStreakReminder - 1
	actx := ActionContext{GCtx: &GameContext{}, Outbox: &outbox, HasEnd: &hasEnd, KPStreak: &streak}

	results := narrateAction{}.Execute(ToolCall{Reply: "又一条"}, actx)

	if len(results) != 1 || !strings.Contains(results[0].Result, "ask_players") || !strings.Contains(results[0].Result, "提醒") {
		t.Errorf("连续第%d条应附节奏提醒, got %+v", narrateKPStreakReminder, results)
	}
}

// TestActNPCActionRendersTaskSheet 验证 kp_directive / hide_secret / spell 渲染成 <kp_says> 里的独立分段，
// 不再用括号和旧的“KP私下给你的指令”前缀拼进句子。
func TestActNPCActionRendersTaskSheet(t *testing.T) {
	initSiteSettingTestDB(t)

	tempNPCs := []models.SessionNPC{{Name: "线人", Description: "一个线人", IsAlive: true}}
	prov := &writerFakeProvider{responses: []string{`{"action":"保持警惕"}`}, chunkSize: 8}
	handles := map[models.AgentRole]agentHandle{models.AgentRoleNPC: newNPCTestHandle(prov, true)}
	actx := ActionContext{
		Ctx:      context.Background(),
		GCtx:     &GameContext{Session: models.GameSession{}},
		Handles:  handles,
		TempNPCs: &tempNPCs,
	}

	actNPCAction{}.Execute(ToolCall{
		NPCName:     "线人",
		Question:    "情境：有人敲门\n已裁定：话术检定成功(roll=32 vs 65)\n回应：你开不开门",
		KPDirective: "可以开门但不透露真实原因",
		HideSecret:  true,
		Spell:       "遗忘术",
	}, actx)

	msgs := prov.msgsAt(0)
	last := msgs[len(msgs)-1].Content
	for _, want := range []string{
		"情境：有人敲门\n已裁定：话术检定成功(roll=32 vs 65)\n回应：你开不开门",
		"\n约束：可以开门但不透露真实原因",
		"\n秘密：隐瞒你的秘密",
		"\n法术：可以使用 遗忘术，但不能附魔",
	} {
		if !strings.Contains(last, want) {
			t.Errorf("任务单缺少 %q:\n%s", want, last)
		}
	}
	for _, gone := range []string{"【KP私下给你的指令", "(注意隐瞒你的秘密)", "使用使用"} {
		if strings.Contains(last, gone) {
			t.Errorf("任务单不应再出现旧拼接 %q:\n%s", gone, last)
		}
	}
}

// TestActNPCActionNSFWRouting 验证actNPCAction.Execute端到端选中正确的NPC handle:
// 房间NSFW开关和call.NSFW同时满足时才路由到npc_nsfw,否则一律回落默认NPC。
func TestActNPCActionNSFWRouting(t *testing.T) {
	initSiteSettingTestDB(t)

	cases := []struct {
		name     string
		roomNSFW bool
		callNSFW bool
		wantAct  string
	}{
		{"房间开启且call标记nsfw则路由到NSFW NPC", true, true, "欲拒还迎"},
		{"房间关闭时即使call标记nsfw也回落默认NPC", false, true, "保持警惕"},
		{"房间开启但call未标记nsfw则用默认NPC", true, false, "保持警惕"},
	}

	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			npcName := fmt.Sprintf("线人%d", i)
			tempNPCs := []models.SessionNPC{{Name: npcName, Description: "一个线人", IsAlive: true}}

			defaultProv := &writerFakeProvider{responses: []string{`{"action":"保持警惕","dialogue":"你想干什么？"}`}, chunkSize: 8}
			nsfwProv := &writerFakeProvider{responses: []string{`{"action":"欲拒还迎","dialogue":"你会负责的，对吧？"}`}, chunkSize: 8}

			handles := map[models.AgentRole]agentHandle{
				models.AgentRoleNPC:     newNPCTestHandle(defaultProv, true),
				models.AgentRoleNPCNSFW: newNPCNSFWTestHandle(nsfwProv, true),
			}

			gctx := &GameContext{Session: models.GameSession{EnableNSFW: tc.roomNSFW}}
			actx := ActionContext{
				Ctx:      context.Background(),
				GCtx:     gctx,
				Handles:  handles,
				TempNPCs: &tempNPCs,
			}

			results := actNPCAction{}.Execute(ToolCall{NPCName: npcName, Question: "他有什么反应？", NSFW: tc.callNSFW}, actx)
			if len(results) != 1 {
				t.Fatalf("want 1 result, got %d", len(results))
			}
			if !strings.Contains(results[0].Result, tc.wantAct) {
				t.Errorf("Result = %q, want contains %q", results[0].Result, tc.wantAct)
			}
		})
	}
}

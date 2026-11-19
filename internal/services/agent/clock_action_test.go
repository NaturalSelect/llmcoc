package agent

import (
	"strings"
	"testing"

	"github.com/llmcoc/server/internal/models"
)

func clockBoolPtr(v bool) *bool { return &v }

func TestUpdateClockAction_AdvanceSetRemoveAndVisibility(t *testing.T) {
	sess := models.GameSession{
		ID: 0,
		Scenario: models.Scenario{
			Content: models.JSONField[models.ScenarioContent]{
				Data: models.ScenarioContent{
					Mechanics: []models.MechanicData{
						{
							Name:        "血月仪式",
							Type:        "clock",
							Description: "教团完成献祭的倒计时",
							Stages: []models.MechanicStage{
								{Label: "低语渐起"},
								{Label: "祭坛点亮"},
								{Label: "裂隙洞开"},
								{Label: "外神降临"},
							},
						},
					},
				},
			},
		},
	}
	gctx := &GameContext{Session: sess}
	actx := ActionContext{GCtx: gctx, Sid: 1}
	act := updateClockAction{}

	// 1. 首次按模组机制名推进：自动推断 max=4、description 与阶段标签
	res := act.Execute(ToolCall{Action: ToolUpdateClock, ClockName: "血月仪式"}, actx)
	if len(res) != 1 || !strings.Contains(res[0].Result, `current="1" max="4"`) || !strings.Contains(res[0].Result, `stage="低语渐起"`) {
		t.Fatalf("unexpected create result: %+v", res)
	}
	if len(res[0].Events) != 1 || res[0].Events[0].Type != "clock" || res[0].Events[0].Op != "create" || res[0].Events[0].To == nil || *res[0].Events[0].To != 1 || res[0].Events[0].Amount != 4 {
		t.Fatalf("unexpected public create event: %+v", res[0].Events)
	}
	if len(gctx.Session.Clocks.Data) != 1 || gctx.Session.Clocks.Data[0].Description != "教团完成献祭的倒计时" {
		t.Fatalf("expected inferred description on clock, got %+v", gctx.Session.Clocks.Data)
	}

	// 2. 继续推进到满格并截断上限
	res = act.Execute(ToolCall{Action: ToolUpdateClock, ClockName: "血月仪式", ClockDelta: intPtr(5)}, actx)
	if len(res) != 1 || !strings.Contains(res[0].Result, `current="4" max="4"`) || !strings.Contains(res[0].Result, `full="true"`) {
		t.Fatalf("expected clamped full clock, got %+v", res)
	}
	if len(res[0].Events) != 1 || res[0].Events[0].Op != "update" || res[0].Events[0].From == nil || *res[0].Events[0].From != 1 || res[0].Events[0].To == nil || *res[0].Events[0].To != 4 || res[0].Events[0].Detail != "外神降临" {
		t.Fatalf("unexpected advance event: %+v", res[0].Events)
	}

	// 3. 建立仅 KP 可见的暗钟：绝不产出公开 GameEvent
	res = act.Execute(ToolCall{
		Action:       ToolUpdateClock,
		ClockName:    "教团警戒度",
		Operate:      "set",
		ClockValue:   intPtr(1),
		ClockMax:     3,
		ClockVisible: clockBoolPtr(false),
		ClockDesc:    "发现入侵者后增长",
	}, actx)
	if len(res) != 1 || !strings.Contains(res[0].Result, `visibility="暗钟(仅KP可见)"`) {
		t.Fatalf("unexpected hidden clock result: %+v", res)
	}
	if len(res[0].Events) != 0 {
		t.Fatalf("hidden clock must not emit public events, got %+v", res[0].Events)
	}

	// 4. 暗钟推进也不产出公开事件
	res = act.Execute(ToolCall{Action: ToolUpdateClock, ClockName: "教团警戒度", ClockDelta: intPtr(1)}, actx)
	if len(res[0].Events) != 0 {
		t.Fatalf("hidden clock advance must not emit public events, got %+v", res[0].Events)
	}

	// 5. buildKPTurnOpening 注入 <active_clocks> 块包含公开钟与暗钟
	msgs := buildKPTurnOpening(*gctx, 1, nil, "", nil, nil)
	if !strings.Contains(msgs[0].Content, "<active_clocks>") ||
		!strings.Contains(msgs[0].Content, "血月仪式 [4/4]（公开）") ||
		!strings.Contains(msgs[0].Content, "教团警戒度 [2/3]（暗钟·仅KP可见）") {
		t.Fatalf("buildKPTurnOpening missing active_clocks content: %s", msgs[0].Content)
	}

	// 6. 移除暗钟不发公开事件；移除公开钟发出 remove 事件
	res = act.Execute(ToolCall{Action: ToolUpdateClock, ClockName: "教团警戒度", Operate: "remove"}, actx)
	if len(res[0].Events) != 0 {
		t.Fatalf("removing hidden clock must not emit public event, got %+v", res[0].Events)
	}
	res = act.Execute(ToolCall{Action: ToolUpdateClock, ClockName: "血月仪式", Operate: "remove"}, actx)
	if len(res[0].Events) != 1 || res[0].Events[0].Op != "remove" || res[0].Events[0].Name != "血月仪式" {
		t.Fatalf("removing public clock should emit remove event, got %+v", res[0].Events)
	}
	if len(gctx.Session.Clocks.Data) != 0 {
		t.Fatalf("expected all clocks removed, got %+v", gctx.Session.Clocks.Data)
	}

	// 7. 移除不存在时钟或空名均拒绝
	res = act.Execute(ToolCall{Action: ToolUpdateClock, ClockName: "不存在", Operate: "remove"}, actx)
	if !strings.HasPrefix(res[0].Result, "SYSTEM REJECT:") {
		t.Fatalf("expected reject for missing clock, got %+v", res)
	}
	res = act.Execute(ToolCall{Action: ToolUpdateClock}, actx)
	if !strings.HasPrefix(res[0].Result, "SYSTEM REJECT:") {
		t.Fatalf("expected reject for empty clock_name, got %+v", res)
	}
}

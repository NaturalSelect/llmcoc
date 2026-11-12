// NOTE: npc_table_test.go 验证 AI 玩家的提示词、场景卡与声明格式。
package agent

import (
	"strings"
	"testing"

	"github.com/llmcoc/server/internal/models"
)

func TestNPCPromptFramesTRPGPlayer(t *testing.T) {
	for _, want := range []string{"AI玩家", "跑团", "守秘人", `"check"`, `"ooc"`, `"intent"`, "场外信息", "<writer_output>", "<npc_role>", "<kp_says>"} {
		if !strings.Contains(npcDefaultPrompt, want) {
			t.Errorf("npcDefaultPrompt 缺少 %q", want)
		}
	}
	if strings.Contains(npcNSFWPromptSuffix, `{"action": "...", "dialogue": "..."}`) {
		t.Error("NSFW 后缀不应再写死只有 action/dialogue 的旧格式")
	}
}

func TestBuildNPCScene(t *testing.T) {
	gctx := GameContext{Session: models.GameSession{
		TurnRound:     1,
		GameStartTime: "1924-09-03T08:00",
		GameTime:      "1924-09-03T09:30",
		Players: []models.SessionPlayer{
			{Location: "酒馆", CharacterCard: models.CharacterCard{Name: "阿明", Appearance: "戴圆框眼镜的瘦高青年"}},
			{Location: "码头", CharacterCard: models.CharacterCard{Name: "小红"}},
			{Location: "酒馆", CharacterCard: models.CharacterCard{Name: "老王", WoundState: "dead"}},
		},
	}}
	tempNPCs := []models.SessionNPC{
		{Name: "老板", Location: "酒馆", IsAlive: true},
		{Name: "醉汉", Location: "酒馆", IsAlive: true},
		{Name: "尸体", Location: "酒馆", WoundState: "dead"},
		{Name: "船夫", Location: "码头", IsAlive: true},
		{Name: "游魂", IsAlive: true},
	}

	scene := buildNPCScene(gctx, "老板", tempNPCs)
	for _, want := range []string{"时间:1924-09-03T09:30(周三，距开局已过1h30m)", "所在位置:酒馆", "阿明(戴圆框眼镜的瘦高青年)", "醉汉"} {
		if !strings.Contains(scene, want) {
			t.Errorf("场景卡缺少 %q:\n%s", want, scene)
		}
	}
	for _, bad := range []string{"小红", "老王", "尸体", "船夫", "其他NPC:老板"} {
		if strings.Contains(scene, bad) {
			t.Errorf("场景卡不应包含 %q:\n%s", bad, scene)
		}
	}

	noLoc := buildNPCScene(gctx, "游魂", tempNPCs)
	if !strings.Contains(noLoc, "未记录") || strings.Contains(noLoc, "阿明") {
		t.Errorf("没有位置的NPC不应列名单:\n%s", noLoc)
	}
}

func TestFormatNPCActionDeclaration(t *testing.T) {
	got := formatNPCAction(NPCAction{NPCName: "老板", Intent: "打发走调查员", Action: "把杯子重重放下", Dialogue: "打烊了。", Check: "话术:让他们相信打烊了", OOC: "他其实在等邪教徒"})
	for _, want := range []string{
		`<npc_declaration npc="老板" adjudicated="false">`,
		"<intent>打发走调查员</intent>",
		"<action>把杯子重重放下</action>",
		"<dialogue>打烊了。</dialogue>",
		"<check>话术:让他们相信打烊了</check>",
		`<ooc visibility="kp_only">他其实在等邪教徒</ooc>`,
		"</npc_declaration>",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("声明缺少 %q: %s", want, got)
		}
	}
	bare := formatNPCAction(NPCAction{NPCName: "老板", Action: "沉默"})
	for _, bad := range []string{"<intent", "<dialogue", "<check", "<ooc"} {
		if strings.Contains(bare, bad) {
			t.Errorf("空字段不应输出 %q: %s", bad, bare)
		}
	}
	if !strings.Contains(bare, "<action>沉默</action>") {
		t.Errorf("非空字段应输出: %s", bare)
	}
	escaped := formatNPCAction(NPCAction{NPCName: `a"b`, Dialogue: "<走>&开"})
	if !strings.Contains(escaped, `npc="a&quot;b"`) || !strings.Contains(escaped, "<dialogue>&lt;走&gt;&amp;开</dialogue>") {
		t.Errorf("属性与正文应转义: %s", escaped)
	}
}

func TestDirectorTreatsNPCAsAIPlayer(t *testing.T) {
	tool := actNPCTool().def.Description
	for _, want := range []string{"AI玩家", "ooc", "申请检定", "[NPC-SKILL-CHECK]"} {
		if !strings.Contains(tool, want) {
			t.Errorf("act_npc 工具说明缺少 %q", want)
		}
	}
	for _, want := range []string{"[NPC] 在场的NPC由AI玩家扮演", "场外话永远不进入玩家可见内容", "AI玩家在声明里申请检定"} {
		if !strings.Contains(kpSystemPrompt, want) {
			t.Errorf("kpSystemPrompt 缺少 %q", want)
		}
	}
	if !strings.Contains(kpTurnReminder, "由AI玩家扮演") {
		t.Error("kpTurnReminder 应提示 NPC 由 AI 玩家扮演")
	}
}

// NOTE: npc_table_test.go 验证 AI 玩家的提示词、场景卡、牌桌记录水位与声明格式。
package agent

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/llmcoc/server/internal/models"
	"github.com/llmcoc/server/internal/services/llm"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func TestNPCPromptFramesTRPGPlayer(t *testing.T) {
	for _, want := range []string{"AI玩家", "跑团", "守秘人", `"check"`, `"ooc"`, `"intent"`, "场外信息", "<table_log>", "<kp_says>"} {
		if !strings.Contains(npcDefaultPrompt, want) {
			t.Errorf("npcDefaultPrompt 缺少 %q", want)
		}
	}
	if strings.Contains(npcNSFWPromptSuffix, `{"action": "...", "dialogue": "..."}`) {
		t.Error("NSFW 后缀不应再写死只有 action/dialogue 的旧格式")
	}
}

func seedTableMsg(t *testing.T, m models.Message) models.Message {
	t.Helper()
	if m.Status == "" {
		m.Status = models.MessageStatusDone
	}
	if err := models.DB.Create(&m).Error; err != nil {
		t.Fatalf("create message: %v", err)
	}
	return m
}

func TestLoadNPCTableLogWatermark(t *testing.T) {
	initAgentTestDB(t)
	const sid = 601
	seedTableMsg(t, models.Message{SessionID: sid, Role: models.MessageRoleAssistant, Kind: models.MessageKindKP, Content: "酒馆里灯光昏暗。"})
	seedTableMsg(t, models.Message{SessionID: sid, Role: models.MessageRoleAssistant, Kind: models.MessageKindWriter, Content: "老板擦着杯子。"})
	seedTableMsg(t, models.Message{SessionID: sid, Role: models.MessageRoleSystem, Kind: models.MessageKindSystem, Content: "某人加入了房间"})
	player := seedTableMsg(t, models.Message{SessionID: sid, Role: models.MessageRoleUser, Kind: models.MessageKindPlayer, Username: "阿明", Content: "我走向吧台"})
	pending := seedTableMsg(t, models.Message{SessionID: sid, Role: models.MessageRoleAssistant, Kind: models.MessageKindWriter, Status: models.MessageStatusPending})
	seedTableMsg(t, models.Message{SessionID: sid, Role: models.MessageRoleAssistant, Kind: models.MessageKindKP, Content: "你想问什么？"})
	seedTableMsg(t, models.Message{SessionID: sid + 1, Role: models.MessageRoleAssistant, Kind: models.MessageKindKP, Content: "别的房间"})

	log, seen := loadNPCTableLog(sid, 0)
	for _, want := range []string{"[KP]: 酒馆里灯光昏暗。", "[正文]: 老板擦着杯子。", "[阿明]: 我走向吧台"} {
		if !strings.Contains(log, want) {
			t.Errorf("第1次记录缺少 %q:\n%s", want, log)
		}
	}
	for _, bad := range []string{"你想问什么", "加入了房间", "别的房间"} {
		if strings.Contains(log, bad) {
			t.Errorf("第1次记录不应包含 %q:\n%s", bad, log)
		}
	}
	if seen != player.ID {
		t.Fatalf("水位 = %d, want %d(停在 pending writer 之前)", seen, player.ID)
	}

	models.DB.Model(&models.Message{}).Where("id = ?", pending.ID).Updates(map[string]any{"status": models.MessageStatusDone, "content": "老板抬起头。"})
	failed := seedTableMsg(t, models.Message{SessionID: sid, Role: models.MessageRoleAssistant, Kind: models.MessageKindWriter, Status: models.MessageStatusFailed, Content: "x"})

	log2, seen2 := loadNPCTableLog(sid, seen)
	if !strings.Contains(log2, "[正文]: 老板抬起头。") || !strings.Contains(log2, "[KP]: 你想问什么？") {
		t.Errorf("第2次记录应包含剩下两条:\n%s", log2)
	}
	if strings.Contains(log2, "酒馆里灯光昏暗") {
		t.Errorf("第2次记录不应重复已读内容:\n%s", log2)
	}
	if seen2 != failed.ID {
		t.Errorf("水位 = %d, want %d(越过失败的 writer)", seen2, failed.ID)
	}

	log3, seen3 := loadNPCTableLog(sid, seen2)
	if log3 != "" || seen3 != seen2 {
		t.Errorf("没有新消息时应返回空记录和原水位, got %q %d", log3, seen3)
	}
}

func TestLoadNPCTableLogTruncatesOldest(t *testing.T) {
	initAgentTestDB(t)
	const sid = 602
	long := strings.Repeat("字", 900)
	for i := 0; i < 8; i++ {
		seedTableMsg(t, models.Message{SessionID: sid, Role: models.MessageRoleAssistant, Kind: models.MessageKindKP, Content: long})
	}
	last := seedTableMsg(t, models.Message{SessionID: sid, Role: models.MessageRoleAssistant, Kind: models.MessageKindKP, Content: "最后一句"})

	log, seen := loadNPCTableLog(sid, 0)
	if !strings.HasPrefix(log, "(更早的") {
		t.Errorf("超出预算时开头应注明省略:\n%.40s", log)
	}
	if !strings.Contains(log, "最后一句") {
		t.Error("应保留最新的记录")
	}
	if n := len([]rune(log)); n > npcTableLogRunes+100 {
		t.Errorf("记录长度 %d 超出预算", n)
	}
	if seen != last.ID {
		t.Errorf("被截掉的旧记录也算已读, 水位 = %d, want %d", seen, last.ID)
	}
}

func TestLoadNPCTableLogDBErrorIsEmpty(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	prev := models.DB
	models.DB = db
	t.Cleanup(func() { models.DB = prev })

	log, seen := loadNPCTableLog(1, 7)
	if log != "" || seen != 7 {
		t.Errorf("messages 表不存在时应返回空记录和原水位, got %q %d", log, seen)
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

type failingJSONProvider struct{ sequentialFakeProvider }

func (p *failingJSONProvider) JsonChat(context.Context, string, []llm.ChatMessage) (string, error) {
	return "", errors.New("boom")
}

func TestRunNPC_AdvancesSeenMessageID(t *testing.T) {
	initAgentTestDB(t)
	const sid = 603
	if err := models.DB.Create(&models.GameSession{ID: sid}).Error; err != nil {
		t.Fatalf("create session: %v", err)
	}
	msg := seedTableMsg(t, models.Message{SessionID: sid, Role: models.MessageRoleAssistant, Kind: models.MessageKindKP, Content: "门被推开了。"})
	tempNPCs := []models.SessionNPC{{Name: "线人", Description: "一个线人", IsAlive: true}}
	gctx := GameContext{Session: models.GameSession{ID: sid}}

	failing := newNPCTestHandle(&failingJSONProvider{}, true)
	if _, err := runNPC(context.Background(), failing, gctx, "线人", "有人进门。", tempNPCs, false); err == nil {
		t.Fatal("provider 出错时 runNPC 应返回错误")
	}
	if got := LoadContext(sid, npcAgentKey("线人"), nil, ContextOptions{}).SeenMessageID(); got != 0 {
		t.Fatalf("失败调用不应推进水位, got %d", got)
	}

	prov := &sequentialFakeProvider{jsonResponses: []string{`{"intent":"看清来人","action":"眯起眼","dialogue":"谁？","check":"侦查:看清来人的脸","ooc":""}`}}
	action, err := runNPC(context.Background(), newNPCTestHandle(prov, true), gctx, "线人", "有人进门。", tempNPCs, false)
	if err != nil {
		t.Fatalf("runNPC: %v", err)
	}
	if action.Check != "侦查:看清来人的脸" || action.Intent != "看清来人" {
		t.Errorf("声明字段解析错误: %+v", action)
	}
	opening := prov.recordedMessages[0][len(prov.recordedMessages[0])-1].Content
	for _, want := range []string{"<scene>", "<table_log", "[KP]: 门被推开了。", "<kp_says>\n有人进门。"} {
		if !strings.Contains(opening, want) {
			t.Errorf("opening 缺少 %q:\n%s", want, opening)
		}
	}
	if got := LoadContext(sid, npcAgentKey("线人"), nil, ContextOptions{}).SeenMessageID(); got != msg.ID {
		t.Errorf("成功调用后水位 = %d, want %d", got, msg.ID)
	}
}

func TestFormatNPCActionDeclaration(t *testing.T) {
	got := formatNPCAction(NPCAction{NPCName: "老板", Intent: "打发走调查员", Action: "把杯子重重放下", Dialogue: "打烊了。", Check: "话术:让他们相信打烊了", OOC: "他其实在等邪教徒"})
	for _, want := range []string{"未裁定", "老板", "意图:打发走调查员", "动作:把杯子重重放下", `台词:"打烊了。"`, "申请检定:话术", "场外(仅KP可见):他其实在等邪教徒"} {
		if !strings.Contains(got, want) {
			t.Errorf("声明缺少 %q: %s", want, got)
		}
	}
	bare := formatNPCAction(NPCAction{NPCName: "老板", Action: "沉默"})
	for _, bad := range []string{"意图:", "台词:", "申请检定:", "场外"} {
		if strings.Contains(bare, bad) {
			t.Errorf("空字段不应输出 %q: %s", bad, bare)
		}
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

package handlers

// NOTE: Writer 配图(paint)的服务端部分：占位分配、后台出图、结果写进 meta.illustrations 并广播 illustration 事件，
// 以及续写轮中途断流时 liveWriter 只撤回当前轮的正文。每个启动了周期的测试都要等到 floor_end 再返回。

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/llmcoc/server/internal/models"
	"github.com/llmcoc/server/internal/services/agent"
	"github.com/llmcoc/server/internal/services/imagestore"
)

func illustrationRunner(t *testing.T, user1ID uint, painter func(context.Context, agent.GameContext, agent.ImagePromptRequest) (string, error), afterStart func()) *fakeRunner {
	t.Helper()
	runner := &fakeRunner{painter: painter}
	runner.writer = func(ctx context.Context, _ agent.GameContext, _ string, _ bool, onToken func(string)) (string, error) {
		start := agent.WriterIllustratorFrom(ctx)
		if start == nil {
			t.Error("writer ctx must carry an illustrator")
			return "", errors.New("no illustrator")
		}
		url := start(agent.ImagePromptRequest{Prompt: "书房", Aspect: "landscape"})
		text := "他推开门。\n\n![书房](" + url + ")"
		onToken(text)
		if afterStart != nil {
			afterStart()
		}
		return text, nil
	}
	runner.run = func(_ context.Context, gctx agent.GameContext) (agent.RunOutput, error) {
		publish(t, gctx, agent.RoundOutputs{Floor: 1, Items: []agent.Output{{Kind: agent.OutputWriter, Text: "推门"}}})
		return agent.RunOutput{Messages: 1, Ask: &agent.AskResult{UserIDs: []uint{user1ID}, Names: []string{"爱丽丝"}}}, nil
	}
	return runner
}

func storedIllustrations(t *testing.T, sessionID uint) []models.Illustration {
	t.Helper()
	m := loadMessages(t, sessionID)[0]
	if m.Meta.Data == nil {
		return nil
	}
	return m.Meta.Data.Illustrations
}

func TestFloor_IllustrationArrivesAfterWriterFinished(t *testing.T) {
	initTestDB(t)
	t.Cleanup(imagestore.SetDefaultDir(t.TempDir()))
	sessionID, user1ID, _ := seedTwoPlayerSession(t, "爱丽丝", "鲍勃")

	releasePainter := make(chan struct{})
	runner := illustrationRunner(t, user1ID, func(context.Context, agent.GameContext, agent.ImagePromptRequest) (string, error) {
		<-releasePainter
		return testPNGDataURL, nil
	}, nil)
	tap := tapRoom(t, sessionID)

	decodeChatAccepted(t, postKPTurn(hostRouter(t, NewSessionHandlers(runner), sessionID), sessionID))
	tap.waitCount("message_update", 1)
	tap.waitCount("floor_end", 1)

	m := loadMessages(t, sessionID)[0]
	if m.Status != models.MessageStatusDone || m.Content != "他推开门。\n\n![书房](illust://1)" {
		t.Fatalf("writer message = %+v", m)
	}
	if got := storedIllustrations(t, sessionID); len(got) != 0 {
		t.Fatalf("a picture still being drawn must not be stored, got %+v", got)
	}
	if len(tap.ofType("illustration")) != 0 {
		t.Fatal("illustration event arrived before the picture was drawn")
	}

	close(releasePainter)
	tap.waitCount("illustration", 1)

	ev := tap.ofType("illustration")[0]
	if ev.Seq == 0 {
		t.Error("illustration must be a durable event so reconnecting clients can replay it")
	}
	data := ev.Data.(illustrationEvent)
	if data.MessageID != m.ID || data.Slot != 1 || data.Status != illustrationDone || !imagestore.ValidHash(data.Hash) {
		t.Errorf("illustration event = %+v", data)
	}
	got := storedIllustrations(t, sessionID)
	if len(got) != 1 || got[0] != (models.Illustration{Slot: 1, Status: illustrationDone, Hash: data.Hash}) {
		t.Errorf("stored illustrations = %+v", got)
	}
	if after := loadMessages(t, sessionID)[0]; after.Status != models.MessageStatusDone || after.Content != m.Content {
		t.Errorf("settling the illustration must not change the writer text: %+v", after)
	}
}

func TestFloor_IllustrationSurvivesWriterFinishingLater(t *testing.T) {
	initTestDB(t)
	t.Cleanup(imagestore.SetDefaultDir(t.TempDir()))
	sessionID, user1ID, _ := seedTwoPlayerSession(t, "爱丽丝", "鲍勃")

	painted := make(chan struct{})
	runner := illustrationRunner(t, user1ID, func(context.Context, agent.GameContext, agent.ImagePromptRequest) (string, error) {
		defer close(painted)
		return testPNGDataURL, nil
	}, func() {
		<-painted
		deadline := time.Now().Add(5 * time.Second)
		for len(storedIllustrations(t, sessionID)) == 0 && time.Now().Before(deadline) {
			time.Sleep(10 * time.Millisecond)
		}
	})
	tap := tapRoom(t, sessionID)

	decodeChatAccepted(t, postKPTurn(hostRouter(t, NewSessionHandlers(runner), sessionID), sessionID))
	tap.waitCount("message_update", 1)
	tap.waitCount("floor_end", 1)

	m := loadMessages(t, sessionID)[0]
	if m.Status != models.MessageStatusDone || m.Content != "他推开门。\n\n![书房](illust://1)" {
		t.Fatalf("writer message = %+v", m)
	}
	if got := storedIllustrations(t, sessionID); len(got) != 1 || got[0].Status != illustrationDone {
		t.Errorf("finishing the writer must keep the illustration saved earlier, got %+v", got)
	}
}

func TestFloor_IllustrationFailureIsRecorded(t *testing.T) {
	initTestDB(t)
	t.Cleanup(imagestore.SetDefaultDir(t.TempDir()))
	sessionID, user1ID, _ := seedTwoPlayerSession(t, "爱丽丝", "鲍勃")

	runner := illustrationRunner(t, user1ID, func(context.Context, agent.GameContext, agent.ImagePromptRequest) (string, error) {
		return "", errors.New("painter down")
	}, nil)
	tap := tapRoom(t, sessionID)

	decodeChatAccepted(t, postKPTurn(hostRouter(t, NewSessionHandlers(runner), sessionID), sessionID))
	tap.waitCount("illustration", 1)
	tap.waitCount("message_update", 1)
	tap.waitCount("floor_end", 1)

	data := tap.ofType("illustration")[0].Data.(illustrationEvent)
	if data.Status != illustrationFailed || data.Hash != "" || data.Slot != 1 {
		t.Errorf("illustration event = %+v", data)
	}
	if got := storedIllustrations(t, sessionID); len(got) != 1 || got[0].Status != illustrationFailed || got[0].Hash != "" {
		t.Errorf("stored illustrations = %+v", got)
	}
}

func TestSettleMessage_ConcurrentIllustrationsAreAllKept(t *testing.T) {
	initTestDB(t)
	sessionID, _, _ := seedTwoPlayerSession(t, "爱丽丝", "鲍勃")
	msg := models.Message{SessionID: sessionID, Role: models.MessageRoleAssistant, Kind: models.MessageKindWriter, Status: models.MessageStatusPending}
	if err := models.DB.Create(&msg).Error; err != nil {
		t.Fatalf("create message: %v", err)
	}

	const n = 8
	var wg sync.WaitGroup
	for slot := 1; slot <= n; slot++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ill := models.Illustration{Slot: slot, Status: illustrationDone, Hash: fmt.Sprintf("%064d", slot)}
			if _, err := settleMessage(msg.ID, func(m *models.Message) { setIllustration(m, ill) }); err != nil {
				t.Errorf("settle slot %d: %v", slot, err)
			}
		}()
	}
	wg.Wait()

	var got models.Message
	if err := models.DB.First(&got, msg.ID).Error; err != nil {
		t.Fatalf("load message: %v", err)
	}
	if got.Meta.Data == nil || len(got.Meta.Data.Illustrations) != n {
		t.Fatalf("illustrations = %+v, want all %d slots kept", got.Meta.Data, n)
	}
}

func TestSetIllustration_ReplacesSameSlot(t *testing.T) {
	var m models.Message
	setIllustration(&m, models.Illustration{Slot: 1, Status: illustrationFailed})
	setIllustration(&m, models.Illustration{Slot: 1, Status: illustrationDone, Hash: "h"})
	if got := m.Meta.Data.Illustrations; len(got) != 1 || got[0].Status != illustrationDone || got[0].Hash != "h" {
		t.Errorf("illustrations = %+v", got)
	}
}

// NOTE: 续写轮中途断流只撤回当前轮：reset 帧带保留的 UTF-16 偏移，之后的 token 从这个偏移接着写，新连接补发的是保留部分加重试的正文。
func TestLiveWriter_ResetToKeepsEarlierRounds(t *testing.T) {
	initTestDB(t)
	sessionID, _, _ := seedTwoPlayerSession(t, "爱丽丝", "鲍勃")
	tap := tapRoom(t, sessionID)

	lw := startLiveWriter(sessionID, 42)
	t.Cleanup(lw.stop)
	lw.append("他推开门。")
	lw.flush()
	lw.append("半截")
	lw.flush()
	lw.resetTo(len("他推开门。"))
	if ev, ok := writerCatchUp(sessionID); !ok || ev.Data.(writerTokenEvent).Text != "他推开门。" {
		t.Errorf("catch-up after reset = %+v ok=%v, want the kept text only", ev, ok)
	}
	lw.append("重来")
	lw.flush()
	if ev, ok := writerCatchUp(sessionID); !ok || ev.Data.(writerTokenEvent).Text != "他推开门。重来" {
		t.Errorf("catch-up after restart = %+v ok=%v", ev, ok)
	}

	tap.waitCount("writer_token", 3)
	var seq []string
	for _, ev := range tap.got {
		switch ev.Type {
		case "writer_token":
			tok := ev.Data.(writerTokenEvent)
			seq = append(seq, fmt.Sprintf("token@%d:%s", tok.Offset, tok.Text))
		case "writer_reset":
			seq = append(seq, fmt.Sprintf("reset@%d", ev.Data.(writerResetEvent).Offset))
		}
	}
	if got, want := fmt.Sprint(seq), "[token@0:他推开门。 token@5:半截 reset@5 token@5:重来]"; got != want {
		t.Errorf("stream events = %s, want %s", got, want)
	}
}

// 撤回点之后还没广播过的正文被丢掉时，已广播的部分和偏移保持不变，不会重复发。
func TestLiveWriter_ResetToAfterSentPartKeepsSentCursor(t *testing.T) {
	initTestDB(t)
	sessionID, _, _ := seedTwoPlayerSession(t, "爱丽丝", "鲍勃")
	tap := tapRoom(t, sessionID)

	lw := startLiveWriter(sessionID, 7)
	t.Cleanup(lw.stop)
	lw.append("abc")
	lw.flush()
	lw.append("def")
	lw.resetTo(3)
	lw.append("X")
	lw.flush()

	tap.waitCount("writer_token", 2)
	tokens := tap.ofType("writer_token")
	if first, second := tokens[0].Data.(writerTokenEvent), tokens[1].Data.(writerTokenEvent); first.Text != "abc" || second.Offset != 3 || second.Text != "X" {
		t.Errorf("tokens = %+v then %+v", first, second)
	}
}

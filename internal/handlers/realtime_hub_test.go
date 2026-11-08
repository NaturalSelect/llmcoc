package handlers

import (
	"sync"
	"testing"
	"time"
)

// resetHubs 清空全局 hub 注册表。测试库每次都从 ID=1 重新开始，不重置的话不同用例会共用同一个会话 hub。
func resetHubs(t *testing.T) {
	t.Helper()
	reset := func() {
		hubs.mu.Lock()
		defer hubs.mu.Unlock()
		hubs.hubs = make(map[uint]*roomHub)
		hubs.floor = make(map[uint]uint64)
	}
	reset()
	t.Cleanup(reset)
}

func recvEvent(t *testing.T, sub *subscriber) roomEvent {
	t.Helper()
	select {
	case ev := <-sub.ch:
		return ev
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for event")
		return roomEvent{}
	}
}

func assertNoEvent(t *testing.T, sub *subscriber) {
	t.Helper()
	select {
	case ev := <-sub.ch:
		t.Fatalf("unexpected event: %+v", ev)
	case <-time.After(50 * time.Millisecond):
	}
}

func replaySeqs(replay []roomEvent) []uint64 {
	seqs := make([]uint64, 0, len(replay))
	for _, ev := range replay {
		seqs = append(seqs, ev.Seq)
	}
	return seqs
}

func assertSeqs(t *testing.T, got []roomEvent, want ...uint64) {
	t.Helper()
	seqs := replaySeqs(got)
	if len(seqs) != len(want) {
		t.Fatalf("seqs = %v, want %v", seqs, want)
	}
	for i := range want {
		if seqs[i] != want[i] {
			t.Fatalf("seqs = %v, want %v", seqs, want)
		}
	}
}

func TestHub_PublishAssignsIncreasingSeqAndDeliversInOrder(t *testing.T) {
	h := newRoomHub(0)
	sub, replay, gap := h.Subscribe(0)
	if len(replay) != 0 || gap {
		t.Fatalf("empty hub: replay=%v gap=%v", replay, gap)
	}

	h.Publish("a", 1)
	h.Publish("b", 2)
	h.Publish("c", 3)

	for i, typ := range []string{"a", "b", "c"} {
		ev := recvEvent(t, sub)
		if ev.Seq != uint64(i+1) || ev.Type != typ {
			t.Fatalf("event %d = %+v, want seq=%d type=%s", i, ev, i+1, typ)
		}
	}
	if h.Seq() != 3 {
		t.Fatalf("Seq() = %d, want 3", h.Seq())
	}
}

func TestHub_SubscribeReplaysEventsAfterSince(t *testing.T) {
	h := newRoomHub(0)
	for i := 0; i < 5; i++ {
		h.Publish("e", i)
	}

	cases := []struct {
		since uint64
		want  []uint64
	}{
		{0, []uint64{1, 2, 3, 4, 5}},
		{2, []uint64{3, 4, 5}},
		{4, []uint64{5}},
		{5, nil},
	}
	for _, tc := range cases {
		sub, replay, gap := h.Subscribe(tc.since)
		if gap {
			t.Errorf("since=%d: unexpected gap", tc.since)
		}
		assertSeqs(t, replay, tc.want...)
		h.Unsubscribe(sub)
	}
}

func TestHub_SubscribeReportsGapWhenBufferOverflowed(t *testing.T) {
	h := newRoomHub(0)
	const total = hubBufferSize + 44 // 缓冲里剩下 seq 45..300
	for i := 0; i < total; i++ {
		h.Publish("e", i)
	}
	oldest := uint64(total - hubBufferSize + 1)

	// since=0 表示没有游标，不算缺口，拿到缓冲里全部事件。
	_, replay, gap := h.Subscribe(0)
	if gap || len(replay) != hubBufferSize || replay[0].Seq != oldest || replay[len(replay)-1].Seq != total {
		t.Fatalf("since=0: gap=%v len=%d first=%d", gap, len(replay), replay[0].Seq)
	}

	// since 恰好等于 oldest-1：紧挨着缓冲，没有丢事件。
	_, replay, gap = h.Subscribe(oldest - 1)
	if gap || len(replay) != hubBufferSize {
		t.Fatalf("since=%d: gap=%v len=%d, want contiguous replay", oldest-1, gap, len(replay))
	}

	// since 再小一点：oldest-1 这条已经被挤掉了。
	_, replay, gap = h.Subscribe(oldest - 2)
	if !gap {
		t.Fatalf("since=%d: want gap", oldest-2)
	}
	if len(replay) != hubBufferSize || replay[0].Seq != oldest {
		t.Fatalf("gap replay should still hold what is left in the buffer, got len=%d", len(replay))
	}

	// since 比服务端还新（例如 hub 之前被回收重建）：同样视为缺口。
	_, replay, gap = h.Subscribe(total + 10)
	if !gap || len(replay) != 0 {
		t.Fatalf("since beyond seq: gap=%v replay=%d", gap, len(replay))
	}
}

func TestHub_EphemeralIsBroadcastButNotBuffered(t *testing.T) {
	h := newRoomHub(0)
	sub, _, _ := h.Subscribe(0)

	h.Publish("persist", "p")
	h.Ephemeral("temp", "t")

	if ev := recvEvent(t, sub); ev.Seq != 1 || ev.Type != "persist" {
		t.Fatalf("first event = %+v", ev)
	}
	if ev := recvEvent(t, sub); ev.Seq != 0 || ev.Type != "temp" {
		t.Fatalf("ephemeral event = %+v, want seq=0 type=temp", ev)
	}
	if h.Seq() != 1 {
		t.Fatalf("Ephemeral must not advance seq, got %d", h.Seq())
	}

	_, replay, _ := h.Subscribe(0)
	assertSeqs(t, replay, 1)
	if replay[0].Type != "persist" {
		t.Fatalf("buffer holds %q, ephemeral events must not be buffered", replay[0].Type)
	}
}

func TestHub_SlowSubscriberIsDroppedWithoutBlockingPublisher(t *testing.T) {
	h := newRoomHub(0)
	slow, _, _ := h.Subscribe(0) // 从不读取
	fast, _, _ := h.Subscribe(0)

	const total = hubSubscriberQueue + 10
	got := make(chan []uint64, 1)
	go func() {
		var seqs []uint64
		for len(seqs) < total {
			select {
			case ev := <-fast.ch:
				seqs = append(seqs, ev.Seq)
			case <-time.After(2 * time.Second):
				got <- seqs
				return
			}
		}
		got <- seqs
	}()

	published := make(chan struct{})
	go func() {
		defer close(published)
		for i := 0; i < total; i++ {
			h.Publish("e", i)
			// 让消费者有机会取走，保证 fast 自己不会写满。
			if i%8 == 7 {
				time.Sleep(time.Millisecond)
			}
		}
	}()
	select {
	case <-published:
	case <-time.After(2 * time.Second):
		t.Fatal("Publish blocked by a slow subscriber")
	}

	select {
	case <-slow.done:
	default:
		t.Fatal("slow subscriber should have been dropped")
	}
	if !slow.slow {
		t.Error("dropped subscriber should be marked slow")
	}
	if n := h.numSubscribers(); n != 1 {
		t.Fatalf("subscribers = %d, want only the fast one", n)
	}

	seqs := <-got
	if len(seqs) != total {
		t.Fatalf("fast subscriber got %d events, want %d", len(seqs), total)
	}
	for i, s := range seqs {
		if s != uint64(i+1) {
			t.Fatalf("fast subscriber out of order at %d: %v", i, seqs)
		}
	}

	// 被踢的订阅者可以带 since 重连并补齐缺失的事件。
	_, replay, gap := h.Subscribe(uint64(hubSubscriberQueue))
	if gap || len(replay) != total-hubSubscriberQueue {
		t.Fatalf("reconnect replay: gap=%v len=%d, want %d", gap, len(replay), total-hubSubscriberQueue)
	}
}

func TestHub_UnsubscribeIsIdempotent(t *testing.T) {
	h := newRoomHub(0)
	sub, _, _ := h.Subscribe(0)
	h.Unsubscribe(sub)
	h.Unsubscribe(sub)
	h.Publish("e", nil)
	assertNoEvent(t, sub)
	if sub.slow {
		t.Error("explicit Unsubscribe is not a slow-consumer drop")
	}
}

func TestHub_ConcurrentPublishKeepsSeqMonotonic(t *testing.T) {
	const writers, perWriter = 8, 100
	const total = writers * perWriter

	h := newRoomHub(0)
	h.queueSize = total // 这个用例只验证 seq，不让订阅者因队列满被踢
	sub, _, _ := h.Subscribe(0)

	var wg sync.WaitGroup
	for w := 0; w < writers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < perWriter; i++ {
				h.Publish("e", i)
			}
		}()
	}
	wg.Wait()

	if h.Seq() != total {
		t.Fatalf("Seq() = %d, want %d", h.Seq(), total)
	}
	for want := uint64(1); want <= total; want++ {
		if ev := recvEvent(t, sub); ev.Seq != want {
			t.Fatalf("received seq %d, want %d (delivery must follow seq order)", ev.Seq, want)
		}
	}
}

// 订阅与发布并发：replay 加上实时事件必须恰好是连续的 1..N，不丢不重。
func TestHub_SubscribeWhilePublishingNoLossNoDuplicates(t *testing.T) {
	const total = 200
	h := newRoomHub(0)
	h.queueSize = total

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < total; i++ {
			h.Publish("e", i)
			if i%16 == 0 {
				time.Sleep(100 * time.Microsecond)
			}
		}
	}()

	time.Sleep(time.Millisecond)
	sub, replay, gap := h.Subscribe(0)
	if gap {
		t.Fatal("unexpected gap")
	}
	wg.Wait()

	seqs := replaySeqs(replay)
	for len(seqs) < total {
		seqs = append(seqs, recvEvent(t, sub).Seq)
	}
	for i, s := range seqs {
		if s != uint64(i+1) {
			t.Fatalf("seq[%d] = %d, want %d; full: %v", i, s, i+1, seqs)
		}
	}
	assertNoEvent(t, sub)
}

func TestHubRegistry_DropOnlyWithoutSubscribers(t *testing.T) {
	resetHubs(t)
	const id = 4242

	h := hubFor(id)
	if hubFor(id) != h {
		t.Fatal("hubFor must return the same hub for the same session")
	}
	h.Publish("e", nil)
	h.Publish("e", nil)

	sub, _, _ := h.Subscribe(0)
	if dropHub(id) {
		t.Fatal("hub with subscribers must not be dropped")
	}
	if hubFor(id) != h {
		t.Fatal("hub should survive a refused drop")
	}

	h.Unsubscribe(sub)
	if !dropHub(id) {
		t.Fatal("hub without subscribers should be dropped")
	}
	if dropHub(id) {
		t.Fatal("dropping a missing hub reports false")
	}

	// 重建后 seq 接着往下数，持有旧游标的客户端不会把新事件误认为已读。
	h2 := hubFor(id)
	if h2 == h {
		t.Fatal("expected a fresh hub after drop")
	}
	h2.Publish("e", nil)
	if h2.Seq() != 3 {
		t.Fatalf("seq after recreate = %d, want 3", h2.Seq())
	}
	if _, replay, gap := h2.Subscribe(1); !gap || len(replay) != 1 {
		// since=1 的客户端缺了 seq=2，必须被告知有缺口。
		t.Fatalf("stale cursor after recreate: gap=%v replay=%v", gap, replaySeqs(replay))
	}
}

func TestHubRegistry_DropIdleHubs(t *testing.T) {
	resetHubs(t)
	idle := hubFor(1)
	busy := hubFor(2)
	sub, _, _ := busy.Subscribe(0)
	defer busy.Unsubscribe(sub)

	time.Sleep(5 * time.Millisecond)
	if n := dropIdleHubs(time.Millisecond); n != 1 {
		t.Fatalf("dropped %d hubs, want 1 (the idle one without subscribers)", n)
	}
	if hubFor(1) == idle {
		t.Error("idle hub should have been recycled")
	}
	if hubFor(2) != busy {
		t.Error("hub with a subscriber must be kept")
	}
	if n := dropIdleHubs(time.Hour); n != 0 {
		t.Fatalf("recently active hubs dropped: %d", n)
	}
}

package handlers

// NOTE: 房间实时推送 hub。每个会话一个 roomHub，KP 流程把进展 Publish（持久，带 seq，进 256 条环形缓冲）
// 或 Ephemeral（临时，seq=0，只广播）出去，WebSocket 订阅者通过带缓冲的 chan 接收。
// 约束：
//   - 广播绝不阻塞发布者：订阅者 chan 写满就被移除（客户端带 since 重连即可补齐）。
//   - Subscribe 在同一把锁内完成"计算 replay + 注册订阅"，保证补发与实时之间不丢不重。
//   - 事件的 Data 发布后不得再被修改，多个订阅者会并发序列化同一份数据。
//   - 发布方每次都应通过 hubFor 取 hub，不要长期持有 *roomHub：空闲且无订阅者的 hub 会被回收，
//     持有旧指针发布的事件不会到达之后新建的 hub 的订阅者。

import (
	"sync"
	"sync/atomic"
	"time"
)

const (
	hubBufferSize      = 256 // 环形缓冲容量：断线重连最多能补发的持久事件条数
	hubSubscriberQueue = 64  // 单个订阅者的待发送队列长度
	hubIdleTTL         = 10 * time.Minute
	hubSweepInterval   = time.Minute
)

type roomEvent struct {
	Seq  uint64 `json:"seq"`
	Type string `json:"type"`
	Data any    `json:"data"`
}

// subscriber 是一个 WebSocket 连接在 hub 里的接收端。
type subscriber struct {
	ch   chan roomEvent
	done chan struct{} // 被 hub 移除（慢消费者或 Unsubscribe）后关闭
	// startSeq 是注册那一刻 hub 的最大 seq（与 replay 同锁取得）：replay 不超过它，之后的实时事件都大于它。
	startSeq uint64
	// slow 为 true 表示因队列写满被 hub 踢掉；先于 close(done) 写入，读取方在 <-done 之后读，无数据竞争。
	slow bool
}

type roomHub struct {
	mu   sync.Mutex
	seq  uint64
	buf  [hubBufferSize]roomEvent // 事件 seq=s 存放在 buf[s%hubBufferSize]，缓冲内的 seq 连续
	n    int                      // 缓冲内有效事件数，<= hubBufferSize
	subs map[*subscriber]struct{}

	queueSize  int
	lastActive atomic.Int64 // UnixNano，供回收判断空闲
}

func newRoomHub(startSeq uint64) *roomHub {
	h := &roomHub{seq: startSeq, subs: make(map[*subscriber]struct{}), queueSize: hubSubscriberQueue}
	h.touch()
	return h
}

func (h *roomHub) touch() { h.lastActive.Store(time.Now().UnixNano()) }

// Publish 发布持久事件：分配单调递增 seq，写入环形缓冲并广播。
func (h *roomHub) Publish(typ string, data any) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.seq++
	ev := roomEvent{Seq: h.seq, Type: typ, Data: data}
	h.buf[h.seq%hubBufferSize] = ev
	if h.n < hubBufferSize {
		h.n++
	}
	h.broadcastLocked(ev)
	h.touch()
}

// Ephemeral 发布临时事件：seq=0，不进缓冲，断线期间的临时事件直接丢弃。
func (h *roomHub) Ephemeral(typ string, data any) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.broadcastLocked(roomEvent{Type: typ, Data: data})
	h.touch()
}

// Seq 返回当前最大 seq。
func (h *roomHub) Seq() uint64 {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.seq
}

// Subscribe 注册订阅者，并返回缓冲里 seq>since 的事件作为 replay。
// gap=true 表示 (since, seq] 里有事件已不在缓冲中（被挤掉、hub 被回收过，或 since 比服务端还新），
// 此时 replay 只含缓冲里剩下的部分，是残缺序列，调用方应让客户端重新拉取全量数据而不是照单补发。
// since=0 视为"没有游标"，不会判定 gap，replay 为缓冲内全部事件。
func (h *roomHub) Subscribe(since uint64) (sub *subscriber, replay []roomEvent, gap bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	sub = &subscriber{
		ch:       make(chan roomEvent, h.queueSize),
		done:     make(chan struct{}),
		startSeq: h.seq,
	}
	h.subs[sub] = struct{}{}
	h.touch()

	if since > h.seq {
		return sub, nil, true
	}
	oldest := h.seq - uint64(h.n) + 1 // n==0 时 oldest=seq+1，即缓冲里没有任何事件
	gap = since > 0 && oldest > since+1
	from := since + 1
	if from < oldest {
		from = oldest
	}
	for s := from; s <= h.seq; s++ {
		replay = append(replay, h.buf[s%hubBufferSize])
	}
	return sub, replay, gap
}

// Unsubscribe 移除订阅者，可重复调用。
func (h *roomHub) Unsubscribe(sub *subscriber) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.removeLocked(sub)
	h.touch()
}

func (h *roomHub) numSubscribers() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.subs)
}

func (h *roomHub) broadcastLocked(ev roomEvent) {
	for sub := range h.subs {
		select {
		case sub.ch <- ev:
		default:
			// NOTE: 慢订阅者直接断开而不是等它，否则会拖住发布者（KP 主流程）。
			sub.slow = true
			h.removeLocked(sub)
		}
	}
}

func (h *roomHub) removeLocked(sub *subscriber) {
	if _, ok := h.subs[sub]; !ok {
		return
	}
	delete(h.subs, sub)
	close(sub.done)
}

// hubRegistry 按会话 ID 懒创建并回收 roomHub。
type hubRegistry struct {
	mu   sync.Mutex
	hubs map[uint]*roomHub
	// floor 记录已回收 hub 的最后 seq：同一会话重新建 hub 时从它继续计数，
	// 否则 seq 回到 1 后，持有旧游标的客户端会把新事件当成已读过的旧事件。每个会话只占一个 uint64。
	floor map[uint]uint64

	sweeper sync.Once
}

var hubs = &hubRegistry{hubs: make(map[uint]*roomHub), floor: make(map[uint]uint64)}

// hubFor 返回会话对应的 hub，不存在则创建。
func hubFor(sessionID uint) *roomHub {
	hubs.sweeper.Do(func() { go hubs.sweepLoop() })
	hubs.mu.Lock()
	defer hubs.mu.Unlock()
	h, ok := hubs.hubs[sessionID]
	if !ok {
		h = newRoomHub(hubs.floor[sessionID])
		delete(hubs.floor, sessionID)
		hubs.hubs[sessionID] = h
	}
	// NOTE: 在注册表锁内刷新活跃时间：拿到 hub 的调用方在 hubIdleTTL 内不会被回收线程摘掉。
	h.touch()
	return h
}

// dropHub 回收会话的 hub，仅当没有订阅者时才删除，返回是否删除。
func dropHub(sessionID uint) bool {
	hubs.mu.Lock()
	defer hubs.mu.Unlock()
	return hubs.dropLocked(sessionID)
}

// dropIdleHubs 回收无订阅者且空闲超过 maxIdle 的 hub，返回回收数量。
func dropIdleHubs(maxIdle time.Duration) int {
	cutoff := time.Now().Add(-maxIdle).UnixNano()
	hubs.mu.Lock()
	defer hubs.mu.Unlock()
	dropped := 0
	for id, h := range hubs.hubs {
		if h.lastActive.Load() <= cutoff && hubs.dropLocked(id) {
			dropped++
		}
	}
	return dropped
}

// dropLocked 要求持有 r.mu；加锁顺序固定为注册表锁 -> hub 锁。
func (r *hubRegistry) dropLocked(sessionID uint) bool {
	h, ok := r.hubs[sessionID]
	if !ok {
		return false
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.subs) > 0 {
		return false
	}
	if h.seq > 0 {
		r.floor[sessionID] = h.seq
	}
	delete(r.hubs, sessionID)
	return true
}

func (r *hubRegistry) sweepLoop() {
	ticker := time.NewTicker(hubSweepInterval)
	defer ticker.Stop()
	for range ticker.C {
		dropIdleHubs(hubIdleTTL)
	}
}

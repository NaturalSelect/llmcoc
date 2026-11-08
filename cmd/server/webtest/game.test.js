// NOTE: 本目录必须放在 cmd/server/web 之外，否则 //go:embed web 会把测试文件打进二进制
// NOTE: 用假的 WebSocket、fetch 和定时器在 vm 里加载 room_state.js 与 game.js，驱动控制器的连接、命令和问题卡逻辑；不涉及 DOM 和 Alpine
'use strict';

const test = require('node:test');
const assert = require('node:assert/strict');
const vm = require('node:vm');
const fs = require('node:fs');
const path = require('node:path');

const WEB = path.join(__dirname, '..', 'web', 'js');

// vm 里创建的数组和对象原型不同，deepEqual 前先规整
const plain = (v) => JSON.parse(JSON.stringify(v));

class FakeWS {
  constructor(url, sockets) {
    this.url = url;
    this.closed = false;
    sockets.push(this);
  }
  close() { this.closed = true; }
  recv(frame) { this.onmessage({ data: JSON.stringify(frame) }); }
  drop() { this.onclose(); }
}

const SPOT_ME = { user_ids: [2], names: ['爱丽丝'], question: '怎么办？', kp_floor: false, asked_me: true, stop: '' };
const WAITING_ME = { pending: 1, total: 1, submitted_names: [], pending_names: ['爱丽丝'] };

function harness({ isHost = false, spotlight = SPOT_ME, waiting = WAITING_ME, messages } = {}) {
  const timers = [];
  let timerId = 0;
  const sockets = [];
  const listeners = {};
  const calls = [];
  const toasts = [];
  const sent = [];
  const on = (n, f) => { (listeners[n] = listeners[n] || []).push(f); };
  const off = (n, f) => { listeners[n] = (listeners[n] || []).filter((x) => x !== f); };

  const ctx = {
    console,
    FormData: class { constructor() { this.d = {}; } append(k, v) { this.d[k] = v; } },
    encodeURIComponent, Promise, JSON, Object, Array, Number, String, Math, Map, Set, Error,
    WebSocket: class extends FakeWS { constructor(url) { super(url, sockets); } },
    location: { protocol: 'http:', host: 'localhost:8080' },
    document: { visibilityState: 'visible', getElementById: () => null, addEventListener: on, removeEventListener: off },
    requestAnimationFrame: (f) => f(),
    setTimeout: (f, ms) => { timers.push({ id: ++timerId, f, ms }); return timerId; },
    clearTimeout: (id) => { const i = timers.findIndex((t) => t.id === id); if (i >= 0) timers.splice(i, 1); },
    fetch: async (url, opts) => { sent.push({ url, opts }); return { status: 202, json: async () => ({}) }; },
  };
  ctx.window = ctx;
  ctx.window.addEventListener = on;
  ctx.window.removeEventListener = off;
  vm.createContext(ctx);
  for (const f of ['room_state.js', 'game.js']) {
    vm.runInContext(fs.readFileSync(path.join(WEB, f), 'utf8'), ctx, { filename: f });
  }

  const session = { id: 7, status: 'playing', created_by: isHost ? 2 : 1, players: [{ user_id: 2, character_card: { name: '爱丽丝', stats: { hp: 10, san: 50 } } }] };
  const app = Object.assign({
    token: 'tok en', user: { id: 2, role: 'user' }, page: 'sessions', currentSession: null, room: null,
    chatInput: '', askPick: [], askEditing: false, hudFlash: {}, _hudLastStats: null,
    $nextTick: (f) => f(),
    showToast: (m, t) => toasts.push([m, t]),
    confirmDialog: async () => true,
    logout() { this.loggedOut = true; },
    goTo(p) { this.page = p; if (p !== 'game') this.leaveRoom(); },
    async api(method, p) {
      calls.push(method + ' ' + p);
      if (p === '/api/sessions/7') return plain(session);
      if (p === '/api/sessions/7/messages') return plain(messages || []);
      if (p === '/api/sessions/7/chat-status') {
        return { kp_running: false, submitted: false, floor: null, spotlight: plain(spotlight), waiting: plain(waiting) };
      }
      return {};
    },
  }, ctx.COC.game);

  const tick = () => new Promise((r) => setImmediate(r));
  const h = {
    app, ctx, timers, sockets, listeners, calls, toasts, sent, tick,
    ws: () => sockets[sockets.length - 1],
    // 进房并完成 hello -> 快照，使房间进入 live
    async enter({ epoch = 'E1', seq = 3 } = {}) {
      await app.enterRoom(7);
      h.ws().recv({ seq: 0, type: 'hello', data: { epoch, seq } });
      await tick(); await tick();
    },
    frame(seq, type, data = {}) { h.ws().recv({ seq, type, data }); },
  };
  return h;
}

test('进房：URL 带 token，首次不带 since/epoch；hello 之前不能发言', async () => {
  const h = harness();
  await h.app.enterRoom(7);
  assert.equal(h.app.page, 'game');
  assert.equal(h.sockets.length, 1);
  assert.match(h.ws().url, /^ws:\/\/localhost:8080\/api\/sessions\/7\/ws\?token=tok%20en$/);
  assert.equal(h.app.room.conn, 'connecting');
  assert.equal(h.app.gates().canSpeak, false);
  assert.equal(h.app.askView(), '', '快照到达前 spotlight 为空，没有问题卡');
});

test('进房：快照到达前没有问题卡，之后显示并带上提问', async () => {
  const h = harness({ messages: [{ id: 1, kind: 'kp', status: 'done', floor: 1, content: '开场', role: 'assistant' }] });
  await h.app.enterRoom(7);
  assert.equal(h.app.askView(), '');
  h.ws().recv({ seq: 0, type: 'hello', data: { epoch: 'E1', seq: 3 } });
  await h.tick(); await h.tick();
  assert.equal(h.app.room.conn, 'live');
  assert.equal(h.app.room.messages.length, 1);
  assert.equal(h.app.gates().canSpeak, true);
  assert.equal(h.app.askView(), 'card');
  assert.equal(h.app.askLocked(), false);
  assert.equal(h.app.askTag(), 'KP 向你提问');
  assert.equal(h.app.room.spotlight.question, '怎么办？');
  assert.deepEqual(plain(h.app.roomBanner()), { text: 'KP 在等你回应', tone: 'me', busy: false });
  assert.equal(h.app.gates().canContinue, false, '不是房主');
  assert.deepEqual(h.calls.filter((c) => c.includes('/api/sessions/7')).sort(), [
    'GET /api/sessions/7', 'GET /api/sessions/7', 'GET /api/sessions/7/chat-status', 'GET /api/sessions/7/messages',
  ]);
});

test('补发的旧帧被丢弃；周期运行中没有问题卡；writer 流式文字', async () => {
  const h = harness();
  await h.enter();
  h.frame(3, 'message_new', { message: { id: 9, kind: 'kp', status: 'done', content: '旧' } });
  assert.equal(h.app.room.byId[9], undefined, 'seq <= hello.seq 的帧被丢弃');

  h.frame(4, 'floor_start', { floor: 1, reason: 'player' });
  assert.equal(h.app.gates().canSpeak, false);
  assert.equal(h.app.askView(), '');
  assert.deepEqual(plain(h.app.askOptions()), []);
  assert.deepEqual(plain(h.app.roomBanner()), { text: 'KP 正在推进剧情…', tone: 'info', busy: true });

  h.frame(5, 'message_new', { message: { id: 10, kind: 'writer', status: 'pending', floor: 1, content: '' } });
  h.frame(0, 'writer_token', { message_id: 10, text: '雨' });
  h.frame(0, 'writer_token', { message_id: 10, text: '落下' });
  assert.equal(h.app.writerText(h.app.room.byId[10]), '雨落下');
  h.frame(6, 'message_update', { id: 10, status: 'done', content: '雨落下了。' });
  assert.equal(h.app.writerText(h.app.room.byId[10]), '雨落下了。');

  h.frame(7, 'floor_end', { floor: 1, reason: 'ask', messages: 1 });
  h.frame(8, 'spotlight', { user_ids: [2], names: ['爱丽丝'], question: '下一步？', kp_floor: false, stop: '' });
  assert.equal(h.app.askView(), 'card');
  assert.equal(h.app.room.spotlight.question, '下一步？');
});

test('问题卡：勾选推荐行动与自己写的拼成一条回应，提交走 FormData', async () => {
  const h = harness();
  await h.enter();
  const { app } = h;

  assert.equal(await app.sendChat(), false, '没有内容不发送');
  const kp = { id: 200, kind: 'kp', status: 'done', meta: { options: ['查看地板', ' ', '敲门'] } };
  app.room.messages.push(kp);
  assert.deepEqual(plain(app.askOptions()), ['查看地板', '敲门'], '空白选项被丢弃');

  app.askToggle('敲门');
  app.askToggle('查看地板');
  app.askToggle('查看地板');
  app.askToggle('查看地板');
  assert.equal(app.askPicked('敲门'), true);
  assert.equal(app.askAnswer(), '查看地板\n敲门', '勾选内容按选项顺序排列');
  app.chatInput = '我推门';
  assert.equal(app.askAnswer(), '查看地板\n敲门\n我推门', '自己写的放在最后');
  assert.equal(app.askCharCount(), [...'查看地板\n敲门\n我推门'].length);

  app.askPick = ['敲门', '不存在的选项'];
  assert.equal(app.askAnswer(), '敲门\n我推门', '不在当前选项里的勾选被忽略');

  app.room.messages.push({ id: 201, kind: 'player', status: 'done', user_id: 2 });
  assert.deepEqual(plain(app.askOptions()), [], '玩家行动之后不再给推荐');
  app.room.messages.pop();

  assert.equal(await app.sendChat(), true);
  assert.equal(h.sent.length, 1);
  assert.equal(h.sent[0].url, '/api/sessions/7/chat');
  assert.equal(h.sent[0].opts.method, 'POST');
  assert.equal(h.sent[0].opts.headers.Authorization, 'Bearer tok en');
  assert.equal(h.sent[0].opts.body.d.content, '敲门\n我推门');
  assert.equal(app.room.me.submitted, true);
  assert.equal(app.room.me.draft, '敲门\n我推门');
  assert.equal(app.chatInput, '');
  assert.deepEqual(plain(app.askPick), []);
  assert.equal(app.askView(), 'done');
});

test('问题卡：已提交后可以修改、取消修改、重新提交', async () => {
  const h = harness();
  await h.enter();
  const { app } = h;
  app.chatInput = '我推门';
  assert.equal(await app.sendChat(), true);
  assert.equal(app.askView(), 'done');

  app.editSubmitted();
  assert.equal(app.askEditing, true);
  assert.equal(app.askView(), 'card');
  assert.equal(app.chatInput, '我推门');

  app.cancelEdit();
  assert.equal(app.askView(), 'done');
  assert.equal(app.chatInput, '');
  assert.equal(app.room.me.draft, '我推门', '取消修改不动已提交的内容');

  app.editSubmitted();
  app.chatInput = '我踹门';
  assert.equal(await app.sendChat(), true);
  assert.equal(app.askEditing, false);
  assert.equal(app.askView(), 'done');
  assert.equal(h.sent.at(-1).opts.body.d.content, '我踹门');
  assert.equal(app.room.me.draft, '我踹门');
});

test('提交：按字符数限制 2000，被拒绝时保留输入并 toast', async () => {
  const h = harness();
  await h.enter();
  const { app } = h;

  app.chatInput = '平'.repeat(2001);
  assert.equal(await app.sendChat(), false);
  assert.match(h.toasts.at(-1)[0], /消息过长/);
  assert.equal(h.sent.length, 0);

  app.chatInput = '平'.repeat(2000);
  assert.equal(await app.sendChat(), true, '正好 2000 个字符可以发送');
  app.room.me.submitted = false;

  h.ctx.fetch = async () => ({ status: 409, json: async () => ({ error: 'KP 正在推进剧情' }) });
  app.chatInput = 'x';
  assert.equal(await app.sendChat(), false);
  assert.equal(h.toasts.at(-1)[0], 'KP 正在推进剧情');
  assert.equal(app.chatInput, 'x', '被拒绝后输入保留');
  assert.equal(app.room.me.submitted, false);
});

test('断线：问题卡和已写的内容保留，卡片置灰且不能提交；恢复后可以继续', async () => {
  const h = harness();
  await h.enter();
  const { app } = h;
  app.room.messages.push({ id: 200, kind: 'kp', status: 'done', meta: { options: ['敲门'] } });
  app.chatInput = '我先写着';
  app.askToggle('敲门');
  assert.deepEqual(plain(app.askPick), ['敲门']);

  const first = h.ws();
  first.drop();
  assert.equal(app.room.conn, 'reconnecting');
  assert.equal(app.askView(), 'card', '断线时卡片还在');
  assert.equal(app.askLocked(), true);
  assert.match(app.askLockNote(), /连接中断/);
  assert.equal(app.askAnswer(), '敲门\n我先写着', '已写的内容和勾选都保留');
  assert.equal(await app.sendChat(), false, '锁定时不能提交');
  assert.equal(h.sent.length, 0);
  assert.equal(app.roomBanner().text, '连接中断，正在重连…');
  assert.equal(app.roomBanner().tone, 'warn');
  app.askToggle('敲门');
  assert.deepEqual(plain(app.askPick), ['敲门'], '锁定时勾选不变');

  const retry = h.timers.find((t) => t.ms === 1000);
  assert.ok(retry, '1 秒后重连');
  h.timers.splice(h.timers.indexOf(retry), 1);
  retry.f();
  assert.equal(h.sockets.length, 2);
  assert.match(h.ws().url, /&since=3&epoch=E1$/);
  h.ws().recv({ seq: 0, type: 'hello', data: { epoch: 'E1', seq: 3 } });
  assert.equal(app.askLocked(), false, '同一 epoch 重连成功后立即解锁');
  assert.equal(app.chatInput, '我先写着');
  assert.equal(await app.sendChat(), true);
  assert.equal(h.sent.at(-1).opts.body.d.content, '敲门\n我先写着');
});

test('断线后服务重启（epoch 变化）：同步快照期间卡片置灰，快照后解锁', async () => {
  const h = harness();
  await h.enter();
  h.app.chatInput = '写到一半';
  h.ws().drop();
  const retry = h.timers.find((t) => t.ms === 1000);
  h.timers.splice(h.timers.indexOf(retry), 1);
  retry.f();

  h.ws().recv({ seq: 0, type: 'hello', data: { epoch: 'E2', seq: 0 } });
  assert.equal(h.app.room.conn, 'snapshot');
  assert.equal(h.app.askLocked(), true);
  assert.match(h.app.askLockNote(), /同步/);
  await h.tick(); await h.tick();
  assert.equal(h.app.room.epoch, 'E2');
  assert.equal(h.app.room.conn, 'live');
  assert.equal(h.app.askLocked(), false);
  assert.equal(h.app.chatInput, '写到一半');
});

test('重连：断线期间错过的内容被补齐；旧连接的迟到帧被忽略', async () => {
  const h = harness();
  await h.enter();
  h.frame(4, 'message_new', { message: { id: 11, kind: 'writer', status: 'pending', floor: 2, content: '' } });
  h.frame(0, 'writer_token', { message_id: 11, text: '半截' });

  const first = h.ws();
  first.drop();
  assert.equal(h.app.writerText(h.app.room.byId[11]), '半截', '已收到的部分保留');
  const retry = h.timers.find((t) => t.ms === 1000);
  h.timers.splice(h.timers.indexOf(retry), 1);
  retry.f();
  assert.match(h.ws().url, /&since=4&epoch=E1$/);
  h.ws().recv({ seq: 0, type: 'hello', data: { epoch: 'E1', seq: 7 } });
  h.frame(5, 'some_future_event', {});
  h.frame(6, 'message_update', { id: 11, status: 'done', content: '完整正文' });
  h.frame(7, 'floor_end', { floor: 2, reason: 'ask', messages: 1 });
  assert.equal(h.app.room.conn, 'live');
  assert.equal(h.app.writerText(h.app.room.byId[11]), '完整正文');

  const n = h.app.room.messages.length;
  first.onmessage({ data: JSON.stringify({ seq: 8, type: 'message_new', data: { message: { id: 99, kind: 'kp', status: 'done', content: 'x' } } }) });
  assert.equal(h.app.room.messages.length, n, '旧连接的帧不生效');
});

test('序号出现缺口：丢弃游标并重连，不带 since', async () => {
  const h = harness();
  await h.enter();
  h.frame(4, 'floor_end', { floor: 1, reason: 'ask', messages: 0 });
  h.frame(9, 'message_new', { message: { id: 50, kind: 'kp', status: 'done', content: 'gap' } });
  assert.equal(h.sockets.length, 2);
  assert.doesNotMatch(h.ws().url, /since=/);
  assert.match(h.ws().url, /epoch=E1/);
});

test('房主：继续与打断的门控、命令和启动中状态', async () => {
  const h = harness({ isHost: true, spotlight: { user_ids: [], names: [], question: '', kp_floor: true, asked_me: false, stop: '' }, waiting: { pending: 0, total: 0 } });
  await h.enter();
  const { app } = h;
  assert.equal(app.gates().canContinue, true);
  assert.equal(app.gates().canStop, false);
  assert.equal(app.continueLabel(), '让 KP 开场');

  h.frame(4, 'floor_start', { floor: 1, reason: 'host_continue' });
  assert.equal(app.gates().canStop, true);
  assert.equal(app.gates().canContinue, false);

  const stops = [];
  const origApi = app.api;
  app.api = async (m, p) => { if (p.endsWith('/kp-stop')) { stops.push(p); return {}; } return origApi.call(app, m, p); };
  assert.equal(await app.hostStop(), true);
  assert.deepEqual(stops, ['/api/sessions/7/kp-stop']);

  h.frame(5, 'floor_end', { floor: 1, reason: 'host_stop', messages: 0 });
  h.frame(6, 'spotlight', { user_ids: [], names: [], question: '', kp_floor: true, stop: 'host_stop' });
  assert.equal(app.roomBanner().text, 'KP 已暂停：房主打断，等待房主让 KP 继续');
  assert.equal(app.roomBanner().busy, false);
  assert.equal(app.askView(), '');
  assert.equal(app.gates().canContinue, true);

  app.api = async (m, p) => { h.calls.push(m + ' ' + p); if (p.endsWith('/kp-turn')) return { floor_started: true }; return origApi.call(app, m, p); };
  assert.equal(await app.hostContinueKP(), true);
  assert.equal(app.room.floor.starting, true);
  assert.equal(app.gates().canContinue, false, '启动中不能重复点击');
  assert.ok(h.calls.includes('POST /api/sessions/7/kp-turn'));
  assert.ok(h.timers.some((t) => t.ms === 10000), '启动超时兜底');
});

test('新周期开始：清掉上一个问题残留的勾选、修改状态和草稿', async () => {
  const h = harness();
  await h.enter();
  h.app.askPick = ['x'];
  h.app.askEditing = true;
  h.app.chatInput = '残留';
  h.frame(4, 'floor_start', { floor: 2, reason: 'player' });
  assert.deepEqual(plain(h.app.askPick), []);
  assert.equal(h.app.askEditing, false);
  assert.equal(h.app.chatInput, '');
});

test('多人点名：只有被点名的人看到卡片，标签列出所有人；其余人看状态栏', async () => {
  const h = harness();
  await h.enter();
  const { app } = h;
  h.frame(4, 'floor_start', { floor: 2, reason: 'player' });
  h.frame(5, 'floor_end', { floor: 2, reason: 'ask', messages: 1 });

  h.frame(6, 'spotlight', { user_ids: [2, 3], names: ['爱丽丝', '鲍勃'], question: '你们呢？', kp_floor: false, stop: '' });
  assert.equal(app.askView(), 'card');
  assert.equal(app.askTag(), 'KP 点名：爱丽丝、鲍勃，请各自回应');

  h.frame(7, 'spotlight', { user_ids: [3], names: ['鲍勃'], question: '你呢？', kp_floor: false, stop: '' });
  assert.equal(app.askView(), '', '没被点名就没有卡片');
  assert.equal(app.roomBanner().text, 'KP 在等 鲍勃 回应');
  assert.equal(app.roomBanner().busy, true);
  assert.equal(app.gates().canSpeak, false);
});

test('已提交后的状态栏文案：等待其他人时显示他们的名字', async () => {
  const h = harness({ waiting: { pending: 2, total: 2, submitted_names: [], pending_names: ['爱丽丝', '鲍勃'] } });
  await h.enter();
  h.app.chatInput = '我推门';
  await h.app.sendChat();
  h.frame(4, 'waiting', { pending: 1, total: 2, submitted_names: ['爱丽丝'], pending_names: ['鲍勃'] });
  assert.equal(h.app.askView(), 'done');
  assert.equal(h.app.roomBanner().text, '已提交行动，还在等 鲍勃');
  assert.equal(h.app.roomBanner().busy, true);
});

test('房间结束：不能继续，也没有问题卡', async () => {
  const h = harness({ isHost: true });
  await h.enter();
  h.frame(4, 'session', { status: 'ended', reason: 'end' });
  assert.equal(h.app.room.status, 'ended');
  assert.equal(h.app.askView(), '');
  assert.equal(h.app.gates().canContinue, false);
  assert.equal(h.app.roomBanner().text, '游戏已结束');
  assert.ok(h.timers.some((t) => t.ms === 300), '重新拉取房间信息有防抖');
});

test('离开房间：关闭连接、清掉定时器和监听；迟到的帧与进房请求都不生效', async () => {
  const h = harness();
  await h.enter();
  const live = h.ws();
  assert.equal(live.closed, false);
  h.app.goTo('sessions');
  assert.equal(h.app.room, null);
  assert.equal(live.closed, true);
  assert.equal(h.timers.length, 0, '剩余定时器：' + JSON.stringify(h.timers.map((t) => t.ms)));
  assert.equal((h.listeners.visibilitychange || []).length, 0);
  assert.equal((h.listeners.online || []).length, 0);
  live.onmessage({ data: JSON.stringify({ seq: 5, type: 'floor_start', data: { floor: 9 } }) });
  assert.equal(h.app.gates().canSpeak, false);
  assert.equal(h.app.askView(), '');

  const before = h.sockets.length;
  const p = h.app.enterRoom(7);
  h.app.leaveRoom();
  await p;
  assert.equal(h.sockets.length, before, '迟到的进房请求没有建立连接');
  assert.equal(h.app.room, null);
});

test('离开房间：断线后排队的重连定时器被取消，不会再建立连接', async () => {
  const h = harness();
  await h.enter();
  h.ws().drop();
  assert.ok(h.timers.some((t) => t.ms === 1000), '断线后排了重连');
  const before = h.sockets.length;
  h.app.goTo('sessions');
  assert.equal(h.timers.length, 0);
  assert.equal(h.sockets.length, before);
  assert.equal(h.app.room, null);
});

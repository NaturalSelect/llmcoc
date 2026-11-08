// NOTE: 本目录必须放在 cmd/server/web 之外，否则 //go:embed web 会把测试文件打进二进制
'use strict';

const test = require('node:test');
const assert = require('node:assert/strict');
const RS = require('../web/js/room_state.js');

const F = (seq, type, data = {}) => ({ seq, type, data });
const msg = (id, extra = {}) => ({ id, role: 'assistant', kind: 'kp', status: 'done', content: '', ...extra });
const writer = (id, extra = {}) => msg(id, { kind: 'writer', status: 'pending', ...extra });

// NOTE: 约定的调用顺序：acceptFrame 只做分类并推进游标，返回 'apply' 时调用方再调 applyFrame
// NOTE: hello 之后的 resync 帧同样返回 'apply'，由 applyFrame 完成 helloPending -> snapshot 的切换并返回 ['fetchSnapshot']
// NOTE: 只有 live 下出现跳号（或 live 下收到 resync）才返回 'resync'，调用方应 resetCursor 后重连
function feed(room, frame) {
  const verdict = RS.acceptFrame(room, frame);
  const effects = verdict === 'apply' ? RS.applyFrame(room, frame) : [];
  return { verdict, effects };
}

function snap(extra = {}) {
  return { session: { status: 'playing' }, messages: [], chatStatus: {}, ...extra };
}

function liveRoom({ seq = 10, userId = 3, isHost = false } = {}) {
  const room = RS.newRoom({ sessionId: 1, userId, isHost });
  RS.onHello(room, { epoch: 'e1', seq });
  RS.applySnapshot(room, snap());
  return room;
}

test('newRoom 初始状态', () => {
  const room = RS.newRoom({ sessionId: 9, userId: 3, isHost: true });
  assert.equal(room.sessionId, 9);
  assert.equal(room.userId, 3);
  assert.equal(room.isHost, true);
  assert.equal(room.epoch, '');
  assert.equal(room.lastSeq, 0);
  assert.equal(room.conn, 'connecting');
  assert.equal(room.status, 'lobby');
  assert.deepEqual(room.floor, { running: false, number: 0, starting: false });
  assert.deepEqual(room.me, { submitted: false, draft: '' });
});

test('首次连接：hello 后补发的 seq 1..5 全部丢弃，快照期间的 6、7 缓冲后依次应用', () => {
  const room = RS.newRoom({ sessionId: 1, userId: 3, isHost: false });
  assert.deepEqual(RS.onHello(room, { epoch: 'e1', seq: 5 }), ['fetchSnapshot']);
  assert.equal(room.conn, 'snapshot');
  assert.equal(room.baseSeq, 5);
  assert.equal(room.epoch, 'e1');

  for (let s = 1; s <= 5; s++) {
    assert.equal(RS.acceptFrame(room, F(s, 'message_new', { message: msg(100 + s) })), 'drop');
  }
  assert.equal(room.messages.length, 0);
  assert.equal(RS.acceptFrame(room, F(0, 'kp_status', { text: '思考中' })), 'drop');

  assert.equal(
    RS.acceptFrame(room, F(6, 'spotlight', { user_ids: [3], names: ['我'], question: '开门吗', kp_floor: false, stop: '' })),
    'buffer',
  );
  assert.equal(RS.acceptFrame(room, F(7, 'message_new', { message: msg(106, { kind: 'ask' }) })), 'buffer');
  assert.equal(room.pending.length, 2);
  assert.equal(room.lastSeq, 0);
  assert.equal(room.spotlight.askedMe, false);

  const effects = RS.applySnapshot(room, snap({ messages: [msg(101), msg(102), msg(103), msg(104), msg(105)] }));
  assert.deepEqual(effects, []);
  assert.deepEqual(room.messages.map((m) => m.id), [101, 102, 103, 104, 105, 106]);
  assert.equal(room.spotlight.askedMe, true);
  assert.equal(room.spotlight.question, '开门吗');
  assert.equal(room.lastSeq, 7);
  assert.equal(room.pending.length, 0);
  assert.equal(room.conn, 'live');
  assert.equal(RS.acceptFrame(room, F(8, 'waiting', {})), 'apply');
  assert.equal(room.lastSeq, 8);
});

test('快照期间到达的帧乱序或重复时，按 seq 去重排序后应用', () => {
  const room = RS.newRoom({ sessionId: 1, userId: 3 });
  RS.onHello(room, { epoch: 'e1', seq: 5 });
  RS.acceptFrame(room, F(7, 'kp_status', { text: 'x' }));
  RS.acceptFrame(room, F(7, 'floor_start', { floor: 2 }));
  RS.acceptFrame(room, F(6, 'floor_start', { floor: 1 }));
  RS.applySnapshot(room, snap());
  assert.equal(room.floor.number, 2);
  assert.equal(room.lastSeq, 7);
});

test('没有缓冲帧时，快照应用后 lastSeq 等于 hello.seq', () => {
  const room = RS.newRoom({ sessionId: 1, userId: 3 });
  RS.onHello(room, { epoch: 'e1', seq: 42 });
  RS.applySnapshot(room, snap());
  assert.equal(room.lastSeq, 42);
  assert.equal(room.conn, 'live');
});

test('onHello 同时接受整帧和仅 data 两种入参', () => {
  const a = RS.newRoom({ sessionId: 1, userId: 3 });
  RS.onHello(a, { seq: 0, type: 'hello', data: { epoch: 'e9', seq: 12 } });
  assert.equal(a.baseSeq, 12);
  assert.equal(a.epoch, 'e9');
});

test('resync：hello 后紧跟 resync，进入 snapshot，用快照整体替换，lastSeq=hello.seq', () => {
  const room = liveRoom({ seq: 10 });
  feed(room, F(11, 'message_new', { message: msg(500) }));
  assert.equal(room.lastSeq, 11);
  RS.markDisconnected(room);

  assert.deepEqual(RS.onHello(room, { epoch: 'e1', seq: 40 }), []);
  assert.equal(room.conn, 'helloPending');

  const r = feed(room, F(0, 'resync'));
  assert.equal(r.verdict, 'apply');
  assert.deepEqual(r.effects, ['fetchSnapshot']);
  assert.equal(room.conn, 'snapshot');
  assert.equal(room.lastSeq, 0);

  assert.equal(RS.acceptFrame(room, F(12, 'waiting', {})), 'drop');
  assert.equal(RS.acceptFrame(room, F(40, 'waiting', {})), 'drop');
  assert.equal(RS.acceptFrame(room, F(41, 'message_new', { message: msg(601) })), 'buffer');

  RS.applySnapshot(room, snap({ messages: [msg(600)] }));
  assert.deepEqual(room.messages.map((m) => m.id), [600, 601]);
  assert.equal(room.byId[500], undefined);
  assert.equal(room.lastSeq, 41);
  assert.equal(room.conn, 'live');
});

test('resync：snapshot 期间再收到 resync 帧直接丢弃', () => {
  const room = RS.newRoom({ sessionId: 1, userId: 3 });
  RS.onHello(room, { epoch: 'e1', seq: 5 });
  assert.equal(RS.acceptFrame(room, F(0, 'resync')), 'drop');
  assert.equal(room.conn, 'snapshot');
});

test('resync：live 下收到 resync 帧返回 resync，不改状态', () => {
  const room = liveRoom({ seq: 10 });
  assert.equal(RS.acceptFrame(room, F(0, 'resync')), 'resync');
  assert.equal(room.conn, 'live');
  assert.equal(room.lastSeq, 10);
});

test('epoch 变化与 resync 走同一条路：立即 snapshot，旧 lastSeq 不影响新 epoch 的帧', () => {
  const room = liveRoom({ seq: 10 });
  RS.markDisconnected(room);
  assert.deepEqual(RS.onHello(room, { epoch: 'e2', seq: 3 }), ['fetchSnapshot']);
  assert.equal(room.conn, 'snapshot');
  assert.equal(room.epoch, 'e2');

  assert.equal(RS.acceptFrame(room, F(3, 'waiting', {})), 'drop');
  assert.equal(RS.acceptFrame(room, F(4, 'floor_start', { floor: 1 })), 'buffer');
  RS.applySnapshot(room, snap());
  assert.equal(room.lastSeq, 4);
  assert.equal(room.floor.running, true);
});

test('epoch 变化后快照没拉成功就断线，下次重连仍然走快照', () => {
  const room = liveRoom({ seq: 10 });
  RS.markDisconnected(room);
  RS.onHello(room, { epoch: 'e2', seq: 3 });
  RS.markDisconnected(room);
  assert.deepEqual(RS.onHello(room, { epoch: 'e2', seq: 5 }), ['fetchSnapshot']);
  assert.equal(room.conn, 'snapshot');
});

test('主动 resync：resetCursor 清游标和缓冲，保留 epoch，下次 hello 走快照', () => {
  const room = liveRoom({ seq: 10 });
  room.pending.push(F(99, 'waiting'));
  RS.resetCursor(room);
  assert.equal(room.lastSeq, 0);
  assert.equal(room.pending.length, 0);
  assert.equal(room.conn, 'connecting');
  assert.equal(room.epoch, 'e1');
  assert.deepEqual(RS.onHello(room, { epoch: 'e1', seq: 20 }), ['fetchSnapshot']);
});

test('普通重连：hello 后补发帧，seq<=lastSeq 丢弃，其余按序应用，live 下跳号返回 resync', () => {
  const room = liveRoom({ seq: 10 });
  RS.markDisconnected(room);
  assert.deepEqual(RS.onHello(room, { epoch: 'e1', seq: 14 }), []);
  assert.equal(room.conn, 'helloPending');
  assert.equal(room.baseSeq, 14);

  assert.equal(feed(room, F(10, 'message_new', { message: msg(1) })).verdict, 'drop');
  assert.equal(room.conn, 'live');
  assert.equal(room.messages.length, 0);

  assert.equal(feed(room, F(11, 'message_new', { message: msg(2) })).verdict, 'apply');
  assert.equal(feed(room, F(12, 'message_new', { message: msg(3) })).verdict, 'apply');
  assert.equal(room.lastSeq, 12);
  assert.equal(feed(room, F(12, 'message_new', { message: msg(3) })).verdict, 'drop');
  assert.deepEqual(room.messages.map((m) => m.id), [2, 3]);

  assert.equal(RS.acceptFrame(room, F(14, 'message_new', { message: msg(5) })), 'resync');
  assert.equal(room.lastSeq, 12);
  assert.equal(room.byId[5], undefined);

  assert.equal(feed(room, F(13, 'message_new', { message: msg(4) })).verdict, 'apply');
  assert.equal(room.lastSeq, 13);
});

test('普通重连：hello 之后没有帧就一直停在 helloPending，且 gates 视为 live', () => {
  const room = liveRoom({ seq: 10, isHost: true });
  RS.markDisconnected(room);
  RS.onHello(room, { epoch: 'e1', seq: 10 });
  assert.equal(room.conn, 'helloPending');
  assert.equal(RS.gates(room).live, true);
  assert.equal(RS.gates(room).canContinue, true);
});

test('普通重连：hello 后第一帧是临时帧也确认为 live', () => {
  const room = liveRoom({ seq: 10 });
  RS.markDisconnected(room);
  RS.onHello(room, { epoch: 'e1', seq: 10 });
  const r = feed(room, F(0, 'kp_status', { text: '思考中' }));
  assert.equal(r.verdict, 'apply');
  assert.equal(room.conn, 'live');
  assert.equal(room.kpStatus, '思考中');
  assert.equal(room.lastSeq, 10);
});

test('临时帧（seq=0）不推进 lastSeq，snapshot 下丢弃', () => {
  const room = liveRoom({ seq: 10 });
  assert.equal(feed(room, F(0, 'kp_status', { text: 'a' })).verdict, 'apply');
  assert.equal(room.lastSeq, 10);
  RS.resetCursor(room);
  RS.onHello(room, { epoch: 'e1', seq: 10 });
  assert.equal(RS.acceptFrame(room, F(0, 'kp_status', { text: 'b' })), 'drop');
  assert.equal(room.kpStatus, 'a');
});

test('connecting / reconnecting 状态下收到的帧一律丢弃', () => {
  const room = liveRoom({ seq: 10 });
  RS.markDisconnected(room);
  assert.equal(RS.acceptFrame(room, F(11, 'waiting', {})), 'drop');
  const fresh = RS.newRoom({ sessionId: 1, userId: 3 });
  assert.equal(RS.acceptFrame(fresh, F(1, 'waiting', {})), 'drop');
});

test('message_new 重放不会把已 done 的消息打回 pending', () => {
  const room = liveRoom();
  feed(room, F(11, 'message_new', { message: writer(7) }));
  feed(room, F(12, 'message_update', { id: 7, status: 'done', content: '终稿' }));
  assert.equal(room.byId[7].status, 'done');

  RS.applyFrame(room, F(11, 'message_new', { message: writer(7) }));
  assert.equal(room.byId[7].status, 'done');
  assert.equal(room.byId[7].content, '终稿');
  assert.equal(room.streams[7], undefined);
  assert.equal(room.messages.length, 1);

  RS.applyFrame(room, F(13, 'message_new', { message: writer(8, { status: 'failed' }) }));
  RS.applyFrame(room, F(13, 'message_new', { message: writer(8) }));
  assert.equal(room.byId[8].status, 'failed');
});

test('快照与缓冲帧重叠时收敛：快照里已 done 的 writer 不会被缓冲的 message_new 打回', () => {
  const room = RS.newRoom({ sessionId: 1, userId: 3 });
  RS.onHello(room, { epoch: 'e1', seq: 5 });
  RS.acceptFrame(room, F(6, 'message_new', { message: writer(20) }));
  RS.acceptFrame(room, F(7, 'message_update', { id: 20, status: 'done', content: '全文' }));
  RS.applySnapshot(room, snap({ messages: [writer(20, { status: 'done', content: '全文' })] }));
  assert.equal(room.byId[20].status, 'done');
  assert.equal(room.byId[20].content, '全文');
  assert.equal(room.streams[20], undefined);
  assert.equal(room.messages.length, 1);
  assert.equal(room.lastSeq, 7);
});

test('消息按 id 升序 upsert，byId 与 messages 指向同一对象', () => {
  const room = liveRoom();
  RS.applyFrame(room, F(11, 'message_new', { message: msg(30, { content: 'c' }) }));
  RS.applyFrame(room, F(12, 'message_new', { message: msg(10) }));
  RS.applyFrame(room, F(13, 'message_new', { message: msg(20) }));
  RS.applyFrame(room, F(14, 'message_new', { message: msg(20, { content: '新' }) }));
  assert.deepEqual(room.messages.map((m) => m.id), [10, 20, 30]);
  assert.equal(room.byId[20], room.messages[1]);
  assert.equal(room.byId[20].content, '新');
});

test('message_update 的 content 覆盖本地流式文本，同时写入 images 和 error，并删除 stream', () => {
  const room = liveRoom();
  feed(room, F(11, 'message_new', { message: writer(7) }));
  feed(room, F(0, 'writer_token', { message_id: 7, text: '雨' }));
  feed(room, F(0, 'writer_token', { message_id: 7, text: '夜' }));
  assert.equal(room.streams[7].text, '雨夜');

  feed(room, F(12, 'message_update', { id: 7, status: 'done', content: '雨夜，钟声响了。' }));
  assert.equal(room.byId[7].content, '雨夜，钟声响了。');
  assert.equal(room.byId[7].status, 'done');
  assert.equal(room.streams[7], undefined);

  feed(room, F(13, 'message_new', { message: msg(8, { kind: 'image', status: 'pending' }) }));
  feed(room, F(14, 'message_update', { id: 8, status: 'done', images: ['/api/images/a.png'] }));
  assert.deepEqual(room.byId[8].images, ['/api/images/a.png']);

  feed(room, F(15, 'message_new', { message: msg(9, { kind: 'image', status: 'pending', meta: { nsfw: true } }) }));
  feed(room, F(16, 'message_update', { id: 9, status: 'failed', error: '额度不足' }));
  assert.equal(room.byId[9].status, 'failed');
  assert.equal(room.byId[9].meta.error, '额度不足');
  assert.equal(room.byId[9].meta.nsfw, true);
});

test('message_update 先于 message_new 到达时不报错也不残留 stream', () => {
  const room = liveRoom();
  assert.deepEqual(RS.applyFrame(room, F(11, 'message_update', { id: 99, status: 'done', content: 'x' })), []);
  assert.equal(room.byId[99], undefined);
  assert.equal(room.messages.length, 0);
});

test('writer 流：live 下看到 message_new 之后才接收 token', () => {
  const room = liveRoom();
  feed(room, F(0, 'writer_token', { message_id: 7, text: '早' }));
  assert.equal(room.streams[7], undefined);

  feed(room, F(11, 'message_new', { message: writer(7) }));
  assert.deepEqual(room.streams[7], { text: '', live: true, broken: false });
  feed(room, F(0, 'writer_token', { message_id: 7, text: '雨' }));
  assert.equal(room.streams[7].text, '雨');
});

test('writer 流：helloPending 下的 message_new 同样开启 live 流', () => {
  const room = liveRoom();
  RS.markDisconnected(room);
  RS.onHello(room, { epoch: 'e1', seq: 12 });
  assert.equal(room.conn, 'helloPending');
  RS.applyFrame(room, F(11, 'message_new', { message: writer(7) }));
  assert.equal(room.streams[7].live, true);
});

test('writer 流：快照里加载的 pending writer 不接收 token', () => {
  const room = RS.newRoom({ sessionId: 1, userId: 3 });
  RS.onHello(room, { epoch: 'e1', seq: 5 });
  RS.applySnapshot(room, snap({ messages: [writer(7, { content: '前半' })] }));
  assert.deepEqual(room.streams[7], { text: '', live: false, broken: false });
  feed(room, F(0, 'writer_token', { message_id: 7, text: '雨' }));
  assert.equal(room.streams[7].text, '');

  feed(room, F(6, 'message_update', { id: 7, status: 'done', content: '完整正文' }));
  assert.equal(room.byId[7].content, '完整正文');
  assert.equal(room.streams[7], undefined);
});

test('writer 流：快照期间缓冲的 message_new 不会得到 live 流', () => {
  const room = RS.newRoom({ sessionId: 1, userId: 3 });
  RS.onHello(room, { epoch: 'e1', seq: 5 });
  RS.acceptFrame(room, F(6, 'message_new', { message: writer(8) }));
  RS.applySnapshot(room, snap());
  assert.equal(room.streams[8].live, false);
  feed(room, F(0, 'writer_token', { message_id: 8, text: '雨' }));
  assert.equal(room.streams[8].text, '');
});

test('writer 流：断线后 broken，保留已收文本但忽略后续 token，message_update 覆盖', () => {
  const room = liveRoom();
  feed(room, F(11, 'message_new', { message: writer(7) }));
  feed(room, F(0, 'writer_token', { message_id: 7, text: '雨' }));
  room.kpStatus = 'KP 正在思考…';

  RS.markDisconnected(room);
  assert.equal(room.conn, 'reconnecting');
  assert.equal(room.kpStatus, '');
  assert.deepEqual(room.streams[7], { text: '雨', live: true, broken: true });

  RS.onHello(room, { epoch: 'e1', seq: 12 });
  RS.applyFrame(room, F(0, 'writer_token', { message_id: 7, text: '夜' }));
  assert.equal(room.streams[7].text, '雨');

  feed(room, F(12, 'message_update', { id: 7, status: 'done', content: '雨夜' }));
  assert.equal(room.byId[7].content, '雨夜');
  assert.equal(room.streams[7], undefined);
});

test('message_new：ask / kp 清空 me.submitted，自己的 player 消息清空 draft', () => {
  const room = liveRoom({ userId: 3 });
  room.me.submitted = true;
  room.me.draft = '我推门';
  RS.applyFrame(room, F(11, 'message_new', { message: msg(1, { kind: 'player', user_id: 4 }) }));
  assert.equal(room.me.draft, '我推门');
  assert.equal(room.me.submitted, true);
  RS.applyFrame(room, F(12, 'message_new', { message: msg(2, { kind: 'player', user_id: 3 }) }));
  assert.equal(room.me.draft, '');
  assert.equal(room.me.submitted, true);
  RS.applyFrame(room, F(13, 'message_new', { message: msg(3, { kind: 'ask' }) }));
  assert.equal(room.me.submitted, false);

  room.me.submitted = true;
  RS.applyFrame(room, F(14, 'message_new', { message: msg(4, { kind: 'kp' }) }));
  assert.equal(room.me.submitted, false);
});

test('message_new 重放不会再次清空 me.submitted', () => {
  const room = liveRoom();
  RS.applyFrame(room, F(11, 'message_new', { message: msg(3, { kind: 'ask' }) }));
  room.me.submitted = true;
  RS.applyFrame(room, F(11, 'message_new', { message: msg(3, { kind: 'ask' }) }));
  assert.equal(room.me.submitted, true);
});

test('spotlight：字段映射，askedMe 由 userIds 推出，不再被点名则清空 me.submitted', () => {
  const room = liveRoom({ userId: 3 });
  room.me.submitted = true;
  RS.applyFrame(room, F(11, 'spotlight', { user_ids: [3, 4], names: ['我', '鲍勃'], question: '怎么开门？', kp_floor: false, stop: '' }));
  assert.deepEqual(room.spotlight, {
    userIds: [3, 4], names: ['我', '鲍勃'], question: '怎么开门？', kpFloor: false, askedMe: true, stop: '',
  });
  assert.equal(room.me.submitted, true);

  RS.applyFrame(room, F(12, 'spotlight', { user_ids: [4], names: ['鲍勃'], question: 'q', kp_floor: false, stop: '' }));
  assert.equal(room.spotlight.askedMe, false);
  assert.equal(room.me.submitted, false);

  RS.applyFrame(room, F(13, 'spotlight', { user_ids: [], names: [], question: '', kp_floor: true, stop: 'host_stop' }));
  assert.deepEqual(room.spotlight.userIds, []);
  assert.equal(room.spotlight.kpFloor, true);
  assert.equal(room.spotlight.stop, 'host_stop');
});

test('waiting：设值；被点名且未提交时返回 fetchChatStatus', () => {
  const room = liveRoom({ userId: 3 });
  const payload = { pending: 1, total: 2, submitted_names: ['爱丽丝'], pending_names: ['我'] };

  assert.deepEqual(RS.applyFrame(room, F(11, 'waiting', payload)), []);
  assert.deepEqual(room.waiting, { pending: 1, total: 2, submittedNames: ['爱丽丝'], pendingNames: ['我'] });

  RS.applyFrame(room, F(12, 'spotlight', { user_ids: [3], names: ['我'], question: '', kp_floor: false, stop: '' }));
  assert.deepEqual(RS.applyFrame(room, F(13, 'waiting', payload)), ['fetchChatStatus']);
  assert.deepEqual(RS.applyFrame(room, F(13, 'waiting', payload)), ['fetchChatStatus']);
  assert.equal(room.waiting.pending, 1);

  room.me.submitted = true;
  assert.deepEqual(RS.applyFrame(room, F(14, 'waiting', { pending: 0, total: 2, submitted_names: ['爱丽丝', '我'], pending_names: [] })), []);
  assert.equal(room.waiting.pending, 0);
});

test('applyChatStatus：只覆盖周期、发言权、等待进度和已提交，不动消息和游标', () => {
  const room = liveRoom({ userId: 3, isHost: true });
  RS.applyFrame(room, F(11, 'message_new', { message: { id: 1, kind: 'kp', status: 'done', content: 'x' } }));
  room.floor.starting = true;
  const seqBefore = room.lastSeq;

  RS.applyChatStatus(room, {
    kp_running: false,
    floor: null,
    submitted: true,
    spotlight: { user_ids: [3, 4], names: ['我', '鲍勃'], question: '怎么办', kp_floor: false, asked_me: true, stop: '' },
    waiting: { pending: 1, total: 2, submitted_names: ['我'], pending_names: ['鲍勃'] },
  });

  assert.equal(room.floor.running, false);
  assert.equal(room.floor.starting, false);
  assert.equal(room.spotlight.askedMe, true);
  assert.equal(room.me.submitted, true);
  assert.deepEqual(room.waiting.pendingNames, ['鲍勃']);
  assert.equal(room.messages.length, 1);
  assert.equal(room.lastSeq, seqBefore);

  RS.applyChatStatus(room, { kp_running: true, floor: { floor: 5 }, spotlight: {}, waiting: {} });
  assert.equal(room.floor.running, true);
  assert.equal(room.floor.number, 5);
});

test('floor_start 清空上一轮的等待进度', () => {
  const room = liveRoom({ userId: 3 });
  RS.applyFrame(room, F(11, 'waiting', { pending: 1, total: 2, submitted_names: ['爱丽丝'], pending_names: ['鲍勃'] }));
  assert.equal(room.waiting.total, 2);

  RS.applyFrame(room, F(12, 'floor_start', { floor: 4, reason: 'player' }));
  assert.deepEqual(room.waiting, { pending: 0, total: 0, submittedNames: [], pendingNames: [] });
  assert.equal(room.floor.running, true);
});

test('session 事件：更新 status 并返回 fetchSession，未知 status 不改本地值', () => {
  const room = liveRoom();
  assert.equal(room.status, 'playing');
  assert.deepEqual(RS.applyFrame(room, F(11, 'session', { status: 'ended', reason: 'end' })), ['fetchSession']);
  assert.equal(room.status, 'ended');
  assert.deepEqual(RS.applyFrame(room, F(12, 'session', { status: 'lobby', reason: 'cards' })), ['fetchSession']);
  assert.equal(room.status, 'lobby');
  assert.deepEqual(RS.applyFrame(room, F(13, 'session', { status: 'bogus', reason: 'join' })), ['fetchSession']);
  assert.equal(room.status, 'lobby');
});

test('floor_end(context) 紧跟 floor_start(resume)，最终 running=true 且编号更新', () => {
  const room = liveRoom({ isHost: true });
  feed(room, F(11, 'floor_start', { floor: 3, reason: 'player' }));
  room.floor.starting = true;
  feed(room, F(0, 'kp_status', { text: 'KP 正在整理记忆' }));
  assert.equal(room.kpStatus, 'KP 正在整理记忆');

  feed(room, F(12, 'floor_end', { floor: 3, reason: 'context', messages: 2 }));
  assert.equal(room.floor.running, false);
  assert.equal(room.kpStatus, '');
  feed(room, F(13, 'floor_start', { floor: 4, reason: 'resume' }));
  assert.equal(room.floor.running, true);
  assert.equal(room.floor.starting, false);
  assert.equal(room.floor.number, 4);
  assert.equal(RS.gates(room).canStop, true);
  assert.equal(RS.gates(room).canContinue, false);
});

test('floor_end 遇到未知 reason 也按周期已结束处理', () => {
  const room = liveRoom();
  feed(room, F(11, 'floor_start', { floor: 1, reason: 'opening' }));
  feed(room, F(12, 'floor_end', { floor: 1, reason: 'something_new', messages: 0 }));
  assert.equal(room.floor.running, false);
});

test('kp_status 设置提示文案', () => {
  const room = liveRoom();
  RS.applyFrame(room, F(0, 'kp_status', { text: 'KP 正在思考…' }));
  assert.equal(room.kpStatus, 'KP 正在思考…');
});

test('未知事件被忽略：不报错、不改状态、无副作用', () => {
  const room = liveRoom();
  const before = JSON.parse(JSON.stringify(room));
  assert.deepEqual(RS.applyFrame(room, F(11, 'future_event', { x: 1 })), []);
  assert.deepEqual(JSON.parse(JSON.stringify(room)), before);
  assert.deepEqual(RS.applyFrame(room, null), []);
  assert.deepEqual(RS.applyFrame(room, { seq: 0 }), []);
  assert.equal(feed(room, F(11, 'future_event')).verdict, 'apply');
  assert.equal(room.lastSeq, 11);
});

test('applySnapshot：整体替换 messages / spotlight / waiting / floor / submitted / status', () => {
  const room = liveRoom({ userId: 3 });
  feed(room, F(11, 'message_new', { message: msg(1) }));
  RS.markDisconnected(room);
  RS.resetCursor(room);
  RS.onHello(room, { epoch: 'e1', seq: 30 });

  RS.applySnapshot(room, {
    session: { status: 'ended' },
    messages: [msg(9), msg(5), writer(7), msg(5, { content: '重复项' })],
    chatStatus: {
      kp_running: false,
      submitted: true,
      floor: { floor: 6, started_at: 'x', beats: 2 },
      spotlight: { user_ids: [4], names: ['鲍勃'], question: '去哪', kp_floor: false, asked_me: true, stop: '' },
      waiting: { pending: 1, total: 2, submitted_names: ['爱丽丝'], pending_names: ['鲍勃'] },
    },
  });
  assert.equal(room.status, 'ended');
  assert.deepEqual(room.messages.map((m) => m.id), [5, 7, 9]);
  assert.equal(room.byId[1], undefined);
  assert.deepEqual(room.floor, { running: true, number: 6, starting: false });
  assert.equal(room.me.submitted, true);
  assert.equal(room.spotlight.askedMe, true);
  assert.deepEqual(room.spotlight.userIds, [4]);
  assert.deepEqual(room.waiting, { pending: 1, total: 2, submittedNames: ['爱丽丝'], pendingNames: ['鲍勃'] });
  assert.deepEqual(Object.keys(room.streams), ['7']);
  assert.equal(room.lastSeq, 30);
});

test('applySnapshot：仅 kp_running 为 true 也算周期运行中；askedMe 可由 userIds 推出', () => {
  const room = RS.newRoom({ sessionId: 1, userId: 3 });
  RS.onHello(room, { epoch: 'e1', seq: 1 });
  RS.applySnapshot(room, snap({
    chatStatus: {
      kp_running: true,
      floor: null,
      spotlight: { user_ids: [3], names: ['我'], question: '', kp_floor: false, asked_me: false, stop: '' },
    },
  }));
  assert.equal(room.floor.running, true);
  assert.equal(room.floor.number, 0);
  assert.equal(room.spotlight.askedMe, true);
  assert.equal(room.me.submitted, false);
});

test('applySnapshot：缺省的 chatStatus 字段按空值处理，不抛错', () => {
  const room = RS.newRoom({ sessionId: 1, userId: 3 });
  RS.onHello(room, { epoch: 'e1', seq: 1 });
  RS.applySnapshot(room, { session: { status: 'lobby' }, messages: null, chatStatus: {} });
  assert.equal(room.status, 'lobby');
  assert.deepEqual(room.messages, []);
  assert.deepEqual(room.floor, { running: false, number: 0, starting: false });
  assert.deepEqual(room.spotlight.userIds, []);
});

test('applySnapshot：拉快照期间连接已断时数据照常应用，但不把 conn 写回 live', () => {
  const room = RS.newRoom({ sessionId: 1, userId: 3 });
  RS.onHello(room, { epoch: 'e1', seq: 5 });
  RS.markDisconnected(room);
  RS.applySnapshot(room, snap({ messages: [msg(1)] }));
  assert.equal(room.conn, 'reconnecting');
  assert.equal(room.messages.length, 1);
  assert.equal(room.lastSeq, 5);
  assert.equal(RS.gates(room).live, false);
  assert.deepEqual(RS.onHello(room, { epoch: 'e1', seq: 7 }), []);
});

test('markDisconnected：所有 stream 标记 broken，kpStatus 清空，conn=reconnecting', () => {
  const room = liveRoom();
  feed(room, F(11, 'message_new', { message: writer(1) }));
  feed(room, F(12, 'message_new', { message: writer(2) }));
  feed(room, F(0, 'writer_token', { message_id: 1, text: 'a' }));
  feed(room, F(0, 'kp_status', { text: '思考中' }));
  RS.markDisconnected(room);
  assert.equal(room.conn, 'reconnecting');
  assert.equal(room.kpStatus, '');
  assert.deepEqual(room.streams[1], { text: 'a', live: true, broken: true });
  assert.deepEqual(room.streams[2], { text: '', live: true, broken: true });
  assert.equal(room.lastSeq, 12);
});

test('所有函数原地修改：嵌套对象引用不变', () => {
  const room = liveRoom();
  const refs = {};
  for (const k of ['messages', 'byId', 'streams', 'floor', 'spotlight', 'waiting', 'me', 'pending']) refs[k] = room[k];

  feed(room, F(11, 'message_new', { message: writer(1) }));
  RS.markDisconnected(room);
  RS.onHello(room, { epoch: 'e2', seq: 3 });
  RS.acceptFrame(room, F(4, 'floor_start', { floor: 1 }));
  RS.applySnapshot(room, snap({
    messages: [msg(2)],
    chatStatus: { spotlight: { user_ids: [3], names: ['我'], question: '', kp_floor: false, asked_me: true, stop: '' } },
  }));
  RS.resetCursor(room);

  for (const k of Object.keys(refs)) assert.equal(room[k], refs[k], k);
});

test('在深层代理（模拟 Alpine）上调用，状态写进原始对象', () => {
  const writes = [];
  const wrap = (obj) => new Proxy(obj, {
    get(t, k, r) {
      const v = Reflect.get(t, k, r);
      return v && typeof v === 'object' ? wrap(v) : v;
    },
    set(t, k, v, r) {
      writes.push(String(k));
      return Reflect.set(t, k, v, r);
    },
    deleteProperty(t, k) {
      writes.push(String(k));
      return Reflect.deleteProperty(t, k);
    },
  });

  const target = RS.newRoom({ sessionId: 1, userId: 3, isHost: true });
  const room = wrap(target);
  RS.onHello(room, { epoch: 'e1', seq: 5 });
  RS.acceptFrame(room, F(6, 'floor_start', { floor: 1 }));
  RS.applySnapshot(room, snap({ messages: [writer(1)] }));
  feed(room, F(7, 'message_new', { message: writer(2) }));
  feed(room, F(0, 'writer_token', { message_id: 2, text: '雨' }));

  assert.equal(target.conn, 'live');
  assert.equal(target.lastSeq, 7);
  assert.equal(target.floor.running, true);
  assert.deepEqual(target.messages.map((m) => m.id), [1, 2]);
  assert.equal(target.streams[2].text, '雨');
  assert.ok(writes.includes('conn') && writes.includes('text'));
});

test('gates 真值表', () => {
  const base = () => {
    const r = RS.newRoom({ sessionId: 1, userId: 3, isHost: true });
    r.conn = 'live';
    r.status = 'playing';
    r.spotlight.userIds = [3];
    r.spotlight.askedMe = true;
    return r;
  };
  const cases = [
    ['被点名，周期空闲', (r) => r, { canSpeak: true, canContinue: true, canStop: false, live: true }],
    ['未被点名', (r) => { r.spotlight.userIds = [4]; r.spotlight.askedMe = false; }, { canSpeak: false, canContinue: true, canStop: false, live: true }],
    ['周期运行中', (r) => { r.floor.running = true; }, { canSpeak: false, canContinue: false, canStop: true, live: true }],
    ['spotlight.stop 非空（userIds 为空）', (r) => { r.spotlight.userIds = []; r.spotlight.askedMe = false; r.spotlight.stop = 'host_stop'; }, { canSpeak: false, canContinue: true, canStop: false, live: true }],
    ['点击继续后等待 floor_start', (r) => { r.floor.starting = true; }, { canSpeak: true, canContinue: false, canStop: false, live: true }],
    ['conn=helloPending 视为 live', (r) => { r.conn = 'helloPending'; }, { canSpeak: true, canContinue: true, canStop: false, live: true }],
    ['conn=snapshot', (r) => { r.conn = 'snapshot'; }, { canSpeak: false, canContinue: false, canStop: false, live: false }],
    ['conn=reconnecting 且周期运行中', (r) => { r.conn = 'reconnecting'; r.floor.running = true; }, { canSpeak: false, canContinue: false, canStop: false, live: false }],
    ['conn=connecting', (r) => { r.conn = 'connecting'; }, { canSpeak: false, canContinue: false, canStop: false, live: false }],
    ['status=ended', (r) => { r.status = 'ended'; }, { canSpeak: false, canContinue: false, canStop: false, live: true }],
    ['status=lobby', (r) => { r.status = 'lobby'; }, { canSpeak: false, canContinue: false, canStop: false, live: true }],
    ['非房主，被点名', (r) => { r.isHost = false; }, { canSpeak: true, canContinue: false, canStop: false, live: true }],
    ['非房主，周期运行中', (r) => { r.isHost = false; r.floor.running = true; }, { canSpeak: false, canContinue: false, canStop: false, live: true }],
  ];
  for (const [name, mutate, want] of cases) {
    const room = base();
    mutate(room);
    assert.deepEqual(RS.gates(room), want, name);
  }
});

test('parseDice：解析多段、难度、暗骰，匹配不上的段保留 raw', () => {
  const input = '侦查鉴定: 45 ; 聆听鉴定: 12 难度:困难 ; 潜行鉴定: 80 难度:极难  (暗骰); 乱七八糟; ';
  assert.deepEqual(RS.parseDice(input), [
    { what: '侦查', roll: '45', level: '', hidden: false },
    { what: '聆听', roll: '12', level: '困难', hidden: false },
    { what: '潜行', roll: '80', level: '极难', hidden: true },
    { raw: '乱七八糟' },
  ]);
});

test('parseDice：只有暗骰没有难度，兼容全角冒号', () => {
  assert.deepEqual(RS.parseDice('图书馆使用鉴定: 33  (暗骰)'), [
    { what: '图书馆使用', roll: '33', level: '', hidden: true },
  ]);
  assert.deepEqual(RS.parseDice('力量鉴定：50 难度：普通'), [
    { what: '力量', roll: '50', level: '普通', hidden: false },
  ]);
});

test('parseDice：空输入返回 []', () => {
  assert.deepEqual(RS.parseDice(''), []);
  assert.deepEqual(RS.parseDice(undefined), []);
  assert.deepEqual(RS.parseDice(null), []);
  assert.deepEqual(RS.parseDice('; ;'), []);
});

test('parseTimePoint：拆出天数、时刻和已过时间', () => {
  assert.deepEqual(RS.parseTimePoint('第2天 08:30(距开局已过16h30m)'), {
    raw: '第2天 08:30(距开局已过16h30m)',
    label: '第2天 08:30',
    elapsed: '距开局已过16h30m',
  });
  assert.deepEqual(RS.parseTimePoint('第1天 8:00（距开局已过0m）'), {
    raw: '第1天 8:00（距开局已过0m）',
    label: '第1天 8:00',
    elapsed: '距开局已过0m',
  });
  assert.deepEqual(RS.parseTimePoint('第3天 21:00'), { raw: '第3天 21:00', label: '第3天 21:00', elapsed: '' });
});

test('parseTimePoint：格式不符保留原文，空输入返回 null', () => {
  assert.deepEqual(RS.parseTimePoint('黄昏时分'), { raw: '黄昏时分', label: '黄昏时分', elapsed: '' });
  assert.equal(RS.parseTimePoint(''), null);
  assert.equal(RS.parseTimePoint('   '), null);
  assert.equal(RS.parseTimePoint(undefined), null);
  assert.equal(RS.parseTimePoint(null), null);
});

test('模块导出的 API 清单', () => {
  assert.deepEqual(
    Object.keys(RS).sort(),
    ['acceptFrame', 'applyChatStatus', 'applyFrame', 'applySnapshot', 'gates', 'markDisconnected', 'newRoom', 'onHello', 'parseDice', 'parseTimePoint', 'resetCursor'],
  );
});

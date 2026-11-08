// NOTE: 纯逻辑模块，不依赖 DOM / Alpine / fetch / WebSocket；所有函数原地修改传入的 room（Alpine 代理必须原地改才会触发渲染），副作用以 effects 字符串数组返回由调用方执行
(function (root) {
  'use strict';

  const STATUSES = ['lobby', 'playing', 'ended'];

  // NOTE: 骰子和时间点的正则沿用旧前端的写法，保证解析结果一致
  const DICE_RE = /^(.*?)鉴定[:：]\s*(\S+)(?:\s*难度[:：]\s*([^\s(（]+))?/;
  const DICE_HIDDEN_RE = /[(（]\s*暗骰\s*[)）]/;
  const TIME_POINT_RE = /^第(\d+)天\s*(\d{1,2}:\d{2})(?:[(（](.*?)[)）])?/;

  function newRoom(opts) {
    const o = opts || {};
    return {
      sessionId: o.sessionId,
      userId: o.userId,
      isHost: !!o.isHost,
      epoch: '',
      lastSeq: 0,
      conn: 'connecting',
      baseSeq: 0,
      pending: [],
      messages: [],
      byId: {},
      streams: {},
      floor: { running: false, number: 0, starting: false },
      spotlight: { userIds: [], names: [], question: '', kpFloor: false, askedMe: false, stop: '' },
      waiting: { pending: 0, total: 0, submittedNames: [], pendingNames: [] },
      me: { submitted: false, draft: '' },
      kpStatus: '',
      status: 'lobby',
    };
  }

  function sameId(a, b) {
    return a != null && b != null && String(a) === String(b);
  }

  function includesId(list, id) {
    return Array.isArray(list) && list.some((x) => sameId(x, id));
  }

  function asArray(v) {
    return Array.isArray(v) ? v.slice() : [];
  }

  function clearObject(o) {
    for (const k of Object.keys(o)) delete o[k];
  }

  function isLive(room) {
    return room.conn === 'live' || room.conn === 'helloPending';
  }

  // NOTE: pending 的优先级低于 done/failed，保证消息状态只升不降（重放 message_new 不会把已完成的消息打回 pending）
  function statusRank(status) {
    return status === 'pending' ? 0 : 1;
  }

  function resetCursor(room) {
    room.lastSeq = 0;
    room.pending.length = 0;
    room.conn = 'connecting';
  }

  function onHello(room, hello) {
    // NOTE: 同时接受整帧和仅 data 两种形态；整帧的 seq 恒为 0，误传会让 baseSeq 静默变成 0
    const h = hello && hello.type === 'hello' && hello.data ? hello.data : hello || {};
    const epoch = h.epoch == null ? '' : String(h.epoch);
    const epochChanged = room.epoch !== '' && room.epoch !== epoch;
    const needSnapshot = room.lastSeq === 0 || epochChanged;
    room.baseSeq = Number(h.seq) || 0;
    room.pending.length = 0;
    room.epoch = epoch;
    room.conn = needSnapshot ? 'snapshot' : 'helloPending';
    // NOTE: epoch 已保存但快照还没应用；清零游标后即使快照拉取失败，下次重连也会重新走快照，而不是带着失效游标进入 helloPending（resync 帧的路径同理）
    if (needSnapshot) room.lastSeq = 0;
    return needSnapshot ? ['fetchSnapshot'] : [];
  }

  function acceptFrame(room, frame) {
    if (!frame || typeof frame.type !== 'string' || frame.type === 'hello') return 'drop';
    const conn = room.conn;

    if (frame.type === 'resync') {
      // NOTE: helloPending 下的 resync 帧返回 apply，由 applyFrame 完成切换并返回 fetchSnapshot；'resync' 返回值只表示"出现缺口"，调用方应 resetCursor 后重连
      if (conn === 'helloPending') return 'apply';
      if (conn === 'snapshot') return 'drop';
      return conn === 'live' ? 'resync' : 'drop';
    }

    const inSnapshot = conn === 'snapshot';
    if (conn === 'helloPending') {
      // NOTE: resync 一定紧跟 hello，所以 hello 后的第一帧不是 resync 就确认是无缝补发，此刻才进入 live
      room.conn = 'live';
    } else if (conn !== 'live' && !inSnapshot) {
      return 'drop';
    }

    const seq = Number(frame.seq) || 0;
    if (seq <= 0) return inSnapshot ? 'drop' : 'apply';

    if (inSnapshot) {
      // NOTE: snapshot 期间只看 baseSeq，不看 lastSeq；epoch 变化后旧 lastSeq 可能比新 epoch 的 seq 还大，按它丢帧会丢事件
      if (seq <= room.baseSeq) return 'drop';
      room.pending.push(frame);
      return 'buffer';
    }

    if (seq <= room.lastSeq) return 'drop';
    if (seq > room.lastSeq + 1) return 'resync';
    room.lastSeq = seq;
    return 'apply';
  }

  function upsertMessage(room, incoming) {
    const id = Number(incoming.id);
    if (!Number.isFinite(id)) return;
    const existing = room.byId[id];
    if (existing) {
      if (statusRank(incoming.status) < statusRank(existing.status)) return;
      Object.assign(existing, incoming, { id });
      return;
    }
    const msg = Object.assign({}, incoming, { id });
    let i = room.messages.length;
    while (i > 0 && room.messages[i - 1].id > id) i--;
    room.messages.splice(i, 0, msg);
    room.byId[id] = msg;
  }

  function onMessageNew(room, data) {
    const m = data.message;
    if (!m || m.id == null) return;
    const id = Number(m.id);
    // NOTE: 只有首次见到的消息才产生副作用，快照与缓冲帧重叠时的重放不会清掉 me.submitted / 重建流
    const isNew = !room.byId[id];
    upsertMessage(room, m);
    const stored = room.byId[id];
    if (!stored) return;
    if (stored.status !== 'pending') delete room.streams[id];
    if (!isNew) return;

    if (stored.kind === 'writer' && stored.status === 'pending' && !room.streams[id]) {
      room.streams[id] = { text: '', live: isLive(room), broken: false };
    }
    if (stored.kind === 'ask' || stored.kind === 'kp') room.me.submitted = false;
    if (stored.kind === 'player' && sameId(stored.user_id, room.userId)) room.me.draft = '';
  }

  function onMessageUpdate(room, d) {
    if (d.id == null) return;
    const id = Number(d.id);
    const msg = room.byId[id];
    if (msg) {
      if (typeof d.status === 'string' && statusRank(d.status) >= statusRank(msg.status)) msg.status = d.status;
      if (typeof d.content === 'string') msg.content = d.content;
      if (Array.isArray(d.images)) msg.images = d.images.slice();
      if (d.error) {
        if (!msg.meta || typeof msg.meta !== 'object') msg.meta = {};
        msg.meta.error = d.error;
      }
    }
    delete room.streams[id];
  }

  function onWriterToken(room, d) {
    const st = room.streams[d.message_id];
    if (!st || !st.live || st.broken) return;
    if (typeof d.text === 'string') st.text += d.text;
  }

  function setSpotlight(room, src) {
    const ids = asArray(src.user_ids);
    const askedMe = includesId(ids, room.userId);
    Object.assign(room.spotlight, {
      userIds: ids,
      names: asArray(src.names),
      question: src.question || '',
      kpFloor: typeof src.kp_floor === 'boolean' ? src.kp_floor : ids.length === 0,
      askedMe,
      stop: src.stop || '',
    });
    return askedMe;
  }

  function setWaiting(room, src) {
    Object.assign(room.waiting, {
      pending: Number(src.pending) || 0,
      total: Number(src.total) || 0,
      submittedNames: asArray(src.submitted_names),
      pendingNames: asArray(src.pending_names),
    });
  }

  function applyFrame(room, frame) {
    const effects = [];
    if (!frame || typeof frame.type !== 'string') return effects;
    const d = frame.data || {};

    switch (frame.type) {
      case 'resync':
        if (room.conn === 'helloPending') {
          room.pending.length = 0;
          room.lastSeq = 0;
          room.conn = 'snapshot';
          effects.push('fetchSnapshot');
        }
        break;
      case 'message_new':
        onMessageNew(room, d);
        break;
      case 'message_update':
        onMessageUpdate(room, d);
        break;
      case 'writer_token':
        onWriterToken(room, d);
        break;
      case 'spotlight':
        if (!setSpotlight(room, d)) room.me.submitted = false;
        break;
      case 'waiting':
        setWaiting(room, d);
        if (room.spotlight.askedMe && !room.me.submitted) effects.push('fetchChatStatus');
        break;
      case 'floor_start':
        room.floor.running = true;
        room.floor.starting = false;
        if (d.floor != null) room.floor.number = Number(d.floor) || 0;
        // NOTE: 周期一开始，上一轮的等待进度就作废；否则周期结束后的新点名会显示残留的"还差几人"
        setWaiting(room, {});
        break;
      case 'floor_end':
        room.floor.running = false;
        room.kpStatus = '';
        break;
      case 'kp_status':
        room.kpStatus = typeof d.text === 'string' ? d.text : '';
        break;
      case 'session':
        if (STATUSES.includes(d.status)) room.status = d.status;
        effects.push('fetchSession');
        break;
      default:
        break;
    }
    return effects;
  }

  function replaceMessages(room, list) {
    const byId = new Map();
    for (const m of Array.isArray(list) ? list : []) {
      if (!m || m.id == null || !Number.isFinite(Number(m.id))) continue;
      byId.set(Number(m.id), Object.assign({}, m, { id: Number(m.id) }));
    }
    const sorted = Array.from(byId.values()).sort((a, b) => a.id - b.id);

    room.messages.splice(0, room.messages.length);
    clearObject(room.byId);
    clearObject(room.streams);
    for (const m of sorted) {
      room.messages.push(m);
      room.byId[m.id] = m;
      if (m.kind === 'writer' && m.status === 'pending') {
        room.streams[m.id] = { text: '', live: false, broken: false };
      }
    }
  }

  // NOTE: 只覆盖 chat-status 能反映的部分（周期、发言权、等待进度、是否已提交），不碰消息列表和连接游标
  function applyChatStatus(room, cs) {
    const c = cs || {};
    const f = c.floor;
    room.floor.running = !!(f || c.kp_running);
    room.floor.number = f ? Number(f.floor) || 0 : 0;
    // NOTE: starting 只是本地"已点继续、等 floor_start"的标记；chat-status 能反映真实状态，整体重置以免丢了 floor_start 后按钮卡死
    room.floor.starting = false;

    const sp = c.spotlight || {};
    const askedMe = setSpotlight(room, sp);
    room.spotlight.askedMe = !!sp.asked_me || askedMe;
    setWaiting(room, c.waiting || {});
    room.me.submitted = !!c.submitted;
  }

  // NOTE: 调用方必须自行丢弃过期的快照响应（例如按连接代号比对），模块不判断一次拉取是否已过期
  function applySnapshot(room, snap) {
    const s = snap || {};

    if (s.session && STATUSES.includes(s.session.status)) room.status = s.session.status;
    replaceMessages(room, s.messages);
    applyChatStatus(room, s.chatStatus);

    const bySeq = new Map();
    for (const fr of room.pending) {
      if (fr && Number(fr.seq) > room.baseSeq) bySeq.set(Number(fr.seq), fr);
    }
    const buffered = Array.from(bySeq.keys())
      .sort((a, b) => a - b)
      .map((seq) => bySeq.get(seq));
    room.pending.length = 0;

    const effects = [];
    let last = room.baseSeq;
    // NOTE: 重放缓冲帧时 conn 仍是 snapshot，所以其中的 pending writer 会得到 live:false；它们的早期 token 在快照期间已被丢弃，不能再拼接
    for (const fr of buffered) {
      for (const e of applyFrame(room, fr)) if (!effects.includes(e)) effects.push(e);
      last = Math.max(last, Number(fr.seq));
    }
    room.lastSeq = last;
    // NOTE: 拉快照期间连接已断（markDisconnected）时，数据照常应用但不能写回 live，否则 gates 会在死连接上放行操作；重连后 onHello 会按 lastSeq 续传
    if (room.conn !== 'reconnecting') room.conn = 'live';
    return effects;
  }

  function markDisconnected(room) {
    for (const id of Object.keys(room.streams)) room.streams[id].broken = true;
    room.kpStatus = '';
    room.conn = 'reconnecting';
  }

  function gates(room) {
    const live = isLive(room);
    const playing = room.status === 'playing';
    const idle = !room.floor.running;
    return {
      canSpeak: playing && live && idle && !!room.spotlight.askedMe,
      canContinue: !!room.isHost && playing && live && idle && !room.floor.starting,
      canStop: !!room.isHost && live && !!room.floor.running,
      live,
    };
  }

  function parseDice(input) {
    if (input == null) return [];
    return String(input)
      .split(/;\s*/)
      .map((part) => part.trim())
      .filter(Boolean)
      .map((part) => {
        const parsed = part.match(DICE_RE);
        if (!parsed) return { raw: part };
        // NOTE: game.js 的解析不输出暗骰标记，这里额外补 hidden 字段，what/roll/level 与其保持一致
        return {
          what: (parsed[1] || '').trim(),
          roll: (parsed[2] || '').trim(),
          level: (parsed[3] || '').trim(),
          hidden: DICE_HIDDEN_RE.test(part),
        };
      });
  }

  function parseTimePoint(input) {
    if (input == null) return null;
    const raw = String(input).trim();
    if (!raw) return null;
    const parsed = raw.match(TIME_POINT_RE);
    if (!parsed) return { raw, label: raw, elapsed: '' };
    return {
      raw,
      label: '第' + parsed[1] + '天 ' + parsed[2],
      elapsed: (parsed[3] || '').trim(),
    };
  }

  const RS = {
    newRoom,
    onHello,
    acceptFrame,
    applySnapshot,
    applyChatStatus,
    applyFrame,
    markDisconnected,
    resetCursor,
    gates,
    parseDice,
    parseTimePoint,
  };
  if (typeof module === 'object' && module.exports) module.exports = RS;
  else (root.COC = root.COC || {}).roomState = RS;
})(typeof window !== 'undefined' ? window : globalThis);

// NOTE: 纯逻辑模块，不依赖 DOM / Vue 组件 / fetch / WebSocket；所有函数原地修改传入的 room（Vue 的 reactive 代理必须原地改才会触发渲染），副作用以 effects 字符串数组返回由调用方执行

export type ConnState = 'connecting' | 'snapshot' | 'helloPending' | 'live' | 'reconnecting'
export type RoomStatus = 'lobby' | 'playing' | 'ended'
export type MessageStatus = 'pending' | 'done' | 'failed'
export type Effect = 'fetchSnapshot' | 'fetchSession' | 'fetchChatStatus'
export type Verdict = 'apply' | 'drop' | 'buffer' | 'resync'

// NOTE: 帧载荷由后端按事件类型决定，字段在各 case 里逐个校验，这里不再逐事件建模
export type FrameData = Record<string, any>

export interface Frame {
  seq: number
  type: string
  data?: FrameData
}

export interface MessageMeta {
  error?: string
  [key: string]: unknown
}

export interface RoomMessage {
  id: number
  role?: string
  kind?: string
  status?: MessageStatus
  content?: string
  floor?: number
  username?: string
  user_id?: number | string | null
  images?: string[]
  meta?: MessageMeta | null
  _new?: boolean
  [key: string]: unknown
}

export interface WriterStream {
  text: string
}

export interface Spotlight {
  userIds: Array<number | string>
  names: string[]
  question: string
  kpFloor: boolean
  askedMe: boolean
  stop: string
}

export interface Waiting {
  pending: number
  total: number
  submittedNames: string[]
  pendingNames: string[]
}

export interface Room {
  sessionId: number | undefined
  userId: number | string | undefined
  isHost: boolean
  epoch: string
  lastSeq: number
  conn: ConnState
  baseSeq: number
  pending: Frame[]
  messages: RoomMessage[]
  byId: Record<number, RoomMessage>
  streams: Record<number, WriterStream>
  floor: { running: boolean; number: number; starting: boolean; startedAt: number }
  spotlight: Spotlight
  waiting: Waiting
  me: { submitted: boolean; draft: string }
  kpStatus: string
  status: RoomStatus
}

export interface NewRoomOptions {
  sessionId?: number
  userId?: number | string
  isHost?: boolean
}

export interface HelloData {
  epoch?: string | number | null
  seq?: number | string
}

export type HelloInput = HelloData | { seq?: number; type: 'hello'; data?: HelloData }

interface SpotlightSource {
  user_ids?: unknown
  names?: unknown
  question?: string
  kp_floor?: boolean
  asked_me?: boolean
  stop?: string
}

interface WaitingSource {
  pending?: number | string
  total?: number | string
  submitted_names?: unknown
  pending_names?: unknown
}

export interface ChatStatus {
  kp_running?: boolean
  floor?: { floor?: number | string; [key: string]: unknown } | null
  submitted?: boolean
  submitted_content?: string
  spotlight?: SpotlightSource
  waiting?: WaitingSource
}

export interface Snapshot {
  session?: { status?: string } | null
  messages?: Array<Partial<RoomMessage> | null> | null
  chatStatus?: ChatStatus | null
}

export interface Gates {
  canSpeak: boolean
  canContinue: boolean
  canStop: boolean
  live: boolean
}

export interface DiceRoll {
  what: string
  roll: string
  level: string
  hidden: boolean
}

export interface DiceRaw {
  raw: string
}

export interface TimePoint {
  raw: string
  label: string
  elapsed: string
}

const STATUSES: readonly string[] = ['lobby', 'playing', 'ended']

// NOTE: 骰子和时间点的正则沿用旧前端的写法，保证解析结果一致
const DICE_RE = /^(.*?)鉴定[:：]\s*(\S+)(?:\s*难度[:：]\s*([^\s(（]+))?/
const DICE_HIDDEN_RE = /[(（]\s*暗骰\s*[)）]/
const TIME_POINT_RE = /^第(\d+)天\s*(\d{1,2}:\d{2})(?:[(（](.*?)[)）])?/
// NOTE: 新消息的时间点是 KP 书写的本地时间 YYYY-MM-DDTHH:MM（也容忍空格分隔、带秒），历史消息仍是上面的旧格式
const RFC_TIME_RE = /^(\d{4})-(\d{2})-(\d{2})[T ](\d{2}):(\d{2})/

export function newRoom(opts?: NewRoomOptions): Room {
  const o = opts || {}
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
    floor: { running: false, number: 0, starting: false, startedAt: 0 },
    spotlight: { userIds: [], names: [], question: '', kpFloor: false, askedMe: false, stop: '' },
    waiting: { pending: 0, total: 0, submittedNames: [], pendingNames: [] },
    me: { submitted: false, draft: '' },
    kpStatus: '',
    status: 'lobby',
  }
}

function sameId(a: unknown, b: unknown): boolean {
  return a != null && b != null && String(a) === String(b)
}

function includesId(list: unknown, id: unknown): boolean {
  return Array.isArray(list) && list.some((x) => sameId(x, id))
}

function asArray<T>(v: unknown): T[] {
  return Array.isArray(v) ? (v.slice() as T[]) : []
}

function clearObject(o: object): void {
  for (const k of Object.keys(o)) delete (o as Record<string, unknown>)[k]
}

function isLive(room: Room): boolean {
  return room.conn === 'live' || room.conn === 'helloPending'
}

// NOTE: pending 的优先级低于 done/failed，保证消息状态只升不降（重放 message_new 不会把已完成的消息打回 pending）
function statusRank(status: string | undefined): number {
  return status === 'pending' ? 0 : 1
}

export function resetCursor(room: Room): void {
  room.lastSeq = 0
  room.pending.length = 0
  room.conn = 'connecting'
}

export function onHello(room: Room, hello: HelloInput | null | undefined): Effect[] {
  // NOTE: 同时接受整帧和仅 data 两种形态；整帧的 seq 恒为 0，误传会让 baseSeq 静默变成 0
  const h: HelloData =
    hello && 'type' in hello && hello.type === 'hello' && hello.data ? hello.data : (hello as HelloData) || {}
  const epoch = h.epoch == null ? '' : String(h.epoch)
  const epochChanged = room.epoch !== '' && room.epoch !== epoch
  const needSnapshot = room.lastSeq === 0 || epochChanged
  room.baseSeq = Number(h.seq) || 0
  room.pending.length = 0
  room.epoch = epoch
  room.conn = needSnapshot ? 'snapshot' : 'helloPending'
  // NOTE: epoch 已保存但快照还没应用；清零游标后即使快照拉取失败，下次重连也会重新走快照，而不是带着失效游标进入 helloPending（resync 帧的路径同理）
  if (needSnapshot) room.lastSeq = 0
  return needSnapshot ? ['fetchSnapshot'] : []
}

export function acceptFrame(room: Room, frame: Partial<Frame> | null | undefined): Verdict {
  if (!frame || typeof frame.type !== 'string' || frame.type === 'hello') return 'drop'
  const conn = room.conn

  if (frame.type === 'resync') {
    // NOTE: helloPending 下的 resync 帧返回 apply，由 applyFrame 完成切换并返回 fetchSnapshot；'resync' 返回值只表示"出现缺口"，调用方应 resetCursor 后重连
    if (conn === 'helloPending') return 'apply'
    if (conn === 'snapshot') return 'drop'
    return conn === 'live' ? 'resync' : 'drop'
  }

  const inSnapshot = conn === 'snapshot'
  if (conn === 'helloPending') {
    // NOTE: resync 一定紧跟 hello，所以 hello 后的第一帧不是 resync 就确认是无缝补发，此刻才进入 live
    room.conn = 'live'
  } else if (conn !== 'live' && !inSnapshot) {
    return 'drop'
  }

  const seq = Number(frame.seq) || 0
  // NOTE: 快照期间也要收 writer_token / writer_reset：握手时的补发帧就在这时到达，丢了它后面带偏移的增量就接不上
  if (seq <= 0) return inSnapshot && frame.type !== 'writer_token' && frame.type !== 'writer_reset' ? 'drop' : 'apply'

  if (inSnapshot) {
    // NOTE: snapshot 期间只看 baseSeq，不看 lastSeq；epoch 变化后旧 lastSeq 可能比新 epoch 的 seq 还大，按它丢帧会丢事件
    if (seq <= room.baseSeq) return 'drop'
    room.pending.push(frame as Frame)
    return 'buffer'
  }

  if (seq <= room.lastSeq) return 'drop'
  if (seq > room.lastSeq + 1) return 'resync'
  room.lastSeq = seq
  return 'apply'
}

function upsertMessage(room: Room, incoming: Partial<RoomMessage>): void {
  const id = Number(incoming.id)
  if (!Number.isFinite(id)) return
  const existing = room.byId[id]
  if (existing) {
    if (statusRank(incoming.status) < statusRank(existing.status)) return
    Object.assign(existing, incoming, { id })
    return
  }
  const msg = Object.assign({}, incoming, { id }) as RoomMessage
  let i = room.messages.length
  while (i > 0 && room.messages[i - 1].id > id) i--
  room.messages.splice(i, 0, msg)
  room.byId[id] = msg
}

function onMessageNew(room: Room, data: FrameData): void {
  const m = data.message
  if (!m || m.id == null) return
  const id = Number(m.id)
  // NOTE: 只有首次见到的消息才产生副作用，快照与缓冲帧重叠时的重放不会清掉 me.submitted / 重建流
  const isNew = !room.byId[id]
  upsertMessage(room, m)
  const stored = room.byId[id]
  if (!stored) return
  if (stored.status !== 'pending') delete room.streams[id]
  if (!isNew) return

  if (stored.kind === 'ask' || stored.kind === 'kp') room.me.submitted = false
  if (stored.kind === 'player' && sameId(stored.user_id, room.userId)) room.me.draft = ''
}

function onMessageUpdate(room: Room, d: FrameData): void {
  if (d.id == null) return
  const id = Number(d.id)
  const msg = room.byId[id]
  if (msg) {
    if (typeof d.status === 'string' && statusRank(d.status) >= statusRank(msg.status)) {
      msg.status = d.status as MessageStatus
    }
    if (typeof d.content === 'string') msg.content = d.content
    if (d.error) {
      if (!msg.meta || typeof msg.meta !== 'object') msg.meta = {}
      msg.meta.error = d.error
    }
  }
  delete room.streams[id]
}

// NOTE: token 帧带偏移（UTF-16，与 JS 字符串下标一致），重叠的部分直接覆盖；偏移超过已有长度说明中间有帧丢了，
// 丢弃并等重连时的全文补发或 message_update。消息还没见过（快照未到、message_new 在缓冲里）时也先记下来
function onWriterToken(room: Room, d: FrameData): void {
  const id = Number(d.message_id)
  if (!Number.isFinite(id) || typeof d.text !== 'string') return
  const msg = room.byId[id]
  if (msg && msg.status !== 'pending') return
  // NOTE: 新建后必须从 room 上重新取：赋值表达式返回的是原始对象，改它不会触发 reactive 代理的更新
  if (!room.streams[id]) room.streams[id] = { text: '' }
  const st = room.streams[id]
  const offset = typeof d.offset === 'number' ? d.offset : st.text.length
  if (offset > st.text.length || offset + d.text.length <= st.text.length) return
  st.text = st.text.slice(0, offset) + d.text
}

// NOTE: Writer 流中途断开后服务端会整段重新请求，已拼接的半截正文作废；之后的 writer_token 从 offset=0 重新开始
function onWriterReset(room: Room, d: FrameData): void {
  const id = Number(d.message_id)
  if (!Number.isFinite(id)) return
  delete room.streams[id]
}

function setSpotlight(room: Room, src: SpotlightSource): boolean {
  const ids = asArray<number | string>(src.user_ids)
  const askedMe = includesId(ids, room.userId)
  Object.assign(room.spotlight, {
    userIds: ids,
    names: asArray<string>(src.names),
    question: src.question || '',
    kpFloor: typeof src.kp_floor === 'boolean' ? src.kp_floor : ids.length === 0,
    askedMe,
    stop: src.stop || '',
  })
  return askedMe
}

function setWaiting(room: Room, src: WaitingSource): void {
  Object.assign(room.waiting, {
    pending: Number(src.pending) || 0,
    total: Number(src.total) || 0,
    submittedNames: asArray<string>(src.submitted_names),
    pendingNames: asArray<string>(src.pending_names),
  })
}

export function applyFrame(room: Room, frame: Partial<Frame> | null | undefined): Effect[] {
  const effects: Effect[] = []
  if (!frame || typeof frame.type !== 'string') return effects
  const d = frame.data || {}

  switch (frame.type) {
    case 'resync':
      if (room.conn === 'helloPending') {
        room.pending.length = 0
        room.lastSeq = 0
        room.conn = 'snapshot'
        effects.push('fetchSnapshot')
      }
      break
    case 'message_new':
      onMessageNew(room, d)
      break
    case 'message_update':
      onMessageUpdate(room, d)
      break
    case 'writer_token':
      onWriterToken(room, d)
      break
    case 'writer_reset':
      onWriterReset(room, d)
      break
    case 'spotlight':
      if (!setSpotlight(room, d)) room.me.submitted = false
      break
    case 'waiting':
      setWaiting(room, d)
      if (room.spotlight.askedMe && !room.me.submitted) effects.push('fetchChatStatus')
      break
    case 'floor_start':
      room.floor.running = true
      room.floor.starting = false
      room.floor.startedAt = Date.now()
      if (d.floor != null) room.floor.number = Number(d.floor) || 0
      // NOTE: 周期一开始，上一轮的等待进度就作废；否则周期结束后的新点名会显示残留的"还差几人"
      setWaiting(room, {})
      break
    case 'floor_end':
      room.floor.running = false
      room.floor.startedAt = 0
      room.kpStatus = ''
      break
    case 'kp_status':
      room.kpStatus = typeof d.text === 'string' ? d.text : ''
      break
    case 'session':
      if (STATUSES.includes(d.status)) room.status = d.status
      effects.push('fetchSession')
      break
    default:
      break
  }
  return effects
}

function replaceMessages(room: Room, list: Snapshot['messages']): void {
  const byId = new Map<number, RoomMessage>()
  for (const m of Array.isArray(list) ? list : []) {
    if (!m || m.id == null || !Number.isFinite(Number(m.id))) continue
    byId.set(Number(m.id), Object.assign({}, m, { id: Number(m.id) }) as RoomMessage)
  }
  const sorted = Array.from(byId.values()).sort((a, b) => a.id - b.id)

  room.messages.splice(0, room.messages.length)
  clearObject(room.byId)
  for (const m of sorted) {
    room.messages.push(m)
    room.byId[m.id] = m
  }
  // NOTE: 仍在生成的 writer 保留已收到的流式正文（快照里它的 content 是空的），只清掉已经有终态的
  for (const id of Object.keys(room.streams)) {
    const m = room.byId[Number(id)]
    if (m && m.status !== 'pending') delete room.streams[Number(id)]
  }
}

// NOTE: 只覆盖 chat-status 能反映的部分（周期、发言权、等待进度、是否已提交），不碰消息列表和连接游标
export function applyChatStatus(room: Room, cs: ChatStatus | null | undefined): void {
  const c = cs || {}
  const f = c.floor
  room.floor.running = !!(f || c.kp_running)
  room.floor.number = f ? Number(f.floor) || 0 : 0
  // NOTE: 服务器给的开始时间优先；解析不出来（或只有 kp_running）时沿用本地记录，没有记录就从现在算起
  const startedAt = typeof f?.started_at === 'string' ? Date.parse(f.started_at) : NaN
  room.floor.startedAt = !room.floor.running ? 0 : startedAt > 0 ? startedAt : room.floor.startedAt || Date.now()
  // NOTE: starting 只是本地"已点继续、等 floor_start"的标记；chat-status 能反映真实状态，整体重置以免丢了 floor_start 后按钮卡死
  room.floor.starting = false

  const sp = c.spotlight || {}
  const askedMe = setSpotlight(room, sp)
  room.spotlight.askedMe = !!sp.asked_me || askedMe
  setWaiting(room, c.waiting || {})
  room.me.submitted = !!c.submitted
  // NOTE: 刷新或重连后本地没有草稿，用服务器记录的原文回填；已有本地草稿（刚提交的）不覆盖
  if (room.me.submitted && !room.me.draft && typeof c.submitted_content === 'string') room.me.draft = c.submitted_content
}

// NOTE: 调用方必须自行丢弃过期的快照响应（例如按连接代号比对），模块不判断一次拉取是否已过期
export function applySnapshot(room: Room, snap: Snapshot | null | undefined): Effect[] {
  const s = snap || {}

  if (s.session && s.session.status && STATUSES.includes(s.session.status)) {
    room.status = s.session.status as RoomStatus
  }
  replaceMessages(room, s.messages)
  applyChatStatus(room, s.chatStatus)

  const bySeq = new Map<number, Frame>()
  for (const fr of room.pending) {
    if (fr && Number(fr.seq) > room.baseSeq) bySeq.set(Number(fr.seq), fr)
  }
  const buffered = Array.from(bySeq.keys())
    .sort((a, b) => a - b)
    .map((seq) => bySeq.get(seq) as Frame)
  room.pending.length = 0

  const effects: Effect[] = []
  let last = room.baseSeq
  for (const fr of buffered) {
    for (const e of applyFrame(room, fr)) if (!effects.includes(e)) effects.push(e)
    last = Math.max(last, Number(fr.seq))
  }
  room.lastSeq = last
  // NOTE: 拉快照期间连接已断（markDisconnected）时，数据照常应用但不能写回 live，否则 gates 会在死连接上放行操作；重连后 onHello 会按 lastSeq 续传
  if (room.conn !== 'reconnecting') room.conn = 'live'
  return effects
}

// NOTE: 断线不动流式正文：重连握手时服务端会从偏移 0 补发全文，之后的增量按偏移继续拼
export function markDisconnected(room: Room): void {
  room.kpStatus = ''
  room.conn = 'reconnecting'
}

export function gates(room: Room): Gates {
  const live = isLive(room)
  const playing = room.status === 'playing'
  const idle = !room.floor.running
  return {
    canSpeak: playing && live && idle && !!room.spotlight.askedMe,
    canContinue: !!room.isHost && playing && live && idle && !room.floor.starting,
    canStop: !!room.isHost && live && !!room.floor.running,
    live,
  }
}

export function parseDice(input: unknown): Array<DiceRoll | DiceRaw> {
  if (input == null) return []
  return String(input)
    .split(/;\s*/)
    .map((part) => part.trim())
    .filter(Boolean)
    .map((part) => {
      const parsed = part.match(DICE_RE)
      if (!parsed) return { raw: part }
      // NOTE: game.js 的解析不输出暗骰标记，这里额外补 hidden 字段，what/roll/level 与其保持一致
      return {
        what: (parsed[1] || '').trim(),
        roll: (parsed[2] || '').trim(),
        level: (parsed[3] || '').trim(),
        hidden: DICE_HIDDEN_RE.test(part),
      }
    })
}

export function parseTimePoint(input: unknown): TimePoint | null {
  if (input == null) return null
  const raw = String(input).trim()
  if (!raw) return null
  const rfc = raw.match(RFC_TIME_RE)
  if (rfc) {
    return { raw, label: `${rfc[1]}年${Number(rfc[2])}月${Number(rfc[3])}日 ${rfc[4]}:${rfc[5]}`, elapsed: '' }
  }
  const parsed = raw.match(TIME_POINT_RE)
  if (!parsed) return { raw, label: raw, elapsed: '' }
  return {
    raw,
    label: '第' + parsed[1] + '天 ' + parsed[2],
    elapsed: (parsed[3] || '').trim(),
  }
}

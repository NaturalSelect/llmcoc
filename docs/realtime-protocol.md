# 房间实时推送协议（WebSocket）

本文档描述房间内所有人共享的实时通道，供前端重写时对接。

核心思路：**命令走 REST，进展走 WebSocket**。`/chat`、`/kp-turn`、`/kp-stop` 提交后立即返回，KP 的所有进展（新消息、流式文字、点名、等待进度、一段结束）都通过本通道广播给房间内每个在线的人。WebSocket 是只读推送通道，客户端发来的任何数据帧都会被服务端丢弃。

实现位置：`internal/handlers/realtime_hub.go`（事件缓冲与广播）、`internal/handlers/session_ws.go`（握手与保活）。

## 1. 连接

```
GET /api/sessions/:id/ws?token=<JWT>&since=<N>&epoch=<E>
```

| 参数 | 必填 | 说明 |
| --- | --- | --- |
| `token` | 是 | 登录 JWT。浏览器的 `WebSocket` 构造函数不能自定义请求头，所以只能走查询参数（也支持 `Authorization: Bearer` 头，供非浏览器客户端使用）。 |
| `since` | 否 | 客户端已处理的最大持久事件 `seq`。首次连接不带或传 0。必须是非负整数，否则返回 400。 |
| `epoch` | 否 | 上次收到的 `hello.epoch`。首次连接不带。 |

升级之前的失败都是普通 JSON 错误（`{"error": "..."}`），此时还没有 WebSocket：

| 状态码 | 含义 |
| --- | --- |
| 400 | `since` 不是合法整数 |
| 401 | 未提供或无效的 token |
| 403 | 账号被封禁，或有密码房间且你不是成员/管理员 |
| 404 | 房间不存在 |

权限口径与 `GET /messages` 一致：无密码房间所有登录用户可订阅；有密码房间仅成员和管理员可订阅。

来源校验：请求没有 `Origin` 头（非浏览器客户端）直接放行；有 `Origin` 时要求其 host 与请求的 `Host` 相同，否则 403。反向代理必须原样透传 `Host` 头。

安全提示：token 会出现在 URL 里，反向代理和访问日志可能记录它。生产环境的代理请对该路径脱敏查询串。

## 2. 帧格式

所有帧都是 JSON 文本帧：

```json
{"seq": 12, "type": "message_new", "data": { ... }}
```

| 字段 | 说明 |
| --- | --- |
| `seq` | 持久事件的序号，每个房间从 1 开始严格递增。临时事件和 `hello`/`resync` 为 `0`。 |
| `type` | 事件类型，见第 5 节。未知类型请忽略，不要报错（便于以后加事件）。 |
| `data` | 事件载荷，结构随 `type` 而定。 |

两类事件：

- **持久事件**（`seq > 0`）：服务端保留最近 256 条，断线重连后可按 `since` 补发。
- **临时事件**（`seq = 0`）：只广播给当前在线的连接，不缓冲、不补发。断线期间发生的临时事件就丢了。

客户端只用持久事件推进 `lastSeq`；收到 `seq = 0` 的帧**不要**更新 `lastSeq`。

## 3. hello 与 resync

### hello

升级成功后的**第一帧**一定是 `hello`：

```json
{"seq": 0, "type": "hello", "data": {"epoch": "k3j9...", "seq": 42}}
```

- `epoch`：服务端进程启动时生成的随机串，**服务重启后会变**。重启会丢掉内存里的事件缓冲，旧 `seq` 不再可比。
- `seq`：此刻房间已发布的最大持久事件序号。

### resync

紧跟在 `hello` 之后，服务端会在以下任一情况发送 `resync`（`data` 为空对象），**并且不重放任何事件**，之后直接进入实时推送：

1. 你带了 `epoch`，且与服务端当前 `epoch` 不同（服务重启过）。
2. 缓冲里的事件不足以补齐你错过的部分：`since > 0` 且你要的下一条（`since + 1`）已不在缓冲里——被挤出 256 条窗口，或房间缓冲因空闲被回收过。
3. `since` 比服务端当前的最大 `seq` 还大（游标非法，例如前端缓存了另一个房间或另一次部署的值）。

收到 `resync` 的意思是：**本地状态可能缺事件，请重新拉全量**：

- `GET /api/sessions/:id/messages`
- `GET /api/sessions/:id/chat-status`

然后用 `hello.seq` 作为新的 `lastSeq`（`resync` 之后到达的实时事件 `seq` 都大于它），把 `hello.epoch` 存下来。

没有 `resync` 时，`hello` 之后是 `seq > since` 的缓冲事件（按 `seq` 升序），之后无缝衔接实时事件，不丢不重。

## 4. 保活与重连

### 保活

- 服务端每 30 秒发一个 WebSocket ping，浏览器会自动回 pong，前端代码不用处理。
- 服务端读超时 60 秒，每收到 pong 续期；连续丢失约两次心跳的连接会被断开。
- 客户端**不需要**发送任何应用层心跳。客户端发的文本/二进制帧会被丢弃，单帧超过 1 KiB 会被断开。
- 服务端写每帧有 10 秒写超时。

### 慢消费者

每个连接在服务端有 64 条待发送队列。队列写满（客户端读得太慢）时，服务端直接断开该连接（关闭码 1013 `try again later`），**不会**拖慢 KP 流程和其他人。客户端按下面的流程带 `since` 重连即可补齐。

### 重连流程

客户端需要持久保存两个值：`lastSeq`（已处理的最大持久事件 seq，初值 0）和 `epoch`（最近一次 `hello.epoch`，初值空）。

1. 连接断开（`onclose`，无论原因）后，等待退避时间再重连。建议指数退避：1s、2s、4s…上限 15s，成功收到 `hello` 后复位。
2. 重连 URL 带上 `since=<lastSeq>&epoch=<epoch>`。
3. 收到 `hello`：保存 `epoch`。
4. 接下来：
   - 收到 `resync`：重新拉 `/messages` 与 `/chat-status`，整体替换本地状态，`lastSeq = hello.seq`。
   - 否则：依次处理补发的持久事件，每处理一条 `lastSeq = event.seq`。
5. 之后正常处理实时事件。

认证失败（握手返回 401/403/404）不要无限重试：401 回登录页，403/404 退出房间页。

### 首次连接与幂等

首次连接（不带 `since`）也会收到缓冲内的持久事件（最多 256 条），这些消息和 `GET /messages` 的内容会重叠。推荐顺序：**先建立 WebSocket，再拉 `/messages`**（避免两者之间漏事件），并让所有事件处理按 `message.id` 幂等（有则更新，无则插入）。同理，重连后同一条消息可能既被 `/messages` 返回又被补发，也靠幂等去重。

首次连接的客户端也可以直接丢弃 `seq <= hello.seq` 的补发事件：它们发生在快照之前，已经包含在随后拉取的全量数据里。快照期间到达的 `seq > hello.seq` 的事件先缓冲，快照应用后再依次应用。

### 其他说明

- 同一用户开多个标签页是多个独立订阅，互不影响。
- 房间没有任何订阅者且空闲 10 分钟后，服务端会回收该房间的缓冲；重建后 `seq` 接着往下数而不是回到 1，所以不会出现新事件的 `seq` 小于等于旧游标的情况。已经追平的客户端重连不受影响；错过了被回收的事件的客户端会得到 `resync`。

## 5. 事件目录

### 路由原则

**前端按 `message.id` 路由事件，不依赖到达顺序。** 流式文字（临时事件）和落库后的更新（持久事件）可能交错到达，渲染时以 `message_id` / `id` 找到对应的消息气泡，再决定如何合并。

### 持久事件

#### `message_new`

新增一条消息。玩家输入、writer 叙事、配图、KP 消息、点名（ask）消息都用它。

```json
{"message": { ...messageResponse... }}
```

`message` 是完整的 `messageResponse`（与 `GET /messages` 数组元素同结构），包含 `id`、`role`、`content`、`username`、`created_at`、`images`，以及 `kind`、`status`、`floor`、`meta` 字段。

`kind`（消息在房间里的角色，前端据此分别展示）：

| 值 | 含义 |
| --- | --- |
| `player` | 玩家本轮声明的行动 |
| `writer` | KP 的一次 `write`：由 Writer 展开的白字正文 |
| `image` | KP 的一次 `generate_image`：Painter 出的配图 |
| `kp` | KP 的一次 `response` 或 `end_game` 的回复 |
| `ask` | KP 的一次 `ask_players`：点名并交出发言权 |
| `system` | 复活提示，复活时经 `message_new` 送达。加入、离开的系统提示不会推送，也不出现在 `GET /messages` 里，客户端以 `session` 事件刷新成员列表 |

`status`（正文是否就绪）：

| 值 | 含义 |
| --- | --- |
| `done` | 已就绪。除下面两种情况外的消息都是它 |
| `pending` | `writer` 与 `image` 先以此状态落库，等异步补全 |
| `failed` | 生成失败，原因在 `meta.error` |

`floor` 是所属周期编号，`system` 消息为 0。`meta` 是结构化附加信息，字段都可省略，只有下表列出的消息类型会带对应字段：

| 字段 | 出现在 | 说明 |
| --- | --- | --- |
| `options` | `kp` | KP 给出的行动选项 |
| `ack` | `kp` | 对玩家声明的确认文字 |
| `dice` | `kp`、`ask` | 本拍的骰子结果文字 |
| `time_point` | `kp`、`ask` | 剧中时间点 |
| `ask` | `ask` | `{"user_ids": [...], "names": [...]}`，被点名的玩家 |
| `ended` | `kp` | 为 true 表示这条回复同时结束了游戏 |
| `auto` | `writer` | 由后端兜底自动补出，不是 KP 显式调用 |
| `nsfw` | `writer`、`image` | 该消息按成人内容生成 |
| `error` | `writer`、`image` | `status=failed` 时的失败原因 |

#### `message_update`

更新已有消息（writer 生成结束、配图生成完成、生成失败）。只有出现的字段才需要更新。

```json
{"id": 123, "status": "done", "content": "...", "images": ["/api/images/..."], "error": "..."}
```

| 字段 | 说明 |
| --- | --- |
| `id` | 目标消息 ID（必有） |
| `status` | 新状态（必有），取值 `done` 或 `failed` |
| `content` | 可选，终态全文，直接覆盖本地内容（用来纠正可能丢失的 `writer_token`） |
| `images` | 可选，图片地址列表，是落盘后的地址而不是 data URL |
| `error` | 可选，失败原因 |

#### `floor_start`

KP 开始一个周期（一次发言权）。

```json
{"floor": 3, "reason": "player"}
```

`reason`：

| 值 | 含义 |
| --- | --- |
| `opening` | 开场 |
| `player` | 玩家（被点名者）回应触发 |
| `host_continue` | 房主点击"继续"（不再等待未回应的被点名玩家） |
| `resume` | 上一个周期因上下文过长被迫结束，服务端总结后自动续跑，没有新的玩家输入（兜底路径，较少出现） |

周期中途上下文逼近窗口时，服务端先发一条 `kp_status`（"KP正在整理记忆"），就地总结较早的剧情后继续同一个周期，客户端看不到额外的 `floor_end` / `floor_start`。只有当前周期本身就超出窗口，或总结失败时，才会走 `resume`：同一次提交产生两对 `floor_start` / `floor_end`，先是 `floor_end(reason=context)`，紧接着一个新编号的 `floor_start(reason=resume)`。续跑没有产出任何消息就不会再续，之后以 `floor_end(reason=context)` 收尾。

#### `waiting`

等待被点名玩家到齐的进度（`waitingPayload`）：

```json
{
  "pending": 1,
  "total": 2,
  "submitted_names": ["爱丽丝"],
  "pending_names": ["鲍勃"]
}
```

| 字段 | 说明 |
| --- | --- |
| `pending` / `total` | 还没提交的人数 / 需要提交的总人数 |
| `submitted_names` / `pending_names` | 已提交 / 未提交玩家的显示名，按房间玩家顺序排列，可直接渲染徽章 |

#### `spotlight`

KP 交出（或收回）发言权。

```json
{"user_ids": [3], "names": ["鲍勃"], "question": "你打算怎么开门？", "kp_floor": false, "stop": ""}
```

| 字段 | 说明 |
| --- | --- |
| `user_ids` / `names` | 被点名的玩家；只有他们此刻可以发言 |
| `question` | KP 的提问 |
| `kp_floor` | 为 true 表示 `user_ids` 为空、发言权仍在 KP 手里（玩家都不能发言，等待 KP 继续） |
| `stop` | KP 持有发言权时，上一个周期被强制结束的原因：`host_stop`、`loop`、`stall`、`error`、`context`。空串表示没有强制结束 |

`stop` 非空时玩家提交会被拒绝，需要房主调用 `kp-turn` 让 KP 继续。游戏已结束的房间不会再收到新的 `spotlight`。

#### `floor_end`

KP 这一个周期结束。

```json
{"floor": 3, "reason": "ask", "messages": 5}
```

`messages` 是这个周期发布的消息条数（数字，不是消息列表）；具体消息已经通过 `message_new` 逐条送达。

`reason`：

| 值 | 含义 |
| --- | --- |
| `ask` | KP 点名玩家，等待回应 |
| `end_game` | 游戏结束 |
| `host_stop` | 房主或管理员调用 `kp-stop` 手动打断 |
| `loop` | 防循环中止：同一批工具调用在滑动窗口内重复过多 |
| `stall` | 连续多轮没有产生任何玩家可见消息 |
| `error` | LLM 调用或消息发布失败，或服务关闭 |
| `context` | 当前周期本身超出上下文窗口，或中途总结失败；通常紧跟一个 `reason=resume` 的 `floor_start`。正常的上下文压缩在周期内就地完成，不产生这个事件 |
| `done` | 周期正常跑完但既没有点名也没有结束游戏（兜底值） |

除 `ask` 和 `end_game` 外，其余 `reason` 都表示发言权回到了 KP。客户端对未知的 `reason` 应按"此周期已结束"处理，不要报错。

#### `session`

房间状态或成员发生变化的信号。它不携带房间数据，客户端收到后重新请求 `GET /api/sessions/:id` 即可。

```json
{"status": "playing", "reason": "cards"}
```

`status` 是事件发出时房间的状态（`lobby` / `playing` / `ended`），`reason`：

| 值 | 含义 | 发出时机 |
| --- | --- | --- |
| `join` | 有人加入大厅 | 加入成功后 |
| `leave` | 有人离开大厅 | 离开成功后。最后一人离开导致房间解散时不发 |
| `start` | 房主开局 | 开局不再附带 `message_new` |
| `end` | 房主或管理员结束游戏，或管理员批量结束 | 房间状态改成 `ended` 之后，结算开始之前 |
| `revive` | 管理员复活房间 | 复活提示的 `message_new` 之后 |
| `cards` | 人物卡可能已变化 | 每发布一条 `kp` / `ask` 消息之后，以及每个周期的 `floor_end` 之后 |

`cards` 在一拍内可能连发多次，客户端应当对重拉请求做防抖。`status` 以事件为准：`end_game` 结束的房间，周期内和 `floor_end` 之后的 `session` 都是 `ended`。

### 临时事件（`seq = 0`）

#### `kp_status`

KP 当前状态的提示文案，用于"KP 正在思考…"这类状态栏。

```json
{"text": "KP 正在思考…"}
```

#### `writer_token`

Writer 流式输出的一小段文字，追加到对应消息末尾。

```json
{"message_id": 123, "text": "雨"}
```

断线期间的 `writer_token` 会丢失。以后到达的 `message_update`（带 `content` 终态全文）或重新拉取的 `/messages` 会给出完整内容，客户端以它们为准覆盖本地拼接的内容。

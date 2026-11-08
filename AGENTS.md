# LLM-COC

Go + Gin + GORM/SQLite 的 COC7 多人跑团服务端，内置前端与多 Agent LLM 管线。业务细节以代码、`README.md`、`docs/business-flow.md`（账号/人物卡/模组/房间/商城/后台流程）为准；本文件只记录读代码前值得先知道的事。

## 命令

```bash
./start.sh                 # go build -o ./bin/llmcoc 后运行；自动 source .env
./start.sh --debug         # GIN_MODE=debug + LOG_LEVEL=debug
./start.sh --dev           # go run ./cmd/server
./start_bg.sh [flags]      # nohup 后台运行，输出到 run.log（run.log 未被 gitignore）

go vet ./...                              # CI 第一步
go test -race -count=1 ./...              # CI 实际使用的测试命令
node --test cmd/server/webtest/*.test.js  # 前端状态模块测试，CI 在 Go 测试之后也会跑（Node 22）
go test -run TestXxx ./internal/handlers  # 单个测试
```

- CI 只有 `.github/workflows/test.yml`（vet + test，Go 版本取自 `go.mod`）。无 Makefile、无 lint 配置、无前端构建。
- 测试不依赖网络、真实 LLM 或外部服务。
- 项目目录里可能已有服务进程在跑（占用 8080、持有 `data/llmcoc.db`）：测试/启动前先确认端口，改代码前先 `git status`。
- `data/`、`bin/`、`tmp/`、`.env`、`config.local.yaml` 被 gitignore。`data/` 含 SQLite 库、`lawyer_cache.json`、`images/`。

## 配置与初始化

- `config.yaml` 只含 `server` / `database` / `jwt` 三段（`internal/config/config.go`）。README 里出现的 `shop:` 段并不存在于结构体；初始金币、卡槽等是数据库里的 `SiteSetting`（`models/db.go` 的 `seedDefaultSiteSettings`，后台可改）。
- 实际读取的环境变量：`CONFIG_PATH`、`JWT_SECRET`、`GIN_MODE`、`LOG_LEVEL`（默认 debug）、`LLM_API_KEY` / `LLM_BASE_URL` / `LLM_PROVIDER` / `LLM_MODEL`（仅在库里没有 Provider 时用于播种默认 Provider 和各 Agent 默认模型）、`RULEBOOK_PATH` / `SPELLBOOK_PATH` / `MONSTERBOOK_PATH`、`LAWYER_CACHE_PATH`、`LLMCOC_IMAGE_DIR`、`LLMCOC_IMAGE_QUALITY`。
- 文档里提到但代码未读取：`AGENT_DEBUG`（README）、`DB_PATH`（start.sh 注释）。不要依赖它们。
- 所有播种都是「缺失才创建」：改 `seedDefaultAgentConfigs` / `seedDefaultSiteSettings` 里的默认值不会影响已有数据库。下线 Agent 角色必须把旧角色名加进 `seedDefaultAgentConfigs` 里的 `DELETE ... WHERE role IN` 清理列表，否则老库会残留。
- 只有 `AutoMigrate`，没有迁移文件。新增列需要回填旧数据时，参照 `with_jailbreak`：迁移前用 `Migrator().HasColumn` 记下列是否存在，迁移后回填。新增模型要加进 `InitDB` 的 AutoMigrate 列表。
- 管理员不会自动创建：注册后用 `sqlite3 data/llmcoc.db "update users set role='admin' where username='...'"`。
- `scenarios/` 目录不在仓库中。`handlers.SeedScenarios("scenarios")` 在目录缺失或库里已有模组时静默返回。

## 架构

### 入口与前端
- `cmd/server/main.go` 完成：加载配置 → `models.InitDB` → 播种 → 加载三本规则资料并算哈希 → 启动统计持久化/图片清理 → 注册路由 → 优雅退出时保存 Lawyer 缓存。
- 前端是 `cmd/server/web` 下的静态文件，通过 `//go:embed web` 打进二进制，没有独立工程；改前端后需要重启（或重新 build）才生效。`web/js/vendor/` 是手工放入的第三方库。未命中的路由由 `NoRoute` 回落到 `index.html`。
- gzip 中间件按路径正则排除了流式接口：房间 WebSocket `/api/sessions/:id/ws`，以及 SSE 的 `/api/scenarios/generate`、`/api/scenarios/compile-story`。`/chat`、`/kp-turn`、`/kp-stop` 已改为立即返回 202 的普通 JSON 接口，不再在排除列表里。新增流式接口必须同步加进 `main.go` 的排除列表。HTTP `WriteTimeout` 为 30 分钟，是为了长时间 AI 生成。
- 房间所有进展（新消息、writer 流式文字、点名、等待进度、周期起止、房间状态变化）都经 `GET /api/sessions/:id/ws` 推送，协议见 `docs/realtime-protocol.md`。游戏页没有任何轮询：`web/js/game.js` 负责连接、重连退避、命令和模板辅助，状态归约全在 `web/js/room_state.js`（纯逻辑，不依赖 DOM、Alpine 和网络）。游戏页没有常驻输入框：玩家只在被点名时看到问题卡（推荐行动可多选，外加自由输入，提交后收成一行摘要，断线时置灰但保留内容），房间状态和房主的继续/打断按钮在底部状态栏。两个模块的测试都在 `cmd/server/webtest/`（`game.test.js` 用假的 WebSocket、fetch 和定时器在 vm 里加载 `game.js`），必须放在 embed 目录之外，否则会被打进二进制。游戏页的模板和样式还没有在真实浏览器里人工验证。
- 路由全部在 `main.go` 里手写；`BanCheck` 中间件只挂在部分分组上，新增分组时注意是否需要。

### 一段 KP 叙事的数据流
整局由 KP（Director）驱动，玩家只在被点名时才有发言权。一次 `agent.Run` 就是一个「周期」（floor）：KP 在一个 ReAct 循环里自由调用工具，没有续跑次数上限，只有 `ask_players` / `end_game` 会正常结束周期；`response` 只是发布一拍消息，不结束周期。
`GameSession.Spotlight` 记录发言权：`UserIDs` 为空（含 NULL，不再兼容旧的玩家驱动房间）是 KP 持有发言权；非空是等这些玩家回应；`Stop` 非空表示上个周期被强制结束（`host_stop` / `loop` / `stall` / `error` / `context`），此时玩家提交会被拒绝，要房主点「继续」。
1. 入口有两个：`handlers/session.go` 的 `ChatStream`（玩家回应：校验是否被点名，写 `SessionTurnAction`，点名对象到齐后才开周期）和 `handlers/session_kp.go` 的 `KPTurn`（房主/管理员触发开场或继续）。两者都立即返回 202，周期在后台跑；`KPStop` 让房主/管理员硬打断，返回 202 或 409。
2. `startFloor` 先占用会话处理锁，在事务里写玩家消息，登记到 `activeFloors`（kp-stop 和关机靠它取消 ctx），再广播 `floor_start`。`runFloor` 用 `context.WithCancelCause` 跑 `Runner.Run`，上下文已满时自动总结并续跑一次（续跑没有产出任何消息就不再续）。`finishFloor` 先落库新发言权、释放处理锁，之后才广播 `spotlight` / `floor_end`，否则被点名者立刻发言会撞上 409。进程关闭时 `StopAllFloors` 取消全部周期并最多等 10 秒。
3. `agent.Run`（`orchestrator.go`）用 `activeSessions` 保证同一会话同时只跑一个周期；`getCachedAgents` 读取 Agent 配置；构造 Director 提示词与上下文（主动周期渲染 `<kp_floor>`，玩家回应周期在有提问时渲染 `<asked>`）后进入 `runToolLoop`（`scripter_toolloop.go`，模组生成管线共用，`maxRounds <= 0` 表示不限轮数）。
4. Director 通过工具调用驱动流程：`decodeDirectorToolCall` 解码 → `actionRegistry[...].Execute`（`actions.go`）落地到业务状态，产出的玩家可见内容以 `Output` 追加到 outbox → 结果作为 `tool` 消息回传。`check_rule` 与按 NPC 的 `act_npc` 在 `directorBatchDispatch` 中并发，其余顺序执行。
5. 每轮工具结果追加完后触发 `afterDispatch`（`floorRun`，`floor_run.go`）：先把 outbox 经 `gctx.Publish` 发布并落库转录检查点，再做防循环判定，最后判断是否要强制结束。强制结束的原因对应 `RunOutput.Stop`：`ErrLoopDetected`（同一批调用在 10 轮窗口内第 3 次追加提醒、第 5 次中止）→ `loop`；连续 `MaxKpRound`（战斗/追逐 `MaxKpRoundEncounter`）轮没有任何玩家可见消息 → `stall`；周期中途上下文逼近窗口时先就地总结再继续同一个周期，只有当前周期本身就超窗口或总结失败才 `ErrContextFull` → `context`；`ErrHostStop` → `host_stop`；其余错误 → `error`。
6. `publishRound`（`session_kp.go`）把一轮的每个 `Output` 落成独立消息：`write` → `writer`（pending，进 FIFO 队列）、`generate_image` → `image`（pending，每张图独立并发的 Painter）、`response` / `end_game` → `kp`、`ask_players` → `ask`。落库后广播 `message_new`，Writer 与 Painter 异步补全后广播 `message_update`。Writer 队列（`writer_queue.go`）按会话 FIFO，每条消息一次 `RunWriterStream`，不会被 kp-stop 取消，也不会阻塞周期结束。
7. 上下文压缩只有一级：LLM 总结（`director_summary.go`）。历史单元原样累积，不做确定性剥离或丢弃，因为单元里有已经生效的结算。总结在周期开始前（`MaybeCompact`）和周期中途上下文逼近窗口时（`CompactInFloor`，总结完继续同一个周期，玩家看不到额外的周期起止）触发，把较早的拍连同已有总结合并成最前面的 `<story_so_far>` 单元。切点只落在拍的边界上（两条 kp/ask 消息之间），当前周期的单元永远不被总结。发给 LLM 的房间聊天记录由 `message_render.go` 按 `Kind` 统一渲染，结束结算用 `LoadEndGameMessages`。
- 战斗/追逐中 DEX 顺序只是建议：`applyCombatAct` / `applyChaseAct` 接受任何本轮未行动且存活的参与者，PC 没有声明仍然不能结算。
- 玩家可见正文由 Writer 产生，Director 只给指令；规则裁定由 Lawyer 负责；NPC 有独立行动与记忆。

### Agent 角色配置
- 角色常量在 `models/models.go`（`AgentRole*`），配置行在表 `agent_configs`，每个角色绑定 Provider + 模型 + 温度 + token 上限 + 思考等级，由后台编辑。
- `batchLoadAgents`（`orchestrator.go`）加载角色；`director`、`lawyer`、`npc` 缺失或未绑定可用 Provider 会直接报错，其余为可选，未启用时降级：
  - `dramaturg` 未启用：`consult_dramaturg` 工具不会出现在 Director 工具列表里。
  - `writer_nsfw` / `npc_nsfw` 未启用：回落到基础 Writer / NPC。
  - `painter_nsfw` 未启用：`nsfw=true` 的配图请求被直接拒绝，**不会**回落到 Painter。
  - `diffusion_translator` 仅在 Painter 开启 `diffusion_mode` 时才被调用。
- 每会话的 Agent 句柄会缓存（`sessionAgents`），后台改配置后靠 `ClearAllCachedAgents` 失效，会话结束时 `deleteCachedAgents`。
- 新增角色要同步改：`models/models.go`（常量）、`models/db.go`（`required` 播种列表）、`batchLoadAgents` 的角色列表、`cmd/server/web/js/admin.js`（角色名/描述/默认值，两处）、`models/db_agent_seed_test.go`。

### 新增 Director 工具要改的地方
`types.go`（`ToolCallType` 常量和 `ToolCall` 字段）→ `director_tools.go`（`XxxTool()` 定义 + 加入 `directorTools` + `decodeDirectorToolCall` 分支）→ `actions.go`（`actionRegistry` 登记，并按需加入 `noSideEffectActions` / `responseCompatibleActions`）→ `director_dispatch.go`（`directorBatchPolicy` 的批次规则；`response` / `ask_players` / `end_game` 是回复类工具，不能和需要先读结果的工具同批，其中只有 `ask_players` / `end_game` 结束周期）。`directorTools(dramaturgEnabled)` 每轮发送同一份完整列表（唯一的条件项是 dramaturg 是否启用），这是为了保持提示词缓存前缀稳定（见提交 1b395d7）；不要按回合状态裁剪工具列表。Director 历史同理：发送版即归档版，不要在归档时再裁剪。

### Lawyer 规则缓存
- `COC_kp.md`、`COC_spell.md`、`COC_monster.md` 各有 1–2MB，启动时加载并计算 SHA-256；不要整文件读取它们。
- 三本全部哈希并加载成功才会启用缓存持久化（`data/lawyer_cache.json`，可由 `LAWYER_CACHE_PATH` 覆盖）。缓存文件里的哈希与当前资料不一致时整体丢弃，所以改这三份 md 会使缓存失效。缓存只在优雅退出时落盘，统计每 30 秒落库。
- `agent` 包的全局状态（`lawyerCache` 4GB 预算 LRU、`sessionAgents`、`activeSessions`）在包导入时创建，且几乎所有函数都依赖全局 `models.DB` 已初始化。

### 图片
Painter 默认走 OpenAI 兼容图片接口；开启 `diffusion_mode` 时先由 `diffusion_translator` 把画面描述翻成标签式正面提示词，再调用 `/images/generations`。负面提示词是固定常量，全年龄会话额外追加分级标签（`painter_diffusion.go`）。图片落盘与定期清理在 `services/imagestore`（默认目录是数据库文件同级的 `images/`）。

## 代码约定

- 注释用中文，只写必要原因；每条注释以 `// NOTE:` / `// FIXME:` / `// TODO:` 开头，文件头通常有一段 `// NOTE:` 说明。涉及英文提示词时，同时给出中文含义。
- 提示词是各 Agent 文件里的 Go 字符串常量（如 `kpSystemPrompt`、`writerDefaultPrompt`），以中文为主。`{{NSFW_...}}` 占位符由 `renderNSFW` 填充。
- 日志用 `logging.For("component")` 取得 slog logger（包级变量）。`logging` 包在 `init()` 里设置默认 handler，未迁移的 `log.Printf` 也会走同一输出。
- 可空 JSON 列用 `models.JSONField[T]`。

## 测试

- 都是包内测试（`package handlers` / `package agent`），直接操作全局 `models.DB`，不要用 `t.Parallel`。
- `handlers`：`testhelpers_test.go` 提供 `jsonReq`、`withAuth`（向 gin 上下文注入 user_id/username/role，绕过 JWT）、`seedUser` / `seedCard` / `seedScenario` / `seedProvider` / `seedShopItem`。`init()` 里已设 `gin.TestMode`。
- `agent`：`testhelpers_test.go` 的 `initSiteSettingTestDB` 把全局 `models.DB` 换成内存 SQLite（`t.Cleanup` 还原）；`sequentialFakeProvider` 按顺序返回预设的 LLM 响应并记录调用；`fakeToolCall` 构造工具调用。
- gomock 的 mock 在 `internal/handlers/mocks/`，没有 `go:generate`，重新生成的 `mockgen` 命令写在各文件头部注释里。

## 已知陷阱

- `Dockerfile` 使用 `golang:1.23-alpine`，而 `go.mod` 要求 Go 1.26.5，且 `COPY scenarios/` 指向仓库里不存在的目录；`docker-compose.yml` 也挂载 `./scenarios`。这些我没有实际构建验证，改 Docker 相关内容前先确认。镜像以 `CGO_ENABLED=1` 构建（SQLite）。
- 修复问题要从根因处理；不确定产品语义时先确认；遇到未预期的改动或他人改动先看差异，无法判断时问用户。

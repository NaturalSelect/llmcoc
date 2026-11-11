# LLM-COC

Go + Gin + GORM/SQLite 的 COC7 多人跑团服务端，内置前端与多 Agent LLM 管线。业务细节以代码、`README.md`、`docs/business-flow.md`（账号/人物卡/模组/房间/商城/后台流程）为准；本文件只记录读代码前值得先知道的事。

## 命令

```bash
./start.sh                 # 先构建前端（frontend/ → cmd/server/dist/app），再 go build -o ./bin/llmcoc 后运行；自动 source .env
./start.sh --debug         # LOG_LEVEL=debug
./start.sh --dev           # go run ./cmd/server
./start.sh --no-web        # 跳过前端构建，复用已有的 cmd/server/dist/app
./start_bg.sh [flags]      # nohup 后台运行，输出到 run.log（run.log 未被 gitignore）

go vet ./...                              # CI 第一步
go test -race -count=1 ./...              # CI 实际使用的测试命令
go test -run TestXxx ./internal/handlers  # 单个测试

cd frontend
npm ci                     # 安装依赖（Node ^20.19 或 >=22.12，CI 用 22）
npm run dev                # Vite 开发服务器（5173），/api 和 WebSocket 代理到 localhost:8080
npm run typecheck          # vue-tsc
npm test                   # vitest
npm run build              # 产物输出到 ../cmd/server/dist/app
```

- CI 只有 `.github/workflows/test.yml`：前端 install/typecheck/test/build，然后 Go vet + test，最后 `go build` 确认带真实产物也能编译（Go 版本取自 `go.mod`）。无 Makefile、无 lint 配置。
- 测试不依赖网络、真实 LLM 或外部服务。
- 项目目录里可能已有服务进程在跑（占用 8080、持有 `data/llmcoc.db`）：测试/启动前先确认端口，改代码前先 `git status`。
- `data/`、`bin/`、`tmp/`、`.env`、`config.local.yaml`、`frontend/node_modules/`、`cmd/server/dist/app/` 被 gitignore。`data/` 含 SQLite 库、`lawyer_cache.json`、`images/`、`portraits/`。
- 本机 `~/.npm` 缓存目录若属主是 root，npm 会报权限错误：给 npm 命令加 `NPM_CONFIG_CACHE=/tmp/llmcoc-npm-cache`。

## 配置与初始化

- `config.yaml` 只含 `server` / `database` / `jwt` 三段（`internal/config/config.go`）。README 里出现的 `shop:` 段并不存在于结构体；初始金币、卡槽等是数据库里的 `SiteSetting`（`models/db.go` 的 `seedDefaultSiteSettings`，后台可改）。
- 实际读取的环境变量：`CONFIG_PATH`、`JWT_SECRET`、`LOG_LEVEL`（默认 debug，日志级别的唯一开关；Gin 固定 release 模式）、`RULEBOOK_PATH` / `SPELLBOOK_PATH` / `MONSTERBOOK_PATH`、`LAWYER_CACHE_PATH`、`LLMCOC_IMAGE_DIR`、`LLMCOC_IMAGE_QUALITY`、`LLMCOC_PORTRAIT_DIR`。
- 文档里提到但代码未读取：`DB_PATH`（start.sh 注释）。不要依赖它们。
- 不再从环境变量播种 LLM Provider：库里没有 Provider 时各 Agent 的 Provider 为空，需在后台手动添加并绑定；Agent 默认模型固定为 `gpt-4o`。
- 所有播种都是「缺失才创建」：改 `seedDefaultAgentConfigs` / `seedDefaultSiteSettings` 里的默认值不会影响已有数据库。下线 Agent 角色必须把旧角色名加进 `seedDefaultAgentConfigs` 里的 `DELETE ... WHERE role IN` 清理列表，否则老库会残留。
- 只有 `AutoMigrate`，没有迁移文件。新增列需要回填旧数据时，参照 `with_jailbreak`：迁移前用 `Migrator().HasColumn` 记下列是否存在，迁移后回填。新增模型要加进 `InitDB` 的 AutoMigrate 列表。
- 人物卡 `Inventory` 和账号 `Warehouse` 存的是 `[]models.Item`（名称、描述、数量、类型）。库里历史上存的是 `"绷带(x3)"` 这样的字符串：`Item.UnmarshalJSON` 同时接受字符串和对象，`InitDB` 里的 `backfillLegacyItems` 把旧行改写成对象数组（可重复执行）。写入一律走 `models.AddItem` / `TakeItem`，堆叠键是名称加描述。回填后旧二进制读不了这两列，回滚只能恢复库备份。
- 管理员不会自动创建：注册后用 `sqlite3 data/llmcoc.db "update users set role='admin' where username='...'"`。
- `scenarios/` 目录不在仓库中。`handlers.SeedScenarios("scenarios")` 在目录缺失或库里已有模组时静默返回。

## 架构

### 入口与前端
- `cmd/server/main.go` 完成：加载配置 → `models.InitDB` → 播种 → 加载三本规则资料并算哈希 → 启动统计持久化/图片清理 → 注册路由 → 优雅退出时保存 Lawyer 缓存。
- 前端是仓库根目录 `frontend/` 下的 Vue 3 + TypeScript + Vite + Tailwind v4 + Pinia + Vue Router 工程。`npm run build` 把产物输出到 `cmd/server/dist/app/`（不入库，`dist/.gitkeep` 保证目录存在），`main.go` 用 `//go:embed all:dist` 嵌入，`frontend.go` 的 `frontendHandler` 挂在 `NoRoute` 上：真实文件按路径返回（`assets/*` 带内容哈希，缓存一年），目录和不存在的路径回落到 `index.html`（`no-cache`），没有 `index.html`（没构建前端）时返回 503 和构建提示。改前端后需要重新 build 并重启（或用 `npm run dev` 热更新）。
- 前端用 history 路由，地址即页面：`/login`、`/`、`/sessions`、`/sessions/:id`（游戏页）、`/shop`、`/admin/:tab?`。路由守卫在 `src/router.ts`，token 仍存 `localStorage['coc_token']`；任何带 token 的请求返回 401 都由 `main.ts` 里注册的处理函数统一登出并跳回登录页。
- gzip 中间件按路径正则排除了流式接口：房间 WebSocket `/api/sessions/:id/ws`，以及 SSE 的 `/api/scenarios/generate`、`/api/scenarios/compile-story`。`/chat`、`/kp-turn`、`/kp-stop` 已改为立即返回 202 的普通 JSON 接口，不再在排除列表里。新增流式接口必须同步加进 `main.go` 的排除列表。HTTP `WriteTimeout` 为 30 分钟，是为了长时间 AI 生成。
- 房间所有进展（新消息、writer 流式文字、点名、等待进度、周期起止、房间状态变化）都经 `GET /api/sessions/:id/ws` 推送，协议见 `docs/realtime-protocol.md`。游戏页没有任何轮询：`frontend/src/game/roomController.ts` 负责连接、重连退避、探测、快照和房主命令（WebSocket、定时器是私有字段，实例用 `markRaw`，不能放进 reactive/Pinia），状态归约全在 `frontend/src/game/roomState.ts`（纯逻辑，不依赖 DOM、Vue 组件和网络，函数原地修改传入的 room，所以必须传 `reactive()` 代理本身）；问题卡、状态栏文案、消息辅助分别是 `ask.ts`、`banner.ts`、`messages.ts` 里的纯函数。游戏页没有常驻输入框：玩家只在被点名时看到问题卡（挂在最近一条点名消息下面，由 `ask.ts` 的 `askAnchor` 定位，找不到点名消息时才放到消息流末尾；推荐行动可多选，外加自由输入，提交后收成一行摘要留在原位，断线时置灰但保留内容），房间状态和房主的继续/打断按钮在底部状态栏。这些模块的测试与源码同目录（`*.test.ts`，vitest；`roomController.test.ts` 用假的 WebSocket 和假定时器），`GameView.test.ts` 会挂载整个游戏页。所有页面的模板和样式（游戏页、首页、大厅、商城、后台）都还没有在真实浏览器里人工验证。
- 前端是深色主题，全部颜色和字体令牌在 `frontend/src/styles/theme.css`（`bg` / `surface` / `line` / `fg` / `accent` 等，强调色是琥珀色），图标用 `@lucide/vue`。基础组件在 `src/components/base/`（按钮、卡片、输入框、弹窗、页头等），页面应优先用它们而不是手写样式。两条测试守住这套规范：`src/styles/contrast.test.ts` 按 WCAG 公式检查令牌色值的对比度，`src/styles/designRules.test.ts` 逐行扫描所有 `.vue`，禁止小于 12px 的字、`rounded-sm`、粗边框、`!` 强制覆盖、原始调色板颜色、emoji 图标和旧的纸张主题色名。两个测试要读文件，所以 tsconfig 把它们放在 `tsconfig.node.json` 里、从 `tsconfig.app.json` 排除。
- 路由全部在 `main.go` 里手写；`BanCheck` 中间件只挂在部分分组上，新增分组时注意是否需要。

### 一段 KP 叙事的数据流
整局由 KP（Director）驱动，玩家只在被点名时才有发言权。一次 `agent.Run` 就是一个「周期」（floor）：KP 在一个 ReAct 循环里自由调用工具，没有续跑次数上限，只有 `ask_players` / `end_game` 会正常结束周期；`narrate` 只是发布一拍消息，不结束周期。
`GameSession.Spotlight` 记录发言权：`UserIDs` 为空（含 NULL，不再兼容旧的玩家驱动房间）是 KP 持有发言权；非空是等这些玩家回应；`Stop` 非空表示上个周期被强制结束（`host_stop` / `loop` / `stall` / `error` / `context`），此时玩家提交会被拒绝，要房主点「继续」。
1. 入口有两个：`handlers/session.go` 的 `ChatStream`（玩家回应：校验是否被点名，写 `SessionTurnAction`，点名对象到齐后才开周期）和 `handlers/session_kp.go` 的 `KPTurn`（房主/管理员触发开场或继续）。两者都立即返回 202，周期在后台跑；`KPStop` 让房主/管理员硬打断，返回 202 或 409。
2. `startFloor` 先占用会话处理锁，在事务里写玩家消息，登记到 `activeFloors`（kp-stop 和关机靠它取消 ctx），再广播 `floor_start`。`runFloor` 用 `context.WithCancelCause` 跑 `Runner.Run`，上下文已满时自动总结并续跑一次（续跑没有产出任何消息就不再续）。`finishFloor` 先落库新发言权、释放处理锁，之后才广播 `spotlight` / `floor_end`，否则被点名者立刻发言会撞上 409。进程关闭时 `StopAllFloors` 取消全部周期并最多等 10 秒。
3. `agent.Run`（`orchestrator.go`）用 `activeSessions` 保证同一会话同时只跑一个周期；`getCachedAgents` 读取 Agent 配置；构造 Director 提示词与上下文（主动周期渲染 `<kp_floor>`，玩家回应周期在有提问时渲染 `<asked>`）后进入 `runToolLoop`（`scripter_toolloop.go`，模组生成管线共用，`maxRounds <= 0` 表示不限轮数）。
4. Director 通过工具调用驱动流程：`decodeDirectorToolCall` 解码 → `actionRegistry[...].Execute`（`actions.go`）落地到业务状态，产出的玩家可见内容以 `Output` 追加到 outbox → 结果作为 `tool` 消息回传。`check_rule` 与按 NPC 的 `act_npc` 在 `directorBatchDispatch` 中并发，其余顺序执行。
5. 每轮工具结果追加完后触发 `afterDispatch`（`floorRun`，`floor_run.go`）：先把 outbox 经 `gctx.Publish` 发布并落库转录检查点，再做防循环判定，最后判断是否要强制结束。强制结束的原因对应 `RunOutput.Stop`：`ErrLoopDetected`（同一批调用在 10 轮窗口内第 3 次追加提醒、第 5 次中止）→ `loop`；连续 `MaxKpRound`（战斗/追逐 `MaxKpRoundEncounter`）轮没有任何玩家可见消息 → `stall`；周期中途上下文逼近窗口时先就地总结再继续同一个周期，只有当前周期本身就超窗口或总结失败才 `ErrContextFull` → `context`；`ErrHostStop` → `host_stop`；其余错误 → `error`。
6. `publishRound`（`session_kp.go`）把一轮的每个 `Output` 落成独立消息：`write` → `writer`（pending，进 FIFO 队列）、`generate_image` → `image`（每张图独立并发的 Painter，画完才落库，落库前不存在这条消息）、`narrate` / `end_game` → `kp`、`ask_players` → `ask`。落库后广播 `message_new`，Writer 在后台补全后广播 `message_update`；`image` 画完才落库并以 `message_new` 推送（`finishImageMessage`，成功是 done 带 `images`，失败是 failed 带 `meta.error`），所以 `id` 和 `created_at` 是画完那一刻的，前端按 id 排序时配图排在已有消息之后，和推送顺序一致，`GET /messages` 里也不会有未画完的图。Writer 队列（`writer_queue.go`）按会话 FIFO，每条消息一次 `RunWriterStream`，不会被 kp-stop 取消。同一轮里的 `kp` / `ask` 要等会话里已入队的 Writer 全部结束才落库广播（`waitWritersForBeat`，等待期间 Director 停在这一轮，周期 ctx 取消时立即放行），玩家先读到叙事再被提问；只有 writer/image 的轮次不等。Writer 正文按 100ms 合并成带 UTF-16 偏移的 `writer_token` 帧，避免逐 token 广播把订阅者队列（64 条）塞满被当成慢消费者踢掉；新连接握手时会补发一帧当前正在生成的全文。Writer 的流在中途遇到 EOF 时（`llm.IsUnexpectedEOF`，含空闲超时），`appendWriterStream` 丢弃半截正文、等 2 秒后整段重新请求，最多共 `writerMaxGenerateAttempts` 次；重试前通过 ctx 里的 `WithWriterRestart` 回调让 `liveWriter.reset` 清空已生成正文并广播 `writer_reset`，客户端据此清掉本地拼接的半截文字，之后的 `writer_token` 从 offset 0 重来；重试用尽才把半截正文留给 `finishWriterMessage`。本轮工具产生的玩家可见结算（`ToolResult.Events`，由 `directorBatchDispatch` 按调用顺序收集）会先落成一条 `event` 消息，id 排在同轮其他消息前面；只有事件的轮次也会即时发布，但不算产生消息（不写检查点、不重置停滞计数、不计入 `RunOutput.Messages`）。事件里只放玩家能看到的信息：暗骰只有 `hidden`，NPC 的变化不产生事件。给 LLM 的聊天记录里，`event` 消息只渲染公开骰。
7. 上下文压缩只有一级：LLM 总结（`director_summary.go`）。历史单元原样累积，不做确定性剥离或丢弃，因为单元里有已经生效的结算。总结在周期开始前（`MaybeCompact`）和周期中途上下文逼近窗口时（`CompactInFloor`，总结完继续同一个周期，玩家看不到额外的周期起止）触发，把较早的拍连同已有总结合并成最前面的 `<story_so_far>` 单元。切点只落在拍的边界上（两条 kp/ask 消息之间），当前周期的单元永远不被总结。发给 LLM 的房间聊天记录由 `message_render.go` 按 `Kind` 统一渲染，结束结算用 `LoadEndGameMessages`。
- 战斗/追逐中 DEX 顺序只是建议：`applyCombatAct` / `applyChaseAct` 接受任何本轮未行动且存活的参与者，PC 没有声明仍然不能结算。
- 玩家可见正文由 Writer 产生，Director 只给指令；规则裁定由 Lawyer 负责；NPC 有独立行动与记忆。
- 结局结算由 Director 在 `end_game` 里完成，没有独立的 Evaluator / Growth Agent。`settlement` 必填，必须为每位调查员（含死亡者）各写一条 `score`（0–100）和本局用过的技能；`validateSettlement`（`endsession.go`）在任何副作用之前校验，缺人、重名、名字对不上或评分越界都会以 `SYSTEM REJECT` 打回，游戏不会结束。`RunEndSession` 按评分折算金币（基础 20 + 评分/2），`win=true` 时才对 Director 报的技能做成长检定并做 POW 增长；HTTP 手动结束和管理员批量结束传 `nil` 结算，只发基础金币、不写评价记录。结束时不再生成 summary。`RunEndSession` 返回玩家可见的结算事件（`coins` 金币、技能成长、HP/MP/SAN 恢复、疯狂解除、`backstory` 经历改写），`end_game` 把它们作为 `ToolResult.Events` 发布成一条 `event` 消息，手动结束丢弃返回值。
- 「个人经历演变」由房间创建时的 `GameSession.EvolveBackstory`（默认 false，之后不可改）控制：只有开关打开且 `win=true` 时，`RunEndSession` 才通过 `backstoryEvolver`（`backstory.go`，借用 Writer 的 Provider，一次调用覆盖全部存活调查员）改写个人经历，并与关系提升并行。尽力而为：LLM 或 JSON 失败时经历保持不变；`filterBackstories` 丢弃非存活调查员、空串、未变化和超过 300 字的改写；死亡者不参与。测试通过替换 `backstoryEvolver` 绕开数据库里的 Provider。
- 人物卡生成/重生成、技能调整和结算时的关系提升借用 Writer 角色的 Provider（`loadSingleAgent(AgentRoleWriter)`），不再有单独配置项。

### Agent 角色配置
- 角色常量在 `models/models.go`（`AgentRole*`），配置行在表 `agent_configs`，每个角色绑定 Provider + 模型 + 温度 + token 上限 + 思考等级，由后台编辑。
- `batchLoadAgents`（`orchestrator.go`）加载角色；`director`、`lawyer`、`npc` 缺失或未绑定可用 Provider 会直接报错，其余为可选，未启用时降级：
  - `dramaturg` 未启用：`consult_dramaturg` 工具不会出现在 Director 工具列表里。
  - `writer_nsfw` / `npc_nsfw` 未启用：回落到基础 Writer / NPC。
  - `painter_nsfw` 未启用：`nsfw=true` 的配图请求被直接拒绝，**不会**回落到 Painter。房间 NSFW 开关关闭时，`write` / `act_npc` / `describe_characters` / `generate_image` 的 `nsfw=true` 一律被忽略，按普通请求处理。
  - Writer / NPC / 配图的 NSFW 提示词按本次调用的 `nsfw` 标记渲染（该标记已经过房间开关过滤），不是按房间开关；只有 Director 的系统提示词按房间开关渲染。
  - `diffusion_translator` 仅在 Painter 开启 `diffusion_mode` 时才被调用。
- 每会话的 Agent 句柄会缓存（`sessionAgents`），后台改配置后靠 `ClearAllCachedAgents` 失效，会话结束时 `deleteCachedAgents`。
- 新增角色要同步改：`models/models.go`（常量）、`models/db.go`（`required` 播种列表）、`batchLoadAgents` 的角色列表、`frontend/src/components/admin/agents/agentRoles.ts`（`ROLE_DEFAULTS` 默认值，以及名称/描述/占位提示三张表，共四处）、`models/db_agent_seed_test.go`。

### 新增 Director 工具要改的地方
`types.go`（`ToolCallType` 常量和 `ToolCall` 字段）→ `director_tools.go`（`XxxTool()` 定义 + 加入 `directorTools` + `decodeDirectorToolCall` 分支）→ `actions.go`（`actionRegistry` 登记，并按需加入 `noSideEffectActions` / `narrateCompatibleActions`）→ `director_dispatch.go`（`directorBatchPolicy` 的批次规则；`narrate` / `ask_players` / `end_game` 是回复类工具，不能和需要先读结果的工具同批，其中只有 `ask_players` / `end_game` 结束周期）。`directorTools(dramaturgEnabled)` 每轮发送同一份完整列表（唯一的条件项是 dramaturg 是否启用），这是为了保持提示词缓存前缀稳定（见提交 1b395d7）；不要按回合状态裁剪工具列表。Director 历史同理：发送版即归档版，不要在归档时再裁剪。工具如果有玩家可见的结算，就在 `ToolResult.Events` 里给出 `models.GameEvent`（辅助函数在 `game_events.go`），并同步前端 `frontend/src/game/events.ts` 的文案。

### Lawyer 规则缓存
- `COC_kp.md`、`COC_spell.md`、`COC_monster.md` 各有 1–2MB，启动时加载并计算 SHA-256；不要整文件读取它们。
- 三本全部哈希并加载成功才会启用缓存持久化（`data/lawyer_cache.json`，可由 `LAWYER_CACHE_PATH` 覆盖）。缓存文件里的哈希与当前资料不一致时整体丢弃，所以改这三份 md 会使缓存失效。缓存只在优雅退出时落盘，统计每 30 秒落库。
- `agent` 包的全局状态（`lawyerCache` 4GB 预算 LRU、`sessionAgents`、`activeSessions`）在包导入时创建，且几乎所有函数都依赖全局 `models.DB` 已初始化。

### 图片
Painter 默认走 OpenAI 兼容图片接口；开启 `diffusion_mode` 时先由 `diffusion_translator` 把画面描述翻成标签式正面提示词，再调用 `/images/generations`。负面提示词是固定常量，`nsfw=false` 的配图额外追加分级标签（`painter_diffusion.go`）。配图按「连续漫画的单格」出：画风（日式ACG漫画）由 `painter.go` 的 `imageSysPrompt` 和扩散翻译提示词统一附加，Director 的 `image_prompt` 只写这一格的内容；`generate_image` 工具说明里的分镜规则和连续性规则要求景别随剧情变化、不画气泡和文字、角色外貌与场景跨图一致，没有额外的角色外貌存储，一致性靠 Director 沿用前几格的写法。图片落盘与定期清理在 `services/imagestore`（默认目录是数据库文件同级的 `images/`）。

调查员立绘是手动触发的单独流程（`POST /api/characters/:id/portrait`，`agent/portrait.go` 的 `GeneratePortrait`）：Writer 用 `writeImagePromptCharacterVisualDescription` 把外貌加已装备物品写成描述，再经 `paintImage`（`RunPainter` 与立绘共用的出图核心）按竖版出图。立绘不属于任何房间，固定按全年龄渲染，只用 `writer` 和 `painter`（扩散模式再加 `diffusion_translator`），缺任何一个都直接报错，不回落 NSFW 角色，也不退回拼接外貌文本。立绘存在 `imagestore.PortraitDir()`（默认数据库同级的 `portraits/`，`LLMCOC_PORTRAIT_DIR` 可覆盖），读取走 `GET /api/portraits/:hash`。它必须在配图目录之外：`StartCleanup` 会递归删掉配图目录里超过 14 天的文件，`main.go` 启动时发现立绘目录嵌套在配图目录内会直接退出。

`character_cards.portrait_hash` 是只读列（`<-:false`）：对局里几乎所有人物卡改动都是整行 `Save`，普通列会被旧快照覆盖，所以它只能用原生 SQL（`UPDATE character_cards SET portrait_hash = ?`）写入，`Create`、`Save`、`Update` 都会跳过它，`models/character_portrait_test.go` 守着这一点。生成成功后在事务里条件扣费（`regenerate_portrait_cost`，默认 100，管理员替别人生成不收费），余额不足时丢弃新文件并返回 402；换新图后旧文件在没有其他卡引用时删除。同一张卡同时只允许一个生成任务（409）。装备或外貌变化后立绘不会标记过期，也不会自动重画。

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

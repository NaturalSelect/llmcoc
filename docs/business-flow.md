# 业务流程设计

本文档补齐项目的业务流程设计。现有项目说明集中在 `README.md`，其中功能概览、API 路由、启动与初始化行为是本文件的主要依据；实现细节参考 `cmd/server/main.go`、`internal/handlers`、`internal/models` 和 `internal/services`。

## 业务边界

LLM-COC 是一个房间制 COC7 在线跑团服务。核心用户是玩家和管理员：

- 玩家注册登录后创建调查员，进入房间，按回合提交行动，与 AI KP 跑团。
- 管理员维护用户、邀请码、商店、模组、LLM Provider、Agent 配置和规则缓存。
- 系统负责持久化角色、房间、消息、模组、经济和 Agent 运行所需状态。

## 启动与初始化流程

服务启动入口是 `cmd/server/main.go`，流程如下：

1. 读取 `CONFIG_PATH` 指向的配置文件，默认使用 `config.yaml`。
2. 初始化 SQLite 数据库，开启 WAL 和外键，并通过 GORM 自动迁移模型。
3. 首次启动时从 `scenarios/` 导入内置模组。
4. 写入默认商店商品。
5. 根据环境变量初始化默认 LLM Provider，并补齐 Agent 配置。
6. 加载 `COC_kp.md`、`COC_spell.md`、`COC_monster.md`，供规则顾问和规则缓存使用。
7. 注册 `/api` 接口和内嵌前端静态资源。

相关说明见 `README.md` 的“默认数据与初始化行为”和“环境变量”。

## 账号与权限流程

注册登录流程：

1. 前端登录页调用 `/api/auth/settings/public` 获取是否需要邀请码。
2. 用户注册时，如果站点设置要求邀请码，后端校验邀请码是否存在且未使用。
3. 注册成功后创建普通用户，发放初始金币和人物卡槽位。
4. 登录成功后返回 JWT，前端存入 `localStorage`。
5. 后续接口通过 `Authorization: Bearer <token>` 鉴权。

权限流程：

- 普通用户可以管理自己的人物卡、进入房间、购买商品、发送行动。
- 管理员通过管理后台维护用户、模组、商城、Provider、Agent、系统设置和规则缓存。
- 项目不会自动创建管理员；README 说明了首次使用时通过 SQLite 修改用户角色。

## 人物卡流程

人物卡是玩家进入游戏的身份载体：

1. 玩家可以创建、编辑、删除人物卡，也可以调用 AI 生成人物卡。
2. 人物卡包含 COC 属性、衍生属性、技能、背包、法术、社会关系、已见神话存在、疯狂状态和伤亡状态。
3. 创建和生成流程会补齐基础属性、HP、MP、SAN、技能等数据。
4. 游戏中 Agent 通过工具修改人物卡状态，例如 HP、SAN、MP、背包、法术、关系、位置和护甲。
5. 死亡角色进入阵亡列表，玩家可按规则消耗金币复活或彻底删除。

主要实现位于 `internal/handlers/character.go`、`internal/services/game` 和 `internal/services/agent/editor.go`。

## 模组流程

模组是房间运行的剧本来源：

1. 启动时如果数据库没有模组，系统从 `scenarios/` 导入 JSON 模组。
2. 管理员可以创建、上传、删除模组，也可以下载 JSON 模板。
3. 管理员可以调用 AI 生成模组，后端异步运行 Scripter 团队，生成完成后写入数据库。
4. 模组内容包含背景、开场、地图、场景、NPC、线索、胜利条件、失败条件和奖励。
5. 前端跑团大厅和创建房间流程读取可用模组列表。

主要实现位于 `internal/handlers/scenario.go`、`internal/models/scenario_module.go` 和 `internal/services/agent/scripter*.go`。

## 房间与跑团流程

房间是一次跑团的运行容器：

1. 玩家选择模组创建房间，可设置最大玩家数和房间密码。
2. 其他玩家选择自己的人物卡加入房间；一张人物卡同一时间只能参与一个未结束房间。
3. 房主开始游戏后，房间状态从 `lobby` 变为 `playing`，系统写入开场消息。
4. 前端进入游戏页后加载房间详情和消息记录，并定时刷新。
5. 玩家提交行动后，后端通过 SSE 返回 `thinking`、`token`、`narration`、`waiting`、`error`、`done` 等事件。
6. 单人房间直接触发 Agent 管线。
7. 多人房间会先记录每名存活调查员本回合行动；全部提交后，只运行一次 Agent 管线处理整轮行动。
8. Agent 产出的 Writer 叙事和 KP 直接回复合并保存为消息，供断线重连和其他玩家轮询读取。
9. 房主或管理员可结束游戏；结束时扣除金币，运行评价和成长结算。
10. 管理员可以复活已结束房间，系统写入复活系统消息。

主要实现位于 `internal/handlers/session.go` 和 `cmd/server/web/js/game.js`。

## Agent 跑团流程

Agent 管线负责把玩家行动转成规则裁定、状态变化和叙事输出：

1. ChatStream 构造 `GameContext`，包含房间、历史消息、当前输入和多人待处理行动。
2. Director 读取剧本、当前状态和玩家行动，输出 JSON 工具调用数组。
3. 后端按工具注册表执行工具，工具结果作为下一轮输入返回给 Director。
4. Lawyer 只负责查 COC 规则、法术和怪物资料，并可使用规则缓存。
5. NPC Agent 负责临时 NPC 的行动和对话，并维护独立记忆。
6. Writer 根据 Director 的叙事指令生成玩家可见正文。
7. Director 调用 `response` 或 `end_game` 后，本轮结束。
8. Writer 历史、NPC 状态、线索、位置、背包、伤亡、疯狂等状态按各自模型持久化。

重点边界：

- LLM API 推送给后端的是模型输出或工具调用 JSON。
- 后端给前端推送的流式输出是 SSE 事件。
- Writer 叙事正文和 KP 直接回复在前端用不同样式展示。

主要实现位于 `internal/services/agent/orchestrator.go`、`internal/services/agent/actions.go`、`internal/services/agent/director.go`、`internal/services/agent/writer.go`、`internal/services/agent/lawyer.go` 和 `internal/services/agent/npc.go`。

## 商城与经济流程

商城提供人物卡槽、装备、武器和配件等商品：

1. 系统启动时写入默认商品。
2. 玩家在商城浏览商品并购买。
3. 购买成功后扣除金币，写入交易记录，并按商品类型更新用户或人物卡。
4. 管理员可以创建和删除商品。
5. 管理员可以给用户充值金币。
6. 结束游戏时每名玩家消耗固定金币，并触发结算流程。

主要实现位于 `internal/handlers/shop.go`、`internal/handlers/admin.go` 和 `internal/handlers/session.go`。

## 管理后台流程

管理后台是现有控制面，前端模板位于 `cmd/server/web/pages/admin.html`：

1. 用户管理：查看用户、充值、切换管理员、封号和解封。
2. LLM Provider：创建、编辑、删除、Ping 测试。
3. Agent 配置：绑定 Provider、设置模型、token、温度、推理强度和启用状态。
4. 模组管理：查看、删除、上传、下载模板、AI 生成。
5. 商城管理：创建和删除商品。
6. 系统设置：控制注册是否需要邀请码。
7. 邀请码管理：批量生成、查看和删除未使用邀请码。
8. 规则缓存：查看命中统计和清空缓存。

新增后台能力时优先扩展这个页面和现有 `/api/admin` 路由。

## 数据与状态设计

项目以 SQLite 作为唯一业务数据库：

- 用户、人物卡、模组、房间、房间玩家、消息、商城、交易、Provider、Agent 配置、系统设置、邀请码都由 GORM 模型管理。
- 剧本内容、角色属性、技能、背包、法术、关系、战斗状态、追逐状态、Writer 历史等复杂结构使用 JSON 字段存储。
- 规则资料来自仓库内 Markdown 文件，不存入数据库。
- Lawyer 规则缓存默认持久化到 `data/lawyer_cache.json`，并通过规则文件哈希判断是否可复用。

## 变更影响检查

修改业务流程时至少检查这些位置：

- API 是否需要更新 README 的路由概览。
- 前端是否需要同步更新 `cmd/server/web/js` 和 `cmd/server/web/pages/admin.html`。
- 数据模型是否需要兼容旧 JSON 字段或旧数据库行。
- Agent 工具是否需要更新 Director 提示词、工具结构、工具执行器和测试。
- 多人回合逻辑是否会影响等待、断线重连和消息持久化。
- 规则、骰子、伤亡、理智、成长等机制是否需要引用规则资料或补测试。

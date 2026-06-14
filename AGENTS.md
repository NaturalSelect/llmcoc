# LLM-COC 项目索引

本文件只做协作入口和项目索引。业务细节以当前代码、`README.md` 和 `docs/business-flow.md` 为准。

## 先读什么

- `README.md`：项目定位、启动方式、环境变量、初始化行为、API 概览。
- `docs/business-flow.md`：账号、人物卡、模组、房间跑团、Agent、商城、管理后台等业务流程。
- `config.yaml`：本地默认服务、数据库、JWT、商城配置。
- `scenarios/lonely_island.json`：内置模组 JSON 示例。
- `COC_kp.md`、`COC_spell.md`、`COC_monster.md`：规则、法术、怪物资料来源。
- `combat.md`、`chase.md`：战斗和追逐相关规则资料。

## 代码索引

- `cmd/server/main.go`：服务入口、初始化流程、路由注册、内嵌前端。
- `cmd/server/web/index.html`：单页前端主模板。
- `cmd/server/web/pages/admin.html`：管理后台模板。
- `cmd/server/web/js/app.js`：前端核心状态、鉴权、导航、通用 API。
- `cmd/server/web/js/game.js`：游戏页聊天、SSE、消息刷新。
- `cmd/server/web/js/sessions.js`：房间列表、创建、加入、开始、结束。
- `cmd/server/web/js/admin.js`：后台用户、Provider、Agent、模组、商城、设置、缓存管理。
- `cmd/server/web/js/dashboard.js`：首页和人物卡相关前端逻辑。
- `cmd/server/web/js/shop.js`：商城和购买逻辑。
- `cmd/server/web/css/style.css`：前端主题和通用样式。
- `internal/config`：配置加载。
- `internal/models`：GORM 模型、数据库初始化、JSON 字段、模组数据兼容。
- `internal/middleware`：JWT 鉴权、管理员权限、封号检查。
- `internal/handlers`：HTTP handler 和接口测试。
- `internal/services/game`：骰子、属性、伤害、疯狂等 COC 机制。
- `internal/services/rulebook`：规则资料加载、检索、常量读取。
- `internal/services/llm`：LLM Provider 抽象和 OpenAI 兼容实现。
- `internal/services/agent`：跑团 Agent、模组生成 Agent、规则缓存、结算成长。

## 关键业务入口

- 账号与权限：`internal/handlers/auth.go`、`internal/handlers/admin.go`、`internal/handlers/admin_invite.go`。
- 人物卡：`internal/handlers/character.go`、`internal/services/agent/character.go`。
- 模组：`internal/handlers/scenario.go`、`internal/models/scenario_module.go`、`internal/services/agent/scripter*.go`。
- 房间与聊天：`internal/handlers/session.go`。
- 商城与经济：`internal/handlers/shop.go`、`internal/handlers/admin.go`。
- LLM Provider 和 Agent 配置：`internal/handlers/admin_config.go`、`internal/models/db.go`。
- 规则缓存后台接口：`internal/handlers/admin_config.go`、`internal/services/agent/lawyer.go`、`internal/services/agent/cache.go`。

## Agent 索引

- Director：`internal/services/agent/director.go`，负责读取局势并输出工具调用。
- Orchestrator：`internal/services/agent/orchestrator.go`，负责执行 Director 工具循环。
- Actions：`internal/services/agent/actions.go`，负责工具调用到业务状态的落地。
- Writer：`internal/services/agent/writer.go`，负责玩家可见叙事正文。
- Lawyer：`internal/services/agent/lawyer.go`，负责规则资料检索和裁定。
- NPC：`internal/services/agent/npc.go`，负责临时 NPC 独立行动与记忆。
- AntiCheat：`internal/services/agent/anti_cheat.go`，负责副作用工具一致性审查。
- Evaluator / Growth：`internal/services/agent/evaluator.go`、`internal/services/agent/growth.go`，负责结算评价和成长。
- Scripter：`internal/services/agent/scripter*.go`，负责 AI 模组生成。

## 协作注意

- 代码注释使用中文，解释必要原因即可。
- 修复问题要从根因处理；不确定产品语义时先确认。
- 变更说明写清楚原逻辑、改了什么、为什么改。
- 涉及英文提示词时，同时给出中文含义。
- 遇到未预期改动或多人协作冲突，先看差异；无法判断时问用户。

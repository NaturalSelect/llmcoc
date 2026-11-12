# LLM-COC

一个基于 Go + Gin + SQLite 的克苏鲁（COC7）多人跑团服务端，内置前端页面与多 Agent 管线，可用于房间制在线跑团、剧本管理、人物卡管理与 AI 主持（KP）对话。

## 功能概览

- 用户系统：注册、登录、JWT 鉴权、个人信息。
- 人物卡：创建/编辑/删除、AI 生成人物卡、背包管理。
- 剧本系统：剧本列表、详情、模板下载、上传与 AI 生成（管理员）。
- 房间与会话：创建房间、加入房间、开局、聊天与消息记录。
- 商店系统：商品列表、购买、交易记录、金币充值（管理员）。
- 公告系统：首页展示当前有效公告，支持置顶、有效期、Markdown 正文（管理员维护）。
- 管理后台接口：
  - 用户与权限管理。
  - 邀请码管理。
  - 公告管理。
  - 站点设置（如是否需要邀请码注册）。
  - LLM Provider 与 Agent 参数管理（模型、温度、token 上限等）。

## 技术栈

- Go 1.26
- Gin
- GORM + SQLite
- JWT（`github.com/golang-jwt/jwt/v5`）
- LLM SDK（`charm.land/fantasy`，统一封装 OpenAI 兼容与 Anthropic 的对话接口）
- 图片生成与嵌入向量 SDK（`github.com/openai/openai-go/v3`）
- 规则缓存向量检索（`github.com/philippgille/chromem-go`，见下方第三方许可证）
- 前端：Vue 3 + TypeScript + Vite + Tailwind CSS 单页应用（源码在 `frontend/`，构建产物嵌入二进制）

## 快速开始

### 1. 准备环境

- Go 1.26+
- Node.js 20.19+ 或 22.12+（构建前端用；只跑 `go test` 或用 `--no-web` 复用已有产物时不需要）
- 可选：Docker / Docker Compose

### 2. 配置

默认配置文件为 `config.yaml`：

```yaml
server:
  host: "0.0.0.0"
  port: 8080

database:
  path: "data/llmcoc.db"

jwt:
  secret: "change-me-to-a-long-random-secret-in-production"
  expire_hours: 168

shop:
  initial_coins: 600
  initial_card_slots: 3
```

可通过环境变量覆盖部分配置（见下文“环境变量”）。

### 3. 本地启动

使用脚本（推荐）：

```bash
./start.sh
```

调试模式（更详细日志）：

```bash
./start.sh --debug
```

开发模式（直接 `go run`）：

```bash
./start.sh --dev
```

脚本会先构建前端（没有 `frontend/node_modules` 时先 `npm ci`），产物输出到 `cmd/server/dist/app` 并由 Go 二进制嵌入；已有产物、只想重启后端时加 `--no-web` 跳过。没有构建产物时页面会返回 503 并提示构建命令。

前端热更新开发：先用 `./start.sh --dev --no-web` 起后端，再在 `frontend/` 下执行 `npm run dev`，打开 `http://localhost:5173`（`/api` 和 WebSocket 会代理到 8080）。

服务默认监听：`http://0.0.0.0:8080`

### 4. Docker 启动

在项目根目录创建 `.env`（至少设置 `JWT_SECRET`），然后拉取 CI 构建的镜像并启动：

```bash
docker compose up -d
```

镜像地址为 `ghcr.io/naturalselect/llmcoc:latest`，每次推送 `main` 分支会自动重新构建。需要本地构建时使用 `docker compose up -d --build`。

## 环境变量

启动脚本会自动加载项目根目录 `.env`（若存在）。

- `CONFIG_PATH`：配置文件路径（默认 `./config.yaml`）
- `LOG_LEVEL`：日志级别，可选 `debug` / `info` / `warn` / `error`（默认 `debug`，`start.sh --debug` 也会强制为 `debug`）
- `JWT_SECRET`：覆盖 `config.yaml` 中的 JWT 密钥
- `RULEBOOK_PATH`：规则书路径（默认 `COC_kp.md`）

## 默认数据与初始化行为

服务首次启动后会自动：

- 初始化数据库并自动迁移表结构。
- 从 `scenarios/` 目录导入剧本。
- 写入默认商店商品。
- 不会自动创建 LLM Provider，首次使用需在后台手动添加并绑定到各 Agent。
- 自动补全所需 Agent 配置项（director、scripter、qa_guard 等）。

## 管理员账号说明

项目不会自动创建管理员。首次使用可按以下方式之一获取管理员权限：

1. 先正常注册一个账号。
2. 用 SQLite 把该用户角色改为 `admin`。

示例（请替换用户名）：

```bash
sqlite3 data/llmcoc.db "update users set role='admin' where username='your_username';"
```

## 主要 API 路由（概览）

统一前缀：`/api`

- 鉴权：
  - `POST /auth/register`
  - `POST /auth/login`
  - `GET /auth/me`
  - `GET /auth/settings/public`
- 人物卡：
  - `GET /characters`
  - `POST /characters`
  - `POST /characters/generate`
  - `GET /characters/:id`
  - `PUT /characters/:id`
  - `DELETE /characters/:id`
  - `GET /characters/:id/inventory`：返回结构化物品数组 `[{name, desc, count, type}]`
  - `POST /characters/:id/inventory`：请求体 `{"item": {"name", "desc", "count", "type"}}`，同名同描述的物品叠加数量
  - `DELETE /characters/:id/inventory/:name?desc=&count=`：路径是物品名，`desc` 区分同名物品，`count` 缺省或为 0 表示整堆移除
- 仓库（账号级，物品格式与物品栏一致）：
  - `GET /warehouse`
  - `POST /warehouse/deposit`、`POST /warehouse/withdraw`：请求体 `{"character_card_id", "item": {"name", "desc"}, "count"}`，`count` 缺省或为 0 表示整堆
  - `DELETE /warehouse/items/:name?desc=&count=`
- 剧本：
  - `GET /scenarios`
  - `GET /scenarios/:id`
  - `GET /scenarios/:id/module`
  - `GET /scenarios/template`
  - `POST /scenarios`（管理员）
  - `POST /scenarios/generate`（管理员）
  - `POST /scenarios/upload`（管理员）
  - `DELETE /scenarios/:id`（管理员）
- 房间与会话：
  - `GET /sessions`
  - `POST /sessions`
  - `GET /sessions/:id`
  - `POST /sessions/:id/join`
  - `POST /sessions/:id/start`
  - `POST /sessions/:id/end`
  - `GET /sessions/:id/messages`
  - `POST /sessions/:id/chat`（被 KP 点名的玩家提交行动，返回 202，进展走 WebSocket）
  - `POST /sessions/:id/kp-turn`（房主或管理员让 KP 先手开场或继续推进，返回 202）
  - `POST /sessions/:id/kp-stop`（房主或管理员打断 KP 正在进行的周期，返回 202；没有周期在跑返回 409）
  - `GET /sessions/:id/ws`（房间实时推送，协议见 `docs/realtime-protocol.md`）
  - `GET /sessions/:id/chat-status`
- 商店：
  - `GET /shop/items`
  - `POST /shop/purchase`
  - `GET /shop/transactions`
- 公告：
  - `GET /announcements`（登录用户可读，只返回当前启用且在有效期内的公告）
- 管理：
  - `GET /admin/users`
  - `POST /admin/recharge`
  - `PUT /admin/users/:id/role`
  - `GET /admin/recharges`
  - `POST /admin/shop/items`
  - `GET /admin/config/providers`
  - `POST /admin/config/providers`
  - `PUT /admin/config/providers/:id`
  - `DELETE /admin/config/providers/:id`
  - `POST /admin/config/providers/:id/ping`
  - `GET /admin/config/agents`
  - `PUT /admin/config/agents/:role`
  - `GET /admin/config/settings`
  - `PUT /admin/config/settings/:key`
  - `GET /admin/invite-codes`
  - `POST /admin/invite-codes`
  - `DELETE /admin/invite-codes/:id`
  - `GET /admin/announcements`
  - `POST /admin/announcements`
  - `PUT /admin/announcements/:id`
  - `DELETE /admin/announcements/:id`

## 开发与测试

运行全部测试：

```bash
go test ./...
```

构建服务（需先构建前端）：

```bash
(cd frontend && npm ci && npm run build)
go build -o ./bin/llmcoc ./cmd/server
```

前端检查：

```bash
cd frontend && npm run typecheck && npm test
```

## 项目结构

```text
cmd/server            # 程序入口与嵌入前端（dist/app 为前端构建产物，不入库）
frontend              # Vue 前端源码与测试
internal/config       # 配置加载
internal/models       # 数据模型与数据库初始化
internal/handlers     # HTTP 处理器
internal/middleware   # 鉴权/权限中间件
internal/services     # LLM、Agent、规则与游戏逻辑
scenarios             # 剧本 JSON
data                  # SQLite 数据目录
```

## 生产建议

- 务必更换 `jwt.secret` 或 `JWT_SECRET`。
- 使用强 API Key，并限制管理接口访问来源。
- 结合反向代理（Nginx/Caddy）做 HTTPS 与限流。
- 对 `data/` 目录做持久化与备份。

## 第三方许可证

本项目以 AGPL-3.0 发布。规则缓存的向量检索依赖 [chromem-go](https://github.com/philippgille/chromem-go)（v0.7.0），它采用 MPL-2.0 许可证，源码可在该仓库获取；MPL-2.0 允许与 AGPL-3.0 作品合并分发，本项目未修改它的源码。分发二进制或镜像时请保留其许可证声明。

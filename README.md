# LLM-COC

一个基于 Go + Gin + SQLite 的克苏鲁（COC7）多人跑团服务端，内置前端页面与多 Agent 管线，可用于房间制在线跑团、剧本管理、人物卡管理与 AI 主持（KP）对话。

## 功能概览

- 用户系统：注册、登录、JWT 鉴权、个人信息。
- 人物卡：创建/编辑/删除、AI 生成人物卡、背包管理。
- 剧本系统：剧本列表、详情、模板下载、上传与 AI 生成（管理员）。
- 房间与会话：创建房间、加入房间、开局、聊天与消息记录。
- 商店系统：商品列表、购买、交易记录、金币充值（管理员）。
- 管理后台接口：
  - 用户与权限管理。
  - 邀请码管理。
  - 站点设置（如是否需要邀请码注册）。
  - LLM Provider 与 Agent 参数管理（模型、温度、token 上限等）。

## 技术栈

- Go 1.23
- Gin
- GORM + SQLite
- JWT（`github.com/golang-jwt/jwt/v5`）
- OpenAI SDK（`github.com/sashabaranov/go-openai`）
- 前端：嵌入式单页页面（`cmd/server/web/index.html`）

## 快速开始

### 1. 准备环境

- Go 1.23+
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

服务默认监听：`http://0.0.0.0:8080`

### 4. Docker 启动

```bash
docker compose up -d --build
```

## 环境变量

启动脚本会自动加载项目根目录 `.env`（若存在）。

- `CONFIG_PATH`：配置文件路径（默认 `./config.yaml`）
- `GIN_MODE`：`release` 或 `debug`
- `AGENT_DEBUG`：`1/true/yes` 开启 Agent/LLM 调试日志
- `LLM_API_KEY`：默认 LLM API Key（初始化 provider 时可使用）
- `LLM_BASE_URL`：LLM 网关地址
- `LLM_PROVIDER`：provider 类型（默认 `openai`）
- `LLM_MODEL`：默认模型名（默认 `gpt-4o`）
- `JWT_SECRET`：覆盖 `config.yaml` 中的 JWT 密钥
- `RULEBOOK_PATH`：规则书路径（默认 `COC_kp.md`）

## 默认数据与初始化行为

服务首次启动后会自动：

- 初始化数据库并自动迁移表结构。
- 从 `scenarios/` 目录导入剧本。
- 写入默认商店商品。
- 若数据库中没有 Provider，且设置了 `LLM_API_KEY`，会自动创建一个默认 LLM Provider。
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
  - `GET /characters/:id/inventory`
  - `POST /characters/:id/inventory`
  - `DELETE /characters/:id/inventory/:item`
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
  - `POST /sessions/:id/chat`
- 商店：
  - `GET /shop/items`
  - `POST /shop/purchase`
  - `GET /shop/transactions`
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

## 开发与测试

运行全部测试：

```bash
go test ./...
```

构建服务：

```bash
go build -o ./bin/llmcoc ./cmd/server
```

## 项目结构

```text
cmd/server            # 程序入口与嵌入前端
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

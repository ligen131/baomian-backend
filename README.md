# 抱眠 MVP 后端

抱眠 P0 演示后端，提供 APP、Anthropic Claude 和 T5 设备对接所需的 REST、WebSocket、状态机与 PostgreSQL 持久化能力。本仓库不包含 Android 代码。

## 技术栈

- Go 1.24+
- Gin
- GORM + PostgreSQL 17
- golang-migrate（嵌入式 SQL migration）
- gorilla/websocket
- Anthropic 官方 Go SDK
- `slog` JSON 日志

## P0 能力

- 今晚状态机：`WAITING_TO_LOCK → LOCKED → CONVERSATION → CHOOSING_GUIDANCE → SLEEPING → SUNRISE → AWAKE`
- 开仓异常状态 `PHONE_REMOVED` 及闭仓恢复
- 最多 3 轮睡前倾诉、提前收尾、记忆卡生成
- Claude 主适配器 + 本地 `FallbackAdapter` + 高风险固定提示
- T5 设备事件幂等、设备命令长轮询和 ACK
- 按 Demo 用户分组的 WebSocket 广播
- PostgreSQL 行锁与事务边界

## 快速开始

### 1. 配置

```bash
cp .env.example .env
```

关键配置：

| 环境变量 | 默认值 | 说明 |
|---|---|---|
| `HTTP_ADDR` | `:8080` | HTTP 监听地址 |
| `DATABASE_URL` | 本地 baomian PostgreSQL | PostgreSQL DSN |
| `DEMO_USER_ID` | `expo-user-001` | 默认演示用户 |
| `DEFAULT_DEVICE_ID` | `expo-device-001` | APP action 对应的默认设备 |
| `ANTHROPIC_API_KEY` | 空 | Anthropic API Key；与 Auth Token 均为空时自动走本地 fallback |
| `ANTHROPIC_AUTH_TOKEN` | 空 | Bearer Auth Token；与 API Key 同时设置时优先使用此项 |
| `ANTHROPIC_BASE_URL` | `https://api.anthropic.com` | Claude API 地址；测试或可信代理环境可覆盖 |
| `ANTHROPIC_MODEL` | `claude-opus-4-8` | Claude 模型 |
| `AI_TIMEOUT` | `8s` | AI 总体超时；主模型会预留约 500ms 给 fallback |
| `DEVICE_LONG_POLL_TIMEOUT` | `20s` | 设备命令长轮询默认时长，服务端最大 30 秒 |

API Key 或 Auth Token 只从环境变量读取，不会写入数据库或业务日志。请仅将 `ANTHROPIC_BASE_URL` 指向受信任的服务，因为认证凭证会随请求发送到该地址。

### 2. 使用 Docker Compose

```bash
docker compose up --build -d
curl http://localhost:8080/api/v1/health
```

Compose 会依次启动 PostgreSQL、执行 migration，再启动 API。停止服务：

```bash
docker compose down
```

如需删除本地数据库卷：

```bash
docker compose down -v
```

### 3. 使用本机 Go

先确保 PostgreSQL 已运行，然后：

```bash
go run ./cmd/migrate up
go run ./cmd/server
```

回滚首个 migration：

```bash
go run ./cmd/migrate down
```

## Claude 与降级策略

调用链：

```text
SafetyAdapter
  → ResilientAdapter
      → AnthropicAdapter
      → FallbackAdapter（主模型失败时）
```

`AnthropicAdapter` 使用官方 Go SDK 调用 `POST /v1/messages`：

- 默认模型 `claude-opus-4-8`
- adaptive thinking + `low` effort
- `output_config.format` JSON Schema 约束
- 不发送 `temperature`、`top_p` 或 `top_k`
- 本地再次校验必填文本、固定 guidance 顺序和推荐值
- 第 3 轮由服务端强制 `shouldFinalize=true`
- `fallback`、`highRisk` 最终由服务端覆盖，不能由模型决定

以下情况会自动使用本地 fallback：

- 未设置 `ANTHROPIC_API_KEY`
- 超时或网络错误
- Claude API 非成功响应或 refusal
- 空响应、非法 JSON、字段缺失或字段校验失败

安全层会在调用模型前识别高风险表达，直接返回固定求助提示。

## 身份与接口

P0 不做正式登录。APP 接口可传：

```http
X-Demo-User-Id: expo-user-001
```

省略时使用默认 Demo 用户。设备事件可在 JSON 中传 `userId`，省略时同样使用默认 Demo 用户。

主要接口：

| 方法 | 路径 | 用途 |
|---|---|---|
| GET | `/health` | 服务与数据库健康检查 |
| GET/PUT | `/api/v1/profile` | 用户设置 |
| GET | `/api/v1/tonight` | 今晚状态 |
| POST | `/api/v1/tonight/actions` | APP 演示动作 |
| POST | `/api/v1/conversations/turn` | 一轮倾诉 |
| POST | `/api/v1/conversations/finalize` | 提前收尾 |
| GET | `/api/v1/journals?limit=7` | 最近记忆卡 |
| GET | `/api/v1/memories?limit=7` | journals 别名 |
| GET | `/api/v1/ws?userId=...` | WebSocket |
| POST | `/api/v1/device/events` | T5 事件上报 |
| GET | `/api/v1/device/commands/next` | 领取命令 |
| POST | `/api/v1/device/commands/ack` | ACK 命令 |

完整契约见 [`api/openapi.yaml`](api/openapi.yaml)，可执行样例见 [`examples/http/demo.http`](examples/http/demo.http)，硬件同学可直接阅读 [`docs/hardware-integration.md`](docs/hardware-integration.md)。

## WebSocket

连接示例：

```text
ws://localhost:8080/api/v1/ws?userId=expo-user-001
```

事件统一格式：

```json
{
  "type": "tonight.updated",
  "occurredAt": "2026-07-23T22:00:00Z",
  "data": {}
}
```

可能的事件：

- `tonight.updated`
- `device.event`
- `conversation.reply`
- `journal.created`
- `error`

Hub 当前为进程内实现，适合单实例 Demo。多实例部署需增加 Redis、NATS 等 pub-sub。

## 设备网关

支持事件：

- `box_closed`
- `box_opened`
- `soft_button/short_press`
- `soft_button/long_press`
- `alarm_start`

`eventId` 有唯一约束。重复上报不会再次推进状态或创建命令，而是返回首次结果并设置 `duplicate=true`。

`GET /v1/device/commands/next?deviceId=expo-device-001&timeoutSec=20` 最长等待 30 秒；没有命令返回 HTTP 204。领取成功后命令标记为 `dispatched`，随后使用 ACK 接口标记为 `acked` 或 `failed`。

## 测试与检查

```bash
go mod tidy
gofmt -w $(find . -name '*.go')
go test ./...
go test -race ./...
go vet ./...
go build ./cmd/server ./cmd/migrate
```

AI 契约测试使用 `httptest.Server`，不会请求真实 Claude，也不依赖 API Key。PostgreSQL 集成闭环：

```bash
docker compose up --build -d
./scripts/smoke.sh
```

也可指定其他地址：

```bash
BASE_URL=http://127.0.0.1:8080 ./scripts/smoke.sh
```

Smoke 覆盖健康检查、Profile、今晚状态、倾诉、finalize、引导、日记、设备事件幂等、晨光、贪睡、起床、命令领取与 ACK。默认每次生成独立的用户和设备 ID，因此可重复运行；如显式设置 `DEMO_USER_ID` 或 `DEFAULT_DEVICE_ID`，调用方需自行保证状态隔离。

## 当前边界

- 不包含 Android 客户端。
- 不包含正式登录、支付、推送和管理后台。
- 不包含生产级设备证书、Redis/NATS 或多实例 WebSocket。
- 不保存原始音频。
- 不做心理治疗、疾病诊断、睡眠分期或医疗建议。
- 后台真实闹钟调度器和 25 分钟定时任务不在 P0 范围；P0 只提供状态、action 与设备命令协议。

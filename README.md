# 抱眠 MVP 后端

抱眠 P0+P1 演示后端，提供 Android 状态接口、T5 双向流式语音、火山引擎 ASR/TTS、Anthropic Claude、状态机、后台计时协调器与 PostgreSQL 持久化能力。本仓库不包含 Android 或 T5 固件代码。

## 技术栈

- Go 1.24+
- Gin
- GORM + PostgreSQL 17
- golang-migrate（嵌入式 SQL migration）
- gorilla/websocket
- Anthropic 官方 Go SDK（Anthropic Messages API）
- CLI Proxy OpenAI-compatible Chat Completions Adapter
- 火山引擎流式 ASR/TTS WebSocket
- `slog` JSON 日志

## P0 能力

- 今晚状态机：`WAITING_TO_LOCK → LOCKED → CONVERSATION → CHOOSING_GUIDANCE → SLEEPING → SUNRISE → AWAKE`
- 开仓异常状态 `PHONE_REMOVED` 及闭仓恢复
- 最多 3 轮睡前倾诉、提前收尾、记忆卡生成
- Claude 主适配器 + 本地 `FallbackAdapter` + 高风险固定提示
- T5 设备事件幂等、设备命令长轮询和 ACK
- 按 Demo 用户分组的 WebSocket 广播
- PostgreSQL 行锁与事务边界

## P1 新增能力

- Profile IANA 时区、提醒开关和“今晚跳过提醒”；提醒和闹钟由 Android 本地调度。
- 倾诉 20 秒静默、4 分钟硬截止、activity 上报、完整历史恢复和 `clientRequestId` 幂等。
- T5 通过独立 Voice WebSocket 流式上行 PCM；后端桥接火山引擎 ASR、现有 Claude 三轮服务和火山引擎 TTS，再向 T5 流式下发 PCM。
- Android 只负责设置、状态、设备在线和晚安日记；正式路径不录音、不 STT、不 TTS。
- 后端不持久化原始音频；开仓后有 10 分钟恢复窗口，白噪音按 10/20/30 分钟自动停止。
- 晚安卡详情、明日待办完成/取消、历史单卡与对应对话删除。
- 设备 heartbeat/online、命令 lease、attempt 和 at-least-once 重投。
- 后台 Session Coordinator 与 `/metrics`。

详细计时和可靠性语义见 [`docs/p1-backend-design.md`](docs/p1-backend-design.md)。

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
| `AI_PROVIDER` | `anthropic` | AI 协议：`anthropic` 或 `openai_compatible` |
| `ANTHROPIC_API_KEY` | 空 | API Key；与 Auth Token 均为空时自动走本地 fallback |
| `ANTHROPIC_AUTH_TOKEN` | 空 | Bearer Auth Token；与 API Key 同时设置时优先使用此项 |
| `ANTHROPIC_BASE_URL` | `https://api.anthropic.com` | AI 服务根地址；变量名为兼容旧配置而保留 |
| `ANTHROPIC_MODEL` | `claude-opus-4-8` | Claude 模型 |
| `AI_TIMEOUT` | `8s` | AI 总体超时；主模型会预留约 500ms 给 fallback |
| `VOLCENGINE_SPEECH_APP_ID` | 空 | 火山引擎 ASR 应用 App ID |
| `VOLCENGINE_SPEECH_ACCESS_TOKEN` | 空 | 火山引擎 ASR Access Token |
| `VOLCENGINE_TTS_API_KEY` | 空 | 火山引擎单向流式 TTS API Key；任一语音凭证为空时 Voice WebSocket 返回 503 |
| `VOLCENGINE_ASR_WS_URL` | `wss://openspeech.bytedance.com/api/v3/sauc/bigmodel_async` | 大模型流式 ASR V3 WebSocket |
| `VOLCENGINE_ASR_RESOURCE_ID` | `volc.bigasr.sauc.duration` | 已在应用中开通的 ASR resource ID |
| `VOLCENGINE_TTS_WS_URL` | `wss://openspeech.bytedance.com/api/v3/tts/unidirectional/stream` | 单向流式 TTS V3 WebSocket |
| `VOLCENGINE_TTS_RESOURCE_ID` | `seed-tts-2.0` | 单向流式 TTS 模型资源 ID |
| `VOLCENGINE_TTS_SPEAKER` | `zh_female_gaolengyujie_uranus_bigtts` | TTS speaker；必须对 API Key 可用 |
| `VOICE_MAX_UTTERANCE_DURATION` | `60s` | 单次长按说话上限 |
| `DEVICE_LONG_POLL_TIMEOUT` | `20s` | 设备命令长轮询默认时长，服务端最大 30 秒 |
| `CONVERSATION_SILENCE_TIMEOUT` | `20s` | 倾诉静默自动收尾时间 |
| `CONVERSATION_MAX_DURATION` | `4m` | 倾诉硬截止 |
| `PHONE_REMOVED_RESUME_WINDOW` | `10m` | 开仓恢复窗口 |
| `SESSION_COORDINATOR_INTERVAL` | `1s` | 后台到期扫描间隔 |
| `DEVICE_COMMAND_LEASE` | `30s` | 设备命令领取 lease |
| `DEVICE_COMMAND_MAX_ATTEMPTS` | `5` | 最大投递次数 |
| `EXPO_TIME_SCALE` | `1` | Demo 计时加速比例；生产保持 1 |

Claude 和火山引擎凭证只从环境变量读取，不会写入数据库或业务日志。请仅将上游 URL 指向受信任的服务，因为认证凭证会随请求发送到该地址。火山引擎 ASR App ID、ASR Access Token 或 TTS API Key 任一为空时，REST/Android 状态功能仍可用，但 T5 Voice WebSocket 会在 upgrade 前返回 503。Secret Key 不用于这两条语音 WebSocket API。

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
      → AnthropicAdapter（`AI_PROVIDER=anthropic`）
        或 OpenAICompatibleAdapter（`AI_PROVIDER=openai_compatible`）
      → FallbackAdapter（主模型失败时）
```

`AnthropicAdapter` 使用官方 Go SDK 调用 `POST /v1/messages`；`OpenAICompatibleAdapter` 专门调用 CLI Proxy 的 `POST /v1/chat/completions`。两者不会根据 URL 隐式猜测协议，由 `AI_PROVIDER` 明确选择：

- 直连 Anthropic：`AI_PROVIDER=anthropic`
- 当前 CLI Proxy：`AI_PROVIDER=openai_compatible`
- 默认模型 `claude-opus-4-8`
- Anthropic 路径使用 adaptive thinking + `low` effort + `output_config.format`
- CLI Proxy 路径使用 `response_format.type=json_schema` 与相同 JSON Schema
- 不发送 `temperature`、`top_p` 或 `top_k`
- 本地再次校验必填文本、固定 guidance 顺序和推荐值
- 第 3 轮由服务端强制 `shouldFinalize=true`
- `fallback`、`highRisk` 最终由服务端覆盖，不能由模型决定

CLI Proxy 示例：

```env
AI_PROVIDER=openai_compatible
ANTHROPIC_BASE_URL=http://127.0.0.1:38109
ANTHROPIC_AUTH_TOKEN=<仅存放在 .env 或秘密管理系统中>
ANTHROPIC_MODEL=claude-opus-4-8
```

凭证只写入 `Authorization: Bearer` 请求头，不会进入请求 JSON、业务日志或错误日志。`ANTHROPIC_BASE_URL` 必须指向可信服务。

以下情况会自动使用本地 fallback：

- API Key 与 Auth Token 均未设置
- 超时或网络错误
- 主 AI 服务非成功响应或 refusal
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
| GET | `/api/v1/conversations/tonight` | 完整对话恢复 |
| POST | `/api/v1/conversations/activity` | 延长静默截止 |
| POST | `/api/v1/conversations/turn` | Debug 文字入口；T5 ASR 在后端内部复用同一服务 |
| POST | `/api/v1/conversations/finalize` | 提前收尾 |
| GET | `/api/v1/journals?limit=7` | 最近记忆卡 |
| GET/PATCH/DELETE | `/api/v1/journals/{id}` | 单卡详情、待办状态和删除 |
| GET | `/api/v1/memories?limit=7` | journals 别名 |
| GET | `/api/v1/ws?userId=...` | Android JSON 状态 WebSocket |
| GET | `/api/v1/device/voice?deviceId=...&userId=...` | T5 双向 JSON 控制 + binary PCM WebSocket |
| POST | `/api/v1/device/events` | T5 持久设备事件上报 |
| POST | `/api/v1/device/heartbeat` | T5 在线快照 |
| GET | `/api/v1/devices/{deviceId}/status` | APP 查询设备在线状态 |
| GET | `/api/v1/device/commands/next` | lease 领取命令 |
| POST | `/api/v1/device/commands/ack` | 幂等 ACK 命令 |
| GET | `/metrics` | Prometheus 文本指标（建议限制公网） |

完整契约见 [`api/openapi.yaml`](api/openapi.yaml)，可执行样例见 [`examples/http/demo.http`](examples/http/demo.http)，硬件同学可直接阅读 [`docs/hardware-integration.md`](docs/hardware-integration.md)。

## WebSocket

Android 状态连接：

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
- `journal.updated`
- `journal.deleted`
- `device.status`
- `error`

Hub 当前为进程内实现，适合单实例 Demo。多实例部署需增加 Redis、NATS 等 pub-sub。

T5 使用独立语音连接：

```text
ws://localhost:8080/api/v1/device/voice?deviceId=expo-device-001&userId=expo-user-001
```

控制消息是 JSON text message；音频是 PCM signed 16-bit little-endian、24000 Hz、mono、20 ms、960-byte binary message。后端将 10 个 T5 帧聚合成约 200 ms 后发送给火山引擎 ASR；火山引擎 TTS 返回的 24 kHz PCM 会重新切成 960-byte 帧下发 T5。Claude 仍由现有 `ConversationService` 调用。`VoiceSessionService` 是确定性的 Go 协调器，不是 AI Agent。完整协议见 [`docs/hardware-integration.md`](docs/hardware-integration.md) 和 [`docs/voice-streaming-design.md`](docs/voice-streaming-design.md)。

## 设备网关

支持事件：

- `box_closed`
- `box_opened`
- `soft_button/short_press`
- `soft_button/long_press`
- `alarm_start`

`eventId` 有唯一约束。重复上报不会再次推进状态或创建命令，而是返回首次结果并设置 `duplicate=true`。

`GET /api/v1/device/commands/next?deviceId=expo-device-001&timeoutSec=20` 最长等待 30 秒；没有命令返回 HTTP 204。领取响应包含 `attempt` 和 `leaseExpiresAt`；lease 到期未 ACK 会重投，因此固件必须按 command ID 去重。达到最大次数后命令标记 `failed`。

## 数据库备份与恢复

```bash
BACKUP_DIR=.local/backups ./scripts/backup.sh
TARGET_DATABASE_URL='postgres://...' ./scripts/restore.sh .local/backups/<backup>.dump
```

备份使用 `pg_dump -Fc`。恢复会覆盖目标库中的对象，必须显式提供 `TARGET_DATABASE_URL`，并在执行前确认目标环境。建议测试环境每日保留 7 天、每月至少演练一次恢复。

## 测试与检查

```bash
go mod tidy
gofmt -w cmd internal
go test ./...
go build ./...
```

AI 与火山引擎语音 adapter 测试使用本地 `httptest.Server`/mock WebSocket，不会请求真实 Claude 或火山引擎，也不依赖真实凭证。PostgreSQL 集成闭环：

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
- 睡前提醒和起床闹钟由 Android 本地调度，不使用 FCM；T5 本地 RTC 仍负责断网闹钟兜底。
- 当前 `/metrics` 为单进程内存指标；多实例聚合需外部 Prometheus。

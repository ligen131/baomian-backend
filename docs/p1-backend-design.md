# 抱眠 P1 后端设计

> 版本：P1 Voice Streaming / 2026-07-24  
> API Base URL：`https://bm.lg.gl/api/v1`

## 1. 范围

P1 在 P0 睡前闭环上增加真实使用所需的计时、恢复、幂等、日记管理、设备可靠性和 T5 实时语音：

- Profile 保存 IANA 时区以及睡前提醒、起床闹钟开关。
- Android 使用本地 AlarmManager 调度提醒，不使用 FCM。
- 倾诉最长 4 分钟；20 秒没有活动自动收尾；最多 3 次有效用户发言。
- T5 通过独立 WebSocket 流式上传和播放 PCM；后端分别桥接火山引擎 ASR/TTS。
- AI 继续使用现有 Claude structured-output 单次调用和本地 fallback，不使用火山引擎智能体。
- 后端不持久化原始音频，不在日志中记录音频 payload 或完整对话文本。
- 开仓后有 10 分钟恢复窗口。
- 晚安卡支持待办完成/取消、单卡删除。
- T5 heartbeat、在线状态、命令 lease 和 at-least-once 重投。
- 进程内 Prometheus 文本指标 `/metrics`。

仍不包含正式鉴权、设备证书、FCM、支付、全账号删除、多实例 WebSocket pub-sub、Opus 或服务端 VAD。

## 2. 架构

```text
T5
  <-> GET /api/v1/device/voice (JSON control + binary PCM)
        -> Volcengine streaming ASR
        -> VoiceSessionService
             -> existing ConversationService
                  -> Claude Adapter -> Fallback
             -> existing TonightService
        -> Volcengine streaming TTS
  <-> REST device events / heartbeat / reliable command queue

Android
  <-> REST + GET /api/v1/ws
        -> Profile / Tonight / Device Status / Journal

PostgreSQL Store <- Service / Coordinator
```

Controller 只绑定参数和管理连接；状态迁移、幂等与事务规则在 Service。`VoiceSessionService` 是确定性的 Go 协调服务，不是 AI Agent。火山引擎只提供语音识别和合成；Claude 继续决定回复及晚安卡结构。

详细设备语音协议以 [`voice-streaming-design.md`](voice-streaming-design.md) 和 [`hardware-integration.md`](hardware-integration.md) 为准。

## 3. T5 流式语音

设备连接：

```text
wss://bm.lg.gl/api/v1/device/voice?deviceId=<deviceId>&userId=<userId>
```

音频固定为 PCM signed 16-bit little-endian、24000 Hz、mono、20 ms、每帧 960 bytes。控制消息使用 UTF-8 JSON text message；PCM 使用 binary message。

正式链路：

```text
T5 PCM
  -> Volcengine ASR
  -> final transcript
  -> ConversationService.Turn(inputMode=voice, clientRequestId=turnId)
  -> Claude result persisted
  -> Volcengine TTS
  -> binary PCM to T5
```

关键规则：

- 长按发送 `input.start`，松开发送 `input.end`；不使用服务端 VAD。
- 单次长按最长 60 秒，一晚会话仍受 4 分钟硬截止。
- `turnId` 直接复用已有 `clientRequestId` 幂等能力。
- 首次 `session.start` 播放固定短开场白，不计入三轮。
- 三轮指 3 次有效用户发言，每次有一次 Claude 回复；第三轮强制生成晚安卡。
- 第三轮回复后自动选引导：T5 内置 `rain`/`brown_noise`；`breathing_46` 经火山引擎 TTS；`silence` 不播放。
- TTS 播放中长按或短按会同时取消上游 TTS，并要求 T5 清空本地播放缓冲。
- 未配置火山引擎 ASR App ID、ASR Access Token 或 TTS API Key 时，设备语音 upgrade 前返回 HTTP 503；其他 REST 功能继续可用。
- T5 每 20 ms 上行 960 bytes；后端聚合成约 200 ms 后发送火山引擎 ASR，TTS PCM 再切成 960-byte 帧下发。

后端不增加音频数据库字段或 migration。断线发生在 `input.end` 前时丢弃半段音频；已持久化 turn 由 `turnId` 防重复。

## 4. 时间语义

### 4.1 今晚日期

服务端读取 Profile 后，通过 `time.LoadLocation(profile.timeZone)` 按用户时区计算今晚日期。数据库中的 `night_sessions.date` 保存纯日期，不直接使用 UTC 日期。

### 4.2 倾诉

进入 `CONVERSATION` 时设置：

- `conversationStartedAt = now`
- `conversationLastActivityAt = now`
- `conversationSilenceDeadlineAt = now + 20s`
- `conversationHardDeadlineAt = now + 4m`

`POST /conversations/activity` 是 Debug 文字客户端兼容入口。T5 语音流由 VoiceSessionService 在实际活动时推进同一服务端计时语义。AI 调用前设置 processing lease；Coordinator 仅在 lease 不存在或到期后自动收尾。

`finalizeReason`：`manual`、`turn_limit`、`silence`、`max_duration`；所有收尾入口都只允许已完成 3 轮的会话，`manual` 仅用于 Debug 收尾。

### 4.3 开仓恢复

开仓记录 `phoneRemovedAt` 和 `resumeDeadlineAt=now+10m`，暂停当前音频。期限内闭仓恢复原阶段；期限后 Coordinator 清除恢复阶段、设置 `pausedForTonight=true` 并排队 `audio.stop`。

### 4.4 白噪音

`rain` 与 `brown_noise` 使用 Profile 的 10/20/30 分钟时长。实时语音连接存在时发送 `guidance.start` 给 T5 播放内置资源；一般离线控制仍可通过 command queue 发送 `audio.play`。Session 保存 `audioEndsAt`，到期后停止。

## 5. Conversation 可靠性

`POST /conversations/turn` 保留为 Debug 文字入口：

```json
{
  "text": "调试输入文字",
  "inputMode": "text",
  "clientRequestId": "客户端生成且重试复用的 ID"
}
```

T5 语音路径由后端内部以 `inputMode=voice` 和 `clientRequestId=turnId` 调用同一服务。

- 相同 `clientRequestId` 已完成时返回已有 assistant reply。
- 相同请求正在 processing lease 内时返回 `409 request_in_progress`。
- lease 过期且 user turn 已存在时可恢复处理，不再增加轮数。
- AI 主调用失败时使用现有本地 fallback；失败后释放 lease。
- `GET /conversations/tonight` 用于 Android 恢复轮数、历史和 processing 状态。

## 6. 日记与隐私

- `GET /journals/{id}` 同时按 `id + user_id` 查询。
- `PATCH /journals/{id}` 维护 `tomorrowTaskCompleted` 和完成时间。
- `DELETE /journals/{id}` 在事务中删除卡片与对应 conversation turns，并清空 `latestAIDraft`。
- 正在进行的 Session 返回 `409 journal_not_deletable`。
- 保留 NightSession、设备事件与命令。
- 原始音频不落盘、不入数据库、不进入日志和 crash report。

## 7. 设备可靠性

T5 每 30–60 秒调用 `POST /device/heartbeat`；设备事件也刷新 `lastSeenAt`。`GET /devices/{deviceId}/status` 在最近 90 秒内视为 online。

非实时设备命令继续使用 lease 队列：

1. `pending` 或 lease 已过期的 `dispatched` 命令可领取。
2. 每次领取 `attempt + 1`，返回 `leaseExpiresAt`。
3. 固件按 command `id` 本地去重并幂等 ACK。
4. 达到最大投递次数后标记 `failed`。

实时对话音频不经过命令队列，避免长轮询增加延迟。晨光、离线控制和一般设备控制继续使用命令队列。

## 8. 两条 WebSocket

Android 状态 WebSocket：

```text
GET /api/v1/ws?userId=<userId>
```

只发送 JSON envelope：`tonight.updated`、`conversation.reply`、`journal.created/updated/deleted`、`device.event`、`device.status`。事件是提示通道；重连后通过 REST 对账。

T5 语音 WebSocket：

```text
GET /api/v1/device/voice?deviceId=<deviceId>&userId=<userId>
```

双向发送 JSON control 和 binary PCM。不得与 Android 状态 WebSocket 混用。

## 9. 可观测性与安全

- `/api/v1/health`：数据库 readiness。
- `/metrics`：HTTP 和状态 WebSocket 基础指标；生产反向代理应限制公网访问。
- 结构化日志只记录 request/turn/playback ID、稳定错误码、火山引擎 request/connect ID 和耗时。
- 不记录对话全文、转写全文、PCM 内容或凭证。
- `VOLCENGINE_SPEECH_APP_ID`、`VOLCENGINE_SPEECH_ACCESS_TOKEN` 和 Claude 凭证只从环境变量读取。

## 10. 配置

```env
VOLCENGINE_SPEECH_APP_ID=
VOLCENGINE_SPEECH_ACCESS_TOKEN=
VOLCENGINE_TTS_API_KEY=
VOLCENGINE_ASR_WS_URL=wss://openspeech.bytedance.com/api/v3/sauc/bigmodel_async
VOLCENGINE_ASR_RESOURCE_ID=volc.bigasr.sauc.duration
VOLCENGINE_TTS_WS_URL=wss://openspeech.bytedance.com/api/v3/tts/unidirectional/stream
VOLCENGINE_TTS_RESOURCE_ID=seed-tts-2.0
VOLCENGINE_TTS_SPEAKER=zh_female_gaolengyujie_uranus_bigtts
VOLCENGINE_ASR_TIMEOUT=20s
VOLCENGINE_TTS_FIRST_FRAME_TIMEOUT=10s
VOLCENGINE_TTS_TOTAL_TIMEOUT=45s
VOICE_MAX_UTTERANCE_DURATION=60s
```

真实 Token 只能写入未提交的 `.env` 或秘密管理系统。

## 11. 数据迁移与部署

P1 数据仍使用 `000002_p1` 和 `000003_conversation_result`。T5 语音功能不增加 migration。

部署顺序：构建和测试，备份现有 `.env` 与服务二进制，配置火山引擎 ASR App ID/Access Token、TTS API Key、resource ID 与 speaker，重启 API，再执行最小真实语音 smoke。T5 实机声音质量仍需单独验收。

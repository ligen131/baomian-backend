# 抱眠硬件（T5）与后端对接文档

> 文档版本：P1 Voice Streaming / 2026-07-24
> 线上 API Base URL：`https://bm.lg.gl/api/v1`

## 1. 对接总览

T5 使用三类后端链路：

1. **实时语音 WebSocket**：长按说话、PCM 上行、TTS PCM 下行、边收边播、打断和三轮完成。
2. **HTTPS 设备接口**：heartbeat、闭仓、开仓和晨光事件。
3. **HTTPS 命令队列**：晨光、闹钟及非实时设备控制，lease + at-least-once 投递。

```text
T5 PCM -> Device Voice WebSocket -> Baomian -> Volcengine ASR
                                         -> Claude
T5 PCM <- Device Voice WebSocket <- Baomian <- Volcengine TTS

T5 -> heartbeat/events -> state machine -> Android state WebSocket
T5 <- command next/ack <- reliable command queue
```

T5 不连接火山引擎、不保存火山引擎 Token、不解析 Claude 响应，也不缓存完整录音。

## 2. 标识与认证边界

| 字段 | 示例 | 说明 |
|---|---|---|
| `deviceId` | `expo-device-001` | 设备唯一 ID，重启后不变 |
| `userId` | `expo-user-001` | 当前 Demo 绑定用户 |
| `firmwareVersion` | `1.1.0` | heartbeat 上报 |

当前 MVP 仍是 Demo User，尚无正式设备证书。所有公网连接必须使用 HTTPS/WSS；不要在固件内加入火山引擎或 Claude 凭证。

## 3. 实时语音 WebSocket

### 3.1 连接

```text
wss://bm.lg.gl/api/v1/device/voice?deviceId=<URL encoded deviceId>&userId=<URL encoded userId>
```

- `deviceId` 必填；`userId` 可省略并使用后端默认 Demo User，但联调建议显式传入。
- 同一 `deviceId` 只允许一条活跃连接；新连接替换旧连接。
- 使用标准 WebSocket ping/pong。固件必须回复 ping，并在断线后指数退避重连。
- 建议重连：`1s -> 2s -> 4s -> 8s -> 15s -> 15s...`。
- 后端未配置火山引擎 ASR App ID、ASR Access Token 或 TTS API Key 时，upgrade 前返回 HTTP 503 `speech_not_configured`。

| WebSocket message type | 内容 |
|---|---|
| Text | UTF-8 JSON control event |
| Binary | raw PCM audio，不含 header |
| Ping/Pong | 标准连接保活 |

火山引擎使用自己的 V3 binary protocol；T5 不收发或解析任何上游供应商事件。后端会把 10 个 20 ms T5 帧聚合成约 200 ms 的 ASR 上游包，T5 固件仍严格发送 960-byte 帧。

### 3.2 固定音频格式

| 参数 | 值 |
|---|---|
| 编码 | PCM signed linear |
| 字节序 | little-endian |
| 采样率 | 24000 Hz |
| 位深 | 16 bit |
| 声道 | mono |
| 帧长 | 20 ms |
| 每条 binary message | 960 bytes |

计算：`24000 * 0.02 * 2 = 960 bytes`。不发送 WAV header，不发送半帧；最后一帧不足时在设备侧补零。

建议播放环形缓冲 100–300 ms，即 5–15 帧。缓冲必须有界，禁止缓存完整回复。

## 4. 语音控制协议

每个 T5 上行控制事件包含 `type`、UUID `eventId`；轮次事件还包含 UUID `turnId`。

### 4.1 建连就绪（后端下行）

```json
{
  "type":"session.ready",
  "phase":"LOCKED",
  "completedTurns":0,
  "audio":{"codec":"pcm","sampleRate":24000,"bitDepth":16,"channels":1,"frameMs":20}
}
```

固件必须校验 audio 参数。不支持返回格式时停止语音流程并上报诊断，不能按错误采样率播放。

### 4.2 开始今晚会话（T5 上行）

闭仓 REST 事件成功并收到 `session.ready` 后发送：

```json
{"type":"session.start","eventId":"3db25df5-5f70-48f4-97d7-e823aa1b8bce"}
```

从 `LOCKED` 首次启动时，后端进入 `CONVERSATION` 并播放短开场白。开场白不计入三轮。

### 4.3 播放（后端下行）

```json
{
  "type":"playback.start",
  "playbackId":"ca7d3ee8-d79c-4df0-b4e3-ceee2c156ceb",
  "kind":"opening",
  "text":"手机已经安放好了。今晚有什么想和眠眠说的吗？"
}
```

之后连续收到 960-byte binary PCM，T5 边收边播。完成时收到：

```json
{"type":"playback.end","playbackId":"ca7d3ee8-d79c-4df0-b4e3-ceee2c156ceb","reason":"completed"}
```

`kind`：`opening`、`reply`、`guidance`。`reason`：`completed`、`interrupted`、`upstream_error`。

### 4.4 长按开始说话（T5 上行）

按键按下并达到固件长按阈值后：

1. 如果正在播放，立即停止播放器并清空环形缓冲。
2. 发送 `playback.stop`。
3. 生成新的 `turnId`。
4. 发送 `input.start`。
5. 等待 `input.accepted` 后实时发送 binary PCM；允许极小预录缓冲避免吞掉开头，但不得缓存完整录音。

```json
{"type":"playback.stop","eventId":"b8391c5d-7746-4705-bb74-4e145749513e"}
```

```json
{
  "type":"input.start",
  "eventId":"ff4af57c-64ef-4e29-a6ed-a5f0bf445247",
  "turnId":"8ea4105c-bc68-46de-b625-c49281c6688f"
}
```

后端接受：

```json
{"type":"input.accepted","turnId":"8ea4105c-bc68-46de-b625-c49281c6688f"}
```

每次长按最长 60 秒。固件到 60 秒时必须自动结束采集并发送 `input.end`。

### 4.5 松开结束说话（T5 上行）

```json
{
  "type":"input.end",
  "eventId":"53bd7e56-2858-499e-a04d-2182ba299178",
  "turnId":"8ea4105c-bc68-46de-b625-c49281c6688f"
}
```

`turnId` 必须与 `input.start` 相同。随后后端可能发送：

```json
{"type":"transcript.final","turnId":"8ea4105c-bc68-46de-b625-c49281c6688f","text":"今天工作有点累"}
```

```json
{"type":"thinking","turnId":"8ea4105c-bc68-46de-b625-c49281c6688f"}
```

`transcript.final.text` 仅供联调，正式固件可忽略，不得持久化。`thinking` 时允许播放一次很短的本地提示音，不得循环。

Claude 回复开始：

```json
{
  "type":"playback.start",
  "playbackId":"7d3cff76-dc99-44ea-b0ae-064d13333a50",
  "kind":"reply",
  "turn":1,
  "turnId":"8ea4105c-bc68-46de-b625-c49281c6688f",
  "text":"辛苦了，今晚先不用急着把所有事情解决。"
}
```

### 4.6 短按静音

短按时固件必须先本地立即停止并清空播放缓冲，然后发送：

```json
{"type":"playback.stop","eventId":"587c89f0-97b4-4892-b5ab-564977b947fe"}
```

不要等待后端 ACK 才停止声音。

### 4.7 后端要求停止

```json
{
  "type":"playback.stop",
  "playbackId":"7d3cff76-dc99-44ea-b0ae-064d13333a50",
  "reason":"user_interrupt"
}
```

收到后立即清空缓冲。

### 4.8 三轮完成

三轮表示 3 次有效用户发言，不含开场白。第三轮晚安日记持久化后收到：

```json
{
  "type":"conversation.completed",
  "completedTurns":3,
  "journalId":"6d1e52fc-46ec-447c-89c6-21a021fb1fb9",
  "guidance":"breathing_46"
}
```

随后自动开始引导。

### 4.9 白噪音（T5 内置）

```json
{"type":"guidance.start","guidance":"rain","source":"device","durationMinutes":20}
```

- `rain`、`brown_noise` 必须内置为可无缝循环资源。
- 按 `durationMinutes` 在本地停止。
- 短按立即停止。
- `breathing_46` 不使用内置资源，由后端按 `kind=guidance` 流式发送 PCM。
- `silence` 不发送音频。

### 4.10 错误

```json
{
  "type":"error",
  "code":"asr_unavailable",
  "message":"语音识别暂时不可用，请重新说一次",
  "retryable":true,
  "turnId":"8ea4105c-bc68-46de-b625-c49281c6688f"
}
```

| code | 是否重试 | 固件行为 |
|---|---:|---|
| `speech_not_configured` | 否 | 停止语音流程并上报配置故障 |
| `invalid_phase` | 否 | 刷新连接或等待真实状态变化 |
| `invalid_event` | 否 | 记录协议错误，修复固件 |
| `invalid_audio_frame` | 是 | 检查必须为 960 bytes |
| `turn_in_progress` | 是 | 不并发开始第二轮 |
| `turn_too_long` | 是 | 结束采集，提示重新简短表达 |
| `conversation_limit` | 否 | 等待引导 |
| `asr_unavailable` | 是 | 用户重新说一次 |
| `empty_transcript` | 是 | 用户重新说一次 |
| `ai_unavailable` | 是 | 保留连接，稍后重试同一 `turnId` |
| `tts_unavailable` | 是 | 不阻塞晚安日记，允许后续流程 |
| `device_too_slow` | 是 | 缩短本地处理路径并重连 |

## 5. 断线与幂等

- `input.end` 前断线：后端丢弃本轮；重连后使用新 `turnId` 重说。
- `input.end` 后超时且结果不确定：重连后可复用相同 `turnId`；后端以 `clientRequestId` 防止重复增加轮数。
- `session.ready.completedTurns` 和 `phase` 来自数据库。
- 后端重启不恢复半段 PCM；已经持久化的 turn 不重复调用 Claude。
- 每个控制动作生成唯一 `eventId`；同一动作网络重试复用原 ID。

## 6. Heartbeat

每 30–60 秒及网络恢复后调用：

```http
POST https://bm.lg.gl/api/v1/device/heartbeat
Content-Type: application/json

{
  "deviceId":"expo-device-001",
  "userId":"expo-user-001",
  "firmwareVersion":"1.1.0",
  "capabilities":{
    "voiceWebSocket":true,
    "pcm24000Mono":true,
    "audioPlayback":true,
    "sunrise":true,
    "builtInGuidance":["rain","brown_noise"]
  },
  "status":{"boxClosed":true,"audioPlaying":false},
  "localTime":"2026-07-24T22:30:00+08:00"
}
```

最近 90 秒收到 heartbeat 或设备事件时，后端认为设备 online。

## 7. 持久设备事件

```http
POST https://bm.lg.gl/api/v1/device/events
Content-Type: application/json

{
  "eventId":"2f83458a-caa1-4ef0-a78f-d3d2810254de",
  "deviceId":"expo-device-001",
  "userId":"expo-user-001",
  "type":"box_closed",
  "payload":{},
  "occurredAt":"2026-07-24T14:30:00Z"
}
```

| type | 场景 | 行为 |
|---|---|---|
| `box_closed` | 仓盖稳定关闭 | 进入或恢复 `LOCKED` 等阶段；随后连接 Voice WebSocket |
| `box_opened` | 仓盖稳定打开 | 进入 `PHONE_REMOVED`，停止实时播放 |
| `soft_button/short_press` | 非语音连接或晨光兼容事件 | 晨光中贪睡；其他阶段停止音频 |
| `soft_button/long_press` | `SUNRISE` | 标记起床并产生 `alarm.stop` |
| `alarm_start` | 本地 RTC 到点 | 进入 `SUNRISE` |

重要：

- `CONVERSATION` 中长按说话不用 REST `soft_button/long_press`，必须使用 Voice WebSocket 的 `input.start`/`input.end`。
- `SUNRISE` 中长按仍上报 REST `soft_button/long_press`。
- 固件必须根据本地模式和 `session.ready.phase` 区分语义。
- 仓盖去抖建议 300–500 ms；同一次物理事件重试复用 `eventId`。
- 事件响应中的 `commands` 不直接执行；统一通过命令队列领取，避免重复执行。

## 8. 命令队列

领取：

```http
GET https://bm.lg.gl/api/v1/device/commands/next?deviceId=expo-device-001&timeoutSec=20
```

- 200：一条命令。
- 204：正常空结果，立即发起下一次轮询。
- 同一设备只能有一个长轮询。
- `leaseExpiresAt` 前未 ACK 会重投，`attempt` 递增，默认最多 5 次。
- 固件在 NVS/Flash 保存有限大小的已完成 command ID；重复命令不重复产生副作用，但仍回复 ACK。

ACK：

```http
POST https://bm.lg.gl/api/v1/device/commands/ack
Content-Type: application/json

{
  "deviceId":"expo-device-001",
  "commandId":"9b5a6fea-d00a-4fb7-b35a-70a0e23b40ee",
  "success":true,
  "payload":{"firmwareVersion":"1.1.0","durationMs":138}
}
```

常见命令：`audio.confirm`、`audio.play`、`audio.pause`、`audio.stop`、`led.off`、`sunrise.start`、`alarm.snooze`、`alarm.stop`。实时 Claude 回复和呼吸引导不经过命令队列。

## 9. 完整睡前流程

1. T5 heartbeat。
2. 用户放入手机并闭仓。
3. T5 POST `box_closed`。
4. T5 建立 Device Voice WebSocket。
5. 收到 `session.ready` 后发送 `session.start`。
6. T5 边收边播开场白。
7. 用户长按，T5 发送 `input.start` 和 PCM；松开发送 `input.end`。
8. T5 收到 Claude reply PCM 并边收边播。
9. 重复至 3 次有效用户发言。
10. 收到 `conversation.completed`。
11. 收到 `guidance.start` 播放内置白噪音，或接收 `kind=guidance` 的呼吸 PCM。
12. 短按可随时本地停止声音。
13. 开仓时 POST `box_opened` 并断开或暂停语音连接。

## 10. 晨光流程

1. 本地 RTC 到点，POST `alarm_start`。
2. 领取并 ACK `sunrise.start`。
3. 短按：POST `soft_button/short_press`，领取 `alarm.snooze`，本地安排 5 分钟。
4. 长按：POST `soft_button/long_press`，领取 `alarm.stop`。
5. 断网时本地闹钟仍必须可用。

## 11. 固件状态建议

持久化：

```text
device_id
bound_user_id
firmware_version
pending_event_queue[]
completed_command_ids[]
pending_ack
local_alarm/snooze_state
```

内存：

```text
voice_ws_state
server_phase
current_turn_id
current_playback_id
capture_ring_buffer
playback_ring_buffer
input_active
audio_playing
box_state
network_backoff
```

不得持久化完整录音、`transcript.final.text`、Claude 回复全文、火山引擎 App ID/Access Token 或 Claude Token。

## 12. 联调验收清单

- [ ] `deviceId` 重启后不变，Android 和 T5 使用相同 `userId`。
- [ ] PCM 采集和播放均为 24kHz/16-bit/mono/little-endian。
- [ ] 每条上行 binary message 恰好 960 bytes。
- [ ] 播放缓冲为 100–300 ms 且有界。
- [ ] 长按开始、松开结束；单次 60 秒自动结束。
- [ ] 短按无需等待网络即可立即静音。
- [ ] 播放中长按能清空旧音频并开始新输入。
- [ ] 断线重连并正确处理 `session.ready`。
- [ ] 相同 `turnId` 重试不会增加两轮。
- [ ] 三轮后收到 `conversation.completed`。
- [ ] `rain`、`brown_noise` 内置且按时长停止。
- [ ] 呼吸引导可以边收边播。
- [ ] REST 事件幂等；command 按 ID 去重并 ACK。
- [ ] 晨光模式和 Conversation 模式的长按语义正确区分。
- [ ] 日志中无音频、转写全文和凭证。

机器可读 REST/upgrade 契约见 [`api/openapi.yaml`](../api/openapi.yaml)；完整语音设计见 [`voice-streaming-design.md`](voice-streaming-design.md)。若摘要冲突，以语音设计文档中的冻结协议为准。

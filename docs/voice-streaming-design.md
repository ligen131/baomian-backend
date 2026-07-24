# T5 流式语音对话设计

> 状态：已冻结设计
> 日期：2026-07-24
> 适用范围：抱眠 MVP 的 T5 语音对话、火山引擎流式 ASR/TTS、Claude 三轮对话及晚安引导

## 1. 目标与边界

正式产品流程是用户与 T5 抱枕对话，而不是与 Android 对话：

```text
手机锁入锁盒
→ T5 播放温柔开场白
→ 用户与 T5 完成 3 轮自然对话
→ 播放白噪音或呼吸引导
→ Android 展示晚安日记
```

本设计遵循以下边界：

- T5 内存有限，不缓存完整录音，仅保留采集和播放所需的小型环形缓冲区。
- T5 只连接抱眠后端，不直接连接火山引擎，不持有火山引擎 Token。
- 后端使用火山引擎流式 ASR 和流式 TTS；AI 对话继续使用现有 Claude structured output，不切换为火山引擎智能体。
- Android 不参与正式流程的录音、STT 或 TTS，只负责设置、状态和晚安日记展示。
- 后端不持久化原始音频，不在日志中记录原始音频、完整对话文本或上游凭证。
- 第一版使用 push-to-talk：长按开始说话，松开结束；不引入服务端 VAD。
- 一晚最多 3 次有效用户发言，每次用户发言对应一次 Claude 回复。

## 2. 选定架构

```text
T5
  ↕ 一条抱眠 Device Voice WebSocket
Baomian DeviceVoiceController
  ↕
VoiceSessionService
  ├── VolcASRClient ── wss://openspeech.bytedance.com/api/v3/sauc/bigmodel_async
  ├── ConversationService ── existing Claude Adapter
  └── VolcTTSClient ── wss://openspeech.bytedance.com/api/v3/tts/unidirectional/stream

Android
  ↕ REST + existing /api/v1/ws
Profile / Tonight / Device Status / Journal
```

采用“单一设备语音 WebSocket + 后端分别桥接火山引擎 ASR/TTS”的原因：

- T5 只维护一条业务连接，固件和内存负担最低。
- 火山引擎 Token 只保存在后端。
- T5 与后端使用二进制 PCM，避免 Base64 增加约 33% 的设备侧传输和处理成本。
- 现有 `ConversationService` 继续负责三轮规则、Claude、幂等、持久化和晚安卡，不复制业务逻辑。
- 火山引擎仅提供语音能力，不改变 Claude 对话质量和 structured output 契约。

现有 `/api/v1/ws` 继续只承载 Android JSON 状态事件。设备语音使用独立路由，禁止在两条通道之间混发消息。

## 3. 音频格式

T5 上行和后端下行统一使用：

| 参数 | 值 |
|---|---|
| 编码 | PCM signed linear |
| 字节序 | little-endian |
| 采样率 | 24000 Hz |
| 位深 | 16 bit |
| 声道 | 1（mono） |
| 帧长 | 20 ms |
| 每帧大小 | 960 bytes |

计算方式：`24000 × 0.02 × 2 × 1 = 960 bytes`。

约束：

- WebSocket 二进制消息只包含 PCM payload，不包含 WAV header 或自定义包头。
- 正常采集期间，每条二进制消息必须为 960 bytes。
- 最后一帧也必须补齐至 960 bytes；T5 不发送半帧。
- 每方向原始音频带宽约为 48 KB/s，不含 WebSocket/TLS 开销。
- T5 播放端使用有界环形缓冲，建议缓存 100–300 ms，禁止缓存完整回复。

## 4. 连接与会话

连接地址：

```text
GET wss://bm.lg.gl/api/v1/device/voice?deviceId=<deviceId>&userId=<userId>
```

MVP 延续 Demo User 身份模型；`userId` 和 `deviceId` 不是正式鉴权凭证。生产化正式鉴权不属于本次范围。

连接规则：

- 同一 `deviceId` 只允许一条活跃语音连接；新连接建立后关闭旧连接。
- WebSocket 使用标准 ping/pong 保活，不额外定义业务心跳。
- 所有业务控制事件使用 WebSocket text message，UTF-8 JSON 编码。
- 所有 PCM 使用 WebSocket binary message。
- 后端只允许一个写协程向 T5 连接写入消息，保证控制事件与音频帧顺序。
- 连接建立后，后端立即发送 `session.ready`。

连接就绪示例：

```json
{
  "type": "session.ready",
  "phase": "LOCKED",
  "completedTurns": 0,
  "audio": {
    "codec": "pcm",
    "sampleRate": 24000,
    "bitDepth": 16,
    "channels": 1,
    "frameMs": 20
  }
}
```

`session.ready` 必须反映数据库中的持久 phase 和已完成轮数，而不是只反映当前进程内状态。音频采集、播放进度和开场白是否播放属于瞬时状态，不跨进程恢复；设备重连后可据此判断是否继续对话。

## 5. T5 上行协议

所有控制事件都包含：

- `type`：事件类型。
- `eventId`：T5 生成的 UUID，用于控制事件去重。
- `turnId`：一次用户发言的 UUID；只在轮次相关事件中存在。

### 5.1 启动今晚语音会话

闭仓事件成功后，T5 建立语音连接并发送：

```json
{
  "type": "session.start",
  "eventId": "3db25df5-5f70-48f4-97d7-e823aa1b8bce"
}
```

后端仅在 `LOCKED` 或可恢复的 `CONVERSATION` 接受该事件。当前 WebSocket 连接第一次从 `LOCKED` 启动时，后端切换到 `CONVERSATION` 并播放开场白；若数据库已有至少一轮对话，重连时不再播放开场白。若进程在第一轮前重启，由于本次不新增持久字段，短开场白可能重播一次，T5 可通过长按立即打断。

### 5.2 长按开始说话

```json
{
  "type": "input.start",
  "eventId": "ff4af57c-64ef-4e29-a6ed-a5f0bf445247",
  "turnId": "8ea4105c-bc68-46de-b625-c49281c6688f"
}
```

后端接受后返回：

```json
{
  "type": "input.accepted",
  "turnId": "8ea4105c-bc68-46de-b625-c49281c6688f"
}
```

随后 T5 每 20 ms 发送一条 960-byte binary message。

如果 TTS 正在播放，T5 必须先本地停止播放并清空缓冲，再发送 `playback.stop` 和 `input.start`，实现用户打断。

### 5.3 松开结束说话

```json
{
  "type": "input.end",
  "eventId": "53bd7e56-2858-499e-a04d-2182ba299178",
  "turnId": "8ea4105c-bc68-46de-b625-c49281c6688f"
}
```

后端向火山引擎提交本轮音频并等待最终转写。最终转写事件仅用于联调，正式 T5 固件可以忽略其文字：

```json
{
  "type": "transcript.final",
  "turnId": "8ea4105c-bc68-46de-b625-c49281c6688f",
  "text": "今天工作有点累"
}
```

ASR 完成、Claude 生成期间发送：

```json
{
  "type": "thinking",
  "turnId": "8ea4105c-bc68-46de-b625-c49281c6688f"
}
```

T5 可以播放一次很短的本地提示音，不得循环播放长提示音。

### 5.4 短按静音或停止播放

```json
{
  "type": "playback.stop",
  "eventId": "b8391c5d-7746-4705-bb74-4e145749513e"
}
```

短按发生时，T5 必须立即停止播放器并清空本地播放缓冲，不等待后端确认。后端收到事件后取消当前 TTS 或引导播放。

## 6. 后端下行协议

### 6.1 开始播放

```json
{
  "type": "playback.start",
  "playbackId": "ca7d3ee8-d79c-4df0-b4e3-ceee2c156ceb",
  "kind": "reply",
  "turn": 1,
  "turnId": "8ea4105c-bc68-46de-b625-c49281c6688f",
  "text": "辛苦了，今晚先不用急着把所有事情解决。"
}
```

字段：

- `kind`：`opening`、`reply` 或 `guidance`。
- `turn`：`reply` 时为 1–3；其他类型可省略。
- `turnId`：`reply` 时为对应用户发言 ID；其他类型可省略。
- `text`：供联调和可访问性使用，固件可忽略。

`playback.start` 后紧跟连续 binary PCM message。T5 应边收边播。

### 6.2 播放结束

```json
{
  "type": "playback.end",
  "playbackId": "ca7d3ee8-d79c-4df0-b4e3-ceee2c156ceb",
  "reason": "completed"
}
```

`reason` 可为：

- `completed`：正常播放完毕。
- `interrupted`：用户说话或短按打断。
- `upstream_error`：TTS 上游失败。

### 6.3 要求设备停止播放

```json
{
  "type": "playback.stop",
  "playbackId": "ca7d3ee8-d79c-4df0-b4e3-ceee2c156ceb",
  "reason": "user_interrupt"
}
```

T5 收到后立即停止播放器并清空缓冲。

### 6.4 对话完成

```json
{
  "type": "conversation.completed",
  "completedTurns": 3,
  "journalId": "6d1e52fc-46ec-447c-89c6-21a021fb1fb9",
  "guidance": "breathing_46"
}
```

该事件表示第三次有效用户发言已经完成 Claude 回复和晚安日记持久化。后端随后自动进入引导流程。

### 6.5 引导播放

对于 T5 内置白噪音：

```json
{
  "type": "guidance.start",
  "guidance": "rain",
  "source": "device",
  "durationMinutes": 20
}
```

`rain` 和 `brown_noise` 由 T5 固件内置并循环播放，播放时长来自 Profile。T5 不通过语音 WebSocket下载完整白噪音文件。

对于呼吸引导：

```json
{
  "type": "playback.start",
  "playbackId": "17fb2836-d46d-485a-890d-0fc20359a7fa",
  "kind": "guidance",
  "text": "跟着眠眠，慢慢吸气……"
}
```

`breathing_46` 使用固定脚本，经火山引擎 TTS 生成并以 PCM 流式播放。`silence` 不产生音频。

### 6.6 错误事件

```json
{
  "type": "error",
  "code": "asr_unavailable",
  "message": "语音识别暂时不可用，请重新说一次",
  "retryable": true,
  "turnId": "8ea4105c-bc68-46de-b625-c49281c6688f"
}
```

稳定错误码：

| code | retryable | 含义 |
|---|---:|---|
| `speech_not_configured` | false | 后端未配置火山引擎 ASR App ID、ASR Access Token 或 TTS API Key |
| `invalid_phase` | false | 当前状态不允许开始或继续对话 |
| `invalid_event` | false | JSON 控制事件无效或顺序错误 |
| `invalid_audio_frame` | true | PCM 帧不是 960 bytes |
| `turn_in_progress` | true | 当前已有一轮正在处理 |
| `turn_too_long` | true | 单次长按超过 60 秒 |
| `conversation_limit` | false | 已完成 3 轮 |
| `asr_unavailable` | true | 火山引擎 ASR 暂时不可用 |
| `empty_transcript` | true | 未识别出有效文字 |
| `ai_unavailable` | true | Claude 和本地 fallback 均失败 |
| `tts_unavailable` | true | 火山引擎 TTS 重试后仍失败 |
| `device_too_slow` | true | T5 消费音频过慢导致有界队列满 |

错误事件不得包含火山引擎 Token、Claude Token、上游完整响应或完整音频内容。

## 7. 三轮业务流程

### 7.1 开场

`session.start` 首次成功后：

1. 后端进入 `CONVERSATION`。
2. 后端并行预连接火山引擎 ASR 和 TTS，降低第一次交互延迟。
3. 后端通过火山引擎 TTS 播放一条短、固定、可配置的温柔开场白。
4. 开场白不计入三轮，不调用 Claude。

开场白必须短且明确邀请用户说话，避免用户不知道何时可以长按。

### 7.2 用户轮次

每次 `input.start` 到 `input.end` 构成一次候选用户发言。只有 ASR 得到非空最终文本且 `ConversationService.Turn` 成功持久化后，才算一轮有效发言。

后端调用现有 ConversationService 时：

```text
inputMode = voice
clientRequestId = turnId
text = final transcript
```

`turnId` 复用已有 `clientRequestId` 幂等能力。设备重连或控制消息重试不会重复调用 Claude，也不会重复增加轮数。

### 7.3 Claude 回复

Claude 保持现有 structured output，一次返回：

- 适合朗读的回复文字。
- 情绪、担忧、明日待办和安慰语。
- 建议引导类型。
- 是否建议结束。

VoiceSessionService 负责严格执行 3 轮产品规则：前两轮继续对话，第 3 轮强制完成。`ConversationService` 继续负责数据库事务、fallback、晚安卡和 Android WebSocket 广播。

### 7.4 第三轮完成

第三轮 Claude 回复和晚安日记写入数据库后：

1. 将回复经火山引擎 TTS 流式播放给 T5。
2. 回复播放完成后发送 `conversation.completed`。
3. 通过现有 Tonight 领域服务执行 `select_guidance`，自动采用 Claude 的 `suggestedGuidance`，从 `CHOOSING_GUIDANCE` 进入 `SLEEPING` 并持久化选择。
4. `rain` / `brown_noise` 由 T5 播放内置资源。
5. `breathing_46` 由后端通过火山引擎 TTS 流式播放固定脚本。
6. `silence` 不产生音频。
7. Android 收到持久化后的 `SLEEPING` 状态。

Android 通过现有 `journal.created` 和 `tonight.updated` 接收晚安日记与状态更新。

## 8. 火山引擎 V3 桥接

### 8.1 流式 ASR

```text
wss://openspeech.bytedance.com/api/v3/sauc/bigmodel_async
resource ID: volc.bigasr.sauc.duration
```

握手 headers：`X-Api-App-Key`、`X-Api-Access-Key`、`X-Api-Resource-Id`、每次连接新生成的 `X-Api-Request-Id`。App ID 和 Access Token 仅保存在后端；Secret Key 不用于该 WebSocket API。

建连后先发送 V3 binary full client request：4-byte protocol header、正 sequence `1`、payload size、gzip JSON。JSON 声明 `pcm/raw/24000/mono`、`model_name=bigmodel`、`enable_itn=true`、`enable_punc=true`、`enable_ddc=false`。

T5 仍每 20 ms 上行 960 bytes。火山引擎建议每个上游音频包 100–200 ms，因此后端聚合 10 个 T5 帧为 9600 bytes（约 200 ms），gzip 后以 audio-only binary request 发送。`input.end` 时冲刷不足 200 ms 的余量并设置 last-package flag。独立读协程持续接收增量结果，最终使用最后一份完整非空 transcript。

### 8.2 单向流式 TTS

```text
wss://openspeech.bytedance.com/api/v3/tts/unidirectional/stream
resource ID: seed-tts-2.0
```

握手 headers：`X-Api-Key`、`X-Api-Resource-Id`、每次请求新生成的 `X-Api-Connect-Id`。建连后发送一次 no-sequence `FullClientRequest`，JSON payload 的 `req_params` 固定包含：

```json
{
  "speaker": "zh_female_gaolengyujie_uranus_bigtts",
  "text": "待合成文本",
  "audio_params": {"format": "pcm", "sample_rate": 24000}
}
```

服务端连续返回 `AudioOnlyServer` 原始 PCM，并以 `SessionFinished(152)` 表示成功结束；不使用双向 TTS 的 connection/session/task 请求事件。

`speaker` 可通过环境变量覆盖，但必须已对当前应用开通。服务端 audio-only response 是原始 PCM，不使用 Base64；后端收到后立即转交 `VoiceSessionService`，再切分成 960-byte T5 帧。播放中断时取消 context 并关闭当前火山引擎 WebSocket，T5 同时清空本地播放缓冲。

## 9. 服务端组件边界

### `speech.VolcASRClient`

职责：

- 建立和关闭火山引擎 V3 ASR WebSocket。
- 发送 gzip JSON 初始化请求。
- 将 20 ms T5 PCM 聚合为约 200 ms 的 gzip binary 音频包。
- 持续读取增量结果，在末包后返回最终文本。
- 将火山引擎错误转换为稳定内部错误，不泄露凭证。

不负责状态机、轮数、Claude 或数据库。

### `speech.VolcTTSClient`

职责：

- 建立和关闭火山引擎 V3 单向流式 TTS WebSocket。
- 发送一次 full client request，设置 speaker、文本和 PCM 24 kHz。
- 提交文字并返回原始 PCM 流。
- 支持 context cancellation 和一次自动重试。

不负责选择回复内容、引导类型或设备状态。

### `service.VoiceSessionService`

职责：

- 管理一个 T5 连接内的瞬时语音状态。
- 校验控制事件和 PCM 帧顺序。
- 协调 ASR → ConversationService → TTS。
- 自动开场、三轮结束和引导播放。
- 将 `turnId` 映射到 `clientRequestId`。
- 处理中断、重连和有界队列背压。

不复制 ConversationService 的 Claude、幂等、持久化和晚安卡规则。

### `controller.DeviceVoiceController`

职责：

- 校验配置和必要 query 参数。
- 完成 WebSocket upgrade。
- 启动读泵和唯一写泵。
- 设置 read limit、deadline、ping/pong 和 close code。
- 将消息交给 VoiceSessionService。

不包含业务规则或火山引擎协议细节。

## 10. 状态机和现有接口兼容

- 不新增数据库 migration。
- 不新增“录音中”或“播放中”持久 phase；这些是 VoiceSession 的瞬时子状态。
- `session.start` 必须通过共享的 Tonight 领域方法复用现有 `StartConversation`，从 `LOCKED` 进入 `CONVERSATION`，不得在 VoiceSessionService 直接修改数据库 phase。
- 第三轮继续通过现有 Conversation finalize 逻辑进入 `CHOOSING_GUIDANCE`，随后通过共享的 Tonight `select_guidance` 领域方法自动进入 `SLEEPING`；该领域方法负责状态持久化和 Android 广播，VoiceSessionService 只负责把相同引导实时发送给当前 T5 连接。
- `POST /api/v1/conversations/turn` 保留为 Debug 文本入口。
- `GET /api/v1/ws` 保留为 Android 状态 WebSocket。
- `POST /api/v1/device/events` 继续处理闭仓、开仓、短按、晨光等持久设备事件。
- `soft_button/long_press` 的语音开始和结束不再通过 REST 事件表达；语音连接内使用 `input.start` / `input.end`。
- `SUNRISE` 状态中的长按起床语义继续保留，硬件应按当前模式区分。
- 一般离线设备控制、晨光和非实时命令继续使用现有 command lease 队列。

## 11. 并发、背压和资源限制

每条设备语音连接包含：

- 一个 WebSocket 读循环。
- 一个唯一写协程。
- 一个有界下行消息队列。
- 当前 `turnId` 和输入状态。
- 当前 ASR/TTS cancel function。
- 当前 `playbackId`。

限制：

- 同时只能处理一个 `turnId`。
- 单次长按最长 60 秒，即最多 3000 个 PCM 帧和约 2.88 MB 原始音频；数据只流经内存，不整段保存。
- 整晚倾诉继续受现有 4 分钟硬截止约束。
- 服务端不为断线保存半段音频。
- 下行有界队列满时，先取消当前播放并发送 `device_too_slow`；无法及时发送错误则以 WebSocket 1011 关闭。
- 严重协议错误使用 WebSocket 1008；服务重启或临时故障使用 1011。

## 12. 故障恢复

| 场景 | 行为 |
|---|---|
| 未配置火山引擎 ASR App ID、ASR Access Token 或 TTS API Key | upgrade 前返回 HTTP 503，错误码 `speech_not_configured` |
| `input.end` 前断线 | 丢弃当前音频，不创建 turn；重连后用户重新说 |
| ASR 连接失败 | 清空本轮，返回可重试 `asr_unavailable` |
| ASR 为空 | 返回 `empty_transcript`，不增加轮数 |
| Claude 主调用失败 | 使用现有本地 fallback |
| Claude 与 fallback 均失败 | 释放 processing lease，返回 `ai_unavailable` |
| TTS 连接失败 | 对已经持久化的回复文字自动重试一次 |
| TTS 重试仍失败 | 返回 `tts_unavailable`；晚安日记和文字记录保持有效 |
| TTS 播放中用户打断 | T5 清空播放缓冲；后端取消并关闭当前 TTS 连接 |
| 后端重启 | 不恢复半段音频；已完成 turn 由 `turnId` 幂等保护 |
| 设备重连 | `session.ready` 返回持久 phase 和轮数；不重复已完成轮次 |

## 13. Android 职责

Android 正式流程只负责：

- Profile、人格、作息、提醒和引导偏好设置。
- Tonight phase 和已完成轮数展示。
- T5 在线状态和固件能力展示。
- WebSocket 重连后的状态刷新。
- `journal.created` 后展示晚安日记。
- 日记待办完成/取消和单卡删除。

Android 不负责：

- `SpeechRecognizer`。
- `TextToSpeech`。
- 音频上传。
- 回复朗读和 TTS 去重。
- 语音轮次计时。

Debug 构建可以保留文字输入页，调用现有 `/conversations/turn`，但不得作为正式产品主路径。

## 14. T5 固件职责

T5 必须实现：

- Device Voice WebSocket 建连、ping/pong 和指数退避重连。
- 24kHz/16-bit/mono PCM 采集和播放。
- 20 ms、960-byte 固定音频帧。
- 长按开始、松开结束的 push-to-talk。
- 短按本地立即静音并上报 `playback.stop`。
- 播放中长按的本地打断。
- 100–300 ms 有界播放环形缓冲和边收边播。
- `eventId`、`turnId` 和 `playbackId` 处理。
- 内置 `rain` 和 `brown_noise` 循环资源。
- `guidance.start` 的时长控制。
- 根据 Sunrise 与 Conversation 模式区分长按语义。

T5 不需要实现：

- 火山引擎协议或 Base64 音频事件。
- Claude 协议。
- 完整录音缓存。
- 晚安日记解析。

## 15. 配置

后端新增配置：

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
VOICE_OPENING_TEXT=手机已经安放好了。今晚有什么想和眠眠说的吗？
VOICE_BREATHING_SCRIPT=跟着眠眠，慢慢吸气四秒，再轻轻呼气六秒。
```

`.env.example` 只保留空 ASR App ID、空 ASR Access Token 和空 TTS API Key；真实凭证只进入未提交的 `.env` 或秘密管理系统。日志只记录 request/connect ID、稳定错误码和耗时，不记录凭证、原始 PCM、转写或完整 TTS 文本。

## 16. 简单测试范围

按当前 MVP 要求执行以下测试，不进行 code review 或大规模压力测试：

1. 配置解析：默认 URL/resource/speaker、空 ASR App ID/Access Token、空 TTS API Key 和 duration 校验。
2. 协议测试：JSON 事件绑定、960-byte PCM 校验、乱序事件和 60 秒限制。
3. 火山引擎 ASR mock WebSocket：鉴权 headers、gzip JSON 初始化、200 ms 聚合、末包、最终文本和错误转换。
4. 火山引擎 TTS mock WebSocket：鉴权 headers、event/session 帧、speaker、原始 PCM 下发、中断和一次重试。
5. VoiceSessionService：自动开场、有效轮次、空转写不计轮、`turnId` 幂等、三轮完成和引导选择。
6. Device Voice WebSocket smoke：握手、`session.ready`、一轮 mock 音频、播放开始/音频/结束。
7. `go test ./...`。
8. `go build ./...`。
9. OpenAPI YAML 解析。

真实火山引擎调用可以使用最小化 smoke 验证鉴权和协议；真实 T5 声音质量、首帧延迟和网络抖动仍需实机端到端联调。

## 17. 完整联调所需输入

跑通真实完整流程前需要：

- 已开通大模型流式 ASR 的火山引擎 App ID/Access Token，以及可用的单向流式 TTS API Key。
- 当前应用可用的 TTS speaker；默认尝试 `zh_female_gaolengyujie_uranus_bigtts`。
- T5 联调设备的 `deviceId`、固件版本和网络可达性。
- T5 对 24kHz/16-bit/mono PCM 采集与播放的实测确认。
- T5 每 20 ms 输出 960-byte 帧及 100–300 ms 播放缓冲的实测确认。
- T5 内置 `rain`、`brown_noise` 资源及可控播放时长的实测确认。
- 期望音色、语速、音量和情绪风格的试听反馈。

真实 Token 不得写入文档、源码、Git commit 或公开日志。
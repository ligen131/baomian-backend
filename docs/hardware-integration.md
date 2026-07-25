# 抱眠硬件（T5）连续对话联调协议

> 文档版本：Continuous Demo Target / 2026-07-25
> 状态：后端已实现，待部署环境验证与 T5 固件联调
> API Base URL：`https://bm.lg.gl/api/v1`

完整设计和后端数据语义见 [`voice-streaming-design.md`](voice-streaming-design.md)。本文主要描述 T5 固件联调顺序；前端独立文本试听使用 `POST /api/v1/tts/stream`，它不进入硬件会话、不增加对话轮数，也不生成晚安日记。

## 1. 演示操作

板载按钮语义：

| 操作 | 业务语义 |
|---|---|
| 按住 RESET | 新的 `box_closed`；开始一场演示 run |
| 按住 KEY | 结束所有 AI 对话；生成晚安日记并播放睡眠引导 |

每轮对话不需要用户按键：开场白或 AI 回复播放结束后，T5 自动录音；本地 VAD 检测到说话后的持续静默时自动结束录音。

板载 RESET 不是数据库 reset。数据库测试重置只能在服务器上运行受保护脚本。

## 2. 完整时序

```text
T5                         Baomian                    Volcengine / Claude
 | POST box_closed              |                              |
 |----------------------------->|                              |
 | GET /device/voice (upgrade)  |                              |
 |----------------------------->|                              |
 |<------ session.ready --------|                              |
 |------ session.start -------->|                              |
 |<----- playback.start --------|--- opening text → TTS ------>|
 |<========= PCM ===============|<========= PCM ===============|
 |<------ playback.end ---------|                              |
 |                              |                              |
 |------ input.start ---------->|------ ASR open ------------->|
 |<----- input.accepted --------|                              |
 |========= PCM ===============>|========= PCM ===============>|
 |------ input.end ------------>|------ ASR final ------------>|
 |<---- transcript.final -------|                              |
 |<--------- thinking ----------|--------- Claude ------------>|
 |<----- playback.start --------|--------- reply TTS --------->|
 |<========= PCM ===============|<========= PCM ===============|
 |<------ playback.end ---------|                              |
 |                              |                              |
 | 自动重复 input.start ...                                    |
 |                              |                              |
 |--- conversation.finish ----->|-- summarize + append journal |
 |<-- conversation.completed ---|                              |
 |<----- playback.start --------|-- stream sleep asset         |
 |<========= PCM ===============|                              |
 |<------ playback.end ---------|                              |
 | 本次工作结束                  |                              |
```

## 3. 固定音频格式

| 参数 | 值 |
|---|---|
| 编码 | raw PCM signed 16-bit little-endian |
| 采样率 | 24000 Hz |
| 声道 | mono |
| 帧长 | 20 ms |
| 每个 binary message | 960 bytes |

不发送 WAV header。T5 播放使用 100–300 ms 有界缓冲并边收边播。

## 4. RESET 开始

T5 每次真实 RESET 长按生成新的 UUID：

```http
POST /api/v1/device/events
Content-Type: application/json

{
  "eventId":"3db25df5-5f70-48f4-97d7-e823aa1b8bce",
  "deviceId":"expo-device-001",
  "userId":"expo-user-001",
  "type":"box_closed",
  "payload":{"source":"reset_button"}
}
```

网络重试必须复用同一个 `eventId`。下一次新的 RESET 按压才生成新 ID。

随后连接：

```text
wss://bm.lg.gl/api/v1/device/voice?deviceId=expo-device-001&userId=expo-user-001
```

首个服务端事件：

```json
{
  "type":"session.ready",
  "runId":"<runId>",
  "phase":"LOCKED",
  "completedTurns":0,
  "audio":{"codec":"pcm","sampleRate":24000,"bitDepth":16,"channels":1,"frameMs":20,"frameBytes":960},
  "recovery":{"runStatus":"active","resumeAction":"listen","guidanceStatus":"pending"},
  "occurredAt":"2026-07-25T00:00:00Z"
}
```

`completedTurns` 为 JSON number，没有最大值 3。ready 之后所有 T5 JSON 控制事件都必须携带该 `runId`。

T5 发送：

```json
{"type":"session.start","runId":"<runId>","eventId":"95a777c5-2ceb-4aea-8802-b7ec6b33d7dd"}
```

后端使用 TTS 播放：

> 手机已经放好了。今晚想和眠眠聊聊什么？

T5 必须等待相同 `playbackId` 的 `playback.end`，不能把 `playback.start` 当作播放完成。

## 5. 自动录音

收到 `kind=opening` 或 `kind=reply` 的：

```json
{"type":"playback.end","runId":"<runId>","playbackId":"...","reason":"completed","occurredAt":"2026-07-25T00:00:00Z"}
```

T5 自动执行：

```json
{
  "type":"input.start",
  "runId":"<runId>",
  "eventId":"ff4af57c-64ef-4e29-a6ed-a5f0bf445247",
  "turnId":"8ea4105c-bc68-46de-b625-c49281c6688f"
}
```

检测到首声后发送 `input.start`，同时继续采集。收到：

```json
{"type":"input.accepted","runId":"<runId>","eventId":"<input.start eventId>","turnId":"8ea4105c-bc68-46de-b625-c49281c6688f","occurredAt":"2026-07-25T00:00:00Z"}
```

后按 pre-roll → pending → live 顺序发送 PCM，允许短时 burst。

推荐本地 VAD：

- 无首声时本地监听 10 秒；超时只重新监听，不发送 `input.start` 或 `input.end`。
- 300 ms pre-roll，即 15 个 960-byte 帧。
- 等待 `input.accepted` 时继续采集，pending 最多 3 秒，即 150 帧、144000 bytes。
- 出现人声后连续静默 2.5 秒结束本轮。
- 单轮最多 60 秒。

结束事件：

```json
{
  "type":"input.end",
  "runId":"<runId>",
  "eventId":"53bd7e56-2858-499e-a04d-2182ba299178",
  "turnId":"8ea4105c-bc68-46de-b625-c49281c6688f"
}
```

后端可能返回：

```json
{"type":"transcript.final","runId":"<runId>","turnId":"8ea4105c-bc68-46de-b625-c49281c6688f","text":"今天工作有点累","occurredAt":"2026-07-25T00:00:00Z"}
```

```json
{"type":"thinking","runId":"<runId>","turnId":"8ea4105c-bc68-46de-b625-c49281c6688f","occurredAt":"2026-07-25T00:00:00Z"}
```

最终 AI 回复仍是：

```text
playback.start(runId=<runId>, kind=reply, turnId=同一 turnId, audio.frameBytes=960)
→ binary PCM × N
→ playback.end(runId=<runId>, reason=completed)
```

回复结束后自动开始下一轮录音。不限三轮，直到 KEY 结束。

## 6. KEY 结束

KEY 长按时，不论正在录音、思考还是播放，T5 都先停止本地采集/播放并清空缓冲，然后发送：

```json
{
  "type":"conversation.finish",
  "runId":"<runId>",
  "eventId":"b8391c5d-7746-4705-bb74-4e145749513e"
}
```

后端仅总结已经完整保存的轮次，并追加一篇晚安日记。成功后发送：

```json
{
  "type":"conversation.completed",
  "runId":"<runId>",
  "eventId":"b8391c5d-7746-4705-bb74-4e145749513e",
  "completedTurns":6,
  "journalId":"6d1e52fc-46ec-447c-89c6-21a021fb1fb9",
  "guidance":"rain",
  "occurredAt":"2026-07-25T00:00:00Z"
}
```

接着流式播放睡眠引导：

```json
{
  "type":"playback.start",
  "runId":"<runId>",
  "playbackId":"17fb2836-d46d-485a-890d-0fc20359a7fa",
  "kind":"guidance",
  "guidance":"rain",
  "audio":{"codec":"pcm","sampleRate":24000,"bitDepth":16,"channels":1,"frameMs":20,"frameBytes":960},
  "occurredAt":"2026-07-25T00:00:00Z"
}
```

临时映射：

- `rain`：服务器 `~/tmp/rainy.wav`
- `breathing_46`：服务器 `~/tmp/miao.mp3`

后端负责解码并统一输出 24 kHz/16-bit/mono PCM。收到 guidance 的 `playback.end(reason=completed)` 后，T5 不再录音，本次工作结束。

## 7. 控制事件表

T5 → 后端：

| type | 时机 |
|---|---|
| `session.start` | RESET 流程中，收到 `session.ready` 后 |
| `input.start` | opening/reply 正常播放完毕后自动发送 |
| `input.end` | 已检测首声后出现 2.5 秒尾静默，或达到 60 秒上限 |
| `playback.stop` | 本地需要中断当前播放时 |
| `conversation.finish` | KEY 长按，结束当前 run |

后端 → T5：

| type | 作用 |
|---|---|
| `session.ready` | 会话与音频格式就绪 |
| `input.accepted` | 可以上传当前 turn PCM |
| `transcript.final` | 联调可见的最终转写 |
| `thinking` | Claude 正在生成 |
| `playback.start` | 后续 binary PCM 属于该 playback |
| `playback.stop` | 立即清空本地播放缓冲 |
| `playback.end` | 该 playback 的唯一终态 |
| `conversation.completed` | 日记已追加，准备播放睡眠引导 |
| `error` | 稳定错误事件 |

## 8. 连接关闭与错误处理

- WebSocket close code `1008`：停止本次自动对话循环，不自动重连。
- close code `1011` 或网络错误：按退避策略重连，并以 `session.ready.recovery.resumeAction` 恢复。
- 每个 `playback.start` 必须由同一 `playbackId` 的 `playback.end`、关联该 playback 的结构化 `error` 或 WebSocket close 三者之一终结。
- guidance 中断后重连从素材开头播放，不做字节偏移续传。

| code | T5 行为 |
|---|---|
| `empty_transcript` | 短暂提示后自动重新录音 |
| `asr_unavailable` | 保持连接并自动重试新 turn |
| `ai_unavailable` | 保持连接；可重试或等待 KEY |
| `tts_unavailable` | 结束当前 playback；可重试或等待 KEY |
| `turn_in_progress` | 不并发开启第二个 turn |
| `invalid_audio_frame` | 修复为恰好 960 bytes |
| `finish_in_progress` | 等待 `conversation.completed`，不更换 finish eventId |
| `journal_unavailable` | 不开始睡眠引导；安全重试相同 finish eventId |

Voice socket 关闭时立即停止录音和播放。任何错误都不能导致无限缓存 PCM。

## 9. 日记与数据库 reset

每次 KEY 成功完成都会创建新的日记，即使同一天已经存在其他日记也不能覆盖。前端继续调用：

```http
GET /api/v1/journals?limit=7
```

不增加 `/journals/tonight`。

服务器测试数据库 reset 会预置 `D-3`、`D-2`、`D-1` 三篇固定演示日记，具体文案见 [`voice-streaming-design.md`](voice-streaming-design.md#10-数据库测试-reset-和三篇种子日记)。重复 reset 不得重复插入种子。

## 10. 固件验收清单

- [ ] RESET 每次新按压产生新 `box_closed eventId`，重试复用旧 ID。
- [ ] 收到有效 `session.ready` 后发送 `session.start`。
- [ ] 开场 PCM 能边收边播并等待 `playback.end`。
- [ ] opening/reply 正常结束后自动录音，不需要按键。
- [ ] 只在 `input.accepted` 后发送 PCM。
- [ ] PCM 始终为 960-byte、24 kHz、16-bit、mono、little-endian。
- [ ] VAD 在首声 10 秒、尾静默 2.5 秒、总长 60 秒三个边界结束录音。
- [ ] AI 回复播放结束后可连续自动进入任意轮数。
- [ ] 不在第三轮停止，不处理 `conversation_limit` 为正常完成。
- [ ] KEY 能在录音/思考/播放任一阶段发送 `conversation.finish`。
- [ ] `conversation.completed` 后播放 guidance PCM。
- [ ] guidance 播完后不再录音。
- [ ] WebSocket 关闭会清空采集和播放状态。
- [ ] 不持久化完整录音、转写或回复正文。

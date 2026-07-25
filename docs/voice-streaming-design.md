# T5 连续语音演示设计

> 状态：后端已实现，待部署环境验证与 T5 固件联调
> 日期：2026-07-25
> 适用范围：抱眠硬件联调、连续 AI 对话、睡眠引导、晚安日记和前端流式 TTS

## 1. 演示目标

演示流程固定为：

```text
按住板载 RESET（box_closed）
→ 后端流式播放开场引导
→ T5 自动开始录音并实时上传 PCM
→ T5 检测静默后自动结束本轮
→ 后端 ASR → Claude → TTS，流式返回回复音频
→ 回复播完后 T5 自动开始下一轮录音
→ 重复任意轮，不限制三轮
→ 按住板载 KEY 结束对话
→ 后端总结完整对话并追加一篇晚安日记
→ 后端流式播放睡眠引导音频
→ 播放结束，本次硬件演示结束
```

正式联调不要求用户在每轮开始或结束时按键。RESET 只负责开始一场新的演示会话，KEY 只负责结束当前会话。

## 2. 总体架构

```text
T5
  ↕ Device Voice WebSocket（JSON 控制 + raw PCM）
VoiceSessionService
  ├→ Volcengine streaming ASR
  ├→ ConversationService → Claude
  ├→ Volcengine streaming TTS
  ├→ SleepAudioService → miao.mp3 / rainy.wav
  └→ JournalService → append MemoryCard

前端
  ├→ POST /api/v1/tts/stream
  ├→ GET /api/v1/journals
  └→ GET /api/v1/ws（journal.created 等状态事件）
```

T5 不保存火山引擎或 Claude 凭证。后端不持久化原始录音，也不在日志中记录完整录音、完整转写或对话正文。

## 3. 音频契约

T5 Voice WebSocket 和前端流式 TTS 都使用相同的原始 PCM：

| 参数 | 值 |
|---|---|
| 编码 | PCM signed linear |
| 字节序 | little-endian |
| 采样率 | 24000 Hz |
| 位深 | 16 bit |
| 声道 | mono |
| T5 帧长 | 20 ms |
| T5 每帧 | 960 bytes |

T5 WebSocket binary message 必须恰好为 960 bytes。前端 HTTP 流不承诺每个网络 chunk 都是 960 bytes，前端应把响应视为连续 PCM 字节流并自行缓冲。

## 4. 前端文本流式 TTS

### 4.1 请求

```http
POST /api/v1/tts/stream
Content-Type: application/json
X-Demo-User-Id: expo-user-001

{"text":"今晚辛苦了，先慢慢放松下来吧。"}
```

请求体：

```json
{
  "text": "非空中文或其他 UTF-8 文本，最多 500 个 Unicode 字符"
}
```

该接口只做文本转语音：

- 不创建 ConversationTurn。
- 不增加对话轮数。
- 不修改 Tonight phase。
- 不创建晚安日记。
- 与 Device Voice WebSocket 相互独立。

### 4.2 成功响应

```http
HTTP/1.1 200 OK
Content-Type: audio/pcm;codec=pcm_s16le;rate=24000;channels=1
Cache-Control: no-store
X-Audio-Codec: pcm_s16le
X-Audio-Sample-Rate: 24000
X-Audio-Bit-Depth: 16
X-Audio-Channels: 1
```

响应 body 是边合成边写出的 raw PCM。服务端拿到火山引擎首个 PCM chunk 后立即 flush，不等待整段语音生成完毕。前端通过 `ReadableStream` 持续读取并放入有界播放缓冲。

### 4.3 错误

在响应 header 尚未发送前：

- `400 validation_error`：JSON 无效、`text` 为空或超过 500 字符。
- `503 speech_not_configured`：TTS 未配置。
- `502 tts_unavailable`：火山引擎在首个音频 chunk 前失败。

响应已经开始后发生上游错误时，HTTP 流直接结束；前端将“流提前结束且没有正常完成”作为可重试播放失败。服务端不得在 PCM body 中混入 JSON 错误。

客户端断开时，后端必须取消火山引擎请求，避免孤儿合成任务。

## 5. 设备开始流程：RESET

### 5.1 RESET 的业务语义

按住板载 RESET 表示一次新的 `box_closed` 物理操作。T5 为每次真实按压生成新的 `eventId`：

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

规则：

- 同一 `eventId` 重试仍然幂等，不重复开始会话。
- 在 `DEMO_CONTINUOUS_CONVERSATION=true` 且身份精确匹配时，新的 RESET `eventId` 创建新的 conversation run。
- 新 run 不删除旧对话和旧晚安日记。
- 普通生产设备的真实仓盖语义保持状态幂等，不因设备重启自动创建新 run。
- 板载 RESET 不是数据库 reset；数据库测试重置是单独的服务器脚本。

### 5.2 建立 Voice WebSocket

```text
GET /api/v1/device/voice?deviceId=<deviceId>&userId=<userId>
```

连接建立后，后端先发送：

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

`completedTurns` 是当前 run 已成功完成的用户/AI 往返次数，只用于恢复和诊断，不再有最大值 3。T5 收到 ready 后，后续所有 JSON 控制事件必须复用这里返回的 `runId`。

T5 随后发送：

```json
{"type":"session.start","runId":"<runId>","eventId":"95a777c5-2ceb-4aea-8802-b7ec6b33d7dd"}
```

后端进入 `CONVERSATION` 并播放固定开场白：

> 手机已经放好了。今晚想和眠眠聊聊什么？

下行顺序：

```text
playback.start(kind=opening)
→ binary PCM × N
→ playback.end(reason=completed)
```

## 6. 自动录音和 VAD

收到 `opening` 或 `reply` 的 `playback.end(reason=completed)` 后，T5 自动开始下一轮，不等待按键：

1. 生成新的 `turnId` 和 `eventId`。
2. 发送 `input.start`。
3. 等待 `input.accepted`。
4. 实时发送 960-byte PCM。
5. 本地 VAD 判断说话结束后发送 `input.end`。

```json
{
  "type":"input.start",
  "runId":"<runId>",
  "eventId":"ff4af57c-64ef-4e29-a6ed-a5f0bf445247",
  "turnId":"8ea4105c-bc68-46de-b625-c49281c6688f"
}
```

```json
{
  "type":"input.end",
  "runId":"<runId>",
  "eventId":"53bd7e56-2858-499e-a04d-2182ba299178",
  "turnId":"8ea4105c-bc68-46de-b625-c49281c6688f"
}
```

演示默认 VAD 参数：

- 本地监听最多等待 10 秒出现首段人声。
- 检测到人声后，连续 2.5 秒静默视为本轮结束。
- 单轮硬上限 60 秒。
- 初始 10 秒没有人声时只重新进入本地监听，不发送 `input.start` 或 `input.end`，也不创建 turn。
- 检测到首声后才发送 `input.start`；保留 300 ms（15 帧）pre-roll，并在等待 `input.accepted` 时继续采集最多 3 秒（150 帧）pending PCM。
- accepted 后按 pre-roll → pending → live 顺序发送，允许短时 burst。

VAD 在 T5 本地完成。后端仍持续接收 PCM 并桥接 ASR，但不依赖 ASR 增量文本来决定何时停止采集。这样不会形成“必须先 `input.end` 才拿到 final、又必须拿到 final 才发送 `input.end`”的循环依赖。

## 7. 连续 AI 对话

`input.end` 后，后端异步执行：

```text
ASR final
→ transcript.final
→ thinking
→ Claude
→ 持久化 user/assistant turn
→ playback.start(kind=reply)
→ binary PCM × N
→ playback.end(reason=completed)
```

代表性服务端事件：

```json
{
  "type":"transcript.final",
  "runId":"<runId>",
  "turnId":"8ea4105c-bc68-46de-b625-c49281c6688f",
  "text":"今天工作有点累",
  "occurredAt":"2026-07-25T00:00:00Z"
}
```

```json
{
  "type":"thinking",
  "runId":"<runId>",
  "turnId":"8ea4105c-bc68-46de-b625-c49281c6688f",
  "occurredAt":"2026-07-25T00:00:00Z"
}
```

```json
{
  "type":"playback.start",
  "runId":"<runId>",
  "playbackId":"17fb2836-d46d-485a-890d-0fc20359a7fa",
  "kind":"reply",
  "turnId":"8ea4105c-bc68-46de-b625-c49281c6688f",
  "audio":{"codec":"pcm","sampleRate":24000,"bitDepth":16,"channels":1,"frameMs":20,"frameBytes":960},
  "occurredAt":"2026-07-25T00:00:00Z"
}
```

```json
{
  "type":"playback.end",
  "runId":"<runId>",
  "playbackId":"17fb2836-d46d-485a-890d-0fc20359a7fa",
  "turnId":"8ea4105c-bc68-46de-b625-c49281c6688f",
  "reason":"completed",
  "occurredAt":"2026-07-25T00:00:00Z"
}
```

只有非空转写且 assistant 回复成功持久化，才增加 `completedTurns`。

演示 run 规则：

- 不限制三轮。
- 不在第三轮自动生成晚安日记。
- 不发送 `conversation_limit`。
- 不因达到原 4 分钟 hard deadline 自动结束。
- 每次回复播放完毕后，T5 自动返回第 6 节开始下一轮录音。
- 同一时刻最多处理一个 `turnId`。
- `turnId` 复用为 `clientRequestId`，重试不重复增加轮次。

Claude 每轮仍可输出结构化的 `emotion`、`worry`、`tomorrowTask` 和 `comfort` 作为中间草稿，但这些字段只在 KEY 结束时统一总结后写入最终晚安日记。

## 8. KEY 结束和睡眠引导

### 8.1 上行结束事件

按住板载 KEY 时，T5 立即停止当前录音或播放、清空缓冲，并发送：

```json
{
  "type":"conversation.finish",
  "runId":"<runId>",
  "eventId":"b8391c5d-7746-4705-bb74-4e145749513e"
}
```

`conversation.finish` 是当前 Voice WebSocket 内的控制事件。它不是 `input.end`，也不是晨光阶段的 `soft_button/long_press`。

后端收到后：

1. 取消尚未完成的 ASR、Claude 或 reply TTS；未完整持久化的半轮不写入总结。
2. 对当前 run 中所有已完成的用户和 assistant turn 做一次最终总结。
3. 追加一篇新的 MemoryCard。
4. 广播 `journal.created`。
5. 发送 `conversation.completed`。
6. 流式播放睡眠引导。

同一 `eventId` 重试必须返回同一个完成结果，不得追加第二篇日记。

### 8.2 完成事件

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

`completedTurns` 可以是任意非负整数。

### 8.3 临时睡眠音频

演示阶段允许使用两个服务器本地素材：

| guidance | 临时源文件 | 下发方式 |
|---|---|---|
| `rain` | `~/tmp/rainy.wav` | 后端解码/重采样后经 Voice WebSocket 流式发送 PCM |
| `breathing_46` | `~/tmp/miao.mp3` | 后端解码/重采样后经 Voice WebSocket 流式发送 PCM |

源文件无论原编码和采样率如何，下发 T5 前都必须统一转换为 PCM s16le、24000 Hz、mono，并切成 960-byte 帧。部署时通过配置指定实际绝对路径；`~` 只表示当前开发机素材位置，不写死在业务源码中。

播放顺序：

```json
{
  "type":"playback.start",
  "runId":"<runId>",
  "playbackId":"61c83ae0-657c-483a-a246-63810327ece0",
  "kind":"guidance",
  "guidance":"rain",
  "audio":{"codec":"pcm","sampleRate":24000,"bitDepth":16,"channels":1,"frameMs":20,"frameBytes":960},
  "occurredAt":"2026-07-25T00:00:00Z"
}
```

```text
binary PCM × N
```

```json
{
  "type":"playback.end",
  "runId":"<runId>",
  "playbackId":"61c83ae0-657c-483a-a246-63810327ece0",
  "reason":"completed",
  "occurredAt":"2026-07-25T00:00:00Z"
}
```

睡眠引导播放结束后，T5 结束本次工作，不再自动开始录音。

## 9. 晚安日记 append 语义

KEY 是唯一的正常日记生成时机。最终总结输入为当前 run 中所有已经完整持久化的用户和 assistant turn。

每次 KEY 成功结束一个 run，后端都创建新的 MemoryCard：

- 新 UUID。
- 关联独立 conversation run。
- `date` 使用用户 Profile 时区的当天日期。
- 同一用户、同一天允许存在多篇日记。
- 不按 `userId + date` 或仅按 `sessionId` 覆盖历史日记。
- 列表按 `date DESC, createdAt DESC` 返回，因此同一天更新创建的日记排在前面。

继续使用现有接口：

```http
GET /api/v1/journals?limit=7
GET /api/v1/journals/{id}
```

不新增 `GET /journals/tonight`。前端需要全部记录时增大 `limit`，上限仍为 30。

为支持同日多 run，持久化层需要让“conversation run”和“日历日期”解耦：每次 RESET 创建新的 run；MemoryCard 对 run 保持一对一，但同一 `userId + date` 不再唯一。

## 10. 数据库测试 reset 和三篇种子日记

服务器测试 reset 与板载 RESET 完全不同。执行测试数据库 reset 时：

1. 清理指定 Demo User/Device 的活动 run、测试日记和未完成命令。
2. 创建一个新的可开始 run。
3. 写入相对演示当天 `D-3`、`D-2`、`D-1` 的三篇固定日记。

固定文案：

| 日期 | emotion | worry | tomorrowTask | comfort | guidance |
|---|---|---|---|---|---|
| D-3 | 轻松 | 今天完成了几件一直惦记的小事，心里松了一些。 | 明早列出最重要的一件事 | 你已经做得很好，今晚可以放心休息。 | rain |
| D-2 | 疲惫 | 工作还有一些没有收尾，担心明天会来不及。 | 明早先处理最紧急的十分钟 | 剩下的事情交给明天，现在先把自己照顾好。 | breathing_46 |
| D-1 | 平静 | 明天有新的安排，期待里也带着一点紧张。 | 起床后确认今天的第一个安排 | 不需要一次准备好所有答案，慢慢来就可以。 | rain |

种子记录必须有固定、可重复计算的幂等标识；重复执行 reset 后仍然只有这三篇种子，不重复追加。硬件演示通过 KEY 产生的日记则始终 append。

## 11. 连接、顺序和故障

- 同一 `deviceId` 只允许一个活跃 Voice WebSocket，新连接替换旧连接并以 close code `1008` 终止旧循环。
- 收到 `1008` 时 T5 停止自动循环；收到 `1011` 或遇到网络错误时退避重连。
- 所有 WebSocket 写操作经过唯一 writer，保持 `playback.start → PCM → playback.end` FIFO；每个 start 由 `playback.end`、结构化 playback error 或 WebSocket close 唯一终结。
- `input.end` 后 ASR final 默认 8 秒内成功或返回 `asr_unavailable`。
- `empty_transcript` 不创建 turn，T5 自动重新监听。
- reply TTS 失败返回 `tts_unavailable`，T5 可重新等待或由用户按 KEY 结束。
- KEY 总结失败时返回可重试错误且不播放睡眠引导；相同 finish `eventId` 可安全重试。
- KEY 日记已写入但连接在引导期间断开时，日记仍有效；重连不得重复 append。
- 连接关闭时，T5 立即停止采集和播放并清空本地缓冲。

## 12. 实现与验收边界

后端源码已实现：前端文本流式 TTS、conversation run、不限轮、`conversation.finish`、同日 append 日记、三篇 reset 种子、WAV/MP3 流式转换以及结构化断线恢复。

仍需在部署环境完成：

- 在 PostgreSQL 备份或测试库验证 migration 004 的 up/down；生产升级只执行 up。
- 用真实火山引擎凭证验证 ASR/TTS 首帧、超时和错误码。
- 用真实 T5 固件验收 10 秒无首声、300 ms pre-roll、3 秒 pending burst、2.5 秒尾静默和 60 秒上限。
- 自动录音和本地 VAD 属于 T5 固件工作，本仓库不包含固件源码。

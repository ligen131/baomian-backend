# 抱眠连续演示后端接口设计

> 版本：Continuous Demo Target / 2026-07-25
> 状态：目标设计，当前实现尚待迁移
> API Base URL：`https://bm.lg.gl/api/v1`

## 1. 范围

本阶段以 T5 实机联调和现场演示为目标：

- RESET 长按等价于新的 `box_closed`，开始一场 conversation run。
- 后端用 TTS 播放固定开场白。
- T5 在开场或 AI 回复播放完毕后自动录音，用本地 VAD 自动结束。
- 对话不限三轮，持续执行 ASR → Claude → TTS。
- KEY 长按通过 `conversation.finish` 结束 run。
- 后端在 KEY 后总结完整对话并 append 一篇晚安日记。
- 后端流式下发 `miao.mp3` 或 `rainy.wav` 作为临时睡眠引导。
- 前端获得独立的文本流式 TTS HTTP 接口。
- `GET /journals` 继续返回全部最近日记，不增加 `/journals/tonight`。
- 测试数据库 reset 后写入三篇固定历史日记。

仍不包含正式用户鉴权、设备证书、多实例 pub-sub、原始录音持久化或 T5 固件代码。

## 2. 架构

```text
T5
  <-> GET /api/v1/device/voice
        -> VoiceSessionService
             -> Volcengine ASR
             -> ConversationService -> Claude
             -> Volcengine TTS
             -> SleepAudioService
        -> ConversationRun / ConversationTurn / MemoryCard

前端
  -> POST /api/v1/tts/stream -> Volcengine TTS -> chunked PCM
  -> GET /api/v1/journals
  <-> GET /api/v1/ws
```

控制器负责参数、HTTP/WebSocket 生命周期和流式写出；业务状态、finish 幂等、journal append 和事务规则属于 Service。

## 3. 前端文本流式 TTS

```http
POST /api/v1/tts/stream
Content-Type: application/json

{"text":"今晚辛苦了，先慢慢放松下来吧。"}
```

约束：

- `text` trim 后非空，最多 500 个 Unicode 字符。
- 成功响应 `Content-Type: audio/pcm;codec=pcm_s16le;rate=24000;channels=1`。
- body 为 PCM s16le、24000 Hz、mono 的流式字节流；同时兼容 HTTP/1.1 和 HTTP/2，不依赖 `Transfer-Encoding` header。
- 设置 `Cache-Control: no-store` 和明确音频 headers。
- 火山引擎首个 PCM chunk 到达后立即 flush。
- 客户端断开时取消上游 TTS。
- 该接口无 Tonight、ConversationTurn 或 Journal 副作用。

错误发生在首个 PCM 前时返回标准 JSON：`400 validation_error`、`503 speech_not_configured` 或 `502 tts_unavailable`。首个 PCM 已写出后失败则终止流，不在音频 body 中混入 JSON。

## 4. Conversation run

原来的 `NightSession(user_id, date)` 不能同时代表“当天状态”和“同日多场对话”。新设计引入独立 conversation run 概念：

- 每次演示 RESET 创建一个 run。
- run 有独立 ID、开始时间、结束时间、状态和完成轮数。
- ConversationTurn 关联 run。
- MemoryCard 与已完成 run 一对一。
- 同一 `userId + date` 可以有多个 run 和多个 MemoryCard。
- NightSession 继续承载盒盖、睡眠、晨光等当天设备状态，但不再以 `ConversationTurns <= 3` 表示产品限制。

推荐新增表而不是继续放宽 `night_sessions` 的唯一约束：

```text
conversation_runs
  id UUID PK
  user_id TEXT INDEX
  device_id TEXT INDEX
  date DATE INDEX
  status active|finishing|completed|aborted
  completed_turns INT
  finish_event_id TEXT UNIQUE NULL
  started_at TIMESTAMPTZ
  finished_at TIMESTAMPTZ NULL
  created_at / updated_at
```

`conversation_turns.session_id` 应迁移为或补充 `run_id`。`memory_cards` 应关联 `run_id UNIQUE`；`user_id + date` 只建普通索引，不能唯一。

## 5. RESET 开始

T5 上报新的 `box_closed`，payload 含 `source=reset_button`。在连续演示配置且 Demo User/Device 精确匹配时：

1. 同一 `eventId` 请求幂等。
2. 中止该设备仍处于 active 的旧 run，但不删除旧日记。
3. 创建新的 active run。
4. 返回可建立 Voice WebSocket 的 `LOCKED` 状态。
5. Voice 收到 `session.start` 后进入 `CONVERSATION` 并播放：

> 手机已经放好了。今晚想和眠眠聊聊什么？

普通生产仓盖事件保持原状态机语义，不把设备重启当作新 run。

## 6. 自动轮次

T5 在 opening/reply 的 `playback.end(completed)` 后主动发送 `input.start`，随后用本地 VAD 决定 `input.end`。后端协议仍接收显式 `input.start/input.end`，不在服务端从 ASR 增量文本推断采集停止。

每轮：

```text
input.start
→ PCM
→ input.end
→ ASR final
→ Claude
→ transactionally persist user + assistant
→ reply TTS
```

规则：

- 不再有三轮上限。
- `completedTurns` 是累计统计，不设置最大值 3。
- 不再由 `turnIndex >= 3` 强制 `ShouldFinalize`。
- 不再因原 4 分钟 hard deadline结束演示 run。
- 每轮只保存完成的 user/assistant 对；半轮不进入最终总结。
- 同时只允许一个 processing turn。
- `turnId` 继续作为 `clientRequestId` 幂等键。

## 7. KEY finish

Voice 新增上行事件：

```json
{"type":"conversation.finish","eventId":"UUID"}
```

处理事务：

1. 以 `finish_event_id` 抢占 active run，使状态变为 `finishing`。
2. 读取全部已完成 turns。
3. 调用专用 journal summarization，得到 `emotion/worry/tomorrowTask/comfort/suggestedGuidance`。
4. 插入新的 MemoryCard；不能 Upsert 覆盖另一场 run。
5. run 标记为 `completed`。
6. NightSession 进入 `SLEEPING` 或等价演示完成状态。
7. 提交事务后广播 `journal.created` 和 `tonight.updated`。
8. 向设备发送 `conversation.completed`，随后播放 guidance。

相同 `finish eventId` 重试返回已有 journal；不同 finish eventId 对已 completed run 也不得创建第二篇日记。

未完成任何有效 turn 时也允许 KEY 结束。总结使用固定安全内容，例如“今晚没有留下具体的心事”，仍 append 一篇可展示日记，从而保证现场流程可结束。

## 8. 睡眠音频

临时素材：

- `rain`：`~/tmp/rainy.wav`
- `breathing_46`：`~/tmp/miao.mp3`

部署配置应存放绝对路径，例如：

```env
DEMO_RAIN_AUDIO_PATH=/home/ligen/tmp/rainy.wav
DEMO_BREATHING_AUDIO_PATH=/home/ligen/tmp/miao.mp3
```

`SleepAudioService` 负责：

- 启动时或首次使用时验证文件存在且可解码。
- 解码 MP3/WAV。
- 重采样为 PCM s16le、24000 Hz、mono。
- 流式切成 960-byte 帧，不把整个解码结果永久缓存。
- 支持 context cancellation。

KEY 后只播放一次，`playback.end(completed)` 后不自动录音。

## 9. 日记查询与 append

保留：

```text
GET /journals?limit=7
GET /journals/{id}
PATCH /journals/{id}
DELETE /journals/{id}
GET /memories?limit=7
```

不增加 `/journals/tonight`。列表排序固定为：

```text
date DESC, created_at DESC
```

因此同一天多篇日记全部返回，并按创建时间从新到旧排列。

连续演示启动新 run 时禁止：

- 删除上一 run 的 MemoryCard。
- 按当天日期 Upsert 覆盖。
- 删除历史 ConversationTurn。

## 10. 测试 reset 数据

服务器脚本的 apply 模式在清理指定 Demo 身份后，写入 D-3、D-2、D-1 三篇固定日记。种子使用固定 namespace UUID 或唯一 seed key，使重复 reset 的结果稳定为恰好三篇。

具体固定文案见 [`voice-streaming-design.md`](voice-streaming-design.md#10-数据库测试-reset-和三篇种子日记)。测试脚本继续保持：

- 默认 dry-run。
- `--apply --confirm RESET-TONIGHT` 双确认。
- SQL 参数化。
- 凭证不进入参数或输出。
- 不开放公网 reset endpoint。

## 11. 状态和实时事件

Android 状态 WebSocket继续广播：

- `tonight.updated`
- `conversation.reply`
- `journal.created`
- `journal.updated`
- `journal.deleted`
- `device.event`
- `device.status`

KEY 成功后必须先完成日记事务，再发 `journal.created`。前端错过事件时通过 `GET /journals` 对账。

旧的 `CHOOSING_GUIDANCE=3`、`SLEEPING=3` 一致性规则需要移除；`completedTurns` 不再决定 phase 合法性。

## 12. 安全与可观测性

- 日志可记录 runId、turnId、playbackId、finishEventId、stage、durationMs 和稳定错误类别。
- 不记录 PCM、完整 transcript、完整 AI 回复、最终总结正文或凭证。
- 文本 TTS 日志只记录字符数，不记录 `text`。
- `/api/v1/tts/stream` 应应用请求体上限、并发限制和总合成时限。
- 音频素材路径只从配置读取，不接受客户端任意文件路径。

## 13. 实施边界

当前实现已通过 conversation run migration、`runId` 全链路、幂等 finish、素材流、reset seed 和前端 TTS controller 支持连续演示。上线前仍必须在目标 PostgreSQL 备份或测试库验证 migration up/down，并完成真实 T5 联调。

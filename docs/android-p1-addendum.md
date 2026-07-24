# Android P1 对接补充

> 本文是 [`android-client-design.md`](android-client-design.md) 的 P1 增量。正式语音发生在 T5，不在 Android；OpenAPI 是字段级 REST 契约。

## 1. P1 架构变化

旧的“Android 录音、系统语音识别、系统语音合成”方案已经废弃。正式路径改为：

```text
T5 PCM -> Baomian Device Voice WebSocket -> Volcengine ASR
       -> existing Claude ConversationService
       -> Volcengine TTS -> T5 PCM playback

Android -> REST + state WebSocket -> Tonight / Device / Journal
```

Android 不连接 `/device/voice`，不申请麦克风权限，不上传音频，不朗读 Claude 回复。`POST /conversations/turn` 仅保留给 debug/expo 文字测试。

## 2. Android 本地提醒

- 使用 AlarmManager 按 Profile 的 `timeZone`、`bedtime`、`wakeTime` 调度。
- 开关分别读取 `bedtimeReminderEnabled` 与 `wakeAlarmEnabled`。
- 睡前提醒策略固定为目标时间 `0/+7/+14` 分钟，最多三次。
- `remindersSkipped=true` 时取消当晚后续睡前提醒，但不改变长期设置。
- 时区、Profile 或系统时间变化后重建本地 alarm。
- 不依赖 FCM。

## 3. P1 API

| 方法 | 路径 | Android 用途 |
|---|---|---|
| GET | `/conversations/tonight` | 冷启动、回前台、WS 重连后恢复 turn/deadline/processing |
| POST | `/conversations/activity` | 仅 debug 文字输入活动 |
| POST | `/conversations/turn` | 仅 debug 文字倾诉，正式语音不调用 |
| GET | `/journals/{id}` | 单卡详情 |
| PATCH | `/journals/{id}` | 完成或取消明日待办 |
| DELETE | `/journals/{id}` | 删除历史单卡及当晚对话；409 时刷新状态 |
| GET | `/devices/{deviceId}/status` | 显示 T5 online/lastSeenAt |
| GET | `/device/voice` | T5 专用；Android 禁止连接 |

Profile 新字段：

```kotlin
val timeZone: String
val bedtimeReminderEnabled: Boolean
val wakeAlarmEnabled: Boolean
```

PUT 是 partial update，不必回传未修改字段。

Tonight 新字段：

```kotlin
val remindersSkipped: Boolean
val finalizeReason: String?
val conversationStartedAt: Instant?
val conversationSilenceDeadlineAt: Instant?
val conversationHardDeadlineAt: Instant?
val conversationProcessingUntil: Instant?
val phoneRemovedAt: Instant?
val resumeDeadlineAt: Instant?
val audioEndsAt: Instant?
```

客户端倒计时只用于显示。倒计时到零后调用 `GET /tonight` 或等待 `tonight.updated`，不要本地强制切 phase。

## 4. Conversation 状态

正式流程：

1. Android 通过 `GET /conversations/tonight` 获取轮数和 processing。
2. T5 完成一轮后，Android 可能收到 `conversation.reply` 和 `tonight.updated`。
3. Android 更新文字或状态，但不播放音频。
4. 第三轮后收到 `journal.created`，缓存并导航到晚安日记。
5. WS 事件丢失时，通过 history/journals 恢复。

Debug 文字流程：

1. 生成稳定 UUID `clientRequestId`。
2. POST `/conversations/turn`，`inputMode=text`。
3. 409 `request_in_progress`：短暂等待后 GET history。
4. 409 `conversation_expired`：刷新 Tonight/History。
5. 超时/5xx：复用同 ID 重试，不能生成新 ID。

Debug 页面不得包装成正式语音交互。

## 5. Journal

卡片增加：

```kotlin
val tomorrowTaskCompleted: Boolean
val tomorrowTaskCompletedAt: Instant?
```

- PATCH 使用乐观 UI，失败回滚。
- DELETE 二次确认；204 后从 Room 删除。
- 收到 `journal.created`：upsert 并展示。
- 收到 `journal.updated`：更新缓存。
- 收到 `journal.deleted`：删除缓存。
- 无法定位事件实体时重新 GET journals。

## 6. Device presence

`online` 由后端按 90 秒窗口计算。Android 只展示在线或最近在线，不把该接口当正式绑定或认证。

推荐 capability 展示：

```json
{
  "voiceWebSocket":true,
  "pcm24000Mono":true,
  "builtInGuidance":["rain","brown_noise"]
}
```

App 不根据 capability 自己决定语音 codec，只用于诊断和兼容提示。

## 7. WebSocket 新事件

Android 状态 WebSocket：

- `device.status`
- `journal.updated`
- `journal.deleted`
- 既有 `tonight.updated`、`conversation.reply`、`journal.created`、`device.event`

WebSocket 是提示通道，不是事件存储。断线后至少重新拉取 Profile、Tonight、Conversation History、Journals 和 Device Status。

设备语音 WebSocket 的 `session.ready`、`input.start`、PCM、`playback.start` 等不是 Android 事件，App 不解析。

## 8. P1 错误处理

| code | Android 行为 |
|---|---|
| `request_in_progress` | Debug turn 保持 pending，延迟拉 history |
| `conversation_expired` | 刷新 Tonight/History |
| `journal_not_deletable` | 回滚删除，提示流程结束后再操作 |
| `not_found` | 移除失效卡片或设备引用 |
| 网络/5xx | 使用缓存，提供重试，不无限 loading |

火山引擎 ASR/TTS 错误主要在 T5 连接内返回。Android 不需要复制设备语音错误状态机。

## 9. 隐私

- 正式 Manifest 不申请麦克风权限。
- 不包含火山引擎 ASR App ID/Access Token 或 TTS API Key 或 Claude Token。
- 不缓存原始录音。
- Release 日志和 crash report 不记录完整对话、转写或日记正文。
- 当前仍是 Demo User；Release 前必须替换为正式认证和设备绑定。

# 抱眠 Android 客户端详细设计方案

> 文档版本：P0+P1 Voice / 2026-07-24
> API Base URL：`https://bm.lg.gl/api/v1`
> 状态 WebSocket：`wss://bm.lg.gl/api/v1/ws?userId=<demoUserId>`

## 1. 产品职责

正式产品中，用户和 T5 抱枕进行语音对话；Android 不是语音端。

```text
T5 <-> Baomian Voice WebSocket <-> Volcengine ASR/TTS + Claude
Android <-> REST + state WebSocket <-> settings / tonight / device / journal
```

Android 负责：

- Profile、人格、作息、提醒和引导偏好。
- AlarmManager 本地睡前提醒和起床提醒。
- Tonight phase、已完成轮数和设备在线状态。
- 晚安日记展示、待办完成/取消和单卡删除。
- WebSocket 状态提示与断线后的 REST 对账。
- Debug/Expo 可选文字倾诉和硬件模拟入口。

Android 正式流程不负责：

- 麦克风采集、语音上传和音频帧协议。
- `SpeechRecognizer`、`TextToSpeech`、Claude 回复朗读或声音去重。
- 白噪音和呼吸引导播放。
- T5 command 领取和 ACK。

因此正式 Manifest 不需要麦克风权限；只需网络及本地通知/精确闹钟所需权限（按目标 Android 版本处理）。

## 2. 技术选型

| 领域 | 选型 |
|---|---|
| 语言/UI | Kotlin、Jetpack Compose、Material 3 |
| 架构 | MVVM、Repository、单向数据流、StateFlow |
| DI | Hilt |
| REST | Retrofit + OkHttp |
| JSON | kotlinx.serialization，`ignoreUnknownKeys=true` |
| Realtime | OkHttp WebSocket |
| 缓存 | Room；设置用 DataStore |
| 本地提醒 | AlarmManager + BroadcastReceiver |
| 测试 | JUnit、Turbine、MockWebServer、Compose UI Test |

建议：`minSdk=26`、JVM target 17、单 Activity。正式 App 不需要 Media3 语音播放器；只有独立的无硬件 Demo 模式确有需求时，才放进 debug/expo source set。

## 3. 后端接口覆盖矩阵

| 方法 | 路径 | 正式 Android 职责 |
|---|---|---|
| GET | `/health` | Debug 诊断 |
| GET/PUT | `/profile` | 必须实现 |
| GET | `/tonight` | 必须实现；冷启动、回前台、重连和错误对账 |
| POST | `/tonight/actions` | 正式使用提醒跳过等 App 动作；模拟 action 只在 Debug |
| GET | `/conversations/tonight` | 获取轮数、processing 和历史，正式 UI 可只展示状态 |
| POST | `/conversations/activity` | 仅 Debug 文字倾诉需要 |
| POST | `/conversations/turn` | 仅 Debug 文字倾诉需要；不是正式语音路径 |
| POST | `/conversations/finalize` | Debug/人工提前结束入口，可选 |
| GET | `/journals` | 必须实现，默认 7 张 |
| GET/PATCH/DELETE | `/journals/{id}` | 必须实现详情、待办、删除 |
| GET | `/memories` | `/journals` 别名，不必重复调用 |
| GET | `/devices/{deviceId}/status` | 必须实现在线状态 |
| GET | `/ws?userId=...` | 必须实现 Android 状态 WebSocket |
| POST | `/device/events` | T5；Android 正式 App 不调用 |
| POST | `/device/heartbeat` | T5；Android 正式 App 不调用 |
| GET/POST | `/device/commands/*` | T5；Android 不调用 |
| GET | `/device/voice` | T5；Android 不连接 |

Android 不得领取 T5 command，否则会与固件竞争 lease。

## 4. 身份与环境

当前没有正式登录。REST 请求使用：

```http
X-Demo-User-Id: expo-user-001
```

状态 WebSocket query 使用相同 `userId`。Android 与 T5 必须使用同一个 Demo User，才能看到同一 Tonight 和 Journal。

Build variants：

| Variant | Base URL | Debug 控件 |
|---|---|---:|
| debug | 可配置 localhost/LAN/online | 开启 |
| expo | `https://bm.lg.gl/api/v1/` | 开启 |
| release | `https://bm.lg.gl/api/v1/` | 关闭 |

## 5. 领域模型

```kotlin
@Serializable
enum class Phase {
    WAITING_TO_LOCK,
    LOCKED,
    CONVERSATION,
    CHOOSING_GUIDANCE,
    SLEEPING,
    SUNRISE,
    AWAKE,
    PHONE_REMOVED,
}

@Serializable
enum class Guidance {
    @SerialName("rain") RAIN,
    @SerialName("brown_noise") BROWN_NOISE,
    @SerialName("breathing_46") BREATHING_46,
    @SerialName("silence") SILENCE,
}
```

Tonight 至少包含：

```kotlin
data class Tonight(
    val id: String,
    val date: LocalDate,
    val phase: Phase,
    val bedtime: LocalTime,
    val wakeTime: LocalTime,
    val conversationTurns: Int,
    val selectedGuidance: Guidance?,
    val audioPlaying: Boolean,
    val pausedForTonight: Boolean,
    val remindersSkipped: Boolean,
    val finalizeReason: String?,
    val conversationStartedAt: Instant?,
    val conversationSilenceDeadlineAt: Instant?,
    val conversationHardDeadlineAt: Instant?,
    val conversationProcessingUntil: Instant?,
    val phoneRemovedAt: Instant?,
    val resumeDeadlineAt: Instant?,
    val audioEndsAt: Instant?,
    val boxClosed: Boolean,
    val sunriseProgress: Int,
)
```

约束：

- `conversationTurns` 只展示，不由 Android 自增。
- 倒计时只用于显示；到零后刷新 REST，不本地强制迁移 phase。
- 未知 enum/phase 不得驱动导航，进入可恢复协议错误态并刷新服务器。

## 6. 页面和正式流程

### Tonight

- `WAITING_TO_LOCK`：提示放入手机。
- `LOCKED`：提示 T5 即将或已经发起对话。
- `CONVERSATION`：展示“正在和眠眠聊聊”及 `N/3`，不显示手机录音控件。
- `CHOOSING_GUIDANCE`：短暂过渡；T5 自动采用 Claude 建议。
- `SLEEPING`：低刺激界面，展示当前引导和设备状态。
- `PHONE_REMOVED`：提示重新放回可在窗口内恢复。
- `SUNRISE`：展示晨光状态和 App 侧控制（如产品保留）。
- `AWAKE`：展示昨晚晚安日记。

### Conversation 状态页

正式 App 只显示 T5 在线状态、当前轮次、“眠眠在听/正在回应/已完成”等服务端状态，以及可选持久化文字历史；不承担语音播放。

Debug variant 可提供文字输入，调用 `/conversations/turn`：

```json
{"text":"明天要做汇报，我有点紧张。","inputMode":"text","clientRequestId":"<uuid>"}
```

同一请求重试复用 UUID。

### Journal

展示最近 7 张卡：日期、情绪、主要担忧、明日待办、安慰语、推荐引导。

- PATCH 乐观勾选待办，失败回滚。
- DELETE 必须二次确认；204 后从 Room 删除。
- 409 `journal_not_deletable` 时刷新 Tonight 并提示流程结束后再删。
- 收到 `journal.created/updated/deleted` 后 upsert/delete Room；无法合并时重新 GET。

### Profile

字段：`bedtime`、`wakeTime`、`persona`、`reminderStyle`、`defaultGuidance`、`whiteNoiseDurationMin`、`timeZone`、`bedtimeReminderEnabled`、`wakeAlarmEnabled`。PUT 是 partial update，未发送字段保持不变。

## 7. 本地提醒

- 根据 `timeZone`、`bedtime`、`wakeTime` 使用 AlarmManager。
- 睡前提醒在目标时间 `0/+7/+14` 分钟，最多三次。
- `remindersSkipped=true` 时取消当晚剩余提醒，不改变长期 Profile 开关。
- Profile、系统时间或时区变化后重建 alarm。
- 不依赖 FCM。
- Android 本地起床提醒不是 T5 断网闹钟的替代品；T5 仍应有本地 RTC 兜底。

## 8. REST 接口骨架

```kotlin
interface BaomianApi {
    @GET("health") suspend fun health(): HealthDto
    @GET("profile") suspend fun getProfile(): ProfileDto
    @PUT("profile") suspend fun updateProfile(@Body body: UpdateProfileRequestDto): ProfileDto
    @GET("tonight") suspend fun getTonight(): TonightDto
    @POST("tonight/actions") suspend fun action(@Body body: TonightActionRequestDto): TonightDto

    @GET("conversations/tonight")
    suspend fun conversationHistory(): ConversationHistoryResponseDto

    @POST("conversations/activity")
    suspend fun conversationActivity(@Body body: ConversationActivityRequestDto): TonightDto

    @POST("conversations/turn")
    suspend fun debugConversationTurn(@Body body: ConversationTurnRequestDto): ConversationTurnResponseDto

    @POST("conversations/finalize") suspend fun debugFinalize(): FinalizeResponseDto

    @GET("journals") suspend fun journals(@Query("limit") limit: Int = 7): List<MemoryCardDto>
    @GET("journals/{id}") suspend fun journal(@Path("id") id: String): MemoryCardDto
    @PATCH("journals/{id}") suspend fun updateJournal(
        @Path("id") id: String,
        @Body body: UpdateMemoryCardRequestDto,
    ): MemoryCardDto
    @DELETE("journals/{id}") suspend fun deleteJournal(@Path("id") id: String): Response<Unit>

    @GET("devices/{deviceId}/status")
    suspend fun deviceStatus(@Path("deviceId") deviceId: String): DeviceStatusDto
}
```

Retrofit Base URL 必须以 `/` 结尾，annotation 不以 `/` 开头。

## 9. Android 状态 WebSocket

连接：

```text
wss://bm.lg.gl/api/v1/ws?userId=<URL encoded userId>
```

```kotlin
@Serializable
data class WsEnvelopeDto(
    val type: String,
    val occurredAt: String,
    val data: JsonElement,
)
```

| type | Android 行为 |
|---|---|
| `tonight.updated` | 完整替换 Tonight、缓存并驱动 UI |
| `device.event` | 诊断提示；状态以随后 Tonight 为准 |
| `device.status` | 更新 T5 online/lastSeenAt |
| `conversation.reply` | 更新文字历史/状态，不朗读 |
| `journal.created` | upsert Room 并展示新日记 |
| `journal.updated` | 更新缓存 |
| `journal.deleted` | 删除缓存 |

WebSocket 不是事件存储。冷启动、回前台和每次重连后至少刷新 Tonight、Conversation History、Journals 和 Device Status。重连建议 `1s -> 2s -> 4s -> 8s -> 15s`；网络断开时暂停重连。

Android 绝不连接 `/device/voice`。

## 10. Repository

```kotlin
interface BaomianRepository {
    val tonight: StateFlow<Tonight?>
    val deviceStatus: StateFlow<DeviceStatus?>
    val journals: StateFlow<List<MemoryCard>>
    val connectionState: StateFlow<ConnectionState>

    suspend fun refreshAll(): AppResult<Unit>
    suspend fun getProfile(): AppResult<Profile>
    suspend fun updateProfile(request: UpdateProfile): AppResult<Profile>
    suspend fun skipTonightReminders(): AppResult<Tonight>
    suspend fun updateTask(id: String, completed: Boolean): AppResult<MemoryCard>
    suspend fun deleteJournal(id: String): AppResult<Unit>
    fun connectRealtime()
    fun disconnectRealtime()
}
```

Remote mutation 串行化；REST 成功响应和 WS snapshot 写入同一 StateFlow。Room 缓存 Profile、Tonight、DeviceStatus、最近日记，不缓存原始音频。

## 11. 错误处理

| 状态/code | 行为 |
|---|---|
| 400 `validation_error` | 表单提示，不重试 |
| 404 `not_found` | 移除失效引用并刷新 |
| 409 `request_in_progress` | Debug turn 等待后拉 history |
| 409 `conversation_expired` | 刷新 Tonight/History |
| 409 `journal_not_deletable` | 回滚删除 UI 并刷新 |
| 5xx/timeout | 保留缓存，提供重试 |
| WS 断开 | 显示非阻塞状态，REST 对账 |

T5 语音错误不直接走 Android 的 `/ws` 语音协议。Android 主要根据 `device.status`、Tonight 和最终 Journal 表达可恢复状态。

## 12. 安全与隐私

- APK 不包含 Claude Token、火山引擎 ASR App ID/Access Token 或 TTS API Key、数据库 DSN 或设备密钥。
- Release 禁用 BODY 日志；不记录对话全文和晚安卡正文。
- Crash report 不附带对话或转写。
- 不申请麦克风权限用于正式产品流程。
- Demo User 不是认证；正式上线前接入账号和设备绑定。
- HTTPS 使用系统信任链，不允许忽略证书错误。

## 13. 推荐目录

```text
core/model/          Phase, Guidance, Tonight, Profile, MemoryCard, DeviceStatus
core/network/        BaomianApi, DTO, errors, interceptors
core/realtime/       RealtimeClient, WsModels, ReconnectPolicy
core/database/       Room entities and DAOs
core/datastore/      local preferences and reminder metadata
core/reminder/       AlarmManager scheduler and receivers
data/                repository interfaces and implementations
feature/tonight/
feature/journal/
feature/profile/
feature/diagnostics/ # debug/expo only
```

## 14. 测试

- DTO/Domain：全部 phase、guidance、可选 deadline 和未知字段。
- Repository：REST/WS 重复结果、Room upsert、mutation 串行化。
- WebSocket：全部状态事件、未知事件忽略、重连后 refreshAll。
- AlarmManager：时区、0/+7/+14、今晚跳过、Profile 变化重建。
- Journal：PATCH 乐观回滚、DELETE 204/404/409。
- Device status：90 秒 online 只展示后端结果，不本地伪造绑定。
- UI：全部 phase、大字体、离线、T5 离线、journal.created。

## 15. 开发顺序

1. Kotlin/Compose/Hilt/Retrofit/serialization/Room/DataStore 骨架。
2. Profile、Tonight、Journal、DeviceStatus DTO 和 Repository。
3. Tonight/Profile/Journal UI。
4. AlarmManager 本地提醒。
5. Android 状态 WebSocket 和 REST 对账。
6. Debug 文字倾诉和模拟 action（仅 debug/expo）。
7. 与真实 T5 联调：同一 userId 下验证闭仓、三轮进度、journal.created 和引导状态。

## 16. Definition of Done

1. 正式 App 不申请麦克风、不执行 STT/TTS、不播放对话音频。
2. T5 闭仓/开仓和 phase 在 Android 及时更新。
3. Conversation 页面正确显示 0–3 轮状态。
4. 第三轮后自动收到并展示晚安日记。
5. Profile 和本地提醒可配置并跨时区恢复。
6. Journal 可查看、勾选待办、取消勾选和删除历史卡。
7. T5 online/lastSeenAt 可见。
8. 冷启动、回前台、WS 重连均 REST 对账。
9. 网络、5xx、WS 断线和 App 杀进程不崩溃。
10. Release 日志无对话正文、晚安卡正文和凭证。

P1 字段和行为增量见 [`android-p1-addendum.md`](android-p1-addendum.md)；字段级契约以 [`../api/openapi.yaml`](../api/openapi.yaml) 为准；T5 语音协议由硬件文档负责。

# 抱眠 Android 客户端详细设计方案

> 文档版本：P0 / 2026-07-23
>
> 后端基线：当前已部署并验证的 Golang 后端
>
> 线上 API Base URL：`https://bm.lg.gl/api/v1`
>
> 目标平台：Android 手机，P0 主演示端

---

## 1. 文档目标

本文档定义抱眠 Android P0 客户端的产品流程、技术架构、模块边界、页面状态、后端协议、WebSocket 同步、离线演示、音频、安全、测试和开发顺序，使 Android、后端和硬件能够基于同一套已实现契约并行联调。

本方案的核心原则：

1. **服务端状态是联网模式下唯一事实来源**。客户端不复制服务端状态机，也不自行推测状态迁移。
2. **完整竖切优先**。先完成“锁仓 → 倾诉 → 晚安卡 → 引导 → 入睡 → 晨光 → 起床”。
3. **真实链路与 Demo 链路共用 UI 和领域模型**。只替换 Repository，不为 Demo 单独写一套页面。
4. **任何故障都不能无限 loading**。网络、AI、WebSocket 或硬件异常时，用户在 10 秒内获得重试或切换演示模式的路径。
5. **不在 Android 中保存 Anthropic Token、数据库凭证或设备密钥**。客户端只访问抱眠后端。
6. **P0 不实现后端尚未支持的功能**，包括正式登录、设备绑定、日记删除、真实后台提醒调度和云端音频下载。

---

## 2. 当前后端基线

Android 只对接以下线上入口：

```text
https://bm.lg.gl/api/v1
```

WebSocket：

```text
wss://bm.lg.gl/api/v1/ws?userId=<demoUserId>
```

已实现接口：

| 方法 | 路径 | Android 用途 |
|---|---|---|
| GET | `/health` | Debug/诊断页检查后端和数据库 |
| GET | `/profile` | 获取并初始化设置 |
| PUT | `/profile` | 保存设置 |
| GET | `/tonight` | 启动、恢复前台、WS 重连后获取完整状态 |
| POST | `/tonight/actions` | 演示动作、引导选择、停止音频、模拟晨光、贪睡、起床 |
| POST | `/conversations/turn` | 提交一轮文字倾诉 |
| POST | `/conversations/finalize` | 用户主动提前结束倾诉 |
| GET | `/journals?limit=7` | 最近晚安卡 |
| GET | `/memories?limit=7` | 与 journals 等价；Android 只使用 journals |
| GET | `/ws?userId=...` | 接收硬件和其他端造成的状态变化 |

设备网关接口由 T5 固件使用，Android P0 不领取或 ACK 硬件命令，也不冒充真实硬件上报事件。Debug 构建通过 `/tonight/actions` 模拟状态即可。

### 2.1 后端操作覆盖矩阵

下表是当前 Router 中全部 12 个操作。文档不省略任何后端操作，但会区分 Android 正常业务、Android Debug/诊断和 T5 固件职责：

| # | 操作 | 当前调用方 | Android 实现要求 | 本文位置 |
|---:|---|---|---|---|
| 1 | `GET /health` | Android Debug/运维 | 实现 Retrofit 方法；诊断页和启动故障排查使用，不作为正常启动前置条件 | §7.1、§14 |
| 2 | `GET /profile` | Android | 必须实现 | §6.9、§7.1 |
| 3 | `PUT /profile` | Android | 必须实现 | §6.9、§7.1 |
| 4 | `GET /tonight` | Android | 必须实现；启动、前台恢复、409 和 WS 重连后调用 | §6.3、§7.1、§8 |
| 5 | `POST /tonight/actions` | Android | 必须实现全部 8 个 action | §6、§7.1、§2.2 |
| 6 | `POST /conversations/turn` | Android | 必须实现 | §6.4、§7.1 |
| 7 | `POST /conversations/finalize` | Android | 必须实现；当前请求无需 JSON body | §6.4、§7.1 |
| 8 | `GET /journals` | Android | 必须实现，默认 `limit=7` | §6.8、§7.1 |
| 9 | `GET /memories` | Android 可选 | 与 `/journals` 使用同一后端逻辑；P0 不重复调用，仅保留可选方法 | §7.1 |
| 10 | `GET /ws` | Android | 必须实现 OkHttp WebSocket | §8 |
| 11 | `POST /device/events` | T5；Android Debug 可选 | 正常 APP 禁止调用；可在独立硬件模拟器中实现 | §2.3 |
| 12 | `GET /device/commands/next` | T5 | Android APP 不实现；由固件长轮询 | §2.3 |
| 13 | `POST /device/commands/ack` | T5 | Android APP 不实现；由固件 ACK | §2.3 |

> Router 实际有 13 条 method + path 组合，其中业务资源共 12 类入口（Profile 的 GET/PUT 分成两个操作）。Android 应用开发者只需实现 1–10；11–13 的协议已完整说明，避免误将固件职责漏判为 APP 缺失。

### 2.2 `/tonight/actions` 全量覆盖

Android 的 `TonightAction` 必须覆盖后端已实现的全部 action：

| action | 正常 UI 或 Debug 入口 | 必要参数 | 合法状态/结果摘要 |
|---|---|---|---|
| `simulate_box_closed` | Expo“模拟闭仓” | 无 | `WAITING_TO_LOCK → LOCKED`；或从 `PHONE_REMOVED` 恢复 |
| `simulate_box_opened` | Expo“模拟开仓” | 无 | 睡前有效阶段进入 `PHONE_REMOVED` |
| `start_conversation` | “和眠眠说说今天” | 无 | `LOCKED → CONVERSATION`；首个 turn 也能自动进入 Conversation |
| `select_guidance` | Guidance 四选一 | `guidance` 必填 | `CHOOSING_GUIDANCE/LOCKED → SLEEPING` |
| `stop_audio` | Sleeping“停止声音”、Expo 安静键 | 无 | 睡前有效阶段进入/保持 `SLEEPING`，`audioPlaying=false` |
| `simulate_alarm` | Expo“模拟闹钟” | 无 | 非 `AWAKE` 状态进入 `SUNRISE` |
| `snooze` | Sunrise“再睡 5 分钟” | 无 | 仅 `SUNRISE` 合法，仍停留 Sunrise |
| `mark_awake` | Sunrise“我醒了” | 无 | `SUNRISE → AWAKE` |

推荐密封类型：

```kotlin
sealed interface TonightAction {
    data object SimulateBoxClosed : TonightAction
    data object SimulateBoxOpened : TonightAction
    data object StartConversation : TonightAction
    data class SelectGuidance(val guidance: Guidance) : TonightAction
    data object StopAudio : TonightAction
    data object SimulateAlarm : TonightAction
    data object Snooze : TonightAction
    data object MarkAwake : TonightAction
}
```

### 2.3 设备接口在 Android 工程中的边界

虽然 Android 正常业务不调用设备接口，但开发者需要知道其存在，以理解 T5 事件为何会通过 WebSocket 改变 APP 状态：

```text
T5 POST /device/events
  → 后端状态机与命令队列
  → WebSocket tonight.updated
  → Android 整体替换 Tonight
```

如团队需要 Android 版“硬件模拟器”，必须放在 `debug`/`expo` source set 或独立工具 App 中，并完整支持：

```http
POST /device/events
GET  /device/commands/next?deviceId=...&timeoutSec=20
POST /device/commands/ack
```

但主演示 APP 不得领取真实 T5 的命令，否则会和固件竞争 `pending` 命令，并把命令提前标成 `dispatched`。

## 2.4 P0 身份

当前没有正式登录。所有 APP REST 请求携带：

```http
X-Demo-User-Id: expo-user-001
```

建议 Debug/Expo 构建允许在开发者设置中覆盖 `demoUserId`，Release 演示包固定为团队约定值。WebSocket query 中必须使用同一个值。

> 当前 `X-Demo-User-Id` 和 WebSocket `userId` 都不是安全认证。客户端不得将其当作正式账号体系。

---

## 3. Android 技术选型

### 3.1 推荐技术栈

| 领域 | 选型 | 说明 |
|---|---|---|
| 语言 | Kotlin | 全 Kotlin 工程 |
| UI | Jetpack Compose + Material 3 | 单 Activity、声明式状态驱动 |
| 导航 | Navigation Compose | 根导航 + 状态驱动自动跳转 |
| 架构 | MVVM + Repository + 单向数据流 | ViewModel 暴露不可变 `StateFlow` |
| DI | Hilt | 网络、Repository、DataStore、播放器统一注入 |
| REST | Retrofit + OkHttp | 与当前 JSON REST 契约匹配 |
| JSON | kotlinx.serialization | DTO 显式序列化，未知字段兼容 |
| WebSocket | OkHttp WebSocket | 复用 OkHttp TLS 与连接池 |
| 本地设置 | DataStore Preferences | Demo 用户、模式、引导偏好、引导完成状态 |
| 本地结构化缓存 | Room | 缓存 Profile、Tonight snapshot、最近 7 张晚安卡 |
| 音频 | AndroidX Media3 ExoPlayer | 播放本地雨声、棕噪音、呼吸音轨 |
| 生命周期 | Lifecycle + `collectAsStateWithLifecycle` | 避免后台无效收集 |
| 后台任务 | P0 不依赖 WorkManager | P1 再加入提醒、同步和真实闹钟 |
| 日志 | Timber 或封装 Android Log | Release 禁止记录对话全文 |
| 测试 | JUnit、Turbine、MockWebServer、Compose UI Test | 单元、契约、UI 覆盖 |

### 3.2 Android 版本建议

```text
minSdk = 26
compileSdk = 当前稳定版本
Kotlin JVM target = 17
```

原因：P0 不需要兼容过旧设备；API 26 可简化日期、音频、TLS 和前后台行为。若团队已有最低版本要求，以团队设备矩阵为准。

### 3.3 Build Variants

建议建立：

| Variant | Base URL | Demo 控件 | 用途 |
|---|---|---:|---|
| `debug` | 可在开发者设置切换线上/局域网 | 显示 | 日常开发 |
| `expo` | `https://bm.lg.gl/api/v1/` | 显示 | 现场展示 APK |
| `release` | `https://bm.lg.gl/api/v1/` | 隐藏 | 后续正式包 |

示例 BuildConfig：

```kotlin
buildConfigField("String", "API_BASE_URL", "\"https://bm.lg.gl/api/v1/\"")
buildConfigField("String", "WS_BASE_URL", "\"wss://bm.lg.gl/api/v1/ws\"")
buildConfigField("boolean", "DEMO_CONTROLS_ENABLED", "true")
```

Retrofit Base URL 必须以 `/` 结尾。

---

## 4. 总体架构

```text
MainActivity
  └── BaomianApp / NavHost
        ├── TonightRoute
        ├── ConversationRoute
        ├── GuidanceRoute
        ├── SleepingRoute
        ├── SunriseRoute
        ├── JournalRoute
        └── ProfileRoute

Compose UI
  ↓ UI Action                         ↑ UiState / UiEffect
ViewModel
  ↓ Use Case / Repository             ↑ Domain Model / Result
BaomianRepository
  ├── RemoteBaomianRepository
  │     ├── BaomianApi (Retrofit)
  │     ├── RealtimeDataSource (WebSocket)
  │     └── Room cache
  └── DemoBaomianRepository
        ├── Demo state reducer
        ├── Fixed AI fixtures
        └── Room/DataStore snapshot

Local services
  ├── GuidancePlayer (Media3)
  ├── PreferencesStore (DataStore)
  └── ConnectivityObserver
```

### 4.1 推荐工程目录

```text
app/src/main/java/.../baomian/
  app/
    BaomianApplication.kt
    MainActivity.kt
    AppNavHost.kt
    AppDestination.kt
  core/
    model/
      Phase.kt
      Guidance.kt
      Tonight.kt
      Profile.kt
      MemoryCard.kt
    network/
      BaomianApi.kt
      ApiModels.kt
      ApiErrorParser.kt
      DemoUserInterceptor.kt
      NetworkModule.kt
    realtime/
      RealtimeClient.kt
      WsModels.kt
      ReconnectPolicy.kt
    database/
      BaomianDatabase.kt
      TonightDao.kt
      MemoryCardDao.kt
      Entities.kt
    datastore/
      PreferencesStore.kt
    audio/
      GuidancePlayer.kt
      Media3GuidancePlayer.kt
    common/
      AppResult.kt
      DispatcherProvider.kt
  data/
    BaomianRepository.kt
    RemoteBaomianRepository.kt
    DemoBaomianRepository.kt
    RepositoryCoordinator.kt
  feature/
    tonight/
    conversation/
    guidance/
    sleeping/
    sunrise/
    journal/
    profile/
    diagnostics/          # debug/expo only
  ui/
    theme/
    components/
  sync/
    AppSessionCoordinator.kt
```

P0 可先单 module，按 package 分层；不要过早拆成大量 Gradle modules。P1 稳定后再抽 `core-network`、`core-data` 等模块。

---

## 5. 领域模型

客户端使用强类型 enum，网络 DTO 与领域模型分离。

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

### 5.1 Tonight 领域模型

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
    val boxClosed: Boolean,
    val sunriseProgress: Int,
    val latestAiDraft: AiDraft?,
)
```

约束：

- `conversationTurns` 仅展示，客户端不自行增加。
- `selectedGuidance` 在 JSON 缺失或空字符串时映射为 `null`。
- `sunriseProgress` 以服务端为准；当前后端只在状态迁移时给出 0/100，不提供连续 25 分钟实时进度。P0 APP 若展示渐变，应标记为本地视觉演示，而不是服务端真实硬件进度。
- `latestAIDraft` 只用于恢复摘要和调试；聊天气泡不依赖它重建完整历史，因为后端目前没有“读取会话历史”接口。

### 5.2 UI 状态与一次性事件

```kotlin
data class TonightUiState(
    val initializing: Boolean = true,
    val mode: RepositoryMode = RepositoryMode.REMOTE,
    val connection: ConnectionState = ConnectionState.Connecting,
    val tonight: Tonight? = null,
    val profile: Profile? = null,
    val latestJournal: MemoryCard? = null,
    val pendingAction: PendingAction? = null,
    val banner: UiMessage? = null,
)

sealed interface UiEffect {
    data class Navigate(val destination: AppDestination) : UiEffect
    data class ShowSnackbar(val message: String) : UiEffect
    data class PlayGuidance(val guidance: Guidance) : UiEffect
    data object StopAudio : UiEffect
}
```

导航不应完全依赖一次性 Effect。应用恢复时应根据 `Tonight.phase` 计算当前主页面；Effect 只用于 Snackbar、聚焦、键盘等一次行为。

---

## 6. 页面与信息架构

### 6.1 根导航

建议底部导航只在非沉浸阶段显示：

1. **今晚**
2. **晚安日记**
3. **我的抱眠**

以下阶段隐藏底部导航，使用沉浸式流程：

- `CONVERSATION`
- `CHOOSING_GUIDANCE`
- `SLEEPING`
- `SUNRISE`

### 6.2 Phase 到页面映射

| 服务端 Phase | 主页面 | 页面目标 |
|---|---|---|
| `WAITING_TO_LOCK` | Tonight | 提示将手机放入抱眠；Expo 可模拟闭仓 |
| `LOCKED` | Tonight/Locked | 显示“手机已安放”，提供“开始倾诉” |
| `CONVERSATION` | Conversation | 最多 3 轮文字倾诉 |
| `CHOOSING_GUIDANCE` | Guidance | 四选一，突出 AI 推荐项 |
| `SLEEPING` | Sleeping | 纯黑低刺激界面；显示必要的停止音频入口 |
| `SUNRISE` | Sunrise | 晨光渐变、贪睡和起床 |
| `AWAKE` | Tonight/Awake | 展示“早安”与最近晚安卡入口 |
| `PHONE_REMOVED` | PhoneRemoved overlay | 提示手机已取出，等待重新闭仓恢复 |

### 6.3 Tonight 页面

内容：

- 当前日期、睡前时间、起床时间。
- 设备状态：未锁仓、已锁仓、手机取出。
- 当晚进度步骤条：安放 → 倾诉 → 引导 → 入睡 → 晨光。
- 主 CTA 根据 Phase 变化。
- Remote/Demo 模式状态标签，仅 Debug/Expo 显示。
- 演示控制区，仅 Debug/Expo 显示：
  - 模拟闭仓
  - 模拟开仓
  - 模拟闹钟
  - 短按安静键等价操作：`stop_audio` 或 `snooze`
  - 标记起床

主 CTA：

| Phase | CTA |
|---|---|
| `WAITING_TO_LOCK` | “等待手机安放”或“模拟安放” |
| `LOCKED` | “和眠眠说说今天” |
| `CONVERSATION` | “继续倾诉” |
| `CHOOSING_GUIDANCE` | “选择今晚的陪伴” |
| `SLEEPING` | “进入睡眠界面” |
| `SUNRISE` | “查看晨光” |
| `AWAKE` | “查看昨晚的晚安卡” |
| `PHONE_REMOVED` | 禁用 CTA，提示重新闭仓 |

### 6.4 Conversation 页面

布局：

- 顶部：返回/关闭、`第 N / 3 轮`。
- 中部：仅展示本次 APP 进程中的用户和眠眠消息。
- 底部：单行或最多 4 行文字输入、发送按钮、“今晚先到这里”。
- AI 请求中显示低刺激 loading，例如“眠眠在听……”，不使用快速旋转动画。

交互规则：

1. 输入 trim 后为空不能发送。
2. 请求期间禁用再次发送和 finalize，防止并发创建两轮。
3. 客户端超时建议 35 秒，略大于当前后端 AI 总超时 30 秒。
4. 收到响应后整体替换 `Tonight`。
5. `result.shouldFinalize=true` 或 `tonight.phase=CHOOSING_GUIDANCE` 时，展示 AI 回复 800–1500ms 后进入 Guidance。
6. `result.highRisk=true` 时使用高可见但不刺激的安全卡；P0 服务端会直接给固定安全回复。
7. `result.fallback=true` 仅在 Debug/Expo 显示“离线安抚模式”，对普通用户不展示技术错误。
8. 达到 3 轮后隐藏输入框。

提前结束：

```http
POST /conversations/finalize
```

成功后保存返回的 `journal`，更新 Tonight，并进入 Guidance。

恢复边界：后端暂未提供历史消息读取接口。因此 APP 被系统杀死后重新进入 `CONVERSATION` 时：

- 展示 `conversationTurns` 和 `latestAIDraft.reply`（若有）。
- 提示“可以接着说，也可以今晚先到这里”。
- 不伪造此前聊天气泡。

### 6.5 Guidance 页面

固定四项，顺序必须和服务端一致：

1. `rain`：雨声
2. `brown_noise`：棕色噪音
3. `breathing_46`：4–6 呼吸
4. `silence`：安静入睡

请求：

```json
{
  "action": "select_guidance",
  "guidance": "breathing_46"
}
```

UI：

- 推荐项依据 `latestAiDraft.suggestedGuidance` 或本轮 AI result。
- 推荐只作为视觉提示，不自动替用户选择。
- 请求成功且服务端进入 `SLEEPING` 后，再启动 APP 本地音频或进入静默页。
- 如果真实 T5 已连接，T5 也会领取同类音频命令。为了避免手机和硬件同时播放，Expo 设置提供 `AudioOutputMode`：
  - `PHONE`：手机本地播放，用于无硬件演示。
  - `DEVICE`：手机不播放，等待 T5。
  - P0 默认由现场演示配置决定，不自动探测，因为后端尚无设备在线状态接口。

### 6.6 Sleeping 页面

视觉原则：

- 默认纯黑背景。
- 不显示循环动画、明亮按钮或持续变化文本。
- 可使用低亮度文字显示当前引导。
- 保留隐藏/低亮度的“停止声音”操作。
- 不强制锁屏、不修改系统安全设置。

停止声音：

```json
{"action":"stop_audio"}
```

收到成功状态后调用 `GuidancePlayer.stop()`。即使网络失败，用户点击停止时也应先立即停止本地音频，再异步尝试同步服务端，避免声音无法停止。

### 6.7 Sunrise 页面

P0 展示：

- 红 → 橙 → 暖黄渐变。
- “再睡 5 分钟”：`snooze`。
- “我醒了”：`mark_awake`。

当前后端不自动推送连续晨光进度，只有 `sunrise.progress` 状态字段。因此：

- 真实硬件模式：APP 只展示“晨光进行中”，不宣称与硬件亮度精确同步。
- Expo 模式：可用本地 25 秒加速动画模拟 25 分钟，但不回写虚假进度到后端。
- `mark_awake` 成功后进入 Awake/Journal。

### 6.8 Journal 页面

调用：

```http
GET /journals?limit=7
```

展示每张卡：

- 日期
- 今晚情绪
- 主要烦恼
- 明日待办
- 晚安语
- 推荐引导

P0 只读，不提供编辑、删除、待办完成标记，因为后端尚无对应接口。

### 6.9 Profile 页面

字段与后端完全一致：

| 字段 | UI 控件 | 合法值 |
|---|---|---|
| `bedtime` | 时间选择器 | `HH:mm` |
| `wakeTime` | 时间选择器 | `HH:mm` |
| `persona` | 单选卡 | `gentle` / `rational` / `firm` |
| `reminderStyle` | 单选 | `gentle` / `firm` |
| `defaultGuidance` | 单选 | 四种 Guidance |
| `whiteNoiseDurationMin` | 单选 | 10 / 20 / 30 |

当前 AI Prompt 主要实现柔软陪伴型；`rational` 和 `firm` 虽能由后端接收，但 P0 文案差异需要联调验证，不应在客户端承诺完整人格体验。

---

## 7. REST 网络层设计

### 7.1 Retrofit 接口

```kotlin
interface BaomianApi {
    @GET("health")
    suspend fun health(): HealthDto

    @GET("profile")
    suspend fun getProfile(): ProfileDto

    @PUT("profile")
    suspend fun updateProfile(@Body body: ProfileDto): ProfileDto

    @GET("tonight")
    suspend fun getTonight(): TonightDto

    @POST("tonight/actions")
    suspend fun action(@Body body: TonightActionRequestDto): TonightDto

    @POST("conversations/turn")
    suspend fun conversationTurn(
        @Body body: ConversationTurnRequestDto,
    ): ConversationTurnResponseDto

    @POST("conversations/finalize")
    suspend fun finalizeConversation(): FinalizeResponseDto

    @GET("journals")
    suspend fun getJournals(@Query("limit") limit: Int = 7): List<MemoryCardDto>

    // 与 journals 是后端别名。P0 正常业务只调用 getJournals；保留用于契约完整性。
    @GET("memories")
    suspend fun getMemories(@Query("limit") limit: Int = 7): List<MemoryCardDto>
}
```

Retrofit Base URL 已包含 `/api/v1/`，所以 annotation 中不能再写 `/api/v1`，且不能以 `/` 开头，否则 Retrofit 会将其当成 host 根路径。

### 7.2 可直接使用的网络 DTO

```kotlin
@Serializable
data class HealthDto(
    val status: String,
    val database: String,
)

@Serializable
data class ProfileDto(
    val bedtime: String,
    val wakeTime: String,
    val persona: String,
    val reminderStyle: String,
    val defaultGuidance: String,
    val whiteNoiseDurationMin: Int,
)

@Serializable
data class DeviceStateDto(val boxClosed: Boolean)

@Serializable
data class SunriseStateDto(val progress: Int)

@Serializable
data class TonightDto(
    val id: String,
    val date: String,
    val phase: String,
    val bedtime: String,
    val wakeTime: String,
    val conversationTurns: Int,
    val selectedGuidance: String? = null,
    val audioPlaying: Boolean,
    val pausedForTonight: Boolean,
    val device: DeviceStateDto,
    val sunrise: SunriseStateDto,
    val latestAIDraft: JsonObject? = null,
)

@Serializable
data class TonightActionRequestDto(
    val action: String,
    val guidance: String? = null,
    val payload: JsonObject? = null,
)

@Serializable
data class ConversationTurnRequestDto(
    val text: String,
    val inputMode: String = "text",
)

@Serializable
data class AiResultDto(
    val reply: String,
    val emotion: String,
    val worry: String,
    val tomorrowTask: String,
    val comfort: String,
    val guidanceOptions: List<String>,
    val suggestedGuidance: String,
    val shouldFinalize: Boolean,
    val fallback: Boolean,
    val highRisk: Boolean = false,
)

@Serializable
data class MemoryCardDto(
    val id: String,
    val date: String,
    val emotion: String,
    val worry: String,
    val tomorrowTask: String,
    val comfort: String,
    val suggestedGuidance: String,
    val fallback: Boolean,
    val createdAt: String,
)

@Serializable
data class ConversationTurnResponseDto(
    val result: AiResultDto,
    val tonight: TonightDto,
    val journal: MemoryCardDto? = null,
)

@Serializable
data class FinalizeResponseDto(
    val journal: MemoryCardDto,
    val tonight: TonightDto,
)

@Serializable
data class ErrorEnvelopeDto(val error: ErrorDetailDto)

@Serializable
data class ErrorDetailDto(
    val code: String,
    val message: String,
    val details: JsonObject? = null,
)
```

后端时间字段：

- `date`：ISO LocalDate，例如 `2026-07-23`。
- `createdAt` / WS `occurredAt`：RFC 3339；使用 `Instant.parse` 或 kotlinx-datetime。
- `bedtime` / `wakeTime`：严格 `HH:mm`。

### 7.3 DTO 校验要求

转换为 Domain 前必须检查：

- `phase` 属于 8 个已知值。
- `conversationTurns` 在 0～3。
- `sunrise.progress` 在 0～100。
- AI `guidanceOptions` 必须严格为 `rain, brown_noise, breathing_46, silence`。
- `suggestedGuidance` 属于 Guidance。
- Profile 时长仅为 10/20/30。

不满足时返回 `AppError.Protocol`，不能让错误数据直接驱动导航或音频。

### 7.4 OkHttp 配置

建议：

```text
connectTimeout = 10s
readTimeout = 35s
writeTimeout = 15s
callTimeout = 40s（Conversation）
```

由于普通与 AI 请求超时不同，可采用：

- 默认 OkHttp：15 秒 read timeout。
- Conversation 专用 client 或 `@Tag` interceptor：40 秒 call timeout。
- WebSocket 使用同一个连接池，但不继承普通 read timeout 的语义。

Interceptor：

1. `DemoUserInterceptor`：注入 `X-Demo-User-Id`。
2. `RequestIdInterceptor`：生成 UUID，注入 `X-Request-Id`，便于和服务端日志关联。
3. Debug logging：只记录 method、path、status、duration、request ID；对 `/conversations/*` 不记录 body。
4. Release 禁用 BODY 级日志。

### 7.5 JSON 配置

```kotlin
val json = Json {
    ignoreUnknownKeys = true
    explicitNulls = false
    encodeDefaults = true
    coerceInputValues = true
}
```

`ignoreUnknownKeys=true` 用于后端增量增加字段时保持兼容，但 enum 遇到未知值不能静默映射错误状态。建议 Phase DTO 先按字符串接收，再显式转换；未知 Phase 进入 `UnsupportedServerState` 错误页并触发诊断日志。

### 7.6 错误解析

后端错误格式：

```json
{
  "error": {
    "code": "invalid_transition",
    "message": "当前状态不允许此操作",
    "details": {}
  }
}
```

客户端错误类型：

```kotlin
sealed interface AppError {
    data object Offline : AppError
    data object Timeout : AppError
    data class Validation(val message: String) : AppError
    data class Conflict(val message: String) : AppError
    data class NotFound(val message: String) : AppError
    data object ServerUnavailable : AppError
    data class Protocol(val cause: Throwable) : AppError
}
```

HTTP 映射：

| 状态 | 映射 | UI 行为 |
|---|---|---|
| 400 | Validation | 表单内提示，不重试 |
| 404 | NotFound | 刷新完整状态，必要时返回 Tonight |
| 409 | Conflict | `GET /tonight` 对账后提示“状态已更新” |
| 500/502/503 | ServerUnavailable | 提供重试或 Demo 模式 |
| timeout/IO | Offline/Timeout | 保留输入，允许重试 |

所有 mutation 失败后，客户端不得乐观地永久改变服务端状态。特别是 `select_guidance`、`finalize`、`mark_awake`，失败时应恢复按钮并调用 `GET /tonight` 对账。

---

## 8. WebSocket 设计

### 8.1 连接时机

- APP 完成初始化并获得 `demoUserId` 后连接。
- 仅当前台保持连接；进入后台可在短延迟后关闭，以节省资源。
- 回到前台时先 `GET /tonight`，再连接 WS。
- URL：`wss://bm.lg.gl/api/v1/ws?userId=<URL encoded userId>`。

### 8.2 WebSocket 建连代码骨架

```kotlin
class RealtimeClient(
    private val okHttpClient: OkHttpClient,
    private val json: Json,
    private val wsBaseUrl: String,
) {
    fun connect(userId: String, listener: WebSocketListener): WebSocket {
        val url = wsBaseUrl.toHttpUrl().newBuilder()
            .addQueryParameter("userId", userId)
            .build()
        val request = Request.Builder().url(url).build()
        return okHttpClient.newWebSocket(request, listener)
    }
}
```

WebSocket 不使用 `X-Demo-User-Id` 识别用户，当前后端只读取 query 参数 `userId`。连接成功后服务端不主动发送初始 snapshot，因此客户端必须先或同时调用 `GET /tonight`。

### 8.3 WebSocket 信封与事件解析

所有事件都有：

```kotlin
@Serializable
data class WsEnvelopeDto(
    val type: String,
    val occurredAt: String,
    val data: JsonElement,
)
```

按 `type` 再解码 `data`：

```kotlin
when (envelope.type) {
    "tonight.updated" -> json.decodeFromJsonElement<TonightDto>(envelope.data)
    "device.event" -> json.decodeFromJsonElement<DeviceEventNoticeDto>(envelope.data)
    "conversation.reply" -> json.decodeFromJsonElement<ConversationTurnResponseDto>(envelope.data)
    "journal.created" -> json.decodeFromJsonElement<MemoryCardDto>(envelope.data)
    "error" -> envelope.data // 当前契约未固定 data schema，按 JsonElement 处理
    else -> ignoreAndLogProtocolEvent(envelope.type)
}
```

```kotlin
@Serializable
data class DeviceEventNoticeDto(
    val eventId: String,
    val type: String,
    val deviceId: String,
)
```

未知 WS 事件必须忽略并记录，不得断开连接或使 APP 崩溃。

### 8.4 事件类型

| 事件 | data | Android 行为 |
|---|---|---|
| `tonight.updated` | `TonightState` | 整体替换 Remote Tonight、持久化、驱动导航 |
| `device.event` | eventId/type/deviceId | 仅用于轻提示或诊断；随后状态以 `tonight.updated` 为准 |
| `conversation.reply` | ConversationTurnResponse | REST 请求已返回时去重；不重复添加气泡或播放音频 |
| `journal.created` | MemoryCard | upsert Room 缓存 |
| `error` | 动态数据 | Debug 记录，普通 UI 使用通用提示 |

### 8.5 状态合并原则

1. `tonight.updated` 是完整 snapshot，直接替换，不做字段 patch。
2. 每次 REST mutation 成功也更新同一 `StateFlow`。
3. REST 与 WS 可能重复送达同一结果。使用 `Tonight.id + updated content` 去重即可，不触发重复导航/音频。
4. `conversation.reply` 用 `tonight.id + conversationTurns + reply` 生成进程内去重 key。
5. 音频启动只由“用户成功选择 Guidance”的本地命令触发，不能仅凭重复 `SLEEPING` snapshot 重复播放。

### 8.6 重连策略

```text
1s → 2s → 4s → 8s → 15s → 15s...
```

规则：

- 网络不可用时暂停重连，等待 Connectivity callback。
- 每次连接成功后重置退避。
- 每次重连成功立刻 `GET /tonight`，不要假设断线期间没有事件。
- 连续失败 10 秒后展示非阻塞提示：“实时连接暂时中断，正在使用最近状态”。
- 不因 WS 中断自动切换 Demo；只有 REST 也持续不可用且用户明确选择时才切换。

---

## 9. Repository 与离线演示模式

### 9.1 统一接口

```kotlin
interface BaomianRepository {
    val tonight: StateFlow<Tonight?>
    val connectionState: StateFlow<ConnectionState>
    val mode: StateFlow<RepositoryMode>

    suspend fun refreshTonight(): AppResult<Tonight>
    suspend fun getProfile(): AppResult<Profile>
    suspend fun updateProfile(profile: Profile): AppResult<Profile>
    suspend fun performAction(action: TonightAction): AppResult<Tonight>
    suspend fun sendTurn(text: String): AppResult<ConversationResponse>
    suspend fun finalizeConversation(): AppResult<FinalizeResult>
    suspend fun getJournals(limit: Int = 7): AppResult<List<MemoryCard>>
    fun connectRealtime()
    fun disconnectRealtime()
}
```

### 9.2 RemoteRepository

职责：

- REST 调用和 DTO 转换。
- Room 快照缓存。
- WebSocket 连接和事件合并。
- mutation 串行化。
- 网络错误分类。
- 不包含 Compose、导航和 Android View 引用。

### 9.3 DemoRepository

DemoRepository 不是 Remote 的“失败后自动偷偷替代”，而是明确模式：

- 初始状态 `WAITING_TO_LOCK`。
- 内部实现与服务端同名 Phase 和 action。
- 使用固定延迟 300–800ms 模拟网络/AI。
- 固定生成完整 AIResult 和 MemoryCard。
- 所有数据存 Room/DataStore，APP 重启可继续。
- Debug/Expo 显示“演示模式”标签。

切换策略：

1. 首次启动默认 Remote。
2. Remote 初始化失败时展示：
   - “重试连接”
   - “进入离线演示”
3. 不自动将真实服务器错误与 Demo 数据混合。
4. 从 Demo 切回 Remote 时先确认，并以服务端 `GET /tonight` 完整覆盖 Demo 状态。

### 9.4 本地缓存策略

Room 表：

```text
cached_profile       // 单行，按 demoUserId
cached_tonight       // 单行，按 demoUserId
cached_memory_cards  // 最近 7～30 张，按 id upsert
demo_session         // DemoRepository 专用，不上传服务器
```

敏感性：对话全文不写 Room；只在当前进程内存中展示。后端返回的晚安卡可以缓存。P0 不缓存原始录音。

---

## 10. 状态驱动与并发控制

### 10.1 客户端不复制服务端 reducer

Remote 模式中，按钮事件只发送 action：

```text
用户点击
  → pendingAction = action
  → POST
  → 使用响应 Tonight 覆盖状态
  → pendingAction = null
```

不能先把本地 Phase 改为目标状态再等待网络确认。

DemoRepository 可以拥有本地 reducer，但其规则必须通过与服务端状态机相同的表驱动测试。

### 10.2 mutation 串行化

使用 `Mutex` 或单消费者 Channel，确保同一时间只有一个状态 mutation：

- action
- conversation turn
- finalize

避免快速点击造成两次 turn 或两次 finalize。UI 同时根据 `pendingAction` 禁用相关按钮。

### 10.3 前后台恢复

`ProcessLifecycleOwner` 或 Activity lifecycle：

- `ON_START`：读取 Room snapshot 立即渲染 → `GET /tonight` → 连接 WS。
- `ON_STOP`：保存当前 snapshot，延迟关闭 WS；本地音频按产品规则决定是否继续。
- 进程被杀后：从 Room 恢复最近 snapshot，但显示“正在同步”；网络返回后整体替换。

---

## 11. 音频设计

### 11.1 素材

P0 将以下资源打包进 APK：

```text
res/raw/rain_loop.*
res/raw/brown_noise_loop.*
res/raw/breathing_46.*
res/raw/box_confirm.*       # 可选，手机演示使用
res/raw/wake_alarm.*        # Expo 模拟使用
```

不依赖后端下载音频，确保断网演示。

### 11.2 GuidancePlayer

```kotlin
interface GuidancePlayer {
    val state: StateFlow<PlayerState>
    suspend fun play(guidance: Guidance, durationMinutes: Int?)
    fun pause()
    fun stop()
    fun release()
}
```

行为：

- Rain/Brown noise 可循环。
- Breathing 4–6 使用预制音轨，避免 P0 实时 TTS 带来延迟。
- Silence 直接 stop。
- 申请 Audio Focus；失去焦点时暂停或降低音量。
- 用户点击停止时立即本地 stop，不等待服务器。
- 同一个 Guidance 重复状态更新不能重新从头播放。

P0 不建议使用前台 MediaSession 长时间后台播放；如果必须锁屏继续播放，再增加 Foreground Service 和媒体通知，属于需单独确认的产品行为。

---

## 12. 视觉与交互规范

### 12.1 色彩方向

- 夜间主背景：深海蓝/近黑。
- 主要文字：暖象牙白，避免纯白刺眼。
- 强调色：低饱和日出橙。
- 错误：避免高饱和红色大面积闪烁。
- Sunrise：深红 → 暖橙 → 柔黄，渐变缓慢。

### 12.2 低刺激原则

- 动画时长 300–800ms，避免快速弹跳。
- 睡眠页无循环装饰动画。
- loading 文案温和，不展示“正在调用 AI”。
- 非 Debug 用户不看到 HTTP、WebSocket、fallback 等技术词。
- 所有关键按钮触控区域至少 48dp。
- 支持系统字体缩放，重要内容不因 1.3–1.5 倍字体截断。

### 12.3 建议用户文案

| 场景 | 文案 |
|---|---|
| WS 断开 | “实时连接暂时中断，正在同步最近状态。” |
| REST 超时 | “刚刚没有连接上，可以再试一次。” |
| AI fallback | 普通用户不标技术状态，直接显示回复 |
| PHONE_REMOVED | “手机被取出来了，重新放回后会继续今晚的流程。” |
| 409 对账 | “今晚的状态刚刚发生了变化，已经为你更新。” |
| Demo 模式 | “当前使用离线演示数据。” |

---

## 13. 安全与隐私

P0 必须做到：

1. AndroidManifest 只申请必要权限：至少 `INTERNET`、`ACCESS_NETWORK_STATE`；P0 文字输入不申请麦克风。
2. 不在 APK 中包含 Anthropic Token、数据库密码或后端内部密钥。
3. Release 禁止网络 BODY 日志和对话全文日志。
4. Crash 报告不得附带用户输入、AI 完整回复或晚安卡正文。
5. 本地不保存原始音频；P1 语音识别时也应默认不落盘。
6. HTTPS 证书使用系统信任链；P0 不自定义 `TrustManager`，不允许忽略证书错误。
7. 当前 Demo User 不是认证，UI 和代码注释必须明确其临时性质。
8. 设置页展示“非医疗产品，不提供诊断或治疗建议”的说明。
9. 高风险回复不得被 Demo fallback 或客户端文案覆盖。

正式上线前需等待后端提供登录 Token 和设备绑定协议，再接入安全存储（Android Keystore / Encrypted DataStore）。

---

## 14. 可观测性与诊断

### 14.1 Debug/Expo 诊断页

显示：

- API Base URL
- Demo user ID
- Remote/Demo mode
- REST 最近状态码和 request ID
- WS 状态、最近连接时间、重连次数
- 当前 Phase
- 本地音频状态
- 最近一次错误分类
- App version / build type

不显示：

- Anthropic Token
- 数据库 DSN
- 对话全文

### 14.2 Request ID

每个 REST 请求生成 UUID 并设置：

```http
X-Request-Id: <uuid>
```

错误反馈时允许用户复制简短诊断信息：时间、路径、状态码、request ID。后端可在 `server.log` 中按 request ID 定位。

---

## 15. 测试方案

### 15.1 单元测试

1. DTO ↔ Domain 映射：所有 Phase、Guidance、缺失可选字段。
2. ApiErrorParser：400/404/409/500/502、非法 JSON。
3. Repository mutation 串行化。
4. WebSocket snapshot 替换和重复事件去重。
5. 重连退避：1、2、4、8、15 秒。
6. DemoRepository 全状态流程。
7. ViewModel：请求中禁用按钮、错误恢复、导航状态。
8. Audio：相同 Guidance 不重复启动；stop 优先于网络。

### 15.2 MockWebServer 契约测试

根据当前 OpenAPI 固定样例验证：

- `GET /tonight`。
- conversation 正常、fallback、高风险、第三轮 finalize。
- `POST /finalize`。
- 选择四种 guidance。
- 409 后刷新 Tonight。
- 502/timeout 后保留输入。
- journals 列表。
- WebSocket 五类事件。

### 15.3 Compose UI 测试

- 每个 Phase 显示正确主 CTA。
- Conversation 发送、loading、3 轮上限。
- Guidance 推荐标记和四选一。
- Sleeping 黑屏与停止音频入口。
- Sunrise 贪睡、起床。
- Profile 非法/合法值。
- 大字体和横竖屏最低可用性。

### 15.4 真机端到端测试

使用线上：

```text
https://bm.lg.gl/api/v1
```

至少覆盖：

1. 无硬件，用 Expo actions 完整走 10 次。
2. 真实 T5 闭仓/开仓，APP 在 2 秒内通过 WS 更新。
3. WS 中断后恢复并 `GET /tonight` 对账。
4. 后端暂时不可用，切 Demo 完成流程。
5. APP 切后台、杀进程、重启后恢复。
6. 快速双击发送、finalize、guidance，不产生重复轮次。
7. 手机和 T5 音频模式分别验证，避免双播。
8. 真实 Claude 与后端 fallback 均可完成流程。

---

## 16. 开发顺序与 TODO

### P0-A：工程骨架

- [ ] 新建 Kotlin + Compose 工程。
- [ ] 配置 Hilt、Navigation、Retrofit、serialization、Room、DataStore、Media3。
- [ ] 建立 debug/expo/release variants。
- [ ] 加入主题、基础组件和根导航。
- [ ] 定义领域 enum、DTO 和 Repository 接口。

### P0-B：Demo 竖切

- [ ] 实现 DemoRepository 和本地状态 reducer。
- [ ] 实现 Tonight、Conversation、Guidance、Sleeping、Sunrise、Journal、Profile 页面。
- [ ] 加入本地音频素材和 GuidancePlayer。
- [ ] 完成无网完整流程。

### P0-C：REST 接入

- [ ] 配置 `https://bm.lg.gl/api/v1/`。
- [ ] 注入 Demo User 和 Request ID。
- [ ] 接入 Profile、Tonight、Action、Conversation、Finalize、Journal。
- [ ] 实现统一错误解析、超时和 mutation 串行化。
- [ ] 加入 Room snapshot 缓存。

### P0-D：WebSocket

- [ ] 连接 `wss://bm.lg.gl/api/v1/ws`。
- [ ] 实现五类事件解析。
- [ ] 实现 snapshot 替换、去重和前后台同步。
- [ ] 实现指数退避和 REST 对账。

### P0-E：软硬件联合

- [ ] 与硬件约定相同 `userId`。
- [ ] 验证 box closed/opened 的 WS 更新。
- [ ] 配置 PHONE/DEVICE 音频输出模式。
- [ ] 验证短按、长按、alarm_start 后 APP 页面变化。

### P0-F：稳定与冻结

- [ ] 单元、契约和 Compose UI 测试。
- [ ] 10 次完整真机闭环。
- [ ] 弱网、断线、杀进程、重复点击演练。
- [ ] 输出主 APK、备用 APK 和录屏。
- [ ] 冻结 P0，只修阻断演示的问题。

---

## 17. 当前后端限制对 Android 的影响

| 后端当前限制 | Android 设计决策 |
|---|---|
| 无正式登录 | 使用固定 Demo User；不构建假登录页 |
| 无设备在线/绑定查询 | 音频输出模式由 Expo 设置手工选择 |
| 无 conversation history GET | 进程重启后只显示轮数与 latest draft，不伪造历史 |
| 无连续晨光进度推送 | APP 渐变只作为本地视觉演示 |
| 无日记删除/编辑 | Journal P0 只读 |
| 无后台提醒调度 | P0 不承诺真实睡前提醒 |
| 无命令状态给 APP | APP 不判断 T5 是否执行成功，只依赖 Tonight 状态 |
| WebSocket Hub 单实例 | 客户端必须支持断线后 REST 对账 |
| Demo User 可伪造 | 仅团队测试；正式发布前必须接入认证 |
| AI 最长约 30 秒 | Conversation 客户端 call timeout 设为约 40 秒 |

---

## 18. 开发者开工清单

拿到本文档后，开发者按以下步骤即可直接开始，不需要再反向猜后端：

### 18.1 创建工程

```text
Application ID：由团队确定，例如 com.baomian.app
minSdk：26
UI：Compose
DI：Hilt
Serialization：kotlinx.serialization
```

最低依赖类别：

```kotlin
implementation(platform("androidx.compose:compose-bom:<stable>"))
implementation("androidx.activity:activity-compose:<stable>")
implementation("androidx.navigation:navigation-compose:<stable>")
implementation("androidx.lifecycle:lifecycle-runtime-compose:<stable>")
implementation("androidx.lifecycle:lifecycle-viewmodel-compose:<stable>")
implementation("com.google.dagger:hilt-android:<stable>")
ksp("com.google.dagger:hilt-compiler:<stable>")
implementation("com.squareup.retrofit2:retrofit:<stable>")
implementation("com.squareup.okhttp3:okhttp:<stable>")
implementation("org.jetbrains.kotlinx:kotlinx-serialization-json:<stable>")
implementation("androidx.room:room-runtime:<stable>")
implementation("androidx.room:room-ktx:<stable>")
ksp("androidx.room:room-compiler:<stable>")
implementation("androidx.datastore:datastore-preferences:<stable>")
implementation("androidx.media3:media3-exoplayer:<stable>")
```

版本号使用创建工程时的当前稳定版本，不要从本文档复制过时的固定版本。

### 18.2 Manifest

```xml
<uses-permission android:name="android.permission.INTERNET" />
<uses-permission android:name="android.permission.ACCESS_NETWORK_STATE" />
```

线上只使用 HTTPS，不开启 cleartext。局域网 HTTP 调试应仅在 `debug` variant 使用独立 `networkSecurityConfig`，不能进入 release/expo 线上包。

### 18.3 第一批必须创建的文件

```text
core/network/BaomianApi.kt
core/network/ApiModels.kt
core/network/ApiErrorParser.kt
core/network/DemoUserInterceptor.kt
core/realtime/RealtimeClient.kt
core/model/Phase.kt
core/model/Guidance.kt
data/BaomianRepository.kt
data/RemoteBaomianRepository.kt
data/DemoBaomianRepository.kt
feature/tonight/TonightViewModel.kt
feature/tonight/TonightScreen.kt
```

### 18.4 最小联通顺序

1. `GET /health`：确认 TLS 和 Retrofit Base URL。
2. `GET /profile`：确认 Demo User Header。
3. `GET /tonight`：完成 Phase 页面映射。
4. `POST /tonight/actions`：逐个验证 8 个 action。
5. Conversation turn/finalize。
6. Journals/memories。
7. WebSocket。
8. 最后接音频和真实 T5。

### 18.5 可直接使用的联调身份

```text
Base URL: https://bm.lg.gl/api/v1/
Demo User: expo-user-001
WebSocket: wss://bm.lg.gl/api/v1/ws?userId=expo-user-001
```

团队多人并行测试时，建议每位开发者使用唯一 ID，例如：

```text
android-<name>-001
```

否则多人会共享同一 Tonight 状态，互相推进 Phase。硬件联调时，Android 的 Demo User 必须与 T5 事件中的 `userId` 一致。

### 18.6 首个可验收里程碑

首个提交不需要全部 UI 完成，但必须达到：

- 启动显示服务器 Tonight Phase。
- 能从 WAITING_TO_LOCK 模拟闭仓。
- 能提交一轮文字倾诉并显示真实 AI/fallback 回复。
- 能 finalize 并显示四种 Guidance。
- WebSocket 收到 T5 的 `tonight.updated` 后刷新页面。
- 无网络时显示可操作的错误态，而不是无限 loading。

## 19. Definition of Done

Android P0 完成需同时满足：

1. Expo APK 可安装在目标真机。
2. 线上后端模式完整走通睡前到晨光流程。
3. 无网络时可明确切换 Demo 并完成相同流程。
4. APP 启动、回前台和 WS 重连后均以 `GET /tonight` 恢复服务端状态。
5. 真实硬件闭仓/开仓事件 2 秒内反映到 APP。
6. 最多 3 轮对话，无重复提交和无限 loading。
7. 引导音频不会因 REST + WS 重复响应而重复播放。
8. 用户可以随时立即停止手机本地音频。
9. 晚安卡可生成、缓存并展示最近 7 张。
10. 断网、后端 5xx、WS 中断、APP 杀进程均不崩溃。
11. Release/Expo 日志不包含对话全文和凭证。
12. 连续 10 次完整闭环成功。

---

## 20. P1 扩展建议

P0 稳定后再加入：

- SpeechRecognizer 按住说话。
- TTS 人格音色和语速。
- WorkManager/AlarmManager 睡前提醒与本地闹钟。
- Media3 Foreground Service 锁屏持续播放。
- 正式用户登录、Token refresh 和设备绑定。
- 日记删除、待办完成标记。
- 真实设备在线状态和命令执行状态。
- 用户时区和跨日会话处理。
- 崩溃、性能和匿名指标系统。

在后端正式提供相应 API 前，不在 P0 客户端预埋不可验证的业务流程。

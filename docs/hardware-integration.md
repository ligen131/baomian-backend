# 抱眠硬件（T5）与后端对接文档

> 文档版本：P0 / 2026-07-23
>
> 适用对象：硬件固件、嵌入式网络与联调人员
>
> 线上 API Base URL：`https://bm.lg.gl/api/v1`

## 1. 对接目标

硬件与后端之间采用 HTTPS + JSON，分为两条链路：

1. **设备上报事件**：仓盖开合、按键、设备本地闹钟触发。
2. **设备领取并执行命令**：音频、灯光、晨光与闹钟控制；执行后回传 ACK。

```text
T5 硬件                              抱眠后端
   │                                    │
   │ POST /device/events                │  上报传感器/按键事件
   ├───────────────────────────────────>│
   │ 200（事件处理结果）                 │
   │<───────────────────────────────────┤
   │                                    │
   │ GET /device/commands/next          │  长轮询领取命令
   ├───────────────────────────────────>│
   │ 200 command / 204 no command       │
   │<───────────────────────────────────┤
   │                                    │
   │ 在硬件本地执行 command              │
   │                                    │
   │ POST /device/commands/ack          │  上报执行结果
   ├───────────────────────────────────>│
   │ 200                                │
   │<───────────────────────────────────┤
```

## 2. 联调前需要约定的标识

每台硬件需要持久化两个标识：

| 字段 | 示例 | 说明 |
|---|---|---|
| `deviceId` | `expo-device-001` | 设备唯一标识；事件、取命令和 ACK 必须使用同一个值 |
| `userId` | `expo-user-001` | 当前绑定的 Demo 用户；P0 阶段由团队约定或烧录配置 |

要求：

- 同时联调多台硬件时，每台设备必须使用不同的 `deviceId`。
- 不要在每次重启时随机生成 `deviceId`，应保存在 NVS/Flash 中。
- 换绑用户时只更新 `userId`，不要改变设备自身的 `deviceId`。
- P0 尚无正式设备注册接口，具体取值请与后端/APP 同学确认。

## 3. 通用协议

### 3.1 请求格式

- 协议：HTTPS
- JSON 编码：UTF-8
- 请求头：`Content-Type: application/json`
- 时间：RFC 3339，推荐 UTC，例如 `2026-07-23T14:30:00Z`
- 线上 Base URL：`https://bm.lg.gl/api/v1`

设备接口当前不需要 `X-Demo-User-Id` Header。`userId` 通过设备事件 JSON 传入。

### 3.2 当前认证边界

P0 设备接口暂未启用设备密钥或签名，仅依赖 HTTPS。固件中不要自行添加尚未约定的 `Authorization` Header。正式生产前，后端会另行增加设备凭证；届时本文档会升级版本。

### 3.3 统一错误格式

```json
{
  "error": {
    "code": "invalid_transition",
    "message": "当前状态不接受此设备事件",
    "details": {
      "phase": "AWAKE",
      "eventType": "alarm_start"
    }
  }
}
```

常见状态码：

| HTTP 状态 | 含义 | 硬件处理建议 |
|---|---|---|
| `200` | 成功 | 解析响应并继续流程 |
| `204` | 长轮询期间没有命令 | 正常情况，立即发起下一次长轮询 |
| `400` | JSON、参数或事件类型错误 | 不要原样重试；记录日志并修复固件/配置 |
| `404` | ACK 的命令不存在，或 `deviceId` 不匹配 | 检查标识；不要无限重试 |
| `409` | 当前业务状态不接受该事件 | 不要高频重试；记录错误并等待下一次真实状态变化 |
| `500` | 后端或数据库暂时异常 | 使用退避策略重试 |

## 4. 上报硬件事件

### 4.1 接口

```http
POST https://bm.lg.gl/api/v1/device/events
Content-Type: application/json
```

请求：

```json
{
  "eventId": "2f83458a-caa1-4ef0-a78f-d3d2810254de",
  "deviceId": "expo-device-001",
  "userId": "expo-user-001",
  "type": "box_closed",
  "payload": {},
  "occurredAt": "2026-07-23T14:30:00Z"
}
```

字段：

| 字段 | 必填 | 类型 | 说明 |
|---|---:|---|---|
| `eventId` | 是 | string | **全局唯一且用于幂等**，推荐 UUID v4 |
| `deviceId` | 是 | string | 当前硬件的固定设备 ID |
| `userId` | 建议必填 | string | 当前绑定用户；省略时后端使用默认 Demo 用户 |
| `type` | 是 | string | 事件类型，见下表 |
| `payload` | 否 | object | P0 可传 `{}`，为后续硬件数据预留 |
| `occurredAt` | 否 | RFC 3339 | 事件在设备上实际发生的时间；省略时以后端接收时间为准 |

成功响应：

```json
{
  "duplicate": false,
  "tonight": {
    "id": "42a74f79-f7f7-43fc-91c9-9b6bf6db1192",
    "date": "2026-07-23",
    "phase": "LOCKED",
    "bedtime": "23:00",
    "wakeTime": "07:30",
    "conversationTurns": 0,
    "audioPlaying": false,
    "pausedForTonight": false,
    "device": {"boxClosed": true},
    "sunrise": {"progress": 0},
    "latestAIDraft": {}
  },
  "commands": [
    {
      "id": "9b5a6fea-d00a-4fb7-b35a-70a0e23b40ee",
      "deviceId": "expo-device-001",
      "type": "audio.confirm",
      "payload": {"message": "手机已经安放好了"},
      "status": "pending",
      "createdAt": "2026-07-23T14:30:00Z"
    }
  ]
}
```

> **重要：不要直接执行事件响应中的 `commands`。** 这些命令已经进入后端命令队列，硬件应统一通过 `/device/commands/next` 领取并执行。否则同一命令会在事件响应和长轮询中各执行一次。

### 4.2 支持的事件

| `type` | 触发条件 | 后端行为 | 可能排队的命令 |
|---|---|---|---|
| `box_closed` | 仓盖由开变为关 | 首次锁仓，或从 `PHONE_REMOVED` 恢复 | `audio.confirm`、`led.off` |
| `box_opened` | 仓盖由关变为开 | 暂停当前音频，状态进入 `PHONE_REMOVED` | `audio.pause` |
| `soft_button/short_press` | 软按键短按 | 晨光中表示贪睡；其他睡前阶段表示停止音频 | `alarm.snooze` + `led.off`，或 `audio.stop` + `led.off` |
| `soft_button/long_press` | 软按键长按 | 晨光中表示已起床；其他阶段为无副作用事件 | 晨光中产生 `alarm.stop` |
| `alarm_start` | 硬件本地闹钟到点 | 后端进入 `SUNRISE` | `sunrise.start` |

### 4.3 仓盖事件要求

- 只在仓盖稳定状态发生**边沿变化**时上报。
- 建议固件去抖 300–500 ms。
- 同一次物理变化的网络重试必须复用同一个 `eventId`。
- 不能为一次事件的每次重试生成新 `eventId`，否则会造成重复状态迁移或 `409`。

### 4.4 事件幂等与重试

后端以 `eventId` 做唯一约束。同一事件重复上报时返回首次结果，并令：

```json
{"duplicate": true}
```

固件建议流程：

1. 物理事件发生时生成一个 UUID v4。
2. 将完整事件写入本地待发送队列。
3. 请求成功（HTTP 200）后删除本地事件。
4. 网络断开、超时或 HTTP 5xx 时，使用**同一个 `eventId`**退避重试。
5. HTTP 400/404/409 不要无限重试。

推荐退避：`1s → 2s → 4s → 8s → 15s`，之后最多每 30 秒重试一次；设备重新联网后立即恢复队列发送。

## 5. 长轮询领取命令

### 5.1 接口

```http
GET https://bm.lg.gl/api/v1/device/commands/next?deviceId=expo-device-001&timeoutSec=20
```

参数：

| 参数 | 必填 | 说明 |
|---|---:|---|
| `deviceId` | 是 | 设备固定 ID |
| `timeoutSec` | 否 | 长轮询时长，默认 20 秒，最大 30 秒；推荐传 20 |

返回规则：

- HTTP `200`：领取到一条命令，命令状态已经变成 `dispatched`。
- HTTP `204`：本轮等待期间没有命令，不是错误。

示例：

```json
{
  "id": "9b5a6fea-d00a-4fb7-b35a-70a0e23b40ee",
  "deviceId": "expo-device-001",
  "type": "audio.play",
  "payload": {
    "guidance": "breathing_46"
  },
  "status": "dispatched",
  "createdAt": "2026-07-23T14:31:00Z"
}
```

### 5.2 固件轮询要求

- 每台设备同一时间只能保持 **1 个** `/commands/next` 请求，禁止并行长轮询。
- HTTP 客户端读超时应大于 `timeoutSec`，推荐 `25–30s`。
- 收到 `204` 后可立即发起下一次请求，不需要额外等待。
- 网络失败后使用 1–5 秒退避，避免离线期间高速重连。
- 收到 `200` 后：校验 `deviceId` → 根据 `type` 执行 → 调 ACK → 再领取下一条。
- 单条命令执行失败也必须 ACK，并设置 `success=false`；不要因一条命令失败而永久阻塞队列。

参考伪代码：

```text
while device_is_running:
    response = GET /device/commands/next?deviceId=...&timeoutSec=20

    if response.status == 204:
        continue

    if response.status == 200:
        command = response.json
        result = execute(command.type, command.payload)
        retry_same_ack_until_resolved(command.id, result)
        continue

    sleep(with_backoff)
```

## 6. 命令类型与硬件行为

| 命令 `type` | `payload` 示例 | 硬件期望行为 |
|---|---|---|
| `audio.confirm` | `{"message":"手机已经安放好了"}` | 播放内置“手机已经安放好了”确认音；支持 TTS 时可使用 `message` |
| `audio.play` | `{"guidance":"rain"}` | 播放指定睡眠引导音频，循环/时长由当前固件策略控制 |
| `audio.pause` | `{}` | 立即暂停当前引导或白噪音，保留可恢复的播放位置 |
| `audio.stop` | `{}` | 立即停止并重置当前音频播放 |
| `led.off` | `{}` | 关闭非必要 LED/灯效，进入低刺激黑暗状态 |
| `sunrise.start` | `{"durationMinutes":25}` | 从最低亮度开始执行 25 分钟渐亮晨光；具体曲线由固件实现 |
| `alarm.snooze` | `{"minutes":5}` | 停止当前闹铃/晨光并在设备本地安排 5 分钟后再次触发 |
| `alarm.stop` | `{}` | 停止闹铃、晨光与相关音频，退出唤醒流程 |

### 6.1 `audio.play` 的 guidance 值

| 值 | 含义 |
|---|---|
| `rain` | 雨声 |
| `brown_noise` | 棕色噪音 |
| `breathing_46` | 4 秒吸气、6 秒呼气的呼吸引导 |

用户选择 `silence` 时，后端不会下发 `audio.play`，而会下发 `audio.stop` 和 `led.off`。

### 6.2 贪睡的本地职责

P0 后端没有后台闹钟调度器。因此收到：

```json
{"type":"alarm.snooze","payload":{"minutes":5}}
```

硬件必须在本地 RTC/定时器中安排 5 分钟。时间到后再次上报一个具有新 `eventId` 的 `alarm_start`，再由后端返回后续 `sunrise.start` 命令。

若网络暂时不可用，建议硬件仍按本地兜底策略启动基础闹铃，联网后补报 `alarm_start`；不能因为后端不可达而完全跳过用户闹钟。

## 7. ACK 命令执行结果

### 7.1 接口

```http
POST https://bm.lg.gl/api/v1/device/commands/ack
Content-Type: application/json
```

成功 ACK：

```json
{
  "deviceId": "expo-device-001",
  "commandId": "9b5a6fea-d00a-4fb7-b35a-70a0e23b40ee",
  "success": true,
  "payload": {
    "durationMs": 138,
    "firmwareVersion": "0.1.0"
  }
}
```

失败 ACK：

```json
{
  "deviceId": "expo-device-001",
  "commandId": "9b5a6fea-d00a-4fb7-b35a-70a0e23b40ee",
  "success": false,
  "payload": {
    "errorCode": "AUDIO_ASSET_NOT_FOUND",
    "message": "breathing_46 asset is missing"
  }
}
```

字段：

| 字段 | 必填 | 说明 |
|---|---:|---|
| `deviceId` | 是 | 必须与领取命令时的设备 ID 一致 |
| `commandId` | 是 | 命令响应中的 `id` |
| `success` | 是 | 命令是否成功完成 |
| `payload` | 否 | 可放执行耗时、固件版本、错误码等诊断信息；不得放密钥 |

ACK 是幂等的。若 ACK 请求超时或响应丢失，可以用相同的 `deviceId`、`commandId` 和结果重复发送。

> 当前 P0 命令在被领取时即标记为 `dispatched`，没有超时自动重新投递机制。因此固件应在本地暂存“待 ACK 命令”，直到收到 ACK 的 HTTP 200；收到命令后也不要在 ACK 前重启或清空执行记录。

## 8. 推荐的设备状态与持久化数据

固件至少持久化：

```text
device_id
bound_user_id
firmware_version
pending_event_queue[]     // 等待后端确认的事件，含固定 eventId
pending_ack               // 已执行但尚未得到 ACK 200 的命令及执行结果
local_alarm/snooze_state  // 本地 RTC 闹钟与贪睡状态
```

建议内存状态：

```text
box_state: open | closed | unknown
current_audio: none | paused | rain | brown_noise | breathing_46
sunrise_active: bool
last_command_id: UUID
network_backoff: duration
```

## 9. 完整联调流程

### 9.1 睡前流程

1. 硬件启动并使用 `deviceId` 开始长轮询。
2. 用户将手机放入设备并闭仓。
3. 硬件上报 `box_closed`。
4. 后端更新状态并排队 `audio.confirm`、`led.off`。
5. 硬件依次领取命令，执行并 ACK。
6. APP 完成 AI 倾诉并选择引导方式。
7. 硬件领取 `audio.play`，开始播放雨声/棕色噪音/呼吸引导。
8. 用户开仓时，硬件上报 `box_opened`，随后领取 `audio.pause`。
9. 用户重新闭仓时，再上报新的 `box_closed` 事件。

### 9.2 晨光与起床流程

1. 设备本地 RTC 到达起床时间，上报 `alarm_start`。
2. 硬件领取 `sunrise.start {durationMinutes:25}` 并开始渐亮。
3. 用户短按：上报 `soft_button/short_press`。
4. 硬件领取 `alarm.snooze {minutes:5}` 和 `led.off`，在本地设置 5 分钟定时器。
5. 贪睡到时：上报新的 `alarm_start`，重新领取 `sunrise.start`。
6. 用户长按：上报 `soft_button/long_press`。
7. 硬件领取 `alarm.stop`，结束唤醒流程。

## 10. cURL 联调示例

以下变量仅用于联调：

```bash
BASE_URL='https://bm.lg.gl/api/v1'
DEVICE_ID='expo-device-001'
USER_ID='expo-user-001'
```

### 10.1 上报闭仓

```bash
curl --fail-with-body -X POST "$BASE_URL/device/events" \
  -H 'Content-Type: application/json' \
  --data "{
    \"eventId\":\"$(uuidgen)\",
    \"deviceId\":\"$DEVICE_ID\",
    \"userId\":\"$USER_ID\",
    \"type\":\"box_closed\",
    \"payload\":{}
  }"
```

### 10.2 领取下一条命令

```bash
curl -i "$BASE_URL/device/commands/next?deviceId=$DEVICE_ID&timeoutSec=20"
```

### 10.3 ACK 命令

```bash
curl --fail-with-body -X POST "$BASE_URL/device/commands/ack" \
  -H 'Content-Type: application/json' \
  --data "{
    \"deviceId\":\"$DEVICE_ID\",
    \"commandId\":\"<上一步返回的命令 id>\",
    \"success\":true,
    \"payload\":{\"firmwareVersion\":\"0.1.0\"}
  }"
```

## 11. 联调验收清单

- [ ] `deviceId` 重启后保持不变。
- [ ] 仓盖事件已做去抖，只上报稳定的边沿变化。
- [ ] 每个新物理事件生成新的 UUID；同一事件重试复用 UUID。
- [ ] 事件响应中的 `commands` 不被直接执行。
- [ ] 每台设备只有一个长轮询请求。
- [ ] HTTP 204 被当作正常空结果处理。
- [ ] 所有领取到的命令均产生成功或失败 ACK。
- [ ] ACK 网络失败时可在重启后继续重试。
- [ ] 未知命令不会导致固件崩溃，而是以 `success=false` ACK。
- [ ] `alarm.snooze` 使用本地 RTC/定时器实现。
- [ ] 断网时闹钟有本地兜底，不依赖云端才能响。
- [ ] 日志中不记录未来增加的设备密钥或其他敏感信息。

## 12. P0 已知边界

- 设备接口暂未启用正式认证。
- 一个命令领取后不会自动超时重投，当前投递语义接近 at-most-once。
- 后端不负责真实闹钟和贪睡定时，硬件必须本地调度。
- 当前没有设备注册、解绑、OTA、心跳、时钟同步、遥测和固件版本策略接口。
- 当前没有晨光进度上报接口，渐亮曲线与实时进度由硬件本地维护。

完整机器可读契约见仓库中的 [`api/openapi.yaml`](../api/openapi.yaml)。若实现与本文档出现冲突，以联调时确认的后端版本和 OpenAPI 为准。

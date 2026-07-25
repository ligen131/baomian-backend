# T5 Voice Streaming — Volcengine Migration Plan

> **Status:** Approved and executing on 2026-07-24. Implementation is inline; subagents and code review are explicitly disabled for this work.

**Goal:** 保持 T5 Device Voice WebSocket、PCM 24 kHz 协议、现有 Claude 三轮对话和晚安引导不变，把无法使用的 Coze speech adapters 完整替换为火山引擎 V3 流式 ASR 与单向流式 TTS。

**Architecture:**

```text
T5 20 ms / 960-byte PCM
  ↕ /api/v1/device/voice
VoiceSessionService
  ├─ VolcASRClient → Volcengine V3 ASR（后端聚合为约 200 ms 上游包）
  ├─ existing ConversationService → existing Claude adapter
  └─ VolcTTSClient → Volcengine V3 TTS → 960-byte PCM frames → T5
```

## Global Constraints

- 不启动 subagents，不做 code review，只执行简单测试。
- 先同步仓库文档，再修改代码和部署。
- 不修改 T5/Android 对外协议，不新增 migration，不修改 Claude structured output/fallback。
- T5 音频固定 PCM signed little-endian、24000 Hz、16 bit、mono、20 ms、960 bytes。
- 不持久化或记录原始音频、转写全文、TTS 文本或凭证。
- 真实 App ID/Access Token 只写入被 Git 忽略且权限为 `0600` 的 `.env`；Secret Key 不用于两条 V3 WebSocket API，不落盘。
- 不提交 Git commit，除非用户另行要求。

## Task 1: Documentation and Configuration Contract

- 更新 `README.md`、`docs/voice-streaming-design.md`、`docs/p1-backend-design.md`、`docs/hardware-integration.md`、Android 文档、OpenAPI 和 HTTP 示例。
- `.env.example` / Compose 使用：

```env
VOLCENGINE_SPEECH_APP_ID=
VOLCENGINE_SPEECH_ACCESS_TOKEN=
VOLCENGINE_TTS_API_KEY=
VOLCENGINE_ASR_WS_URL=wss://openspeech.bytedance.com/api/v3/sauc/bigmodel_async
VOLCENGINE_ASR_RESOURCE_ID=volc.bigasr.sauc.duration
VOLCENGINE_TTS_WS_URL=wss://openspeech.bytedance.com/api/v3/tts/unidirectional/stream
VOLCENGINE_TTS_RESOURCE_ID=seed-tts-2.0
VOLCENGINE_TTS_SPEAKER=ICL_uranus_zh_female_wenrouwenya_tob
VOLCENGINE_ASR_TIMEOUT=20s
VOLCENGINE_TTS_FIRST_FRAME_TIMEOUT=10s
VOLCENGINE_TTS_TOTAL_TIMEOUT=45s
```

- 保持稳定错误码 `speech_not_configured`，但错误描述供应商中立。

## Task 2: Volcengine Binary Protocol

- `internal/speech/volc_protocol.go`：ASR V3 4-byte header、sequence/last flags、gzip JSON/PCM、response/error 解析。
- `internal/speech/volc_tts_protocol.go`：单向 TTS full client request、audio-only/event/error 响应解析。
- 固定字节向量单元测试覆盖大端长度、gzip、event/ID 顺序和错误脱敏。

## Task 3: Streaming ASR Adapter

- Endpoint：`wss://openspeech.bytedance.com/api/v3/sauc/bigmodel_async`。
- Headers：`X-Api-App-Key`、`X-Api-Access-Key`、`X-Api-Resource-Id`、`X-Api-Request-Id`。
- 初始化 gzip JSON 声明 `pcm/raw/24000/mono` 与 `model_name=bigmodel`。
- 每 10 个 T5 frame 聚合为 9600 bytes（约 200 ms）后发送；结束时冲刷余量并发送 last-package。
- 独立 reader goroutine 持续读取增量结果；Complete 返回最终非空 transcript。
- mock WebSocket 测试 headers、聚合、末包、结果和错误。

## Task 4: Unidirectional Streaming TTS Adapter

- Endpoint：`wss://openspeech.bytedance.com/api/v3/tts/unidirectional/stream`。
- Headers：API Key/resource/connect ID。
- 建连后发送一次 no-sequence full client request；`req_params` 包含 text、speaker 与 `audio_params={format:"pcm",sample_rate:24000}`。
- 连续读取 audio-only response，直到 `SessionFinished(152)`。
- audio-only response 原样交给 `VoiceSessionService`；context cancel 立即关闭 socket。
- 保留仅首个 PCM 交付前重试一次的 wrapper。

## Task 5: Production Wiring

- Config 移除活动的 `Coze*` 字段，新增 Volcengine 字段和 `VolcengineSpeechConfigured()`。
- `cmd/server/main.go` 装配 VolcASR/VolcTTS；日志只记录 `speechProvider=volcengine`、configured 布尔值与固定 PCM 格式。
- Controller 配置错误消息改为“语音服务尚未配置”。

## Task 6: Simple Verification

```bash
gofmt -w <modified-go-files>
go test ./internal/config ./internal/speech ./internal/service ./internal/controller -count=1
go test ./... -count=1
go build ./...
```

同时解析 OpenAPI、执行 `git diff --check`、扫描日志语句和活动的旧供应商引用。测试使用本地 mock，不请求真实供应商。

## Task 7: Safe Deployment and Real Smoke

- 备份 `.env` 与 `.local/baomian-server`，不改数据库。
- 写入授权的 ASR App ID/Access Token 与 TTS API Key，保持 `.env` 为 `0600`；不写 Secret Key。
- 通过 8325 监听 PID、`/proc/<pid>/exe` 和 cwd 验证实际服务后再 TERM；不信任过期 pid 文件。
- 原子替换构建成功的二进制，启动并验证本地及 `https://bm.lg.gl/api/v1/health`。
- 临时 user/device 真实验证：开场 TTS 必须返回 960-byte PCM；静音 ASR 应返回 `empty_transcript` 而不是 `asr_unavailable`。
- 如果账号未开通 resource/speaker，恢复旧 `.env` 和二进制，报告脱敏错误码/request ID。
- 实机人声识别、声音质量、三轮自然度和延迟最终仍由 T5 人工试听验收。

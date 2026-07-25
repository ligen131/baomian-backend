package controller

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/baomian/baomian-backend/internal/service"
	"github.com/baomian/baomian-backend/internal/voice"
	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
)

const (
	voiceReadLimit    = 16 * 1024
	voiceWriteQueue   = 64
	voiceWriteTimeout = 10 * time.Second
	voicePongWait     = 60 * time.Second
	voicePingPeriod   = 45 * time.Second
)

type DeviceVoiceController struct {
	factory     service.VoiceSessionFactory
	configured  bool
	defaultUser string
	logger      *slog.Logger
	upgrader    websocket.Upgrader
	registry    *voiceConnectionRegistry
}

func NewDeviceVoiceController(factory service.VoiceSessionFactory, configured bool, defaultUser string, logger ...*slog.Logger) *DeviceVoiceController {
	var serviceLogger *slog.Logger
	if len(logger) > 0 {
		serviceLogger = logger[0]
	}
	return &DeviceVoiceController{
		factory: factory, configured: configured, defaultUser: defaultUser, logger: serviceLogger,
		upgrader: websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }},
		registry: newVoiceConnectionRegistry(),
	}
}

func (h *DeviceVoiceController) Connect(c *gin.Context) {
	if !h.configured {
		c.JSON(http.StatusServiceUnavailable, errorBody{Error: errorDetail{
			Code: voice.ErrorSpeechNotConfigured, Message: "语音服务尚未配置",
		}})
		return
	}
	deviceID := c.Query("deviceId")
	if deviceID == "" {
		respondBindingError(c, errors.New("deviceId is required"))
		return
	}
	userID := c.Query("userId")
	if userID == "" {
		userID = h.defaultUser
	}
	connection, err := h.upgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		if h.logger != nil {
			h.logger.WarnContext(c.Request.Context(), "device voice upgrade failed", "deviceId", deviceID, "stage", "upgrade", "errorCategory", "websocket_upgrade")
		}
		return
	}
	connectedAt := time.Now()
	if h.logger != nil {
		h.logger.InfoContext(c.Request.Context(), "device voice connected", "deviceId", deviceID)
	}
	connection.SetReadLimit(voiceReadLimit)

	connectionContext, cancel := context.WithCancel(context.Background())
	defer cancel()
	outbound := make(chan voiceOutboundMessage, voiceWriteQueue)
	output := &websocketVoiceOutput{messages: outbound}
	session := h.factory.NewSession(userID, deviceID, output)
	if previous := h.registry.Replace(deviceID, connection); previous != nil {
		_ = previous.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.ClosePolicyViolation, "replaced by a new device connection"), time.Now().Add(time.Second))
		_ = previous.Close()
	}
	defer func() {
		h.registry.Remove(deviceID, connection)
		_ = session.Close()
		_ = connection.Close()
		if h.logger != nil {
			h.logger.Info("device voice disconnected", "deviceId", deviceID, "durationMs", time.Since(connectedAt).Milliseconds())
		}
	}()

	writerDone := make(chan error, 1)
	go voiceWritePump(connectionContext, connection, outbound, writerDone)
	if err := session.Ready(connectionContext); err != nil {
		if h.logger != nil {
			h.logger.Warn("device voice ready failed", "deviceId", deviceID, "stage", "ready", "errorCategory", "session_ready")
		}
		return
	}

	_ = connection.SetReadDeadline(time.Now().Add(voicePongWait))
	connection.SetPongHandler(func(string) error {
		return connection.SetReadDeadline(time.Now().Add(voicePongWait))
	})
	for {
		messageType, payload, err := connection.ReadMessage()
		if err != nil {
			if h.logger != nil {
				h.logger.Info("device voice reader closed", "deviceId", deviceID, "stage", "read", "errorCategory", "connection_closed")
			}
			return
		}
		switch messageType {
		case websocket.TextMessage:
			event, decodeErr := voice.DecodeClientEvent(payload)
			if decodeErr != nil {
				if h.logger != nil {
					h.logger.Warn("device voice control event rejected", "deviceId", deviceID, "stage", "decode", "errorCategory", "invalid_event", "reason", decodeErr.Error())
				}
				_ = output.SendEvent(connectionContext, voice.ServerEvent{
					Type: voice.EventError, Code: voice.ErrorInvalidEvent,
					Message: decodeErr.Error(), Retryable: false,
				})
				continue
			}
			if err := session.HandleEvent(connectionContext, event); err != nil {
				return
			}
		case websocket.BinaryMessage:
			if err := session.HandlePCM(connectionContext, payload); err != nil {
				return
			}
		default:
			_ = output.SendEvent(connectionContext, voice.ServerEvent{
				Type: voice.EventError, Code: voice.ErrorInvalidEvent,
				Message: "不支持的 WebSocket message type", Retryable: false,
			})
		}
		select {
		case err := <-writerDone:
			if h.logger != nil {
				h.logger.Warn("device voice writer failed", "deviceId", deviceID, "stage", "write", "errorCategory", voiceWriterErrorCategory(err))
			}
			return
		default:
		}
	}
}

func voiceWriterErrorCategory(err error) string {
	if errors.Is(err, context.Canceled) {
		return "context_canceled"
	}
	return "websocket_write"
}

type voiceOutboundMessage struct {
	messageType int
	payload     any
}

type websocketVoiceOutput struct {
	messages chan<- voiceOutboundMessage
	mu       sync.Mutex
	runID    string
}

func (o *websocketVoiceOutput) SendEvent(ctx context.Context, event voice.ServerEvent) error {
	o.mu.Lock()
	if event.RunID != "" {
		o.runID = event.RunID
	} else if event.Type == voice.EventError {
		event.RunID = o.runID
	}
	o.mu.Unlock()
	if event.Type == voice.EventError {
		if event.TerminalFor == "" {
			event.TerminalFor = "event"
		}
		if event.OccurredAt == "" {
			event.OccurredAt = time.Now().UTC().Format(time.RFC3339Nano)
		}
	}
	return o.enqueue(ctx, voiceOutboundMessage{messageType: websocket.TextMessage, payload: event})
}

func (o *websocketVoiceOutput) SendPCM(ctx context.Context, frame []byte) error {
	return o.enqueue(ctx, voiceOutboundMessage{
		messageType: websocket.BinaryMessage, payload: append([]byte(nil), frame...),
	})
}

func (o *websocketVoiceOutput) enqueue(ctx context.Context, message voiceOutboundMessage) error {
	select {
	case o.messages <- message:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func voiceWritePump(ctx context.Context, connection *websocket.Conn, messages <-chan voiceOutboundMessage, done chan<- error) {
	ticker := time.NewTicker(voicePingPeriod)
	defer ticker.Stop()
	defer connection.Close()
	for {
		select {
		case message := <-messages:
			_ = connection.SetWriteDeadline(time.Now().Add(voiceWriteTimeout))
			var err error
			if message.messageType == websocket.TextMessage {
				err = connection.WriteJSON(message.payload)
			} else {
				err = connection.WriteMessage(websocket.BinaryMessage, message.payload.([]byte))
			}
			if err != nil {
				done <- err
				return
			}
		case <-ticker.C:
			_ = connection.SetWriteDeadline(time.Now().Add(voiceWriteTimeout))
			if err := connection.WriteMessage(websocket.PingMessage, nil); err != nil {
				done <- err
				return
			}
		case <-ctx.Done():
			done <- ctx.Err()
			return
		}
	}
}

type voiceConnectionRegistry struct {
	mu          sync.Mutex
	connections map[string]*websocket.Conn
}

func newVoiceConnectionRegistry() *voiceConnectionRegistry {
	return &voiceConnectionRegistry{connections: make(map[string]*websocket.Conn)}
}

func (r *voiceConnectionRegistry) Replace(deviceID string, connection *websocket.Conn) *websocket.Conn {
	r.mu.Lock()
	defer r.mu.Unlock()
	previous := r.connections[deviceID]
	r.connections[deviceID] = connection
	return previous
}

func (r *voiceConnectionRegistry) Remove(deviceID string, connection *websocket.Conn) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.connections[deviceID] == connection {
		delete(r.connections, deviceID)
	}
}

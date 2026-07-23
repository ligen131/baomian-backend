package controller

import (
	"net/http"
	"time"

	"github.com/baomian/baomian-backend/internal/dto"
	"github.com/baomian/baomian-backend/internal/realtime"
	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
)

const (
	writeWait  = 10 * time.Second
	pongWait   = 60 * time.Second
	pingPeriod = 45 * time.Second
)

type WebSocketController struct {
	hub         *realtime.Hub
	defaultUser string
	upgrader    websocket.Upgrader
}

func NewWebSocketController(hub *realtime.Hub, defaultUser string) *WebSocketController {
	return &WebSocketController{
		hub: hub, defaultUser: defaultUser,
		upgrader: websocket.Upgrader{CheckOrigin: func(_ *http.Request) bool { return true }},
	}
}

func (h *WebSocketController) Connect(c *gin.Context) {
	userID := c.Query("userId")
	if userID == "" {
		userID = h.defaultUser
	}
	connection, err := h.upgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		return
	}
	client := &realtime.Client{UserID: userID, Send: make(chan dto.WSEvent, 32)}
	h.hub.Register(client)
	defer func() {
		h.hub.Unregister(client)
		_ = connection.Close()
	}()

	done := make(chan struct{})
	go readPump(connection, done)
	ticker := time.NewTicker(pingPeriod)
	defer ticker.Stop()

	for {
		select {
		case event := <-client.Send:
			_ = connection.SetWriteDeadline(time.Now().Add(writeWait))
			if err := connection.WriteJSON(event); err != nil {
				return
			}
		case <-ticker.C:
			_ = connection.SetWriteDeadline(time.Now().Add(writeWait))
			if err := connection.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		case <-done:
			return
		case <-c.Request.Context().Done():
			return
		}
	}
}

func readPump(connection *websocket.Conn, done chan<- struct{}) {
	defer close(done)
	connection.SetReadLimit(4096)
	_ = connection.SetReadDeadline(time.Now().Add(pongWait))
	connection.SetPongHandler(func(string) error {
		return connection.SetReadDeadline(time.Now().Add(pongWait))
	})
	for {
		if _, _, err := connection.ReadMessage(); err != nil {
			return
		}
	}
}

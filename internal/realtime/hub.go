package realtime

import (
	"sync"

	"github.com/baomian/baomian-backend/internal/dto"
)

type Client struct {
	UserID string
	Send   chan dto.WSEvent
}

type Hub struct {
	mu      sync.RWMutex
	clients map[string]map[*Client]struct{}
}

func NewHub() *Hub {
	return &Hub{clients: make(map[string]map[*Client]struct{})}
}

func (h *Hub) Register(client *Client) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.clients[client.UserID] == nil {
		h.clients[client.UserID] = make(map[*Client]struct{})
	}
	h.clients[client.UserID][client] = struct{}{}
}

func (h *Hub) Unregister(client *Client) {
	h.mu.Lock()
	defer h.mu.Unlock()
	clients := h.clients[client.UserID]
	if clients == nil {
		return
	}
	if _, ok := clients[client]; ok {
		delete(clients, client)
	}
	if len(clients) == 0 {
		delete(h.clients, client.UserID)
	}
}

func (h *Hub) Publish(userID string, event dto.WSEvent) {
	h.mu.RLock()
	clients := make([]*Client, 0, len(h.clients[userID]))
	for client := range h.clients[userID] {
		clients = append(clients, client)
	}
	h.mu.RUnlock()
	for _, client := range clients {
		select {
		case client.Send <- event:
		default:
			// 慢消费者丢弃本条事件；客户端重连后通过 GET /v1/tonight 恢复完整状态。
		}
	}
}

func (h *Hub) Count(userID string) int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.clients[userID])
}

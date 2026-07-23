package realtime

import (
	"testing"

	"github.com/baomian/baomian-backend/internal/dto"
)

func TestHubPublish(t *testing.T) {
	hub := NewHub()
	client := &Client{UserID: "u1", Send: make(chan dto.WSEvent, 1)}
	hub.Register(client)
	if hub.Count("u1") != 1 {
		t.Fatal("client not registered")
	}
	hub.Publish("u1", dto.WSEvent{Type: "tonight.updated"})
	select {
	case event := <-client.Send:
		if event.Type != "tonight.updated" {
			t.Fatalf("unexpected event %s", event.Type)
		}
	default:
		t.Fatal("event not published")
	}
	hub.Unregister(client)
	if hub.Count("u1") != 0 {
		t.Fatal("client not unregistered")
	}
}

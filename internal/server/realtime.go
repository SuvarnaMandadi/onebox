package server

import (
	"sync"
)

// realtimeEvent is broadcast over SSE whenever a record is created,
// updated, or deleted.
type realtimeEvent struct {
	Action     string         `json:"action"` // "create", "update", or "delete"
	Collection string         `json:"collection"`
	Record     map[string]any `json:"record"`
}

// realtimeClient is one connected SSE subscriber. isAdmin/userID capture
// enough of the connection's identity to apply the same access rules used
// by the records API, so a subscriber never receives an event for a
// record it couldn't otherwise view.
type realtimeClient struct {
	ch      chan realtimeEvent
	isAdmin bool
	userID  string
}

// realtimeHub fans out record-change events to connected SSE clients,
// filtered per event by the collection's view rule.
type realtimeHub struct {
	mu      sync.Mutex
	clients map[*realtimeClient]struct{}
}

func newRealtimeHub() *realtimeHub {
	return &realtimeHub{clients: make(map[*realtimeClient]struct{})}
}

func (h *realtimeHub) subscribe(isAdmin bool, userID string) *realtimeClient {
	c := &realtimeClient{ch: make(chan realtimeEvent, 16), isAdmin: isAdmin, userID: userID}
	h.mu.Lock()
	h.clients[c] = struct{}{}
	h.mu.Unlock()
	return c
}

func (h *realtimeHub) unsubscribe(c *realtimeClient) {
	h.mu.Lock()
	if _, ok := h.clients[c]; ok {
		delete(h.clients, c)
		close(c.ch)
	}
	h.mu.Unlock()
}

func (h *realtimeHub) clientCount() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.clients)
}

// publish sends evt to every subscriber allowed to view it under rule
// (the collection's view rule) and ownerID (the record's owner_id, if
// any). Slow consumers are dropped rather than blocking the publisher.
func (h *realtimeHub) publish(evt realtimeEvent, rule RuleKind, ownerID string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for c := range h.clients {
		if !clientCanSee(c, rule, ownerID) {
			continue
		}
		select {
		case c.ch <- evt:
		default:
		}
	}
}

func clientCanSee(c *realtimeClient, rule RuleKind, ownerID string) bool {
	if c.isAdmin {
		return true
	}
	switch rule {
	case RulePublic:
		return true
	case RuleAuthenticated:
		return c.userID != ""
	case RuleOwner:
		return c.userID != "" && c.userID == ownerID
	default:
		return false
	}
}

func ownerIDOf(rec map[string]any) string {
	if v, ok := rec["owner_id"].(string); ok {
		return v
	}
	return ""
}

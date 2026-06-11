// Package events is the in-process realtime hub behind the SSE endpoint (Phase 6).
//
// Design: events are CACHE-INVALIDATION HINTS, not a data stream. Each event names
// a topic ("workloads", "nodes", "queue", "budgets", "members") and optionally a
// few ids; the SPA reacts by re-fetching the relevant queries it already knows how
// to fetch. This keeps the hub trivial (no payload schemas, no ordering or replay
// requirements — a missed event is healed by the next one or the poll fallback)
// and means SSE is purely an optimization over the existing 5s polling.
//
// Scope note: in-process only, like the agent dispatcher. A multi-replica control
// plane would need a pub/sub bridge (Postgres LISTEN/NOTIFY or Redis) behind the
// same Publish/Subscribe seam.
package events

import (
	"sync"

	"github.com/google/uuid"
)

// Event is one org-scoped change notification.
type Event struct {
	Topic string         `json:"topic"`
	Data  map[string]any `json:"data,omitempty"`
}

// Standard topics. The frontend maps each to the TanStack Query keys it refreshes.
const (
	TopicNodes     = "nodes"
	TopicWorkloads = "workloads"
	TopicQueue     = "queue"
	TopicBudgets   = "budgets"
	TopicMembers   = "members"
)

type Hub struct {
	mu   sync.RWMutex
	subs map[uuid.UUID]map[chan Event]struct{}
}

func NewHub() *Hub {
	return &Hub{subs: map[uuid.UUID]map[chan Event]struct{}{}}
}

// Subscribe registers a listener for one org's events. The returned cancel must be
// called when the consumer goes away (the SSE handler defers it).
func (h *Hub) Subscribe(orgID uuid.UUID) (<-chan Event, func()) {
	ch := make(chan Event, 16)
	h.mu.Lock()
	if h.subs[orgID] == nil {
		h.subs[orgID] = map[chan Event]struct{}{}
	}
	h.subs[orgID][ch] = struct{}{}
	h.mu.Unlock()
	return ch, func() {
		h.mu.Lock()
		if set, ok := h.subs[orgID]; ok {
			delete(set, ch)
			if len(set) == 0 {
				delete(h.subs, orgID)
			}
		}
		h.mu.Unlock()
	}
}

// Publish fans an event out to the org's subscribers. Non-blocking: a slow
// consumer's full buffer drops the hint (the poll fallback heals it) rather than
// stalling the publishing request path.
func (h *Hub) Publish(orgID uuid.UUID, e Event) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	for ch := range h.subs[orgID] {
		select {
		case ch <- e:
		default:
		}
	}
}

// PublishTopics is the common multi-topic convenience (e.g. a workload transition
// touches "workloads" + "queue" + "budgets").
func (h *Hub) PublishTopics(orgID uuid.UUID, topics ...string) {
	for _, t := range topics {
		h.Publish(orgID, Event{Topic: t})
	}
}

// Subscribers reports the live subscriber count (diagnostics/metrics).
func (h *Hub) Subscribers() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	n := 0
	for _, set := range h.subs {
		n += len(set)
	}
	return n
}

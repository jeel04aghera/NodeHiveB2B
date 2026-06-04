package agentgw

import (
	"context"
	"fmt"
	"sync"
	"time"

	agentv1 "github.com/nodehive/gpu-platform/gen/go/agent/v1"
	"github.com/google/uuid"
)

// Dispatcher routes server-to-agent commands to the correct open stream.
// V1 is in-memory (single process). Replace Send() with a Redis pub/sub
// publish to scale beyond one control-plane replica.
type Dispatcher struct {
	mu      sync.RWMutex
	streams map[uuid.UUID]chan *agentv1.ServerMessage
}

var GlobalDispatcher = &Dispatcher{streams: make(map[uuid.UUID]chan *agentv1.ServerMessage)}

func (d *Dispatcher) Register(nodeID uuid.UUID) chan *agentv1.ServerMessage {
	ch := make(chan *agentv1.ServerMessage, 8)
	d.mu.Lock()
	d.streams[nodeID] = ch
	d.mu.Unlock()
	return ch
}

// Deregister removes the node's channel ONLY if it is still the one passed in.
// On reconnect a newer stream may have already replaced it; deleting blindly would
// orphan the live stream (node appears online via heartbeats but never receives
// commands). Returns true if this call actually removed the current registration.
func (d *Dispatcher) Deregister(nodeID uuid.UUID, ch chan *agentv1.ServerMessage) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	if cur, ok := d.streams[nodeID]; ok && cur == ch {
		close(cur)
		delete(d.streams, nodeID)
		return true
	}
	return false
}

// Send delivers a command to the agent. Returns error if the agent is not connected or the
// channel is full after a short timeout.
func (d *Dispatcher) Send(ctx context.Context, nodeID uuid.UUID, msg *agentv1.ServerMessage) error {
	d.mu.RLock()
	ch, ok := d.streams[nodeID]
	d.mu.RUnlock()
	if !ok {
		return fmt.Errorf("agent %s not connected", nodeID)
	}
	select {
	case ch <- msg:
		return nil
	case <-time.After(5 * time.Second):
		return fmt.Errorf("agent %s command channel full", nodeID)
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (d *Dispatcher) ConnectedNodes() []uuid.UUID {
	d.mu.RLock()
	defer d.mu.RUnlock()
	ids := make([]uuid.UUID, 0, len(d.streams))
	for id := range d.streams {
		ids = append(ids, id)
	}
	return ids
}

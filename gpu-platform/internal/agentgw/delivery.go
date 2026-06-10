package agentgw

import (
	"context"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/protobuf/proto"

	agentv1 "github.com/nodehive/gpu-platform/gen/go/agent/v1"
	"github.com/nodehive/gpu-platform/internal/commands"
)

// DeliveryEngine drains the agent_commands outbox to connected agents.
//
// Delivery is at-least-once: a row is claimed under FOR UPDATE SKIP LOCKED, pushed
// down the node's stream, and marked 'sent' with a resend backoff. If the agent's
// CommandResult ack never arrives (stream died mid-flight, control plane restarted),
// the row comes due again and is resent — the agent deduplicates by command id.
// Rows are finalized by Ack (acked/failed), by the workload sweep (expired →
// explicit workload failure, superseded → workload already terminal).
//
// Wake-ups: an immediate Nudge after enqueue/reconnect for latency, plus a periodic
// tick as the reliable fallback (a lost nudge delays delivery, never loses it).
type DeliveryEngine struct {
	db   *pgxpool.Pool
	d    *Dispatcher
	log  *slog.Logger
	wake chan uuid.UUID
}

func NewDeliveryEngine(db *pgxpool.Pool, d *Dispatcher, log *slog.Logger) *DeliveryEngine {
	return &DeliveryEngine{db: db, d: d, log: log, wake: make(chan uuid.UUID, 64)}
}

// Nudge implements workloads.Dispatcher: non-blocking hint that nodeID has new
// deliverable commands. Dropped when the buffer is full — the tick catches up.
func (e *DeliveryEngine) Nudge(nodeID uuid.UUID) {
	select {
	case e.wake <- nodeID:
	default:
	}
}

// Run is the delivery loop. Tick cadence is short (5s) because a due scan for a
// node with nothing outstanding is a single cheap partial-index probe.
func (e *DeliveryEngine) Run(ctx context.Context) {
	t := time.NewTicker(5 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case nodeID := <-e.wake:
			e.deliverNode(ctx, nodeID)
		case <-t.C:
			for _, nodeID := range e.d.ConnectedNodes() {
				e.deliverNode(ctx, nodeID)
			}
		}
	}
}

// deliverNode sends every due command for one node. Claim + send + bookkeeping run
// in one transaction per batch; SKIP LOCKED keeps a concurrent deliverer (nudge vs
// tick) from double-sending the same rows in the same instant.
func (e *DeliveryEngine) deliverNode(ctx context.Context, nodeID uuid.UUID) {
	for {
		n, err := e.deliverBatch(ctx, nodeID)
		if err != nil {
			e.log.Warn("command delivery failed", "node_id", nodeID, "err", err)
			return
		}
		if n == 0 {
			return
		}
	}
}

func (e *DeliveryEngine) deliverBatch(ctx context.Context, nodeID uuid.UUID) (int, error) {
	tx, err := e.db.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	due, err := commands.ClaimDue(ctx, tx, nodeID, 16)
	if err != nil || len(due) == 0 {
		return 0, err
	}
	delivered := 0
	for _, c := range due {
		msg := &agentv1.ServerMessage{}
		if err := proto.Unmarshal(c.Payload, msg); err != nil {
			// Unparseable payload can never be delivered; fail it explicitly.
			if err := commands.Ack(ctx, tx, c.ID, false, "malformed payload: "+err.Error()); err != nil {
				return delivered, err
			}
			continue
		}
		sendCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		sendErr := e.d.Send(sendCtx, nodeID, msg)
		cancel()
		if sendErr != nil {
			// Not connected / channel full: stays due, retried after backoff.
			if err := commands.MarkAttemptFailed(ctx, tx, c.ID, c.Attempts, sendErr.Error()); err != nil {
				return delivered, err
			}
			continue
		}
		if err := commands.MarkSent(ctx, tx, c.ID, c.Attempts); err != nil {
			return delivered, err
		}
		delivered++
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, err
	}
	return delivered, nil
}

// OnConnect runs when an agent's stream (re)establishes: every outstanding command
// for the node becomes immediately due (a reconnect is new information — waiting
// out a backoff computed while the agent was unreachable would just delay
// recovery) and is delivered synchronously.
func (e *DeliveryEngine) OnConnect(ctx context.Context, nodeID uuid.UUID) {
	if _, err := e.db.Exec(ctx,
		`UPDATE agent_commands SET next_attempt_at=now()
		  WHERE node_id=$1 AND status IN ('pending','sent')`, nodeID); err != nil {
		e.log.Warn("reset due on reconnect failed", "node_id", nodeID, "err", err)
		return
	}
	e.deliverNode(ctx, nodeID)
}

// Ack finalizes a command from the agent's CommandResult.
func (e *DeliveryEngine) Ack(ctx context.Context, commandID string, ok bool, errMsg string) {
	id, err := uuid.Parse(commandID)
	if err != nil {
		return
	}
	if err := commands.Ack(ctx, e.db, id, ok, errMsg); err != nil {
		e.log.Warn("command ack persist failed", "command_id", commandID, "err", err)
	}
}

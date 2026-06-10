// Package commands is the transactional outbox for server→agent commands.
//
// A command row is INSERTed in the same database transaction as the workload state
// change it implements ("pending" workload ⇔ launch row, "stopping" ⇔ stop row), so
// the intent is durable before anything is sent. Delivery is at-least-once: the
// delivery engine (internal/agentgw) pushes due rows down the agent stream and the
// agent acknowledges by command id, deduplicating replays. Rows that can't be
// delivered by their deadline are expired, which the workload sweep converts into an
// explicit workload failure — a command is never silently lost.
package commands

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

type Kind string

const (
	KindLaunch       Kind = "launch"
	KindStop         Kind = "stop"
	KindGetInventory Kind = "get_inventory"
)

// Command is one durable server→agent instruction.
type Command struct {
	ID         uuid.UUID
	OrgID      uuid.UUID
	NodeID     uuid.UUID
	WorkloadID *uuid.UUID
	Kind       Kind
	Payload    []byte // marshaled agentv1.ServerMessage (CommandId == ID)
	DeliverBy  *time.Time
}

// DB is the minimal query surface shared by *pgxpool.Pool and pgx.Tx, so Enqueue
// can participate in the caller's transaction (the whole point of an outbox).
type DB interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// Enqueue stores a command. Call inside the transaction that records the state the
// command implements.
func Enqueue(ctx context.Context, db DB, c Command) error {
	_, err := db.Exec(ctx,
		`INSERT INTO agent_commands (id, org_id, node_id, workload_id, kind, payload, deliver_by)
		 VALUES ($1,$2,$3,$4,$5,$6,$7)`,
		c.ID, c.OrgID, c.NodeID, c.WorkloadID, c.Kind, c.Payload, c.DeliverBy)
	return err
}

// Due is a deliverable command claimed by the delivery engine.
type Due struct {
	ID       uuid.UUID
	Kind     Kind
	Payload  []byte
	Attempts int
}

// resendBackoff spaces redelivery attempts: 15s, 30s, 1m, 2m, then 4m. The agent
// dedupes by command id, so an early resend is wasted bandwidth, not a bug.
func resendBackoff(attempts int) time.Duration {
	d := 15 * time.Second << min(attempts, 4)
	if d > 4*time.Minute {
		d = 4 * time.Minute
	}
	return d
}

// ClaimDue locks and returns up to limit deliverable commands for a node, advancing
// their attempt bookkeeping. FOR UPDATE SKIP LOCKED makes concurrent deliverers
// (ticker + reconnect nudge) cooperate instead of double-sending in a tight race.
// Run inside a transaction; the rows stay 'sent' until acked or expired.
func ClaimDue(ctx context.Context, tx pgx.Tx, nodeID uuid.UUID, limit int) ([]Due, error) {
	rows, err := tx.Query(ctx,
		`SELECT id, kind, payload, attempts FROM agent_commands
		  WHERE node_id=$1 AND status IN ('pending','sent') AND next_attempt_at <= now()
		  ORDER BY created_at
		  LIMIT $2
		  FOR UPDATE SKIP LOCKED`, nodeID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Due
	for rows.Next() {
		var d Due
		if err := rows.Scan(&d.ID, &d.Kind, &d.Payload, &d.Attempts); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// MarkSent records a successful hand-off to the agent stream.
func MarkSent(ctx context.Context, tx pgx.Tx, id uuid.UUID, attempts int) error {
	_, err := tx.Exec(ctx,
		`UPDATE agent_commands SET status='sent', attempts=$2,
		        sent_at=COALESCE(sent_at, now()), next_attempt_at=now()+make_interval(secs => $3)
		  WHERE id=$1`,
		id, attempts+1, resendBackoff(attempts).Seconds())
	return err
}

// MarkAttemptFailed records a failed delivery attempt (agent not connected, channel
// full); the row stays due and is retried after backoff.
func MarkAttemptFailed(ctx context.Context, tx pgx.Tx, id uuid.UUID, attempts int, reason string) error {
	_, err := tx.Exec(ctx,
		`UPDATE agent_commands SET attempts=$2, last_error=$3, next_attempt_at=now()+make_interval(secs => $4)
		  WHERE id=$1`,
		id, attempts+1, reason, resendBackoff(attempts).Seconds())
	return err
}

// Ack finalizes a command from the agent's CommandResult. ok=false is an explicit
// failure (the agent received the command and reports it could not be executed).
func Ack(ctx context.Context, db DB, id uuid.UUID, ok bool, errMsg string) error {
	status := "acked"
	if !ok {
		status = "failed"
	}
	_, err := db.Exec(ctx,
		`UPDATE agent_commands SET status=$2, acked_at=now(), last_error=$3
		  WHERE id=$1 AND status IN ('pending','sent')`, id, status, errMsg)
	return err
}

// Expired is a command that missed its delivery deadline.
type Expired struct {
	ID         uuid.UUID
	Kind       Kind
	WorkloadID *uuid.UUID
}

// ExpireOverdue marks commands that missed deliver_by without an ack. The caller
// (workload sweep) turns expired launches into explicit workload failures.
func ExpireOverdue(ctx context.Context, db DB) ([]Expired, error) {
	rows, err := db.Query(ctx,
		`UPDATE agent_commands SET status='expired',
		        last_error=trim('; ' FROM last_error || '; delivery deadline exceeded')
		  WHERE status IN ('pending','sent') AND deliver_by IS NOT NULL AND deliver_by < now()
		  RETURNING id, kind, workload_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Expired
	for rows.Next() {
		var e Expired
		if err := rows.Scan(&e.ID, &e.Kind, &e.WorkloadID); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// SupersedeForTerminalWorkloads cancels undelivered commands whose workload already
// reached a terminal state by another path (sweep reclaim, agent report), so the
// engine stops retrying instructions that no longer matter.
func SupersedeForTerminalWorkloads(ctx context.Context, db DB) (int, error) {
	ct, err := db.Exec(ctx,
		`UPDATE agent_commands c SET status='superseded'
		  FROM workloads w
		 WHERE c.workload_id = w.id
		   AND c.status IN ('pending','sent')
		   AND w.status IN ('stopped','failed')`)
	if err != nil {
		return 0, err
	}
	return int(ct.RowsAffected()), nil
}

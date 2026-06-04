// Package transport owns the single outbound connection to the control plane:
// Enroll once, then a persistent bidirectional stream. It handles TLS/pinned-CA,
// reconnect-with-backoff, and on-reconnect reconciliation.
//
// AgentMessage/ServerMessage are the generated agent/v1 proto types once
// `make generate` runs; they are `any` here so this package compiles before
// codegen. The Transport interface is the seam the rest of the agent tests against.
package transport

import "context"

type Transport interface {
	// Enroll exchanges a one-time token for a durable credential (first run only).
	Enroll(ctx context.Context, token, publicKey string) (nodeID, credential string, err error)
	// Connect opens the persistent outbound stream and blocks until ctx is done
	// or the stream fails (caller retries with backoff).
	Connect(ctx context.Context) error
	// Send delivers an AgentMessage (heartbeat/inventory/metrics/status/result).
	Send(ctx context.Context, msg any) error
	// Recv returns the next ServerMessage (launch/stop/get_inventory/update).
	Recv(ctx context.Context) (any, error)
	Close() error
}

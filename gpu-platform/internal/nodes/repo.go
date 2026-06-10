// Package nodes implements node enrollment, heartbeat persistence, and node queries
// for the V1 slice. The repository uses pgx directly (hand-written SQL) so the slice
// compiles and runs without a codegen step beyond proto — sqlc adoption is a clean
// follow-up (sqlc.yaml is already in the repo).
package nodes

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	ErrNotFound            = errors.New("node not found")
	ErrInvalidToken        = errors.New("invalid enrollment token")
	ErrTokenExpired        = errors.New("enrollment token expired")
	ErrTokenExhausted      = errors.New("enrollment token exhausted")
	ErrFingerprintConflict = errors.New("fingerprint already enrolled by another organization")
)

// NodeView is the read model returned to the API (includes derived gpu_count).
type NodeView struct {
	ID           uuid.UUID
	OrgID        uuid.UUID
	Hostname     string
	Status       string
	OS           string
	NvidiaDriver string
	CUDAVersion  string
	AgentVersion string
	GPUCount     int
	EnrolledAt   time.Time
	LastSeenAt   *time.Time
}

// NodeInfo is the hardware/software facts supplied at enrollment.
type NodeInfo struct {
	Hostname     string
	OS           string
	Kernel       string
	CPUModel     string
	CPUCores     int
	RAMMB        int64
	NvidiaDriver string
	CUDAVersion  string
	AgentVersion string
}

type Repo struct{ pool *pgxpool.Pool }

func NewRepo(pool *pgxpool.Pool) *Repo { return &Repo{pool: pool} }

// Enroll validates+consumes the token, registers (or re-registers) the node by
// fingerprint, and stores the agent credential — all in a single transaction.
//
// Fingerprints are CLIENT-SUPPLIED, so they are never trusted to move a node between
// tenants: a fingerprint already enrolled by a different org is rejected with
// ErrFingerprintConflict instead of silently re-homing the node (C2: node hijack).
// A same-org re-enrollment is a credential rotation: every previous credential for
// the node is revoked before the new one is stored, so a leaked old credential dies
// the moment the box re-enrolls.
func (r *Repo) Enroll(ctx context.Context, tokenHash, fingerprint string, info NodeInfo, credentialHash string) (nodeID, orgID uuid.UUID, err error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return uuid.Nil, uuid.Nil, fmt.Errorf("begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }() // no-op after Commit

	var tokenID uuid.UUID
	var expiresAt time.Time
	var uses, maxUses int
	err = tx.QueryRow(ctx,
		`SELECT id, org_id, expires_at, uses, max_uses FROM enrollment_tokens WHERE token_hash=$1 FOR UPDATE`,
		tokenHash).Scan(&tokenID, &orgID, &expiresAt, &uses, &maxUses)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, uuid.Nil, ErrInvalidToken
	}
	if err != nil {
		return uuid.Nil, uuid.Nil, fmt.Errorf("lookup token: %w", err)
	}
	if time.Now().After(expiresAt) {
		return uuid.Nil, uuid.Nil, ErrTokenExpired
	}
	if uses >= maxUses {
		return uuid.Nil, uuid.Nil, ErrTokenExhausted
	}
	if _, err = tx.Exec(ctx, `UPDATE enrollment_tokens SET uses = uses + 1 WHERE id=$1`, tokenID); err != nil {
		return uuid.Nil, uuid.Nil, fmt.Errorf("consume token: %w", err)
	}

	// Lock the fingerprint row (if any) so a concurrent enrollment of the same
	// fingerprint serializes here instead of racing the insert below.
	var existingID, existingOrg uuid.UUID
	err = tx.QueryRow(ctx,
		`SELECT id, org_id FROM gpu_nodes WHERE fingerprint=$1 FOR UPDATE`,
		fingerprint).Scan(&existingID, &existingOrg)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		// First enrollment of this fingerprint. The unique index on fingerprint
		// backstops the (tiny) insert race left after the lock-by-select.
		err = tx.QueryRow(ctx,
			`INSERT INTO gpu_nodes
			   (org_id, fingerprint, hostname, status, os, kernel, cpu_model, cpu_cores, ram_mb,
			    nvidia_driver, cuda_version, agent_version, last_seen_at)
			 VALUES ($1,$2,$3,'online',$4,$5,$6,$7,$8,$9,$10,$11, now())
			 RETURNING id`,
			orgID, fingerprint, info.Hostname, info.OS, info.Kernel, info.CPUModel,
			info.CPUCores, info.RAMMB, info.NvidiaDriver, info.CUDAVersion, info.AgentVersion,
		).Scan(&nodeID)
		if err != nil {
			return uuid.Nil, uuid.Nil, fmt.Errorf("insert node: %w", err)
		}
	case err != nil:
		return uuid.Nil, uuid.Nil, fmt.Errorf("lookup node: %w", err)
	case existingOrg != orgID:
		// Fingerprint belongs to another tenant — refuse, never re-home.
		return uuid.Nil, uuid.Nil, ErrFingerprintConflict
	default:
		// Same-org re-enrollment: refresh node facts, rotate credentials.
		nodeID = existingID
		if _, err = tx.Exec(ctx,
			`UPDATE gpu_nodes SET
			   hostname=$2, status='online', os=$3, kernel=$4, cpu_model=$5, cpu_cores=$6,
			   ram_mb=$7, nvidia_driver=$8, cuda_version=$9, agent_version=$10, last_seen_at=now()
			 WHERE id=$1`,
			nodeID, info.Hostname, info.OS, info.Kernel, info.CPUModel,
			info.CPUCores, info.RAMMB, info.NvidiaDriver, info.CUDAVersion, info.AgentVersion); err != nil {
			return uuid.Nil, uuid.Nil, fmt.Errorf("update node: %w", err)
		}
		if _, err = tx.Exec(ctx,
			`UPDATE agent_credentials SET revoked_at=now()
			   WHERE node_id=$1 AND revoked_at IS NULL`, nodeID); err != nil {
			return uuid.Nil, uuid.Nil, fmt.Errorf("revoke old credentials: %w", err)
		}
	}

	if _, err = tx.Exec(ctx,
		`INSERT INTO agent_credentials (org_id, node_id, public_key) VALUES ($1,$2,$3)
		 ON CONFLICT (public_key) DO NOTHING`,
		orgID, nodeID, credentialHash); err != nil {
		return uuid.Nil, uuid.Nil, fmt.Errorf("store credential: %w", err)
	}

	if err = tx.Commit(ctx); err != nil {
		return uuid.Nil, uuid.Nil, fmt.Errorf("commit: %w", err)
	}
	return nodeID, orgID, nil
}

// AuthenticateAgent resolves a credential hash to its node identity.
func (r *Repo) AuthenticateAgent(ctx context.Context, credentialHash string) (nodeID, orgID uuid.UUID, err error) {
	err = r.pool.QueryRow(ctx,
		`SELECT n.id, n.org_id
		   FROM agent_credentials c JOIN gpu_nodes n ON n.id = c.node_id
		  WHERE c.public_key=$1 AND c.revoked_at IS NULL`, credentialHash).Scan(&nodeID, &orgID)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, uuid.Nil, ErrNotFound
	}
	return nodeID, orgID, err
}

// RecordHeartbeat updates node liveness and appends a heartbeat row (one tx).
func (r *Repo) RecordHeartbeat(ctx context.Context, orgID, nodeID uuid.UUID, agentVersion string, gpuCount int, meanUtil float32) error {
	summary, _ := json.Marshal(map[string]any{"gpu_count": gpuCount, "mean_util_pct": meanUtil})
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err = tx.Exec(ctx,
		`UPDATE gpu_nodes SET last_seen_at = now(), status='online', agent_version=$2 WHERE id=$1`,
		nodeID, agentVersion); err != nil {
		return fmt.Errorf("update node: %w", err)
	}
	if _, err = tx.Exec(ctx,
		`INSERT INTO agent_heartbeats (org_id, node_id, ts, status, agent_version, summary)
		 VALUES ($1,$2, now(), 'healthy', $3, $4)`,
		orgID, nodeID, agentVersion, summary); err != nil {
		return fmt.Errorf("insert heartbeat: %w", err)
	}
	return tx.Commit(ctx)
}

func (r *Repo) SetNodeStatus(ctx context.Context, nodeID uuid.UUID, status string) error {
	_, err := r.pool.Exec(ctx, `UPDATE gpu_nodes SET status=$2 WHERE id=$1`, nodeID, status)
	return err
}

const selectNodeCols = `
SELECT n.id, n.org_id, n.hostname, n.status, n.os, n.nvidia_driver, n.cuda_version, n.agent_version,
       (SELECT count(*) FROM gpus g WHERE g.node_id = n.id) AS gpu_count,
       n.enrolled_at, n.last_seen_at
  FROM gpu_nodes n `

func scanNode(row pgx.Row) (NodeView, error) {
	var v NodeView
	var lastSeen sql.NullTime
	if err := row.Scan(&v.ID, &v.OrgID, &v.Hostname, &v.Status, &v.OS, &v.NvidiaDriver,
		&v.CUDAVersion, &v.AgentVersion, &v.GPUCount, &v.EnrolledAt, &lastSeen); err != nil {
		return NodeView{}, err
	}
	if lastSeen.Valid {
		t := lastSeen.Time
		v.LastSeenAt = &t
	}
	return v, nil
}

func (r *Repo) ListNodes(ctx context.Context, orgID uuid.UUID) ([]NodeView, error) {
	rows, err := r.pool.Query(ctx, selectNodeCols+`WHERE n.org_id=$1 ORDER BY n.hostname`, orgID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []NodeView
	for rows.Next() {
		v, err := scanNode(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func (r *Repo) GetNodeByID(ctx context.Context, id uuid.UUID) (NodeView, error) {
	v, err := scanNode(r.pool.QueryRow(ctx, selectNodeCols+`WHERE n.id=$1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return NodeView{}, ErrNotFound
	}
	return v, err
}

func (r *Repo) GetNodeByFingerprint(ctx context.Context, fingerprint string) (NodeView, error) {
	v, err := scanNode(r.pool.QueryRow(ctx, selectNodeCols+`WHERE n.fingerprint=$1`, fingerprint))
	if errors.Is(err, pgx.ErrNoRows) {
		return NodeView{}, ErrNotFound
	}
	return v, err
}

func (r *Repo) OrgIDBySlug(ctx context.Context, slug string) (uuid.UUID, error) {
	var id uuid.UUID
	err := r.pool.QueryRow(ctx, `SELECT id FROM organizations WHERE slug=$1`, slug).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, ErrNotFound
	}
	return id, err
}

// EnsureEnrollmentToken upserts a token by hash (used to seed the dev token).
func (r *Repo) EnsureEnrollmentToken(ctx context.Context, orgID uuid.UUID, tokenHash string, expiresAt time.Time, maxUses int) error {
	_, err := r.pool.Exec(ctx,
		`INSERT INTO enrollment_tokens (org_id, token_hash, expires_at, max_uses, uses)
		 VALUES ($1,$2,$3,$4,0)
		 ON CONFLICT (token_hash) DO UPDATE SET expires_at=EXCLUDED.expires_at, max_uses=EXCLUDED.max_uses`,
		orgID, tokenHash, expiresAt, maxUses)
	return err
}

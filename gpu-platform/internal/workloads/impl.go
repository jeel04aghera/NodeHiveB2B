package workloads

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/protobuf/proto"

	agentv1 "github.com/nodehive/gpu-platform/gen/go/agent/v1"
	workloadv1 "github.com/nodehive/gpu-platform/gen/go/workload/v1"
	"github.com/nodehive/gpu-platform/internal/commands"
	"github.com/nodehive/gpu-platform/internal/domain"
)

var ErrNotFound = errors.New("workload not found")
var ErrNoGPUsAvailable = errors.New("no GPUs available matching request")
var ErrNotQueued = errors.New("workload is not queued")
var ErrInvalidRequest = errors.New("invalid launch request")

// Launch request bounds (M6). These are abuse limits, not capacity planning: gpu_count
// is capped so a single request can't squat the queue, and the idle timeout is bounded
// so a negative value can't wrap to a near-infinite uint32 on the wire and a huge value
// can't opt a workload out of idle reclaim entirely.
const (
	maxGPUsPerWorkload = 16
	maxNameLen         = 120
	maxImageLen        = 256
	minIdleTimeoutSec  = 60
	maxIdleTimeoutSec  = 7 * 24 * 3600 // 7 days
)

// validateLaunch normalizes and bounds a launch request. Returns ErrInvalidRequest
// (wrapped with the specific reason) so the API layer can map it to a 400.
func validateLaunch(req *LaunchRequest) error {
	req.Name = strings.TrimSpace(req.Name)
	switch {
	case req.Name == "":
		return fmt.Errorf("%w: name is required", ErrInvalidRequest)
	case len(req.Name) > maxNameLen:
		return fmt.Errorf("%w: name must be at most %d characters", ErrInvalidRequest, maxNameLen)
	case req.GPUCount > maxGPUsPerWorkload:
		return fmt.Errorf("%w: gpu_count must be between 1 and %d", ErrInvalidRequest, maxGPUsPerWorkload)
	case len(req.Image) > maxImageLen:
		return fmt.Errorf("%w: image reference too long", ErrInvalidRequest)
	case req.Image != "" && !imageRefPattern.MatchString(req.Image):
		return fmt.Errorf("%w: image is not a valid container image reference", ErrInvalidRequest)
	}
	if req.GPUCount <= 0 {
		req.GPUCount = 1
	}
	if req.IdleTimeoutSec != nil {
		t := *req.IdleTimeoutSec
		switch {
		case t < 0:
			return fmt.Errorf("%w: idle_timeout_sec cannot be negative", ErrInvalidRequest)
		case t == 0:
			req.IdleTimeoutSec = nil // 0 = use the default, never "no timeout"
		case t < minIdleTimeoutSec:
			return fmt.Errorf("%w: idle_timeout_sec must be at least %d", ErrInvalidRequest, minIdleTimeoutSec)
		case t > maxIdleTimeoutSec:
			return fmt.Errorf("%w: idle_timeout_sec must be at most %d (7 days)", ErrInvalidRequest, maxIdleTimeoutSec)
		}
	}
	return nil
}

// imageRefPattern is a conservative container image reference check: registry/repo,
// optional :tag or @sha256:digest. It rejects whitespace, shell metacharacters and
// control bytes before the string travels to a docker invocation on the agent.
var imageRefPattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._\-]*(?::[0-9]+)?(?:/[a-zA-Z0-9._\-]+)*(?::[a-zA-Z0-9._\-]+)?(?:@sha256:[a-fA-F0-9]{64})?$`)

// Biller is the billing seam — satisfied by billing.Service. AuthorizeLaunch gates
// admission (credit balance + budgets); MeterWorkload settles a stopped workload's
// final slice (the periodic sweep recovers it if this call is lost to a crash).
type Biller interface {
	AuthorizeLaunch(ctx context.Context, orgID uuid.UUID, departmentID, projectID *uuid.UUID) error
	MeterWorkload(ctx context.Context, workloadID uuid.UUID) error
}

// ServiceImpl wires together placement, DB persistence, and command dispatch.
type ServiceImpl struct {
	db       *pgxpool.Pool
	dispatch Dispatcher
	billing  Biller
	auditRec func(ctx context.Context, e domain.AuditLog) // optional, fire-and-forget
	publish  func(orgID uuid.UUID, topics ...string)      // optional realtime hints (Phase 6)
}

// Option configures optional service features without breaking existing callers.
type Option func(*ServiceImpl)

// WithAudit records system/agent-driven workload lifecycle transitions (sweep
// reclaims, idle stops, agent-reported failures) in the audit trail. User-initiated
// launch/stop are audited at the HTTP layer where the actor is known.
func WithAudit(rec func(ctx context.Context, e domain.AuditLog)) Option {
	return func(s *ServiceImpl) { s.auditRec = rec }
}

// WithEvents publishes realtime cache-invalidation hints (SSE, Phase 6) on
// workload/queue state transitions.
func WithEvents(pub func(orgID uuid.UUID, topics ...string)) Option {
	return func(s *ServiceImpl) { s.publish = pub }
}

// notify is the nil-safe publish helper.
func (s *ServiceImpl) notify(orgID uuid.UUID, topics ...string) {
	if s.publish != nil {
		s.publish(orgID, topics...)
	}
}

func NewService(db *pgxpool.Pool, dispatch Dispatcher, billing Biller, opts ...Option) Service {
	s := &ServiceImpl{db: db, dispatch: dispatch, billing: billing}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// auditSystem emits a system-actor audit event when an auditor is wired.
func (s *ServiceImpl) auditSystem(ctx context.Context, orgID uuid.UUID, actorType, action string, workloadID uuid.UUID, meta map[string]any) {
	if s.auditRec == nil {
		return
	}
	s.auditRec(ctx, domain.AuditLog{
		OrgID: orgID, ActorType: actorType,
		Action: action, TargetType: "workload", TargetID: workloadID.String(),
		Metadata: meta,
	})
}

func (s *ServiceImpl) Launch(ctx context.Context, orgID uuid.UUID, req LaunchRequest) (domain.Workload, error) {
	if err := validateLaunch(&req); err != nil {
		return domain.Workload{}, err
	}
	// Admission gate (Billing P0): no credit, or an exhausted budget in scope, refuses
	// the launch BEFORE any GPU is reserved or queue slot taken.
	if s.billing != nil {
		if err := s.billing.AuthorizeLaunch(ctx, orgID, req.DepartmentID, req.ProjectID); err != nil {
			return domain.Workload{}, err
		}
	}
	image := req.Image
	if image == "" {
		image = "ubuntu:22.04"
	}
	idleTimeout := 3600
	if req.IdleTimeoutSec != nil {
		idleTimeout = *req.IdleTimeoutSec
	}
	sshPassword := ""
	if req.ExposeSSH {
		sshPassword = randomPassword()
	}
	gpuType := req.GPUType
	if gpuType == "" {
		gpuType = "any"
	}

	id := uuid.New()
	wl := domain.Workload{
		ID:                id,
		OrgID:             orgID,
		ProjectID:         req.ProjectID,
		DepartmentID:      req.DepartmentID,
		TemplateID:        req.TemplateID,
		UserID:            req.UserID,
		Name:              req.Name,
		Image:             image,
		ContainerID:       "nodehive-" + strings.ReplaceAll(id.String(), "-", "")[:20],
		RequestedGPUCount: req.GPUCount,
		IdleTimeoutSec:    &idleTimeout,
		ExposeSSH:         req.ExposeSSH,
		ExposeJupyter:     req.ExposeJupyter,
		SSHPassword:       sshPassword,
		CreatedAt:         time.Now(),
	}

	// Placement, workload insert, GPU claim and the launch command are ONE
	// transaction: either the workload exists with its GPUs held and a durable
	// command to deliver, or nothing happened. The GPU rows are locked
	// (FOR UPDATE SKIP LOCKED) for the duration, so a concurrent launch or queue
	// promotion can never claim the same GPUs.
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return domain.Workload{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	placed, err := s.placeAndAttach(ctx, tx, orgID, id, req.GPUType, req.GPUCount)
	if errors.Is(err, ErrNoGPUsAvailable) {
		// F3: no capacity → queue instead of failing.
		wl.Status = domain.WorkloadQueued
		now := time.Now()
		if err := s.insertWorkload(ctx, tx, wl, "queued", gpuType, &now); err != nil {
			return domain.Workload{}, fmt.Errorf("create workload: %w", err)
		}
		if err := tx.Commit(ctx); err != nil {
			return domain.Workload{}, err
		}
		_ = s.recordEvent(ctx, id, orgID, "queued", "no matching GPU capacity — queued")
		s.notify(orgID, "workloads", "queue")
		return wl, nil
	}
	if err != nil {
		return domain.Workload{}, err
	}

	wl.NodeID = &placed.nodeID
	wl.Status = domain.WorkloadPending
	if err := s.insertWorkload(ctx, tx, wl, "preparing", gpuType, nil); err != nil {
		return domain.Workload{}, fmt.Errorf("create workload: %w", err)
	}
	if err := placed.attach(ctx, tx); err != nil {
		return domain.Workload{}, fmt.Errorf("attach gpus: %w", err)
	}
	if err := s.enqueueLaunch(ctx, tx, wl, placed); err != nil {
		return domain.Workload{}, fmt.Errorf("enqueue launch: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.Workload{}, err
	}

	_ = s.recordEvent(ctx, id, orgID, "scheduling", "")
	_ = s.recordEvent(ctx, id, orgID, "node_selected", "")
	_ = s.recordEvent(ctx, id, orgID, "preparing", "")
	s.notify(orgID, "workloads")
	s.dispatch.Nudge(placed.nodeID)
	return wl, nil
}

func (s *ServiceImpl) Stop(ctx context.Context, id uuid.UUID, reason domain.StopReason) error {
	// The status flip and the stop command are one transaction (outbox): once the
	// workload reads 'stopping', a durable command exists and will be (re)delivered
	// until the agent acks it or the sweep declares the workload failed.
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var nodeID *uuid.UUID
	var orgID uuid.UUID
	err = tx.QueryRow(ctx,
		`UPDATE workloads SET status='stopping', stop_reason=$2, stopping_at=now()
		 WHERE id=$1 AND status IN ('pending','running')
		 RETURNING node_id, org_id`, id, reason).Scan(&nodeID, &orgID)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if nodeID != nil {
		if err := s.enqueueStop(ctx, tx, orgID, *nodeID, id, reason); err != nil {
			return fmt.Errorf("enqueue stop: %w", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	_ = s.recordEvent(ctx, id, orgID, "stopping", "")
	// User/admin stops are audited at the HTTP layer (with actor + IP); system
	// reclaims (idle timeout, failure cleanup) are only visible here.
	if reason != domain.StopUser && reason != domain.StopAdmin {
		s.auditSystem(ctx, orgID, "system", "workload.stop", id,
			map[string]any{"reason": string(reason)})
	}
	s.notify(orgID, "workloads")
	if nodeID != nil {
		s.dispatch.Nudge(*nodeID)
	}
	return nil
}

// SweepStuck reclaims leaked GPUs. Two cases:
//  1. Workloads in pending/running/stopping whose node is offline → mark failed + free GPUs.
//  2. Any GPU still in_use whose only workloads are terminal (stopped/failed) → set idle.
func (s *ServiceImpl) SweepStuck(ctx context.Context) (int, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// Case 1: active workloads whose node has been *genuinely* offline (not seen for
	// >2min), OR workloads stuck in 'stopping' for >2min since the stop was issued
	// (agent never confirmed) → mark failed + free GPUs.
	//
	// The node-offline arm requires last_seen_at to be stale, not just status='offline'.
	// A node is marked offline the instant its stream drops — which also happens on a
	// control-plane restart or a transient network blip, when the agent (and its
	// containers) are perfectly healthy and about to reconnect. Reclaiming on that
	// transient flag wrongly fails live workloads and frees their GPUs (and, paired
	// with an in-flight stop dropped during the reconnect window, leaks the container).
	// The stopping arm is measured from stopping_at, not created_at — otherwise any
	// long-running workload would be eligible the instant it's stopped, mislabelling a
	// clean stop the agent is about to confirm as a failure.
	rows, err := tx.Query(ctx,
		`SELECT w.id FROM workloads w
		   JOIN gpu_nodes n ON n.id = w.node_id
		  WHERE (w.status IN ('pending','running','stopping')
		         AND n.status = 'offline'
		         AND (n.last_seen_at IS NULL OR n.last_seen_at < now() - interval '2 minutes'))
		     OR (w.status = 'stopping'
		         AND COALESCE(w.stopping_at, w.created_at) < now() - interval '2 minutes')`)
	if err != nil {
		return 0, err
	}
	var stuck []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		_ = rows.Scan(&id)
		stuck = append(stuck, id)
	}
	rows.Close()

	now := time.Now()
	for _, id := range stuck {
		if _, err := tx.Exec(ctx,
			`UPDATE workloads SET status='failed', stopped_at=$2,
			        stop_reason=COALESCE(stop_reason,'failure') WHERE id=$1`, id, now); err != nil {
			return 0, err
		}
		if _, err := tx.Exec(ctx,
			`UPDATE workload_gpus SET detached_at=$2 WHERE workload_id=$1 AND detached_at IS NULL`,
			id, now); err != nil {
			return 0, err
		}
	}

	// Case 2: free any GPU marked in_use that has no active workload holding it
	if _, err := tx.Exec(ctx,
		`UPDATE gpus SET status='idle'
		  WHERE status='in_use'
		    AND id NOT IN (
		      SELECT wg.gpu_id FROM workload_gpus wg
		        JOIN workloads w ON w.id = wg.workload_id
		       WHERE wg.detached_at IS NULL
		         AND w.status IN ('pending','running','stopping')
		    )`); err != nil {
		return 0, err
	}

	// Case 3 (delivery guarantee): a launch command that could not be handed to the
	// agent before its deliver_by deadline becomes an EXPLICIT failure — the
	// workload is failed and its GPUs freed, instead of sitting in 'pending' forever
	// on a node that looks online but never received the command.
	expired, err := commands.ExpireOverdue(ctx, tx)
	if err != nil {
		return 0, err
	}
	for _, e := range expired {
		if e.Kind != commands.KindLaunch || e.WorkloadID == nil {
			continue
		}
		ct, err := tx.Exec(ctx,
			`UPDATE workloads SET status='failed', stopped_at=$2, stage='failed',
			        stop_reason=COALESCE(stop_reason,'failure'),
			        logs=COALESCE(NULLIF(logs,''),'launch command could not be delivered to the agent (node unreachable)')
			 WHERE id=$1 AND status='pending'`, *e.WorkloadID, now)
		if err != nil {
			return 0, err
		}
		if ct.RowsAffected() > 0 {
			if _, err := tx.Exec(ctx,
				`UPDATE workload_gpus SET detached_at=$2 WHERE workload_id=$1 AND detached_at IS NULL`,
				*e.WorkloadID, now); err != nil {
				return 0, err
			}
			stuck = append(stuck, *e.WorkloadID)
		}
	}

	// Case 4: commands whose workload already reached a terminal state by another
	// path no longer matter — stop retrying them.
	if _, err := commands.SupersedeForTerminalWorkloads(ctx, tx); err != nil {
		return 0, err
	}

	// Re-run the GPU free for workloads failed in case 3.
	if _, err := tx.Exec(ctx,
		`UPDATE gpus SET status='idle'
		  WHERE status='in_use'
		    AND id NOT IN (
		      SELECT wg.gpu_id FROM workload_gpus wg
		        JOIN workloads w ON w.id = wg.workload_id
		       WHERE wg.detached_at IS NULL
		         AND w.status IN ('pending','running','stopping')
		    )`); err != nil {
		return 0, err
	}

	if err := tx.Commit(ctx); err != nil {
		return 0, err
	}
	// Audit + realtime hints for the reclaims (post-commit; already durable).
	if (s.auditRec != nil || s.publish != nil) && len(stuck) > 0 {
		rows, err := s.db.Query(ctx,
			`SELECT id, org_id FROM workloads WHERE id = ANY($1)`, stuck)
		if err == nil {
			seen := map[uuid.UUID]bool{}
			for rows.Next() {
				var wid, oid uuid.UUID
				if rows.Scan(&wid, &oid) == nil {
					s.auditSystem(ctx, oid, "system", "workload.failed", wid,
						map[string]any{"reason": "sweep_reclaim"})
					if !seen[oid] {
						seen[oid] = true
						s.notify(oid, "workloads", "queue")
					}
				}
			}
			rows.Close()
		}
	}
	return len(stuck), nil
}

// workloadCols COALESCEs nullable text columns so scanning into Go strings never
// fails on NULL (logs/ssh_password/ssh_endpoint/jupyter_endpoint/stop_reason).
const workloadCols = `id, org_id, project_id, department_id, template_id, user_id, node_id,
	        name, image, coalesce(container_id,''),
	        requested_gpu_count, status, idle_timeout_sec,
	        expose_ssh, expose_jupyter, coalesce(ssh_password,''),
	        coalesce(ssh_endpoint,''), coalesce(jupyter_endpoint,''), coalesce(logs,''),
	        started_at, stopped_at, coalesce(stop_reason,''), created_at, coalesce(stage,'')`

// Get returns a workload only if it belongs to orgID. A mismatch (or missing row)
// returns ErrNotFound, so a caller in another org cannot read it (cross-tenant IDOR).
func (s *ServiceImpl) Get(ctx context.Context, orgID, id uuid.UUID) (domain.Workload, error) {
	w, err := s.getByID(ctx, id)
	if err != nil {
		return domain.Workload{}, err
	}
	if w.OrgID != orgID {
		return domain.Workload{}, ErrNotFound
	}
	return w, nil
}

// getByID loads a workload by primary key with NO org scoping. Internal use only
// (system-initiated paths: queue promotion, sweeps) — never reachable from a user
// request. All request-facing reads MUST go through the org-scoped Get above.
func (s *ServiceImpl) getByID(ctx context.Context, id uuid.UUID) (domain.Workload, error) {
	return scanWorkloadRow(s.db.QueryRow(ctx,
		`SELECT `+workloadCols+` FROM workloads WHERE id=$1`, id))
}

// getByIDTx is getByID inside the caller's transaction (queue promotion reads the
// row it just CAS'd to build the launch command).
func (s *ServiceImpl) getByIDTx(ctx context.Context, tx pgx.Tx, id uuid.UUID) (domain.Workload, error) {
	return scanWorkloadRow(tx.QueryRow(ctx,
		`SELECT `+workloadCols+` FROM workloads WHERE id=$1`, id))
}

func scanWorkloadRow(row pgx.Row) (domain.Workload, error) {
	var w domain.Workload
	err := row.Scan(&w.ID, &w.OrgID, &w.ProjectID, &w.DepartmentID, &w.TemplateID, &w.UserID, &w.NodeID,
		&w.Name, &w.Image, &w.ContainerID,
		&w.RequestedGPUCount, &w.Status, &w.IdleTimeoutSec,
		&w.ExposeSSH, &w.ExposeJupyter, &w.SSHPassword,
		&w.SSHEndpoint, &w.JupyterEndpoint, &w.Logs,
		&w.StartedAt, &w.StoppedAt, &w.StopReason, &w.CreatedAt, &w.Stage)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Workload{}, ErrNotFound
	}
	return w, err
}

// Detail returns the workload plus its GPU allocation and a live runtime cost.
// Scoped to orgID — a cross-org id returns ErrNotFound.
func (s *ServiceImpl) Detail(ctx context.Context, orgID, id uuid.UUID) (DetailView, error) {
	wl, err := s.Get(ctx, orgID, id)
	if err != nil {
		return DetailView{}, err
	}
	d := DetailView{Workload: wl, GPUs: []WorkloadGPU{}, Currency: "USD"}

	rows, err := s.db.Query(ctx,
		`SELECT g.uuid, g.model FROM workload_gpus wg JOIN gpus g ON g.id = wg.gpu_id
		  WHERE wg.workload_id=$1 ORDER BY g.gpu_index`, id)
	if err != nil {
		return DetailView{}, err
	}
	defer rows.Close()
	var models []string
	for rows.Next() {
		var g WorkloadGPU
		if err := rows.Scan(&g.UUID, &g.Model); err != nil {
			return DetailView{}, err
		}
		d.GPUs = append(d.GPUs, g)
		models = append(models, g.Model)
	}

	// Runtime: from start to stop (or now if still running).
	if wl.StartedAt != nil {
		end := time.Now()
		if wl.StoppedAt != nil {
			end = *wl.StoppedAt
		}
		d.RuntimeSeconds = int64(end.Sub(*wl.StartedAt).Seconds())
		if d.RuntimeSeconds < 0 {
			d.RuntimeSeconds = 0
		}
	}

	// Live cost: sum each attached GPU's hourly rate × runtime hours. Falls back to
	// the org default rate when no rate card matches the model.
	hours := float64(d.RuntimeSeconds) / 3600
	var defaultRate float64
	var defaultCurrency string
	_ = s.db.QueryRow(ctx,
		`SELECT coalesce((settings->>'default_rate')::float,0), coalesce(settings->>'currency','USD')
		   FROM organizations WHERE id=$1`, wl.OrgID).Scan(&defaultRate, &defaultCurrency)
	if defaultCurrency != "" {
		d.Currency = defaultCurrency
	}
	for _, model := range models {
		rate := defaultRate
		var r float64
		var cur string
		err := s.db.QueryRow(ctx,
			`SELECT rate_per_gpu_hour, currency FROM rate_cards
			  WHERE org_id=$1 AND gpu_model=$2 AND effective_to IS NULL
			  ORDER BY effective_from DESC LIMIT 1`, wl.OrgID, model).Scan(&r, &cur)
		if err == nil {
			rate = r
			if cur != "" {
				d.Currency = cur
			}
		}
		d.RuntimeCost += rate * hours
	}
	return d, nil
}

func (s *ServiceImpl) List(ctx context.Context, orgID uuid.UUID, f ListFilter) ([]domain.Workload, error) {
	q := `SELECT ` + workloadCols + ` FROM workloads WHERE org_id=$1`
	args := []any{orgID}
	if f.Status != "" {
		args = append(args, f.Status)
		q += fmt.Sprintf(" AND status=$%d", len(args))
	}
	if f.UserID != nil {
		args = append(args, *f.UserID)
		q += fmt.Sprintf(" AND user_id=$%d", len(args))
	}
	if f.ProjectID != nil {
		args = append(args, *f.ProjectID)
		q += fmt.Sprintf(" AND project_id=$%d", len(args))
	}
	// Project-level isolation (Phase 6): hide workloads living in restricted
	// projects the viewer doesn't belong to. The launcher always sees their own.
	if f.Viewer != nil && !f.ViewerIsAdmin {
		args = append(args, *f.Viewer)
		q += fmt.Sprintf(` AND (user_id=$%[1]d
		     OR project_id IS NULL
		     OR NOT EXISTS (SELECT 1 FROM projects p
		                     WHERE p.id = workloads.project_id AND p.visibility='restricted')
		     OR EXISTS (SELECT 1 FROM project_members m
		                 WHERE m.project_id = workloads.project_id AND m.user_id=$%[1]d))`, len(args))
	}
	q += " ORDER BY created_at DESC LIMIT 200"
	rows, err := s.db.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.Workload
	for rows.Next() {
		var w domain.Workload
		if err := rows.Scan(&w.ID, &w.OrgID, &w.ProjectID, &w.DepartmentID, &w.TemplateID, &w.UserID, &w.NodeID,
			&w.Name, &w.Image, &w.ContainerID,
			&w.RequestedGPUCount, &w.Status, &w.IdleTimeoutSec,
			&w.ExposeSSH, &w.ExposeJupyter, &w.SSHPassword,
			&w.SSHEndpoint, &w.JupyterEndpoint, &w.Logs,
			&w.StartedAt, &w.StoppedAt, &w.StopReason, &w.CreatedAt, &w.Stage); err != nil {
			return nil, err
		}
		out = append(out, w)
	}
	return out, rows.Err()
}

// requireWorkloadOnNode verifies an agent-originated workload reference: the workload
// must exist AND be assigned to the authenticated node. Both "no such workload" and
// "assigned elsewhere" return ErrNotFound so a probing agent learns nothing (C1:
// cross-tenant workload write prevention at the agent trust boundary).
func (s *ServiceImpl) requireWorkloadOnNode(ctx context.Context, nodeID, id uuid.UUID) error {
	var assigned *uuid.UUID
	err := s.db.QueryRow(ctx, `SELECT node_id FROM workloads WHERE id=$1`, id).Scan(&assigned)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if assigned == nil || *assigned != nodeID {
		return ErrNotFound
	}
	return nil
}

func (s *ServiceImpl) UpdateStatus(ctx context.Context, nodeID, id uuid.UUID, state domain.WorkloadState, ssh, jupyter, msg, logs string) error {
	if err := s.requireWorkloadOnNode(ctx, nodeID, id); err != nil {
		return err
	}
	now := time.Now()
	switch state {
	case domain.WorkloadRunning:
		var orgID uuid.UUID
		var cur string
		_ = s.db.QueryRow(ctx, `SELECT org_id, status FROM workloads WHERE id=$1`, id).Scan(&orgID, &cur)
		// A 'running' report may only land on pending/running workloads. It must
		// never resurrect a terminal row (a late or reconcile-time report for a
		// workload the sweep already failed) and must not undo an in-flight stop.
		ct, err := s.db.Exec(ctx,
			`UPDATE workloads SET status=$2, ssh_endpoint=$3, jupyter_endpoint=$4,
			        started_at=COALESCE(started_at,$5), logs=COALESCE(NULLIF($6,''),logs)
			 WHERE id=$1 AND status IN ('pending','running')`,
			id, state, ssh, jupyter, now, logs)
		if err != nil {
			return err
		}
		if ct.RowsAffected() == 0 {
			// Terminal on our side but running on the node: the container leaked
			// (e.g. reclaimed during an agent outage). Tell the agent to stop it.
			if cur == string(domain.WorkloadStopped) || cur == string(domain.WorkloadFailed) {
				if err := s.enqueueStop(ctx, s.db, orgID, nodeID, id, domain.StopFailure); err == nil {
					s.dispatch.Nudge(nodeID)
				}
			}
			return nil
		}
		if ssh != "" {
			_ = s.recordEvent(ctx, id, orgID, "ssh_enabled", ssh)
		}
		if jupyter != "" {
			_ = s.recordEvent(ctx, id, orgID, "jupyter_enabled", jupyter)
		}
		_ = s.recordEvent(ctx, id, orgID, "ready", "workload running")
		s.notify(orgID, "workloads")
		return nil

	case domain.WorkloadStopped, domain.WorkloadFailed:
		var startedAt *time.Time
		var orgID uuid.UUID
		_ = s.db.QueryRow(ctx,
			`SELECT started_at, org_id FROM workloads WHERE id=$1`, id).
			Scan(&startedAt, &orgID)

		rows, _ := s.db.Query(ctx,
			`SELECT gpu_id FROM workload_gpus WHERE workload_id=$1 AND detached_at IS NULL`, id)
		var gpuIDs []uuid.UUID
		for rows.Next() {
			var gid uuid.UUID
			_ = rows.Scan(&gid)
			gpuIDs = append(gpuIDs, gid)
		}
		rows.Close()

		tx, err := s.db.Begin(ctx)
		if err != nil {
			return err
		}
		defer func() { _ = tx.Rollback(ctx) }()
		// Idempotent: a duplicate terminal report (redelivered stop, reconcile after
		// reconnect) must not re-stamp stopped_at, re-meter, or re-promote.
		ct, err := tx.Exec(ctx,
			`UPDATE workloads SET status=$2, stopped_at=$3, logs=COALESCE(NULLIF($4,''),logs)
			 WHERE id=$1 AND status NOT IN ('stopped','failed')`, id, state, now, logs)
		if err != nil {
			return err
		}
		if ct.RowsAffected() == 0 {
			return nil
		}
		if _, err := tx.Exec(ctx,
			`UPDATE workload_gpus SET detached_at=$2 WHERE workload_id=$1 AND detached_at IS NULL`,
			id, now); err != nil {
			return err
		}
		for _, gid := range gpuIDs {
			if _, err := tx.Exec(ctx, `UPDATE gpus SET status='idle' WHERE id=$1`, gid); err != nil {
				return err
			}
		}
		if err := tx.Commit(ctx); err != nil {
			return err
		}
		// F1: terminal event. F3: a freed GPU may let a queued workload start.
		stageName := "stopped"
		if state == domain.WorkloadFailed {
			stageName = "failed"
		}
		_ = s.recordEvent(ctx, id, orgID, stageName, "")
		s.auditSystem(ctx, orgID, "agent", "workload."+stageName, id, nil)
		// Terminal transition: workload list, queue (a freed GPU may promote) and
		// budgets (final metering settles spend) all change.
		s.notify(orgID, "workloads", "queue", "budgets")
		// Low-latency promotion nudge; the periodic queue sweep is the reliable
		// fallback if this goroutine is lost to a restart.
		go func() { _, _ = s.PromoteQueued(context.Background(), orgID) }()
		// Final settlement: bill the remaining unmetered slice. Async but recoverable —
		// the metering sweep settles terminal workloads whose watermark lags stopped_at,
		// so a crash here loses nothing (Billing P0: survives control-plane restarts).
		if s.billing != nil {
			go func() {
				_ = s.billing.MeterWorkload(context.Background(), id)
			}()
		}
		return nil

	default:
		_, err := s.db.Exec(ctx, `UPDATE workloads SET status=$2 WHERE id=$1`, id, state)
		return err
	}
}

// ── Placement ──────────────────────────────────────────────────────────────────

// placement is a claimed-but-not-yet-attached GPU set. The rows are locked by the
// caller's transaction; attach() materializes the claim (workload_gpus + in_use)
// after the workload row exists.
type placement struct {
	workloadID uuid.UUID
	nodeID     uuid.UUID
	gpuIDs     []uuid.UUID
	gpuUUIDs   []string
}

// placeAndAttach finds count idle GPUs on a single online node and locks exactly
// those rows for the duration of tx. Two phases:
//
//  1. An UNLOCKED candidate scan groups idle GPUs by node (first-fit order).
//  2. Per candidate node, the needed rows are claimed with FOR UPDATE SKIP LOCKED
//     + a re-check of status='idle'. Rows locked by a concurrent placement are
//     skipped, and getting back fewer than count means we lost the race on this
//     node — move to the next candidate.
//
// Locking only the claimed rows matters: locking the whole candidate scan would
// make every concurrent launch see "no capacity" while a single transaction holds
// the org's idle GPUs (verified by TestPlacementConcurrencyStress). The partial
// unique index on workload_gpus (one active attachment per GPU) backstops the
// invariant at the schema level.
func (s *ServiceImpl) placeAndAttach(ctx context.Context, tx pgx.Tx, orgID, workloadID uuid.UUID, gpuType string, count int) (*placement, error) {
	q := `SELECT g.id, g.node_id FROM gpus g
	       JOIN gpu_nodes n ON n.id = g.node_id
	      WHERE g.org_id=$1 AND g.status='idle' AND n.status='online'`
	args := []any{orgID}
	if gpuType != "" && gpuType != "any" {
		q += " AND g.model ILIKE $2"
		args = append(args, "%"+gpuType+"%")
	}
	q += " ORDER BY g.node_id, g.gpu_index"
	rows, err := tx.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	byNode := map[uuid.UUID][]uuid.UUID{}
	var nodeOrder []uuid.UUID
	for rows.Next() {
		var gpuID, nodeID uuid.UUID
		if err := rows.Scan(&gpuID, &nodeID); err != nil {
			rows.Close()
			return nil, err
		}
		if _, seen := byNode[nodeID]; !seen {
			nodeOrder = append(nodeOrder, nodeID)
		}
		byNode[nodeID] = append(byNode[nodeID], gpuID)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	for _, nid := range nodeOrder {
		if len(byNode[nid]) < count {
			continue
		}
		claimed, err := tx.Query(ctx,
			`SELECT id, uuid FROM gpus
			  WHERE id = ANY($1) AND status='idle'
			  ORDER BY gpu_index
			  LIMIT $2
			  FOR UPDATE SKIP LOCKED`, byNode[nid], count)
		if err != nil {
			return nil, err
		}
		p := &placement{workloadID: workloadID, nodeID: nid}
		for claimed.Next() {
			var gid uuid.UUID
			var guuid string
			if err := claimed.Scan(&gid, &guuid); err != nil {
				claimed.Close()
				return nil, err
			}
			p.gpuIDs = append(p.gpuIDs, gid)
			p.gpuUUIDs = append(p.gpuUUIDs, guuid)
		}
		claimed.Close()
		if err := claimed.Err(); err != nil {
			return nil, err
		}
		if len(p.gpuIDs) == count {
			return p, nil
		}
		// Lost the race for this node (rows locked or no longer idle); the few
		// rows we did lock stay locked until tx end, which is fine — placement
		// transactions are short. Try the next node.
	}
	return nil, ErrNoGPUsAvailable
}

// attach records the GPU assignment under the locks placeAndAttach took: the
// workload_gpus rows (guarded by idx_workload_gpus_one_active) and the in_use flip
// happen in the SAME transaction as the workload row — no post-commit window where
// a GPU is assigned but still reads idle.
func (p *placement) attach(ctx context.Context, tx pgx.Tx) error {
	for _, gid := range p.gpuIDs {
		if _, err := tx.Exec(ctx,
			`INSERT INTO workload_gpus (workload_id, gpu_id) VALUES ($1,$2)`, p.workloadID, gid); err != nil {
			return err
		}
	}
	ct, err := tx.Exec(ctx,
		`UPDATE gpus SET status='in_use' WHERE id = ANY($1) AND status='idle'`, p.gpuIDs)
	if err != nil {
		return err
	}
	if int(ct.RowsAffected()) != len(p.gpuIDs) {
		// Unreachable while the rows are locked; kept as a hard guard so a future
		// regression aborts the transaction instead of double-assigning.
		return fmt.Errorf("gpu claim raced: claimed %d of %d", ct.RowsAffected(), len(p.gpuIDs))
	}
	return nil
}

// ── Persistence ─────────────────────────────────────────────────────────────

func (s *ServiceImpl) insertWorkload(ctx context.Context, tx pgx.Tx, w domain.Workload, stage, gpuType string, queuedAt *time.Time) error {
	if _, err := tx.Exec(ctx,
		`INSERT INTO workloads
		   (id, org_id, project_id, department_id, template_id, user_id, node_id, name, image, container_id,
		    requested_gpu_count, status, idle_timeout_sec,
		    expose_ssh, expose_jupyter, ssh_password, created_at,
		    stage, requested_gpu_type, queued_at)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20)`,
		w.ID, w.OrgID, w.ProjectID, w.DepartmentID, w.TemplateID, w.UserID, w.NodeID, w.Name, w.Image, w.ContainerID,
		w.RequestedGPUCount, w.Status, w.IdleTimeoutSec,
		w.ExposeSSH, w.ExposeJupyter, w.SSHPassword, w.CreatedAt,
		stage, gpuType, queuedAt); err != nil {
		return err
	}
	return nil
}

// ── Command outbox — durable launch/stop, delivered by the agent gateway ──────

// launchDeliveryDeadline bounds how long a launch command may sit undelivered
// (agent unreachable) before the sweep converts it into an explicit workload
// failure. Note this is a DELIVERY deadline: once the agent acks, long image pulls
// can take as long as they take.
const launchDeliveryDeadline = 10 * time.Minute

func (s *ServiceImpl) enqueueLaunch(ctx context.Context, tx pgx.Tx, w domain.Workload, p *placement) error {
	idleTimeout := uint32(3600)
	if w.IdleTimeoutSec != nil {
		idleTimeout = uint32(*w.IdleTimeoutSec)
	}
	env := map[string]string{}
	if w.SSHPassword != "" {
		env["SSH_PASSWORD"] = w.SSHPassword
	}
	cmdID := uuid.New()
	msg := &agentv1.ServerMessage{
		CommandId: cmdID.String(),
		Payload: &agentv1.ServerMessage_Launch{
			Launch: &agentv1.LaunchWorkload{
				Spec: &workloadv1.WorkloadSpec{
					WorkloadId:     w.ID.String(),
					Image:          w.Image,
					GpuCount:       uint32(w.RequestedGPUCount),
					GpuUuids:       p.gpuUUIDs,
					IdleTimeoutSec: idleTimeout,
					Env:            env,
					ExposeSsh:      w.ExposeSSH,
					ExposeJupyter:  w.ExposeJupyter,
				},
			},
		},
	}
	payload, err := proto.Marshal(msg)
	if err != nil {
		return err
	}
	wid := w.ID
	deadline := time.Now().Add(launchDeliveryDeadline)
	return commands.Enqueue(ctx, tx, commands.Command{
		ID: cmdID, OrgID: w.OrgID, NodeID: p.nodeID, WorkloadID: &wid,
		Kind: commands.KindLaunch, Payload: payload, DeliverBy: &deadline,
	})
}

func (s *ServiceImpl) enqueueStop(ctx context.Context, db commands.DB, orgID, nodeID, workloadID uuid.UUID, reason domain.StopReason) error {
	cmdID := uuid.New()
	msg := &agentv1.ServerMessage{
		CommandId: cmdID.String(),
		Payload: &agentv1.ServerMessage_Stop{
			Stop: &agentv1.StopWorkload{
				WorkloadId: workloadID.String(),
				Reason:     domainToProtoStopReason(reason),
			},
		},
	}
	payload, err := proto.Marshal(msg)
	if err != nil {
		return err
	}
	// No deliver_by: a stop stays deliverable until the workload reaches a terminal
	// state by some other path (sweep), at which point it is superseded.
	return commands.Enqueue(ctx, db, commands.Command{
		ID: cmdID, OrgID: orgID, NodeID: nodeID, WorkloadID: &workloadID,
		Kind: commands.KindStop, Payload: payload,
	})
}

func domainToProtoStopReason(r domain.StopReason) workloadv1.StopReason {
	switch r {
	case domain.StopUser:
		return workloadv1.StopReason_STOP_REASON_USER
	case domain.StopIdleReclaim:
		return workloadv1.StopReason_STOP_REASON_IDLE_RECLAIM
	case domain.StopAdmin:
		return workloadv1.StopReason_STOP_REASON_ADMIN
	case domain.StopFailure:
		return workloadv1.StopReason_STOP_REASON_FAILURE
	default:
		return workloadv1.StopReason_STOP_REASON_UNSPECIFIED
	}
}

func randomPassword() string {
	b := make([]byte, 10)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)[:16]
}

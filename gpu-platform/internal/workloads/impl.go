package workloads

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/protobuf/types/known/wrapperspb"

	agentv1 "github.com/nodehive/gpu-platform/gen/go/agent/v1"
	workloadv1 "github.com/nodehive/gpu-platform/gen/go/workload/v1"
	"github.com/nodehive/gpu-platform/internal/domain"
)

var _ = wrapperspb.String // suppress unused import if needed

var ErrNotFound = errors.New("workload not found")
var ErrNoGPUsAvailable = errors.New("no GPUs available matching request")
var ErrNotQueued = errors.New("workload is not queued")

// UsageRecorder is the billing seam — satisfied by billing.Service.
type UsageRecorder interface {
	RecordWorkloadUsage(ctx context.Context, workloadID, orgID uuid.UUID, start, end time.Time) error
}

// ServiceImpl wires together placement, DB persistence, and command dispatch.
type ServiceImpl struct {
	db       *pgxpool.Pool
	dispatch Dispatcher
	billing  UsageRecorder
}

func NewService(db *pgxpool.Pool, dispatch Dispatcher, billing UsageRecorder) Service {
	return &ServiceImpl{db: db, dispatch: dispatch, billing: billing}
}

func (s *ServiceImpl) Launch(ctx context.Context, orgID uuid.UUID, req LaunchRequest) (domain.Workload, error) {
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

	gpuIDs, nodeID, err := s.place(ctx, orgID, req.GPUType, req.GPUCount)
	if errors.Is(err, ErrNoGPUsAvailable) {
		// F3: no capacity → queue instead of failing.
		wl.Status = domain.WorkloadQueued
		now := time.Now()
		if err := s.createWorkload(ctx, wl, nil, "queued", gpuType, &now); err != nil {
			return domain.Workload{}, fmt.Errorf("create workload: %w", err)
		}
		_ = s.recordEvent(ctx, id, orgID, "queued", "no matching GPU capacity — queued")
		return wl, nil
	}
	if err != nil {
		return domain.Workload{}, err
	}

	wl.NodeID = &nodeID
	wl.Status = domain.WorkloadPending
	if err := s.createWorkload(ctx, wl, gpuIDs, "preparing", gpuType, nil); err != nil {
		return domain.Workload{}, fmt.Errorf("create workload: %w", err)
	}
	for _, gid := range gpuIDs {
		_, _ = s.db.Exec(ctx, `UPDATE gpus SET status='in_use' WHERE id=$1`, gid)
	}
	_ = s.recordEvent(ctx, id, orgID, "scheduling", "")
	_ = s.recordEvent(ctx, id, orgID, "node_selected", "")
	_ = s.recordEvent(ctx, id, orgID, "preparing", "")

	go s.sendLaunchCommand(context.Background(), nodeID, wl, gpuIDs)
	return wl, nil
}

func (s *ServiceImpl) Stop(ctx context.Context, id uuid.UUID, reason domain.StopReason) error {
	var nodeID uuid.UUID
	err := s.db.QueryRow(ctx,
		`UPDATE workloads SET status='stopping', stop_reason=$2, stopping_at=now()
		 WHERE id=$1 AND status IN ('pending','running')
		 RETURNING node_id`, id, reason).Scan(&nodeID)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	var orgID uuid.UUID
	_ = s.db.QueryRow(ctx, `SELECT org_id FROM workloads WHERE id=$1`, id).Scan(&orgID)
	_ = s.recordEvent(ctx, id, orgID, "stopping", "")
	if nodeID != uuid.Nil {
		go s.sendStopCommand(context.Background(), nodeID, id, reason)
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

	if err := tx.Commit(ctx); err != nil {
		return 0, err
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

func (s *ServiceImpl) Get(ctx context.Context, id uuid.UUID) (domain.Workload, error) {
	var w domain.Workload
	err := s.db.QueryRow(ctx,
		`SELECT `+workloadCols+` FROM workloads WHERE id=$1`, id).
		Scan(&w.ID, &w.OrgID, &w.ProjectID, &w.DepartmentID, &w.TemplateID, &w.UserID, &w.NodeID,
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
func (s *ServiceImpl) Detail(ctx context.Context, id uuid.UUID) (DetailView, error) {
	wl, err := s.Get(ctx, id)
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

func (s *ServiceImpl) UpdateStatus(ctx context.Context, id uuid.UUID, state domain.WorkloadState, ssh, jupyter, msg, logs string) error {
	now := time.Now()
	switch state {
	case domain.WorkloadRunning:
		var orgID uuid.UUID
		_ = s.db.QueryRow(ctx, `SELECT org_id FROM workloads WHERE id=$1`, id).Scan(&orgID)
		_, err := s.db.Exec(ctx,
			`UPDATE workloads SET status=$2, ssh_endpoint=$3, jupyter_endpoint=$4,
			        started_at=COALESCE(started_at,$5), logs=COALESCE(NULLIF($6,''),logs)
			 WHERE id=$1`,
			id, state, ssh, jupyter, now, logs)
		if err == nil {
			if ssh != "" {
				_ = s.recordEvent(ctx, id, orgID, "ssh_enabled", ssh)
			}
			if jupyter != "" {
				_ = s.recordEvent(ctx, id, orgID, "jupyter_enabled", jupyter)
			}
			_ = s.recordEvent(ctx, id, orgID, "ready", "workload running")
		}
		return err

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
		if _, err := tx.Exec(ctx,
			`UPDATE workloads SET status=$2, stopped_at=$3, logs=COALESCE(NULLIF($4,''),logs)
			 WHERE id=$1`, id, state, now, logs); err != nil {
			return err
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
		go s.tryPromoteQueue(context.Background(), orgID)
		if s.billing != nil && startedAt != nil {
			go func() {
				_ = s.billing.RecordWorkloadUsage(context.Background(), id, orgID, *startedAt, now)
			}()
		}
		return nil

	default:
		_, err := s.db.Exec(ctx, `UPDATE workloads SET status=$2 WHERE id=$1`, id, state)
		return err
	}
}

// ── Placement ──────────────────────────────────────────────────────────────────

func (s *ServiceImpl) place(ctx context.Context, orgID uuid.UUID, gpuType string, count int) ([]uuid.UUID, uuid.UUID, error) {
	q := `SELECT g.id, g.node_id FROM gpus g
	       JOIN gpu_nodes n ON n.id = g.node_id
	      WHERE g.org_id=$1 AND g.status='idle' AND n.status='online'`
	args := []any{orgID}
	if gpuType != "" && gpuType != "any" {
		q += " AND g.model ILIKE $2"
		args = append(args, "%"+gpuType+"%")
	}
	q += " ORDER BY g.node_id, g.gpu_index LIMIT $" + fmt.Sprintf("%d", len(args)+1)
	args = append(args, count*10)
	rows, err := s.db.Query(ctx, q, args...)
	if err != nil {
		return nil, uuid.Nil, err
	}
	defer rows.Close()

	type entry struct{ gpuID, nodeID uuid.UUID }
	byNode := map[uuid.UUID][]uuid.UUID{}
	var nodeOrder []uuid.UUID
	for rows.Next() {
		var e entry
		if err := rows.Scan(&e.gpuID, &e.nodeID); err != nil {
			return nil, uuid.Nil, err
		}
		if _, seen := byNode[e.nodeID]; !seen {
			nodeOrder = append(nodeOrder, e.nodeID)
		}
		byNode[e.nodeID] = append(byNode[e.nodeID], e.gpuID)
	}
	for _, nid := range nodeOrder {
		if len(byNode[nid]) >= count {
			return byNode[nid][:count], nid, nil
		}
	}
	return nil, uuid.Nil, ErrNoGPUsAvailable
}

// ── Persistence ─────────────────────────────────────────────────────────────

func (s *ServiceImpl) createWorkload(ctx context.Context, w domain.Workload, gpuIDs []uuid.UUID, stage, gpuType string, queuedAt *time.Time) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
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
	for _, gid := range gpuIDs {
		if _, err := tx.Exec(ctx,
			`INSERT INTO workload_gpus (workload_id, gpu_id) VALUES ($1,$2)`, w.ID, gid); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

// ── Dispatch — builds real proto messages (fixes the silent-drop bug) ─────────

func (s *ServiceImpl) sendLaunchCommand(ctx context.Context, nodeID uuid.UUID, w domain.Workload, gpuIDs []uuid.UUID) {
	// Resolve GPU DB IDs → GPU UUIDs for the agent
	rows, err := s.db.Query(ctx, `SELECT uuid FROM gpus WHERE id = ANY($1)`, gpuIDs)
	if err != nil {
		return
	}
	var gpuUUIDs []string
	for rows.Next() {
		var u string
		_ = rows.Scan(&u)
		gpuUUIDs = append(gpuUUIDs, u)
	}
	rows.Close()

	idleTimeout := uint32(3600)
	if w.IdleTimeoutSec != nil {
		idleTimeout = uint32(*w.IdleTimeoutSec)
	}

	env := map[string]string{}
	if w.SSHPassword != "" {
		env["SSH_PASSWORD"] = w.SSHPassword
	}

	msg := &agentv1.ServerMessage{
		CommandId: uuid.New().String(),
		Payload: &agentv1.ServerMessage_Launch{
			Launch: &agentv1.LaunchWorkload{
				Spec: &workloadv1.WorkloadSpec{
					WorkloadId:     w.ID.String(),
					Image:          w.Image,
					GpuCount:       uint32(w.RequestedGPUCount),
					GpuUuids:       gpuUUIDs,
					IdleTimeoutSec: idleTimeout,
					Env:            env,
					ExposeSsh:      w.ExposeSSH,
					ExposeJupyter:  w.ExposeJupyter,
				},
			},
		},
	}
	_ = s.dispatch.Send(ctx, nodeID, msg)
}

func (s *ServiceImpl) sendStopCommand(ctx context.Context, nodeID, workloadID uuid.UUID, reason domain.StopReason) {
	msg := &agentv1.ServerMessage{
		CommandId: uuid.New().String(),
		Payload: &agentv1.ServerMessage_Stop{
			Stop: &agentv1.StopWorkload{
				WorkloadId: workloadID.String(),
				Reason:     domainToProtoStopReason(reason),
			},
		},
	}
	_ = s.dispatch.Send(ctx, nodeID, msg)
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

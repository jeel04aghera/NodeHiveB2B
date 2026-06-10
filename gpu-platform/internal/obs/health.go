package obs

import (
	"context"
	"runtime"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Health aggregates the control plane's component checks for /readyz and the admin
// diagnostics endpoint. All fields are optional; nil components are skipped.
type Health struct {
	DB              *pgxpool.Pool
	Jobs            *JobTracker
	ConnectedAgents func() int // live agent gRPC streams (deployment-wide)
	Version         string
	Env             string
	GRPCTLS         bool
	Started         time.Time
}

// Component is one subsystem's check result.
type Component struct {
	OK     bool           `json:"ok"`
	Error  string         `json:"error,omitempty"`
	Detail map[string]any `json:"detail,omitempty"`
}

// Report is the full diagnostics document.
type Report struct {
	Status     string         `json:"status"` // ok | degraded
	Database   Component      `json:"database"`
	Queue      Component      `json:"queue"`
	Agents     Component      `json:"agents"`
	Jobs       []JobStatus    `json:"jobs"`
	Deployment map[string]any `json:"deployment"`
}

// Ready is the cheap readiness probe: can we reach the database? Used by /readyz so
// the platform (Railway, k8s) stops routing to an instance that lost its DB.
func (h *Health) Ready(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	return h.DB.Ping(ctx)
}

// Check runs the full component sweep (admin diagnostics).
func (h *Health) Check(ctx context.Context) Report {
	r := Report{Status: "ok"}

	// Database: ping latency + pool saturation.
	start := time.Now()
	err := h.Ready(ctx)
	r.Database = Component{OK: err == nil}
	if err != nil {
		r.Database.Error = err.Error()
		r.Status = "degraded"
	} else {
		st := h.DB.Stat()
		r.Database.Detail = map[string]any{
			"ping_ms":            time.Since(start).Milliseconds(),
			"conns_total":        st.TotalConns(),
			"conns_idle":         st.IdleConns(),
			"conns_max":          st.MaxConns(),
			"acquire_wait_total": st.EmptyAcquireCount(),
		}
	}

	// Command outbox: depth + overdue rows. A growing overdue count means agents are
	// not picking up commands (disconnected fleet or delivery-engine stall).
	r.Queue = h.queueHealth(ctx)
	if !r.Queue.OK {
		r.Status = "degraded"
	}

	// Agents: live streams vs nodes the DB believes are online.
	r.Agents = h.agentHealth(ctx)

	if h.Jobs != nil {
		r.Jobs = h.Jobs.Status()
		for _, j := range r.Jobs {
			if !j.Healthy {
				r.Status = "degraded"
			}
		}
	}

	r.Deployment = map[string]any{
		"version":    h.Version,
		"env":        h.Env,
		"grpc_tls":   h.GRPCTLS,
		"go_version": runtime.Version(),
		"uptime_s":   int(time.Since(h.Started).Seconds()),
	}
	return r
}

func (h *Health) queueHealth(ctx context.Context) Component {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	var pending, sent, overdue int
	var oldestAge *float64
	err := h.DB.QueryRow(ctx, `
		SELECT count(*) FILTER (WHERE status='pending'),
		       count(*) FILTER (WHERE status='sent'),
		       count(*) FILTER (WHERE status IN ('pending','sent') AND deliver_by IS NOT NULL AND deliver_by < now()),
		       extract(epoch from now() - min(created_at) FILTER (WHERE status IN ('pending','sent')))
		  FROM agent_commands`).Scan(&pending, &sent, &overdue, &oldestAge)
	if err != nil {
		return Component{OK: false, Error: err.Error()}
	}
	c := Component{
		// Overdue commands mean intent the fleet hasn't executed past its deadline.
		OK: overdue == 0,
		Detail: map[string]any{
			"pending": pending, "in_flight": sent, "overdue": overdue,
		},
	}
	if oldestAge != nil {
		c.Detail["oldest_undelivered_s"] = int(*oldestAge)
	}
	if !c.OK {
		c.Error = "commands past their delivery deadline"
	}
	return c
}

func (h *Health) agentHealth(ctx context.Context) Component {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	var online, total int
	err := h.DB.QueryRow(ctx,
		`SELECT count(*) FILTER (WHERE status='online'), count(*) FROM gpu_nodes`).
		Scan(&online, &total)
	if err != nil {
		return Component{OK: false, Error: err.Error()}
	}
	detail := map[string]any{"nodes_online": online, "nodes_total": total}
	if h.ConnectedAgents != nil {
		detail["streams_connected"] = h.ConnectedAgents()
	}
	// An empty fleet is not unhealthy (fresh deployment); mismatch is informational.
	return Component{OK: true, Detail: detail}
}

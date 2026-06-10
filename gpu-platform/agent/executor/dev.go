package executor

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// DevExecutor simulates container launches without Docker so the workload flow
// can be demonstrated end-to-end on any machine. Enabled via AGENT_DEV_MODE=true.
type DevExecutor struct {
	mu      sync.Mutex
	running map[string]Status
	port    int
}

func NewDev() Executor {
	return &DevExecutor{running: make(map[string]Status), port: 22000}
}

func (e *DevExecutor) Launch(ctx context.Context, spec LaunchSpec) (Status, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	e.port++
	sshPass := spec.SSHPassword
	if sshPass == "" {
		sshPass = spec.Env["SSH_PASSWORD"]
	}
	st := Status{
		WorkloadID:  spec.WorkloadID,
		State:       "running",
		SSHPassword: sshPass,
		Message:     "dev executor: simulated container",
	}
	if spec.ExposeSSH {
		st.SSHEndpoint = fmt.Sprintf("localhost:%d", e.port)
	}
	if spec.ExposeJupyter {
		e.port++
		st.JupyterEndpoint = fmt.Sprintf("localhost:%d", e.port)
	}
	e.running[spec.WorkloadID] = st
	return st, nil
}

func (e *DevExecutor) Stop(ctx context.Context, workloadID string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	delete(e.running, workloadID)
	return nil
}

func (e *DevExecutor) Status(ctx context.Context, workloadID string) (Status, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if st, ok := e.running[workloadID]; ok {
		return st, nil
	}
	return Status{WorkloadID: workloadID, State: "stopped"}, nil
}

func (e *DevExecutor) List(ctx context.Context) ([]Status, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make([]Status, 0, len(e.running))
	for _, st := range e.running {
		out = append(out, st)
	}
	return out, nil
}

func (e *DevExecutor) Logs(ctx context.Context, workloadID string, tail int) (string, error) {
	return fmt.Sprintf(
		"[dev executor] workload %s\n%s  container started\n%s  GPU bound, ready\n[dev executor] simulated output — no real container\n",
		workloadID,
		time.Now().Add(-time.Minute).Format("15:04:05"),
		time.Now().Format("15:04:05"),
	), nil
}

package obs

import (
	"sort"
	"sync"
	"time"
)

// JobTracker records the last outcome of each background sweep so health checks and
// metrics can tell "the binary is up" apart from "the binary is up but the metering
// sweep has been failing for an hour" — the failure mode liveness probes miss.
type JobTracker struct {
	mu   sync.Mutex
	jobs map[string]*jobState
}

type jobState struct {
	interval    time.Duration
	lastRun     time.Time
	lastSuccess time.Time
	lastError   string
	runs        uint64
	failures    uint64
}

// JobStatus is one job's health snapshot.
type JobStatus struct {
	Name        string     `json:"name"`
	Healthy     bool       `json:"healthy"`
	LastRun     *time.Time `json:"last_run,omitempty"`
	LastSuccess *time.Time `json:"last_success,omitempty"`
	LastError   string     `json:"last_error,omitempty"`
	Runs        uint64     `json:"runs"`
	Failures    uint64     `json:"failures"`
}

func NewJobTracker() *JobTracker { return &JobTracker{jobs: map[string]*jobState{}} }

// Register declares a job and its expected cadence before it starts ticking, so a job
// that never runs at all still shows up (unhealthy once interval×3 passes since boot).
func (t *JobTracker) Register(name string, interval time.Duration) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.jobs[name] = &jobState{interval: interval, lastRun: time.Time{}}
}

// Record stores one run's outcome. err == nil counts as success.
func (t *JobTracker) Record(name string, err error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	j, ok := t.jobs[name]
	if !ok {
		j = &jobState{interval: time.Hour}
		t.jobs[name] = j
	}
	now := time.Now()
	j.lastRun = now
	j.runs++
	if err != nil {
		j.failures++
		j.lastError = err.Error()
		return
	}
	j.lastSuccess = now
	j.lastError = ""
}

// trackerStarted anchors the health window for jobs that have never run.
var trackerStarted = time.Now()

// Status snapshots every job. A job is healthy when its last success is within 3×
// its cadence (tolerates a couple of failed or slow ticks before alarming).
func (t *JobTracker) Status() []JobStatus {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := make([]JobStatus, 0, len(t.jobs))
	for name, j := range t.jobs {
		s := JobStatus{Name: name, Runs: j.runs, Failures: j.failures, LastError: j.lastError}
		if !j.lastRun.IsZero() {
			lr := j.lastRun
			s.LastRun = &lr
		}
		if !j.lastSuccess.IsZero() {
			ls := j.lastSuccess
			s.LastSuccess = &ls
		}
		window := 3 * j.interval
		anchor := j.lastSuccess
		if anchor.IsZero() {
			anchor = trackerStarted
		}
		s.Healthy = time.Since(anchor) < window
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

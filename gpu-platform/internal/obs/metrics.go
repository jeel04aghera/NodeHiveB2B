package obs

import (
	"context"
	"net/http"
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Metrics is the control plane's Prometheus instrumentation. Everything hangs off a
// private registry (not the global default) so tests can build isolated instances.
type Metrics struct {
	registry     *prometheus.Registry
	httpRequests *prometheus.CounterVec
	httpDuration *prometheus.HistogramVec
}

// NewMetrics builds the registry: Go runtime + process collectors, build info, HTTP
// request counters/latency, and a state collector that samples fleet/queue gauges
// from the database and job tracker at scrape time (no background goroutine).
func NewMetrics(h *Health) *Metrics {
	reg := prometheus.NewRegistry()
	reg.MustRegister(
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
	)

	build := prometheus.NewGauge(prometheus.GaugeOpts{
		Name:        "nodehive_build_info",
		Help:        "Build metadata (value is always 1).",
		ConstLabels: prometheus.Labels{"version": h.Version, "env": h.Env},
	})
	build.Set(1)
	reg.MustRegister(build)

	m := &Metrics{
		registry: reg,
		httpRequests: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "nodehive_http_requests_total",
			Help: "HTTP requests by method, chi route pattern and status code.",
		}, []string{"method", "route", "status"}),
		httpDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "nodehive_http_request_duration_seconds",
			Help:    "HTTP request latency by method and chi route pattern.",
			Buckets: []float64{.005, .01, .025, .05, .1, .25, .5, 1, 2.5, 5, 10},
		}, []string{"method", "route"}),
	}
	reg.MustRegister(m.httpRequests, m.httpDuration)
	reg.MustRegister(&stateCollector{health: h})
	return m
}

// ObserveHTTP records one served request. route is the chi pattern ("/api/v1/nodes/{id}")
// so label cardinality stays bounded.
func (m *Metrics) ObserveHTTP(method, route string, status int, dur time.Duration) {
	m.httpRequests.WithLabelValues(method, route, strconv.Itoa(status)).Inc()
	m.httpDuration.WithLabelValues(method, route).Observe(dur.Seconds())
}

// Handler serves the Prometheus exposition endpoint.
func (m *Metrics) Handler() http.Handler {
	return promhttp.HandlerFor(m.registry, promhttp.HandlerOpts{})
}

// ── Scrape-time state collector ───────────────────────────────────────────────

var (
	descAgents = prometheus.NewDesc("nodehive_agents_connected",
		"Live agent gRPC streams.", nil, nil)
	descNodes = prometheus.NewDesc("nodehive_nodes",
		"Enrolled nodes by status.", []string{"status"}, nil)
	descOutbox = prometheus.NewDesc("nodehive_command_outbox",
		"Agent command outbox rows by status.", []string{"status"}, nil)
	descOutboxOverdue = prometheus.NewDesc("nodehive_command_outbox_overdue",
		"Undelivered commands past their delivery deadline.", nil, nil)
	descWorkloads = prometheus.NewDesc("nodehive_workloads",
		"Workloads by status.", []string{"status"}, nil)
	descJobSuccess = prometheus.NewDesc("nodehive_job_last_success_timestamp_seconds",
		"Unix time of each background job's last successful run.", []string{"job"}, nil)
	descJobFailures = prometheus.NewDesc("nodehive_job_failures_total",
		"Cumulative background job failures.", []string{"job"}, nil)
	descScrapeOK = prometheus.NewDesc("nodehive_state_scrape_ok",
		"1 when the database state sample succeeded.", nil, nil)
)

// stateCollector samples operational gauges at scrape time. One short DB round-trip
// per scrape (15–60s cadence) is far cheaper than keeping counters coherent in code.
type stateCollector struct{ health *Health }

func (c *stateCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- descAgents
	ch <- descNodes
	ch <- descOutbox
	ch <- descOutboxOverdue
	ch <- descWorkloads
	ch <- descJobSuccess
	ch <- descJobFailures
	ch <- descScrapeOK
}

func (c *stateCollector) Collect(ch chan<- prometheus.Metric) {
	if c.health.ConnectedAgents != nil {
		ch <- prometheus.MustNewConstMetric(descAgents, prometheus.GaugeValue,
			float64(c.health.ConnectedAgents()))
	}
	if c.health.Jobs != nil {
		for _, j := range c.health.Jobs.Status() {
			if j.LastSuccess != nil {
				ch <- prometheus.MustNewConstMetric(descJobSuccess, prometheus.GaugeValue,
					float64(j.LastSuccess.Unix()), j.Name)
			}
			ch <- prometheus.MustNewConstMetric(descJobFailures, prometheus.CounterValue,
				float64(j.Failures), j.Name)
		}
	}

	ok := 1.0
	if err := c.collectDB(ch); err != nil {
		ok = 0
	}
	ch <- prometheus.MustNewConstMetric(descScrapeOK, prometheus.GaugeValue, ok)
}

func (c *stateCollector) collectDB(ch chan<- prometheus.Metric) error {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if err := groupCount(ctx, c.health, ch, descNodes,
		`SELECT status, count(*) FROM gpu_nodes GROUP BY status`); err != nil {
		return err
	}
	if err := groupCount(ctx, c.health, ch, descWorkloads,
		`SELECT status, count(*) FROM workloads GROUP BY status`); err != nil {
		return err
	}
	if err := groupCount(ctx, c.health, ch, descOutbox,
		`SELECT status, count(*) FROM agent_commands GROUP BY status`); err != nil {
		return err
	}
	var overdue float64
	if err := c.health.DB.QueryRow(ctx,
		`SELECT count(*) FROM agent_commands
		  WHERE status IN ('pending','sent') AND deliver_by IS NOT NULL AND deliver_by < now()`,
	).Scan(&overdue); err != nil {
		return err
	}
	ch <- prometheus.MustNewConstMetric(descOutboxOverdue, prometheus.GaugeValue, overdue)
	return nil
}

func groupCount(ctx context.Context, h *Health, ch chan<- prometheus.Metric,
	desc *prometheus.Desc, sql string) error {
	rows, err := h.DB.Query(ctx, sql)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var label string
		var n float64
		if err := rows.Scan(&label, &n); err != nil {
			return err
		}
		ch <- prometheus.MustNewConstMetric(desc, prometheus.GaugeValue, n, label)
	}
	return rows.Err()
}

// Package metrics samples GPU telemetry (DCGM preferred, NVML fallback) and emits
// batches. The Sampler is the test boundary; the Collector wires it to a ticker.
package metrics

import (
	"context"
	"time"
)

type Sample struct {
	GPUUUID    string
	TS         time.Time
	UtilPct    float32
	MemUsedMB  uint32
	PowerW     float32
	TempC      float32
	SMClockMHz uint32
	ECCErrors  uint32
	ProcCount  uint32
}

// Sampler reads one snapshot across all local GPUs. Fake it in tests.
type Sampler interface {
	Sample(ctx context.Context) ([]Sample, error)
}

// Collector runs a Sampler on an interval and pushes batches to out.
type Collector interface {
	Run(ctx context.Context, interval time.Duration, out chan<- []Sample) error
}

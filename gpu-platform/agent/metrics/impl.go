package metrics

import (
	"bufio"
	"bytes"
	"context"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// NvidiaSMISampler reads GPU telemetry via nvidia-smi. Returns empty slice
// (not an error) when no GPU is present — the agent just skips the send.
type NvidiaSMISampler struct{}

func New() Sampler { return &NvidiaSMISampler{} }

func (s *NvidiaSMISampler) Sample(ctx context.Context) ([]Sample, error) {
	// Fields: uuid, util%, mem_used_mb, power_w, temp_c, sm_clock_mhz, ecc_errors_uncorr, proc_count
	out, err := exec.CommandContext(ctx, "nvidia-smi",
		"--query-gpu=uuid,utilization.gpu,memory.used,power.draw,temperature.gpu,clocks.sm,ecc.errors.uncorrected.volatile.total,compute-apps",
		"--format=csv,noheader,nounits",
	).Output()
	if err != nil {
		return nil, nil // no GPU — not fatal
	}

	ts := time.Now().UTC()
	var samples []Sample
	scanner := bufio.NewScanner(bytes.NewReader(out))
	for scanner.Scan() {
		parts := strings.SplitN(scanner.Text(), ", ", 8)
		if len(parts) < 7 {
			continue
		}
		f := func(i int) float64 { v, _ := strconv.ParseFloat(strings.TrimSpace(parts[i]), 64); return v }
		u := func(i int) uint32 { v, _ := strconv.ParseUint(strings.TrimSpace(parts[i]), 10, 32); return uint32(v) }

		samples = append(samples, Sample{
			GPUUUID:    strings.TrimSpace(parts[0]),
			TS:         ts,
			UtilPct:    float32(f(1)),
			MemUsedMB:  uint32(f(2)),
			PowerW:     float32(f(3)),
			TempC:      float32(f(4)),
			SMClockMHz: u(5),
			ECCErrors:  u(6),
		})
	}
	return samples, nil
}

// TickCollector drives a Sampler on an interval and pushes non-empty batches to out.
type TickCollector struct{ sampler Sampler }

func NewCollector(s Sampler) Collector { return &TickCollector{sampler: s} }

func (c *TickCollector) Run(ctx context.Context, interval time.Duration, out chan<- []Sample) error {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			samples, err := c.sampler.Sample(ctx)
			if err != nil || len(samples) == 0 {
				continue
			}
			select {
			case out <- samples:
			default: // drop if consumer is slow
			}
		}
	}
}

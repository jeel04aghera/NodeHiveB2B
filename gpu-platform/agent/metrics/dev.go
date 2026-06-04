package metrics

import (
	"context"
	"math"
	"math/rand"
	"time"
)

// DevSampler emits realistic synthetic telemetry for the dev GPUs so the
// utilization charts populate without physical NVIDIA hardware.
// Enabled via AGENT_DEV_MODE=true.
type DevSampler struct {
	uuids []string
	start time.Time
}

func NewDev() Sampler {
	return &DevSampler{
		uuids: []string{"GPU-DEV-AGENT-0001", "GPU-DEV-AGENT-0002"},
		start: time.Now(),
	}
}

func (s *DevSampler) Sample(ctx context.Context) ([]Sample, error) {
	ts := time.Now().UTC()
	elapsed := time.Since(s.start).Minutes()
	out := make([]Sample, 0, len(s.uuids))
	for i, u := range s.uuids {
		// A smooth sine wave plus jitter so each GPU has its own pattern.
		phase := float64(i) * 1.7
		base := 55 + 30*math.Sin(elapsed/8+phase)
		util := clamp(base+(rand.Float64()-0.5)*12, 0, 100)
		mem := clamp(40+25*math.Sin(elapsed/10+phase)+(rand.Float64()-0.5)*8, 0, 100)
		out = append(out, Sample{
			GPUUUID:    u,
			TS:         ts,
			UtilPct:    float32(util),
			MemUsedMB:  uint32(mem / 100 * 24576),
			PowerW:     float32(150 + util*2),
			TempC:      float32(55 + util*0.3),
			SMClockMHz: 2520,
			ECCErrors:  0,
			ProcCount:  uint32(1 + i),
		})
	}
	return out, nil
}

func clamp(v, lo, hi float64) float64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

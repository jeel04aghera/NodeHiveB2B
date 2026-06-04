package discovery

import (
	"context"
	"os"
	"runtime"
)

// DevDiscoverer returns synthetic GPUs so the full flow can be demonstrated on a
// machine without NVIDIA hardware. Enabled via AGENT_DEV_MODE=true.
type DevDiscoverer struct{}

func NewDev() Discoverer { return &DevDiscoverer{} }

func (d *DevDiscoverer) Discover(ctx context.Context) (Node, []GPU, error) {
	host, _ := os.Hostname()
	node := Node{
		Hostname:     host,
		OS:           runtime.GOOS,
		Kernel:       "dev-kernel",
		CPUModel:     "Development CPU (synthetic)",
		CPUCores:     runtime.NumCPU(),
		RAMMB:        65536,
		NvidiaDriver: "n/a (dev)",
		CUDAVersion:  "n/a (dev)",
	}
	// Honest labels — these are NOT real GPUs. Workloads run as CPU-only Docker
	// containers in dev mode; nothing here pretends to be NVIDIA hardware.
	gpus := []GPU{
		{Index: 0, UUID: "GPU-DEV-AGENT-0001", Model: "Development GPU (Synthetic)", MemoryMB: 24576},
		{Index: 1, UUID: "GPU-DEV-AGENT-0002", Model: "Development GPU (Synthetic)", MemoryMB: 24576},
	}
	return node, gpus, nil
}

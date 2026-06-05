# GPU Platform — developer Makefile. Startup-simple, no magic.
.DEFAULT_GOAL := help
SHELL := /bin/bash

DB_URL ?= postgres://gpu:gpu@localhost:5432/gpu?sslmode=disable
COMPOSE := docker compose -f deploy/docker-compose.yml
BIN := ./bin

.PHONY: help
help: ## Show this help
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | awk 'BEGIN{FS=":.*?## "}{printf "  \033[36m%-16s\033[0m %s\n", $$1, $$2}'

.PHONY: tools
tools: ## Install dev tools (buf, sqlc, goose, golangci-lint, air)
	go install github.com/bufbuild/buf/cmd/buf@latest
	go install github.com/sqlc-dev/sqlc/cmd/sqlc@latest
	go install github.com/pressly/goose/v3/cmd/goose@latest
	go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest
	go install github.com/air-verse/air@latest

CERTS_DIR := certs

.PHONY: certs
certs: ## Generate self-signed dev CA + server cert (requires openssl)
	@mkdir -p $(CERTS_DIR)
	@echo "Generating dev CA..."
	openssl genrsa -out $(CERTS_DIR)/ca.key 4096
	openssl req -x509 -new -nodes -key $(CERTS_DIR)/ca.key -sha256 -days 3650 \
	  -subj "/CN=NodeHive-Dev-CA" -out $(CERTS_DIR)/ca.crt
	@echo "Generating server key + cert..."
	openssl genrsa -out $(CERTS_DIR)/server.key 2048
	openssl req -new -key $(CERTS_DIR)/server.key \
	  -subj "/CN=localhost" -out $(CERTS_DIR)/server.csr
	openssl x509 -req -in $(CERTS_DIR)/server.csr -CA $(CERTS_DIR)/ca.crt \
	  -CAkey $(CERTS_DIR)/ca.key -CAcreateserial -sha256 -days 3650 \
	  -extfile <(printf "subjectAltName=DNS:localhost,IP:127.0.0.1") \
	  -out $(CERTS_DIR)/server.crt
	@rm -f $(CERTS_DIR)/server.csr
	@echo ""
	@echo "✓ Certs in $(CERTS_DIR)/. Set these env vars for TLS mode:"
	@echo "  GRPC_INSECURE=false"
	@echo "  GRPC_CERT_FILE=$(CERTS_DIR)/server.crt"
	@echo "  GRPC_KEY_FILE=$(CERTS_DIR)/server.key"
	@echo "  AGENT_INSECURE=false"
	@echo "  AGENT_CA_CERT=$(CERTS_DIR)/ca.crt"

.PHONY: generate
generate: ## Codegen: proto (buf). Run this once before building.
	buf generate

.PHONY: tidy
tidy: ## go mod tidy
	go mod tidy

.PHONY: migrate
migrate: ## Apply migrations (goose up)
	goose -dir migrations postgres "$(DB_URL)" up

.PHONY: migrate-down
migrate-down: ## Roll back one migration
	goose -dir migrations postgres "$(DB_URL)" down

.PHONY: seed
seed: ## Load dev seed data (org, projects, rate cards, sample fleet)
	psql "$(DB_URL)" -f scripts/seed_dev.sql

.PHONY: dev
dev: ## Start Postgres, migrate, seed; then print run instructions
	$(COMPOSE) up -d
	@echo "waiting for postgres..."
	@until $(COMPOSE) exec -T postgres pg_isready -U gpu >/dev/null 2>&1; do sleep 1; done
	@$(MAKE) migrate
	@$(MAKE) seed
	@echo ""
	@echo "Infra ready. If you haven't yet: 'make tools && make generate && make tidy'."
	@echo "Then in two terminals:"
	@echo "  make backend     # control plane (HTTP :8080, gRPC :9090)"
	@echo "  make agent       # enrolls a node, sends heartbeats"
	@echo "Then: curl localhost:8080/api/v1/nodes"

.PHONY: backend
backend: ## Run the control plane (hot reload if air is installed)
	ENV=development air -c .air.controlplane.toml 2>/dev/null || ENV=development go run ./cmd/controlplane

.PHONY: agent
agent: ## Run an agent against localhost (enrolls with the dev token)
	go run ./cmd/agent --server localhost:9090 --insecure --token dev-enroll-token

.PHONY: web
web: ## Run the Next.js dev server
	cd web && npm install && npm run dev

.PHONY: build
build: ## Build controlplane + agent into ./bin
	mkdir -p $(BIN)
	go build -o $(BIN)/controlplane ./cmd/controlplane
	go build -o $(BIN)/agent ./cmd/agent

.PHONY: agent-dist
agent-dist: ## Build prebuilt agent binaries the control plane serves at /dist (for `curl install.sh | sh`)
	mkdir -p dist
	CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build -o dist/agent-darwin-arm64 ./cmd/agent
	CGO_ENABLED=0 GOOS=darwin GOARCH=amd64 go build -o dist/agent-darwin-amd64 ./cmd/agent
	CGO_ENABLED=0 GOOS=linux  GOARCH=amd64 go build -o dist/agent-linux-amd64  ./cmd/agent
	CGO_ENABLED=0 GOOS=linux  GOARCH=arm64 go build -o dist/agent-linux-arm64  ./cmd/agent
	@echo "✓ Agent binaries in ./dist (served at /dist/agent-<os>-<arch>)"
	@$(MAKE) verify-dist

.PHONY: verify-dist
verify-dist: ## Fail if any expected agent artifact is missing from ./dist
	bash scripts/verify-dist.sh

.PHONY: test
test: ## Run unit tests
	go test ./...

.PHONY: test-integration
test-integration: ## Run the enrollment integration test (requires Docker)
	go test -tags=integration -count=1 -v ./test/integration/...

.PHONY: lint
lint: ## Lint Go + proto
	golangci-lint run ./...
	buf lint

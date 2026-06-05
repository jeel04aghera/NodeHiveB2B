FROM golang:1.25.7-alpine AS builder
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
# Control plane (linux, native arch).
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /bin/controlplane ./cmd/controlplane
# Prebuilt agent binaries served at /dist/agent-<os>-<arch> by the control plane.
# The agent is pure Go and cross-compiles cleanly. ALL four targets must be built
# here or `curl install.sh | sh` 404s for that platform (see scripts/verify-dist.sh).
RUN set -eux; \
    for target in darwin/arm64 darwin/amd64 linux/amd64 linux/arm64; do \
      os="${target%/*}"; arch="${target#*/}"; \
      CGO_ENABLED=0 GOOS="$os" GOARCH="$arch" \
        go build -ldflags="-s -w" -o "/dist/agent-$os-$arch" ./cmd/agent; \
    done; \
    ls -la /dist

FROM alpine:3.20
RUN apk add --no-cache ca-certificates tzdata
COPY --from=builder /bin/controlplane /bin/controlplane
# Agent binaries the installer downloads. AGENT_DIST_DIR points the file server here.
COPY --from=builder /dist /dist
ENV AGENT_DIST_DIR=/dist
EXPOSE 8080 9090
ENTRYPOINT ["/bin/controlplane"]

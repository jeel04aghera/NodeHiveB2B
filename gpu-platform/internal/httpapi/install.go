package httpapi

import (
	"fmt"
	"net/http"
	"os"
	"strings"
)

// agentDistDir is where prebuilt agent binaries live (agent-<os>-<arch>).
func agentDistDir() string {
	if d := os.Getenv("AGENT_DIST_DIR"); d != "" {
		return d
	}
	return "./dist"
}

// publicGRPCAddr is the gRPC address baked into the install script. Defaults to the
// request host on the gRPC port (dev), overridable for real deployments.
func publicGRPCAddr(host string) string {
	if v := os.Getenv("AGENT_PUBLIC_GRPC_ADDR"); v != "" {
		return v
	}
	h := host
	if i := strings.IndexByte(h, ':'); i >= 0 {
		h = h[:i]
	}
	if h == "" {
		h = "localhost"
	}
	return h + ":9090"
}

// installScript serves a self-contained one-line installer:
//
//	curl -fsSL <cp>/install.sh | sh -s -- --token <TOKEN>
//
// It detects OS/arch, downloads the matching prebuilt agent binary, and runs it —
// enrolling the host into the org the token belongs to.
func (a *API) installScript(w http.ResponseWriter, r *http.Request) {
	scheme := "http"
	if r.Header.Get("X-Forwarded-Proto") == "https" || r.TLS != nil {
		scheme = "https"
	}
	httpBase := scheme + "://" + r.Host
	grpc := publicGRPCAddr(r.Host)

	w.Header().Set("Content-Type", "text/x-shellscript; charset=utf-8")
	fmt.Fprintf(w, installTemplate, httpBase, grpc)
}

const installTemplate = `#!/bin/sh
# NodeHive agent installer. Enrolls this machine into your GPU fleet.
#   curl -fsSL %[1]s/install.sh | sh -s -- --token <YOUR_TOKEN>
set -e

NH_HTTP="%[1]s"
SERVER="%[2]s"
TOKEN=""
DEV=""

while [ $# -gt 0 ]; do
  case "$1" in
    --token) TOKEN="$2"; shift 2 ;;
    --server) SERVER="$2"; shift 2 ;;
    --dev) DEV="--dev"; shift ;;
    *) shift ;;
  esac
done

if [ -z "$TOKEN" ]; then
  echo "error: --token is required (copy it from NodeHive → Nodes)" >&2
  exit 1
fi

OS=$(uname -s | tr '[:upper:]' '[:lower:]')
ARCH=$(uname -m)
case "$ARCH" in
  x86_64|amd64) ARCH=amd64 ;;
  arm64|aarch64) ARCH=arm64 ;;
esac

# No NVIDIA GPU (e.g. Apple Silicon)? Run in dev mode: synthetic GPU inventory,
# real Docker containers for workloads.
if [ -z "$DEV" ] && ! command -v nvidia-smi >/dev/null 2>&1; then
  DEV="--dev"
  echo "▸ No nvidia-smi found — installing in dev mode (synthetic GPUs, real Docker)."
fi

DIR="$HOME/.nodehive"
BIN="$DIR/nodehive-agent"
mkdir -p "$DIR"

echo "▸ Downloading NodeHive agent ($OS/$ARCH)…"
if ! curl -fsSL "$NH_HTTP/dist/agent-$OS-$ARCH" -o "$BIN"; then
  echo "error: no prebuilt agent for $OS/$ARCH at $NH_HTTP/dist/agent-$OS-$ARCH" >&2
  exit 1
fi
chmod +x "$BIN"

echo "▸ Enrolling with $SERVER and starting the agent…"
echo "  (leave this running; the node stays online while the agent runs)"
exec "$BIN" --server "$SERVER" --insecure --token "$TOKEN" $DEV
`

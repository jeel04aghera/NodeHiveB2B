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

// publicGRPCAddr is the gRPC address baked into the install script. An explicit
// AGENT_PUBLIC_GRPC_ADDR always wins (this is how you point agents at a publicly
// routable gRPC endpoint — e.g. a Railway TCP-proxy host:port, since Railway's HTTP
// edge can't carry gRPC and won't route a second port). With no override we derive
// <request-host>:9090, which is only correct when that port is directly reachable
// (local/dev, or a VM that exposes both ports). The bool reports that derived case so
// the installer can warn when it's likely unreachable (a hosted HTTPS control plane).
func publicGRPCAddr(host string) (addr string, derived bool) {
	if v := os.Getenv("AGENT_PUBLIC_GRPC_ADDR"); v != "" {
		return v, false
	}
	h := host
	if i := strings.IndexByte(h, ':'); i >= 0 {
		h = h[:i]
	}
	if h == "" {
		h = "localhost"
	}
	return h + ":9090", true
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
	grpc, derived := publicGRPCAddr(r.Host)

	// A hosted (HTTPS) control plane that derived <host>:9090 is almost certainly
	// behind a proxy that doesn't route that port (Railway, most PaaS) — enrollment
	// will i/o-timeout on gRPC. Surface the exact remedy instead of failing silently.
	warn := ""
	if derived && scheme == "https" {
		warn = `echo "⚠ This control plane advertises gRPC at ` + grpc + `, derived from the HTTP host." >&2
echo "  If enrollment times out, that port is not publicly routable. Expose the agent" >&2
echo "  gateway (container port 9090) over a raw TCP route and set AGENT_PUBLIC_GRPC_ADDR" >&2
echo "  on the control plane to that host:port (e.g. a Railway TCP Proxy endpoint)." >&2
`
	}

	w.Header().Set("Content-Type", "text/x-shellscript; charset=utf-8")
	fmt.Fprintf(w, installTemplate, httpBase, grpc, warn)
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

%[3]sOS=$(uname -s | tr '[:upper:]' '[:lower:]')
ARCH=$(uname -m)
case "$ARCH" in
  x86_64|amd64) ARCH=amd64 ;;
  arm64|aarch64) ARCH=arm64 ;;
esac

# Friendly name for the detected platform.
PLATFORM="$OS/$ARCH"
case "$OS/$ARCH" in
  darwin/arm64) PRETTY="macOS Apple Silicon" ;;
  darwin/amd64) PRETTY="macOS Intel" ;;
  linux/amd64)  PRETTY="Linux x86_64" ;;
  linux/arm64)  PRETTY="Linux ARM64" ;;
  *)            PRETTY="$PLATFORM" ;;
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

ARTIFACT="agent-$OS-$ARCH"
echo "▸ Detected $PRETTY ($PLATFORM)."
echo "▸ Downloading NodeHive agent ($ARTIFACT)…"
if ! curl -fsSL "$NH_HTTP/dist/$ARTIFACT" -o "$BIN"; then
  # List what the control plane actually publishes (parsed from the /dist listing).
  AVAILABLE=$(curl -fsSL "$NH_HTTP/dist/" 2>/dev/null \
    | grep -oE 'agent-[a-z0-9]+-[a-z0-9]+' | sort -u | sed 's/^/    /')
  echo "" >&2
  echo "error: no prebuilt agent available for $PRETTY." >&2
  echo "" >&2
  echo "  Expected artifact:" >&2
  echo "    dist/$ARTIFACT" >&2
  echo "" >&2
  if [ -n "$AVAILABLE" ]; then
    echo "  Available artifacts:" >&2
    echo "$AVAILABLE" >&2
  else
    echo "  Available artifacts: none — the server is publishing no agent binaries." >&2
    echo "  (control-plane image was built without 'make agent-dist' / the /dist copy step)" >&2
  fi
  echo "" >&2
  exit 1
fi
chmod +x "$BIN"

echo "▸ Enrolling with $SERVER and starting the agent…"
echo "  (leave this running; the node stays online while the agent runs)"
exec "$BIN" --server "$SERVER" --insecure --token "$TOKEN" $DEV
`

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

// agentDownloadBase is the base URL the installer downloads agent binaries from,
// as "<base>/agent-<os>-<arch>". Defaults to the control plane's own /dist (fine for
// dev/self-host/airgap), but in production set AGENT_DIST_BASE_URL to a CDN-backed,
// versioned location so the ~11 MB binary doesn't stream from the single-region app
// container. Examples (GitHub Releases — global CDN, free, versioned by tag):
//
//	AGENT_DIST_BASE_URL=https://github.com/<org>/<repo>/releases/download/v1.4.0   # pinned
//	AGENT_DIST_BASE_URL=https://github.com/<org>/<repo>/releases/latest/download   # auto-latest
//
// The control plane keeps serving /dist either way, so it stays a working fallback.
func agentDownloadBase(httpBase string) string {
	if v := os.Getenv("AGENT_DIST_BASE_URL"); v != "" {
		return strings.TrimRight(v, "/")
	}
	return httpBase + "/dist"
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
	fmt.Fprintf(w, installTemplate, httpBase, grpc, warn, agentDownloadBase(httpBase))
}

const installTemplate = `#!/bin/sh
# NodeHive agent installer. Enrolls this machine into your GPU fleet.
#   curl -fsSL %[1]s/install.sh | sh -s -- --token <YOUR_TOKEN>
set -e

NH_HTTP="%[1]s"
SERVER="%[2]s"
DLBASE="%[4]s"
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

# Verbose tracing for support:  NH_DEBUG=1 curl -fsSL <cp>/install.sh | sh -s -- --token …
# Normal runs stay quiet but still show errors (-sS); debug adds full curl traces + set -x.
CURLOPTS="-sS"
if [ "${NH_DEBUG:-}" = "1" ] || [ "${NH_DEBUG:-}" = "true" ]; then
  set -x
  CURLOPTS="-v"
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
URL="$DLBASE/$ARTIFACT"
echo "▸ Detected $PRETTY ($PLATFORM)."

# Distinguish a genuinely missing artifact (404) from a flaky transfer by probing first.
# The HEAD also tells us the expected size so we can verify the download is complete.
if ! HEAD=$(curl -fL $CURLOPTS -I "$URL" </dev/null 2>/dev/null); then
  AVAILABLE=$(curl -fL $CURLOPTS "$NH_HTTP/dist/" </dev/null 2>/dev/null \
    | grep -oE 'agent-[a-z0-9]+-[a-z0-9]+' | sort -u | sed 's/^/    /')
  echo "" >&2
  echo "error: no prebuilt agent available for $PRETTY." >&2
  echo "" >&2
  echo "  Expected artifact:" >&2
  echo "    $URL" >&2
  echo "" >&2
  if [ -n "$AVAILABLE" ]; then
    echo "  Available artifacts (bundled in the control plane):" >&2
    echo "$AVAILABLE" >&2
  else
    echo "  Available artifacts: none — the server is publishing no agent binaries." >&2
    echo "  (control-plane image was built without 'make agent-dist' / the /dist copy step)" >&2
  fi
  echo "" >&2
  exit 1
fi
EXPECT=$(printf '%%s' "$HEAD" | awk 'tolower($1) == "content-length:" { print $2 }' | tr -d '\r ' | tail -1)

# Railway's HTTP edge can STALL or RESET a large transfer mid-stream (the agent is
# ~11 MB), surfacing as a hang or "curl: (56) Recv failure: Connection reset by peer".
# Retry with resume (-C -) and a stall timeout (--speed-time) so a flaky network
# recovers instead of writing a truncated binary or aborting the install.
echo "▸ Downloading NodeHive agent ($ARTIFACT${EXPECT:+, $EXPECT bytes})…"
TMP="$BIN.partial"
rm -f "$TMP"
if ! curl -fL $CURLOPTS --retry 5 --retry-delay 2 --retry-all-errors --retry-connrefused \
        --connect-timeout 20 --speed-limit 2048 --speed-time 20 \
        -C - "$URL" -o "$TMP" </dev/null; then
  rc=$?
  rm -f "$TMP"
  echo "error: failed to download the agent after retries (curl exit $rc)." >&2
  echo "  URL: $URL" >&2
  echo "  Usually a transient network/edge reset mid-download — re-run the installer." >&2
  echo "  For details: NH_DEBUG=1 curl -fsSL $NH_HTTP/install.sh | sh -s -- --token <TOKEN>" >&2
  exit 1
fi

# Integrity gate: never chmod+exec an empty or truncated binary.
GOT=$(wc -c < "$TMP" | tr -d ' ')
if [ -z "$GOT" ] || [ "$GOT" -eq 0 ]; then
  echo "error: downloaded agent is empty; re-run the installer." >&2
  rm -f "$TMP"; exit 1
fi
if [ -n "$EXPECT" ] && [ "$GOT" != "$EXPECT" ]; then
  echo "error: agent download incomplete — got $GOT of $EXPECT bytes; re-run the installer." >&2
  rm -f "$TMP"; exit 1
fi

chmod +x "$TMP"
mv -f "$TMP" "$BIN"
echo "▸ Downloaded $GOT bytes → $BIN"

echo "▸ Enrolling with the control plane (gRPC $SERVER) and starting the agent…"
echo "  command: $BIN --server $SERVER --insecure --token <redacted> $DEV"
echo "  (leave this running; the node stays online while the agent runs)"
exec "$BIN" --server "$SERVER" --insecure --token "$TOKEN" $DEV
`

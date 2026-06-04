package executor

import (
	"context"
	crand "crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

// DockerExecutor runs workloads as Docker containers using the NVIDIA Container Toolkit.
// For SSH/Jupyter-enabled workloads, it installs and starts the relevant daemon
// inside the container and returns the reachable host:port endpoint.
type DockerExecutor struct {
	hostAddr       string
	gpuPassthrough bool
}

// New returns a Docker executor with GPU passthrough enabled (production default).
func New() Executor {
	return NewWithOptions(Options{GPUPassthrough: true})
}

// NewWithOptions returns a Docker executor with explicit options. Use this on a
// dev machine without NVIDIA hardware (GPUPassthrough:false) so SSH/Jupyter
// workloads still launch as real, connectable containers.
func NewWithOptions(opt Options) Executor {
	host := opt.AdvertiseHost
	if host == "" {
		host, _ = os.Hostname()
	}
	return &DockerExecutor{hostAddr: host, gpuPassthrough: opt.GPUPassthrough}
}

const containerPrefix = "nodehive-"

func containerName(workloadID string) string {
	id := strings.ReplaceAll(workloadID, "-", "")
	if len(id) > 20 {
		id = id[:20]
	}
	return containerPrefix + id
}

func (e *DockerExecutor) Launch(ctx context.Context, spec LaunchSpec) (Status, error) {
	name := containerName(spec.WorkloadID)
	sshPass := spec.SSHPassword
	if sshPass == "" {
		sshPass = spec.Env["SSH_PASSWORD"]
	}
	exposeSSH := spec.ExposeSSH && sshPass != ""

	// Jupyter token: reuse the SSH password if present, else a fresh random token.
	jToken := ""
	if spec.ExposeJupyter {
		jToken = sshPass
		if jToken == "" {
			jToken = randToken()
		}
	}

	// Resolve the runtime image. The template's base image is what actually runs
	// (PyTorch, TensorFlow, CUDA, …). For SSH/Jupyter we build a thin derived image
	// FROM that base — sshd/jupyter layered on top, cached so it builds once per
	// (base image + access) combination. Plain compute workloads run the base as-is.
	spec.stage("pulling_image")
	if exposeSSH || spec.ExposeJupyter {
		spec.stage("building_env")
	}
	image, buildErr := e.ensureRunImage(ctx, spec.Image, exposeSSH, spec.ExposeJupyter)
	if buildErr != nil {
		return Status{
			WorkloadID: spec.WorkloadID,
			State:      "failed",
			Message:    "image preparation failed: " + buildErr.Error(),
		}, buildErr
	}

	args := []string{"run", "-d", "--name", name, "--restart=no"}

	// GPU access via NVIDIA Container Toolkit. Skipped on hosts without it
	// (e.g. a dev laptop) so the workload still launches without a GPU.
	if e.gpuPassthrough && len(spec.GPUUUIDs) > 0 {
		args = append(args, "--gpus", "device="+strings.Join(spec.GPUUUIDs, ","))
	}

	if spec.IdleTimeoutSec > 0 {
		args = append(args, "--label", fmt.Sprintf("nodehive.idle_timeout=%d", spec.IdleTimeoutSec))
	}
	args = append(args, "--label", "nodehive.workload_id="+spec.WorkloadID)

	// Credentials the prebuilt images read from the environment.
	if exposeSSH {
		args = append(args, "-e", "SSH_PASSWORD="+sshPass)
	}
	if spec.ExposeJupyter {
		args = append(args, "-e", "JUPYTER_TOKEN="+jToken)
	}
	for k, v := range spec.Env {
		if k == "SSH_PASSWORD" || k == "JUPYTER_TOKEN" {
			continue // already handled above
		}
		args = append(args, "-e", k+"="+v)
	}

	if exposeSSH {
		args = append(args, "-p", "127.0.0.1::22") // ephemeral host port → container :22
	}
	if spec.ExposeJupyter {
		args = append(args, "-p", "127.0.0.1::8888")
	}
	args = append(args, image)

	spec.stage("starting_container")
	cmd := exec.CommandContext(ctx, "docker", args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return Status{
			WorkloadID: spec.WorkloadID,
			State:      "failed",
			Message:    "docker run failed: " + strings.TrimSpace(string(out)),
		}, fmt.Errorf("docker run: %w: %s", err, out)
	}

	// Liveness gate: prebuilt images start in <1s. Give it a moment, then confirm
	// the container is actually running. If it crashed, surface the logs as a
	// failure instead of falsely reporting "running" with no endpoint.
	spec.stage("configuring")
	time.Sleep(1500 * time.Millisecond)
	if running, code := e.containerState(ctx, name); !running {
		logs, _ := e.Logs(ctx, spec.WorkloadID, 50)
		_ = e.Stop(ctx, spec.WorkloadID)
		return Status{
			WorkloadID: spec.WorkloadID,
			State:      "failed",
			Message:    fmt.Sprintf("container exited (code %s) on launch:\n%s", code, strings.TrimSpace(logs)),
		}, fmt.Errorf("container %s exited code %s on launch", name, code)
	}

	status := Status{WorkloadID: spec.WorkloadID, State: "running", SSHPassword: sshPass}
	if exposeSSH {
		if p := e.inspectPort(ctx, name, "22/tcp"); p != "" {
			status.SSHEndpoint = e.hostAddr + ":" + p
		}
	}
	if spec.ExposeJupyter {
		if p := e.inspectPort(ctx, name, "8888/tcp"); p != "" {
			ep := e.hostAddr + ":" + p
			if jToken != "" {
				ep += "/lab?token=" + jToken // direct, authenticated open-in-browser URL
			}
			status.JupyterEndpoint = ep
		}
	}
	return status, nil
}

// ensureRunImage returns the image to run. With no access overlay it's the base
// image itself. With SSH/Jupyter it builds (once, then cached) a thin derived
// image FROM the base that adds the daemon(s) + a nodehive entrypoint. The derived
// tag is keyed by base image + access combo so it's reused across launches.
func (e *DockerExecutor) ensureRunImage(ctx context.Context, baseImage string, ssh, jupyter bool) (string, error) {
	if !ssh && !jupyter {
		return baseImage, nil
	}
	combo := "ssh"
	if ssh && jupyter {
		combo = "ssh-jupyter"
	} else if jupyter {
		combo = "jupyter"
	}
	sum := sha256.Sum256([]byte(baseImage + "|" + combo + "|" + derivedImageVersion))
	tag := fmt.Sprintf("nodehive/run-%s:%s", combo, hex.EncodeToString(sum[:])[:12])

	if e.imageExists(ctx, tag) {
		return tag, nil
	}
	if err := e.buildDerivedImage(ctx, baseImage, combo, tag); err != nil {
		return "", err
	}
	return tag, nil
}

func (e *DockerExecutor) imageExists(ctx context.Context, tag string) bool {
	return exec.CommandContext(ctx, "docker", "image", "inspect", tag).Run() == nil
}

// buildDerivedImage builds `tag` FROM baseImage, layering sshd and/or jupyter and a
// runtime entrypoint that reads SSH_PASSWORD / JUPYTER_TOKEN from the environment.
func (e *DockerExecutor) buildDerivedImage(ctx context.Context, baseImage, combo, tag string) error {
	dir, err := os.MkdirTemp("", "nodehive-build-")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(dir) }()

	ssh := strings.Contains(combo, "ssh")
	jupyter := strings.Contains(combo, "jupyter")

	var install strings.Builder
	if ssh {
		install.WriteString(`( apt-get update && apt-get install -y --no-install-recommends openssh-server ca-certificates ) || ( microdnf install -y openssh-server ) || ( yum install -y openssh-server ) || ( apk add --no-cache openssh ) ; mkdir -p /run/sshd /root/.ssh ; ssh-keygen -A ; ` +
			`sed -i -e 's/^#\?PermitRootLogin.*/PermitRootLogin yes/' -e 's/^#\?PasswordAuthentication.*/PasswordAuthentication yes/' /etc/ssh/sshd_config ; `)
	}
	if jupyter {
		install.WriteString(`( command -v jupyter >/dev/null 2>&1 ) || ( pip install --no-cache-dir jupyterlab ) || ( pip3 install --no-cache-dir jupyterlab ) || ( apt-get update && apt-get install -y python3-pip && pip3 install --no-cache-dir jupyterlab ) ; `)
		// Some bases (e.g. the conda-based pytorch image) ship a stale `packaging`
		// whose utils.py lacks InvalidName, which crashes modern JupyterLab on import.
		// Force it (and jupyterlab's other deps) up to a compatible version.
		install.WriteString(`( pip install --no-cache-dir -U packaging >/dev/null 2>&1 || pip3 install --no-cache-dir -U packaging >/dev/null 2>&1 || true ) ; `)
	}

	entrypoint := buildEntrypointScript(ssh, jupyter)
	if err := os.WriteFile(dir+"/nodehive-entrypoint", []byte(entrypoint), 0o755); err != nil {
		return err
	}
	dockerfile := fmt.Sprintf("FROM %s\nUSER root\nRUN %s true\nCOPY nodehive-entrypoint /usr/local/bin/nodehive-entrypoint\nRUN chmod +x /usr/local/bin/nodehive-entrypoint\nENTRYPOINT [\"/usr/local/bin/nodehive-entrypoint\"]\n",
		baseImage, install.String())
	if err := os.WriteFile(dir+"/Dockerfile", []byte(dockerfile), 0o644); err != nil {
		return err
	}

	cmd := exec.CommandContext(ctx, "docker", "build", "-t", tag, dir)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("build %s from %s: %w: %s", tag, baseImage, err, strings.TrimSpace(string(out)))
	}
	return nil
}

// derivedImageVersion is mixed into the derived-image tag hash so that changes to
// the entrypoint/build below invalidate cached images. Bump when this file changes.
const derivedImageVersion = "v3-jupyterfix"

// buildEntrypointScript returns the /bin/sh entrypoint baked into a derived image.
// It reads credentials from env at runtime so one derived image serves all workloads.
func buildEntrypointScript(ssh, jupyter bool) string {
	var b strings.Builder
	b.WriteString("#!/bin/sh\nset -eu\n")
	// Persist the image environment so SSH/login shells inherit PATH and the toolchain
	// (e.g. CUDA's /usr/local/cuda/bin, conda) — sshd resets the environment, which is
	// why `nvcc` looks "command not found" over SSH even though it's installed.
	b.WriteString(`( set +eu
  _vars="PATH LD_LIBRARY_PATH LIBRARY_PATH CUDA_HOME CUDA_PATH CUDA_VERSION CONDA_DIR VIRTUAL_ENV"
  : > /etc/environment
  : > /etc/profile.d/nodehive-env.sh
  echo "# Added by NodeHive — restore image PATH/toolchain for login shells" >> /etc/profile.d/nodehive-env.sh
  for _v in $_vars; do
    eval "_val=\${$_v:-}"
    if [ -n "$_val" ]; then
      printf '%s="%s"\n' "$_v" "$_val" >> /etc/environment
      printf 'export %s="%s"\n' "$_v" "$_val" >> /etc/profile.d/nodehive-env.sh
    fi
  done
  chmod 644 /etc/profile.d/nodehive-env.sh
) >/dev/null 2>&1 || true
` + "\n")
	if ssh {
		b.WriteString(`: "${SSH_PASSWORD:=nodehive}"` + "\n")
		b.WriteString(`echo "root:${SSH_PASSWORD}" | chpasswd` + "\n")
	}
	switch {
	case ssh && jupyter:
		// Run Jupyter in the background and let sshd be the foreground supervisor, so a
		// Jupyter problem degrades to "SSH still works" instead of killing the workload.
		b.WriteString(`TOKEN="${JUPYTER_TOKEN:-}"` + "\n")
		b.WriteString(`jupyter lab --ip=0.0.0.0 --port=8888 --no-browser --allow-root --ServerApp.token="${TOKEN}" --ServerApp.password='' &` + "\n")
		b.WriteString("exec /usr/sbin/sshd -D -e\n")
	case jupyter:
		b.WriteString(`TOKEN="${JUPYTER_TOKEN:-}"` + "\n")
		b.WriteString(`exec jupyter lab --ip=0.0.0.0 --port=8888 --no-browser --allow-root --ServerApp.token="${TOKEN}" --ServerApp.password=''` + "\n")
	default: // ssh only
		b.WriteString("exec /usr/sbin/sshd -D -e\n")
	}
	return b.String()
}

func randToken() string {
	b := make([]byte, 12)
	if _, err := crand.Read(b); err != nil {
		return "nodehive"
	}
	return hex.EncodeToString(b)
}

// containerState reports whether the named container is running and its exit code.
func (e *DockerExecutor) containerState(ctx context.Context, name string) (bool, string) {
	out, err := exec.CommandContext(ctx, "docker", "inspect",
		"--format={{.State.Running}} {{.State.ExitCode}}", name).Output()
	if err != nil {
		return false, "?"
	}
	parts := strings.Fields(strings.TrimSpace(string(out)))
	if len(parts) < 2 {
		return false, "?"
	}
	return parts[0] == "true", parts[1]
}

func (e *DockerExecutor) Stop(ctx context.Context, workloadID string) error {
	name := containerName(workloadID)
	_ = exec.CommandContext(ctx, "docker", "stop", "-t", "10", name).Run()
	_ = exec.CommandContext(ctx, "docker", "rm", "-f", name).Run()
	return nil
}

func (e *DockerExecutor) Status(ctx context.Context, workloadID string) (Status, error) {
	name := containerName(workloadID)
	out, err := exec.CommandContext(ctx, "docker", "inspect",
		"--format={{.State.Status}}", name).Output()
	if err != nil {
		return Status{WorkloadID: workloadID, State: "stopped"}, nil
	}
	s := Status{WorkloadID: workloadID}
	switch strings.TrimSpace(string(out)) {
	case "running":
		s.State = "running"
	case "exited", "dead":
		s.State = "stopped"
	default:
		s.State = strings.TrimSpace(string(out))
	}
	return s, nil
}

func (e *DockerExecutor) Logs(ctx context.Context, workloadID string, tail int) (string, error) {
	name := containerName(workloadID)
	out, err := exec.CommandContext(ctx, "docker", "logs",
		"--tail", fmt.Sprintf("%d", tail), name).CombinedOutput()
	return string(out), err
}

func (e *DockerExecutor) inspectPort(ctx context.Context, name, port string) string {
	type binding struct {
		HostIP   string `json:"HostIP"`
		HostPort string `json:"HostPort"`
	}
	type portMap map[string][]binding
	type result struct {
		NetworkSettings struct {
			Ports portMap `json:"Ports"`
		} `json:"NetworkSettings"`
	}
	out, err := exec.CommandContext(ctx, "docker", "inspect", name).Output()
	if err != nil {
		return ""
	}
	var rs []result
	if json.Unmarshal(out, &rs) != nil || len(rs) == 0 {
		return ""
	}
	if bs := rs[0].NetworkSettings.Ports[port]; len(bs) > 0 {
		return bs[0].HostPort
	}
	return ""
}

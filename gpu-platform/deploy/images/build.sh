#!/usr/bin/env bash
# Build the nodehive prebuilt workload images. Run once on each agent host (or
# push to a registry). Workloads using SSH/Jupyter launch instantly afterwards.
set -euo pipefail
cd "$(dirname "$0")"

build() {
  local name="$1" dir="$2"
  echo "==> building nodehive/${name}:latest"
  docker build -t "nodehive/${name}:latest" "$dir"
}

build ssh-base          ssh-base
build jupyter-base      jupyter-base
build ssh-jupyter-base  ssh-jupyter-base

echo
echo "Built:"
docker images 'nodehive/*' --format '  {{.Repository}}:{{.Tag}}  {{.Size}}'

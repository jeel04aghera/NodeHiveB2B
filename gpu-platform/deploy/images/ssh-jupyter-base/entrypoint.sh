#!/bin/sh
# nodehive ssh-jupyter-base entrypoint. Starts sshd in the background, then runs
# JupyterLab in the foreground so the container stays alive on one process.
set -eu
: "${SSH_PASSWORD:=nodehive}"
echo "root:${SSH_PASSWORD}" | chpasswd
/usr/sbin/sshd -e
TOKEN="${JUPYTER_TOKEN:-}"
exec jupyter lab \
  --ip=0.0.0.0 --port=8888 --no-browser --allow-root \
  --ServerApp.token="${TOKEN}" --ServerApp.password=''

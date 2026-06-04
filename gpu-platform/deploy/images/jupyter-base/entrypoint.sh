#!/bin/sh
# nodehive jupyter-base entrypoint. Launches JupyterLab on 0.0.0.0:8888 with the
# token from $JUPYTER_TOKEN (control-plane managed). Foreground = container stays alive.
set -eu
TOKEN="${JUPYTER_TOKEN:-}"
exec jupyter lab \
  --ip=0.0.0.0 --port=8888 --no-browser --allow-root \
  --ServerApp.token="${TOKEN}" --ServerApp.password=''

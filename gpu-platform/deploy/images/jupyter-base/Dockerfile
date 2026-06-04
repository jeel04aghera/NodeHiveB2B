# nodehive/jupyter-base — JupyterLab pre-installed, runs as root, starts instantly.
# Token is injected at runtime via the JUPYTER_TOKEN env var (empty = open, dev only).
FROM python:3.11-slim

RUN pip install --no-cache-dir jupyterlab \
 && mkdir -p /root/work

COPY entrypoint.sh /usr/local/bin/nodehive-entrypoint
RUN chmod +x /usr/local/bin/nodehive-entrypoint

WORKDIR /root/work
EXPOSE 8888
ENTRYPOINT ["/usr/local/bin/nodehive-entrypoint"]

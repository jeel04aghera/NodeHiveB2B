# nodehive/ssh-jupyter-base — sshd + JupyterLab pre-installed, runs as root.
# sshd runs in the background; JupyterLab holds the foreground. Starts instantly.
FROM python:3.11-slim

RUN apt-get update \
 && apt-get install -y --no-install-recommends openssh-server ca-certificates \
 && rm -rf /var/lib/apt/lists/* \
 && mkdir -p /run/sshd /root/.ssh /root/work \
 && sed -i \
      -e 's/^#\?PermitRootLogin.*/PermitRootLogin yes/' \
      -e 's/^#\?PasswordAuthentication.*/PasswordAuthentication yes/' \
      /etc/ssh/sshd_config \
 && ssh-keygen -A \
 && pip install --no-cache-dir jupyterlab

COPY entrypoint.sh /usr/local/bin/nodehive-entrypoint
RUN chmod +x /usr/local/bin/nodehive-entrypoint

WORKDIR /root/work
EXPOSE 22 8888
ENTRYPOINT ["/usr/local/bin/nodehive-entrypoint"]

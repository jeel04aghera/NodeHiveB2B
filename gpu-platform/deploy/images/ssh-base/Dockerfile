# nodehive/ssh-base — openssh pre-installed, runs as root, starts instantly.
# Password is injected at runtime via the SSH_PASSWORD env var.
FROM ubuntu:22.04

RUN apt-get update \
 && apt-get install -y --no-install-recommends openssh-server ca-certificates \
 && rm -rf /var/lib/apt/lists/* \
 && mkdir -p /run/sshd /root/.ssh \
 && sed -i \
      -e 's/^#\?PermitRootLogin.*/PermitRootLogin yes/' \
      -e 's/^#\?PasswordAuthentication.*/PasswordAuthentication yes/' \
      /etc/ssh/sshd_config \
 && ssh-keygen -A

COPY entrypoint.sh /usr/local/bin/nodehive-entrypoint
RUN chmod +x /usr/local/bin/nodehive-entrypoint

EXPOSE 22
ENTRYPOINT ["/usr/local/bin/nodehive-entrypoint"]

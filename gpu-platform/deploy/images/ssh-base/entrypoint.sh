#!/bin/sh
# nodehive ssh-base entrypoint. Sets the root password from $SSH_PASSWORD (so the
# control plane controls credentials) then runs sshd in the foreground.
set -eu
: "${SSH_PASSWORD:=nodehive}"
echo "root:${SSH_PASSWORD}" | chpasswd
exec /usr/sbin/sshd -D -e

#!/usr/bin/env bash
# Starts the inner Docker daemon, then hands off to CMD (sleep infinity).
set -euo pipefail

# The image runs as the user so `docker exec` without --user doesn't land as root and leave
# root-owned files in persisted volumes. The setup below needs root, so redo it through sudo.
if [ "$(id -u)" -ne 0 ]; then
  sudo --preserve-env=SSH_AUTH_SOCK "$0"
  exec "$@"
fi

# cgroup v2 nesting (same approach as moby's official dind script): move our
# processes into a child cgroup so dockerd can delegate controllers to containers.
if [ -f /sys/fs/cgroup/cgroup.controllers ]; then
  mkdir -p /sys/fs/cgroup/init
  xargs -rn1 < /sys/fs/cgroup/cgroup.procs > /sys/fs/cgroup/init/cgroup.procs 2>/dev/null || :
  sed -e 's/ / +/g' -e 's/^/+/' < /sys/fs/cgroup/cgroup.controllers \
    > /sys/fs/cgroup/cgroup.subtree_control 2>/dev/null || :
fi

# Let the dev user use the forwarded macOS SSH agent socket.
if [ -S "${SSH_AUTH_SOCK:-}" ]; then
  chmod 666 "$SSH_AUTH_SOCK" || :
fi

# Inner containers can't reach the outer embedded DNS (127.0.0.11) and would fall
# back to 8.8.8.8. Point the inner dockerd at the outer upstream resolver instead
# (Docker Desktop lists it as "ExtServers: [host(192.168.65.7)]").
upstream_dns=$(sed -n 's/^# ExtServers: \[\(host(\)\{0,1\}\([0-9.]*\).*/\2/p' /etc/resolv.conf | head -1)
if [ -n "$upstream_dns" ] && [ ! -f /etc/docker/daemon.json ]; then
  mkdir -p /etc/docker
  printf '{ "dns": ["%s"] }\n' "$upstream_dns" > /etc/docker/daemon.json
fi

# Clean up a stale pid file left behind by an unclean stop.
rm -f /var/run/docker.pid

dockerd < /dev/null > /var/log/dockerd.log 2>&1 &

for _ in $(seq 1 30); do
  if docker info > /dev/null 2>&1; then
    echo "dockerd is up"
    break
  fi
  sleep 1
done
docker info > /dev/null 2>&1 || echo "WARNING: dockerd did not start; see /var/log/dockerd.log" >&2

exec "$@"

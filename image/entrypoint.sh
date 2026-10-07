#!/usr/bin/env bash
set -euo pipefail

mkdir -p "${T3CODE_HOME}" \
  "${T3CODE_HOME}/userdata/logs/terminals" \
  /home/ai-agent/repos \
  /home/ai-agent/.cache/npm \
  /home/ai-agent/.cache/bun \
  /home/ai-agent/.cache/go/mod \
  /home/ai-agent/.cache/go/build \
  /home/ai-agent/.cache/pip

# vswarm-dev keeps its registry of long-running servers here. /run is a tmpfs
# the compose template declares, so the registry cannot outlive the container,
# and root owns it, so this is the one moment that can hand it to the agent.
# Losing it costs vswarm-dev and nothing else, so it is not worth a workspace
# that will not start.
if ! sudo install -d -o ai-agent -g ai-agent -m 0755 /run/vswarm; then
  echo "entrypoint: /run/vswarm unavailable; vswarm-dev will refuse to run" >&2
fi

T3_RUNTIME=/opt/t3-runtime
child_pid=""
stopping=0

forward() {
  stopping=1
  if [[ -n "${child_pid}" ]]; then
    kill -TERM "${child_pid}" 2>/dev/null || true
  fi
}
trap forward TERM INT

# t3 serves from a runtime it owns under ${T3CODE_HOME}/runtime, so that the
# app's own update can install a version and switch to it without this image
# being rebuilt. Seeding that runtime from the baked copy is what lets a fresh
# workspace start with no network.
vswarm-t3 bootstrap

# A shim that made the runtime executable answer `-e` used to live here, so that
# t3 could verify its Antigravity browser-suppression helper. It cannot work in
# any form: t3 builds that helper from process.execPath, and on Linux that is
# always the real executable the kernel mapped, never a wrapper script that
# exec'd it. Antigravity sign-in therefore stays unsupported in a workspace until
# t3 offers a runtime that evaluates `-e`, or a way to name the helper itself.

# The launcher supervises t3 -- it restarts it across an update and rolls back a
# version that will not open the database -- but it exits when t3 dies for any
# other reason, expecting its own supervisor to bring it back. Under systemd
# that is Restart=; here it is this loop.
#
# Each update t3 takes leaves the runtime it replaced on disk, ~200MB apiece,
# and only bootstrap drops them, so a workspace that stays up for weeks keeps
# every one. Waking hourly while the launcher runs drops them as they go.
prune_every=3600
while true; do
  "${T3_RUNTIME}/t3" __service-launcher &
  child_pid=$!
  set +e
  while true; do
    sleep "${prune_every}" &
    ticker=$!
    finished=""
    wait -n -p finished "${child_pid}" "${ticker}"
    status=$?
    if [[ "${finished:-}" != "${ticker}" || "${stopping}" -eq 1 ]]; then
      kill "${ticker}" 2>/dev/null
      break
    fi
    vswarm-t3 prune
  done
  set -e
  child_pid=""
  if [[ "${stopping}" -eq 1 ]]; then
    exit "${status}"
  fi
  # The launcher exited on its own, so bring it back; brief pause so a crash
  # loop cannot spin.
  sleep 2
done

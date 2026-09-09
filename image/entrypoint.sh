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

LAUNCHER=/usr/local/lib/node_modules/t3/dist/service-launcher.mjs
PREVIEW_HOST=/opt/preview-host/host.ts
child_pid=""
preview_pid=""
stopping=0

forward() {
  stopping=1
  for pid in "${child_pid}" "${preview_pid}"; do
    if [[ -n "${pid}" ]]; then
      kill -TERM "${pid}" 2>/dev/null || true
    fi
  done
}
trap forward TERM INT

# The host serves t3's preview automation tools from the browser baked into this
# image. It needs a credential, delivered either in the environment or as
# ~/.preview-host.env the way ~/.pg.env is delivered. Starting it unconditionally
# is what lets `vswarm pair` deliver that file after the container is healthy:
# the host retries until the credential appears, and idles cheaply until then.
node "${PREVIEW_HOST}" &
preview_pid=$!

# t3 serves from a runtime it owns under ${T3CODE_HOME}/runtime, so that the
# app's own update can install a version and switch to it without this image
# being rebuilt. Seeding that runtime from the baked copy is what lets a fresh
# workspace start with no network.
vswarm-t3 bootstrap

# The launcher supervises t3 -- it restarts it across an update and rolls back a
# version that will not open the database -- but it exits when t3 dies for any
# other reason, expecting its own supervisor to bring it back. Under systemd
# that is Restart=; here it is this loop.
while true; do
  node "${LAUNCHER}" &
  child_pid=$!
  set +e
  wait "${child_pid}"
  status=$?
  set -e
  child_pid=""
  if [[ "${stopping}" -eq 1 ]]; then
    exit "${status}"
  fi
  # The launcher exited on its own, so bring it back; brief pause so a crash
  # loop cannot spin.
  sleep 2
done

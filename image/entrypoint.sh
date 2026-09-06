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

T3_BIN=/usr/local/bin/t3
child_pid=""
stopping=0

forward() {
  stopping=1
  if [[ -n "${child_pid}" ]]; then
    kill -TERM "${child_pid}" 2>/dev/null || true
  fi
}
trap forward TERM INT

while true; do
  "${T3_BIN}" serve \
    --mode web \
    --host 0.0.0.0 \
    --port 3773 \
    --base-dir "${T3CODE_HOME}" \
    --auto-bootstrap-project-from-cwd \
    /home/ai-agent/repos &
  child_pid=$!
  set +e
  wait "${child_pid}"
  status=$?
  set -e
  child_pid=""
  if [[ "${stopping}" -eq 1 ]]; then
    exit "${status}"
  fi
  # t3 exited on its own, so bring it back; brief pause so a crash loop
  # cannot spin.
  sleep 2
done

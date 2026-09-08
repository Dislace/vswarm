#!/usr/bin/env bash
# playwright-core resolves a browser build number from its own version, so the
# preview host can only launch what the image baked if the two pins match.
set -euo pipefail

baked=$(sed -n 's/^ARG PLAYWRIGHT_VERSION=\(.*\)$/\1/p' image/Dockerfile)
client=$(sed -n 's/.*"playwright-core": "\([^"]*\)".*/\1/p' image/preview-host/package.json)

if [[ -z "${baked}" || -z "${client}" ]]; then
  echo "could not read both pins: PLAYWRIGHT_VERSION='${baked}' playwright-core='${client}'" >&2
  exit 1
fi

if [[ "${baked}" != "${client}" ]]; then
  echo "pin mismatch: image bakes Playwright ${baked}, preview host pins playwright-core ${client}" >&2
  echo "they must move together, or the host cannot resolve the baked browser" >&2
  exit 1
fi

echo "playwright pins agree: ${baked}"

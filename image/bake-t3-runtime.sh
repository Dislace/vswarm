#!/usr/bin/env bash
# Runs once at image build. Since 0.0.41 the `t3` npm package is a thin
# installer and the release payload -- the self-contained executable, the web
# client and the native packages beside it -- ships as @t3code/t3-<platform>.
# That payload is what t3 calls a pinned runtime, and the launcher lives inside
# the executable as `t3 __service-launcher`.
#
# Give the payload a stable path, because the platform package's name varies
# with the build architecture and vswarm-t3 seeds every workspace from it.
set -euo pipefail

runtime=$(echo /usr/local/lib/node_modules/t3/node_modules/@t3code/t3-linux-*)
if [ ! -x "${runtime}/t3" ]; then
  echo "t3 ships no executable at ${runtime}/t3" >&2
  exit 1
fi

mkdir -p /opt
ln -s "${runtime}" /opt/t3-runtime
/opt/t3-runtime/t3 --version

# The launcher refuses to start against a service-state.json written for a
# protocol other than its own, so the number vswarm-t3 writes has to follow the
# pinned version rather than sit in that script as a constant. Reading it out of
# the executable is what makes the next protocol bump a version bump and nothing
# else. Finding no single number fails the build, because guessing one would
# cost every tenant its launcher.
protocol=""
while read -r number; do
  if [ -z "${protocol}" ]; then
    protocol="${number}"
  elif [ "${protocol}" != "${number}" ]; then
    echo "t3 names more than one service protocol: ${protocol} and ${number}" >&2
    exit 1
  fi
done < <(grep -aoE 'protocol !== [0-9]+' /opt/t3-runtime/t3 | grep -oE '[0-9]+$')

if [ -z "${protocol}" ]; then
  echo "t3 names no service protocol; the build cannot guess one" >&2
  exit 1
fi

printf '%s\n' "${protocol}" >/opt/t3-service-protocol
cat /opt/t3-service-protocol

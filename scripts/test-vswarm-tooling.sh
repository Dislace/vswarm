#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
updater="${repo_root}/templates/vswarm-tooling.sh.tmpl"
test_root="$(mktemp -d)"
old_claude_pid=""

cleanup() {
  if [[ -n "${old_claude_pid}" ]]; then
    kill "${old_claude_pid}" 2>/dev/null || true
    wait "${old_claude_pid}" 2>/dev/null || true
  fi
  find "${test_root}" -depth -delete
}
trap cleanup EXIT

mkdir -p \
  "${test_root}/bin" \
  "${test_root}/fake-bin" \
  "${test_root}/home" \
  "${test_root}/state/releases/claude/0.0.1/bin"

manifest="${test_root}/tools.tsv"
cat >"${manifest}" <<'EOF'
claude|npm|@anthropic-ai/claude-code|claude|2.1.0|-
codex|npm|@openai/codex|codex|3.4.0|-
go|go|go.dev|go|1.26.5|gofmt
EOF

cp /bin/sleep "${test_root}/state/releases/claude/0.0.1/bin/claude"
ln -s "${test_root}/state/releases/claude/0.0.1/bin/claude" "${test_root}/bin/claude"
"${test_root}/bin/claude" 120 &
old_claude_pid=$!

case "$(dpkg --print-architecture)" in
  amd64) go_arch=amd64 ;;
  arm64) go_arch=arm64 ;;
  *) echo "unsupported test architecture" >&2; exit 1 ;;
esac
go_fixture="${test_root}/go1.26.5.linux-${go_arch}.tar.gz"
go_tree="${test_root}/go-fixture/go"
mkdir -p "${go_tree}/bin"
cat >"${go_tree}/bin/go" <<EOF
#!/usr/bin/env bash
echo 'go version go1.26.5 linux/${go_arch}'
EOF
cat >"${go_tree}/bin/gofmt" <<'EOF'
#!/usr/bin/env bash
echo 'gofmt fixture'
EOF
chmod 0755 "${go_tree}/bin/go" "${go_tree}/bin/gofmt"
tar -C "${test_root}/go-fixture" -czf "${go_fixture}" go
go_checksum="$(sha256sum "${go_fixture}" | awk '{print $1}')"

cat >"${test_root}/fake-bin/npm" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
prefix=""
spec=""
while [[ "$#" -gt 0 ]]; do
  case "$1" in
    --prefix) prefix="$2"; shift 2 ;;
    *@*) spec="$1"; shift ;;
    *) shift ;;
  esac
done
[[ -n "${prefix}" && -n "${spec}" ]]
version="${spec##*@}"
package="${spec%@*}"
case "${package}" in
  @anthropic-ai/claude-code) binary=claude ;;
  @openai/codex) binary=codex ;;
  *) exit 1 ;;
esac
mkdir -p "${prefix}/bin"
cat >"${prefix}/bin/${binary}" <<SCRIPT
#!/usr/bin/env bash
echo '${binary} ${version}'
SCRIPT
chmod 0755 "${prefix}/bin/${binary}"
EOF
chmod 0755 "${test_root}/fake-bin/npm"

cat >"${test_root}/fake-bin/curl" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
output=""
url="${!#}"
while [[ "$#" -gt 0 ]]; do
  case "$1" in
    -o) output="$2"; shift 2 ;;
    *) shift ;;
  esac
done
case "${url}" in
  'https://go.dev/dl/?mode=json&include=all')
    printf '[{"version":"go1.26.5","stable":true,"files":[{"filename":"go1.26.5.linux-%s.tar.gz","os":"linux","arch":"%s","kind":"archive","sha256":"%s"}]}]\n' "${FAKE_GO_ARCH}" "${FAKE_GO_ARCH}" "${FAKE_GO_CHECKSUM}"
    ;;
  "https://go.dev/dl/go1.26.5.linux-${FAKE_GO_ARCH}.tar.gz")
    cp "${FAKE_GO_FIXTURE}" "${output}"
    ;;
  *)
    exit 1
    ;;
esac
EOF
chmod 0755 "${test_root}/fake-bin/curl"

export PATH="${test_root}/fake-bin:${PATH}"
export FAKE_GO_ARCH="${go_arch}"
export FAKE_GO_CHECKSUM="${go_checksum}"
export FAKE_GO_FIXTURE="${go_fixture}"
export VSWARM_TOOLING_ROOT="${test_root}/state"
export VSWARM_TOOLING_MANIFEST="${manifest}"
export VSWARM_TOOLING_BIN_DIR="${test_root}/bin"
export VSWARM_TOOLING_HOME="${test_root}/home"
ln -s "${updater}" "${test_root}/bin/vswarm-tooling"

# The reconciler takes no arguments.
if "${updater}" update all >/dev/null 2>&1; then
  echo "verbs must be gone: 'update' was accepted" >&2
  exit 1
fi
if "${updater}" status all >/dev/null 2>&1; then
  echo "verbs must be gone: 'status' was accepted" >&2
  exit 1
fi

# Converge from a stale release and missing tools.
"${updater}" >"${test_root}/converge-out"
grep -F 'codex: missing -> 3.4.0' "${test_root}/converge-out"
test "$(readlink "${test_root}/bin/claude")" = \
  "${test_root}/state/releases/claude/2.1.0/vswarm-bin/claude"
test "$(env -u DISABLE_AUTOUPDATER "${test_root}/bin/claude" --version)" = 'claude 2.1.0'
grep -q 'DISABLE_AUTOUPDATER=1' "${test_root}/state/releases/claude/2.1.0/vswarm-bin/claude"
grep -q "NPM_CONFIG_PREFIX=\"${test_root}/state/releases/codex/3.4.0\"" \
  "${test_root}/state/releases/codex/3.4.0/vswarm-bin/codex"
"${test_root}/bin/codex" --version | grep -F '3.4.0'
"${test_root}/bin/go" version | grep -F 'go1.26.5'
"${test_root}/bin/gofmt" | grep -F 'gofmt fixture'
# The stale 0.0.1 release stays while its process is alive.
test -x "${test_root}/state/releases/claude/0.0.1/bin/claude"

# Idempotent: a second run performs no version transitions (retention notices
# about in-use superseded releases are allowed).
second="$("${updater}")"
! grep -F -- ' -> ' <<<"${second}"

# A held lock makes a concurrent reconcile a silent no-op, not an error.
exec 8>"${test_root}/state/update.lock"
flock -n 8
"${updater}"
test "$(readlink "${test_root}/bin/claude")" = \
  "${test_root}/state/releases/claude/2.1.0/vswarm-bin/claude"
flock -u 8

# Undeclared binaries are never touched.
printf '#!/usr/bin/env bash\n' >"${test_root}/bin/some-undeclared"
chmod 0755 "${test_root}/bin/some-undeclared"
"${updater}" >/dev/null
test -x "${test_root}/bin/some-undeclared"

# Manifest bump: converge flips the link; the superseded in-use release is
# retained until its last process dies, then pruned on the next pass.
sed -i 's/|claude|2\.1\.0|/|claude|2.2.0|/' "${manifest}"
"${updater}" | grep -F 'claude: 2.1.0 -> 2.2.0'
test "$(readlink "${test_root}/bin/claude")" = \
  "${test_root}/state/releases/claude/2.2.0/vswarm-bin/claude"

kill "${old_claude_pid}"
wait "${old_claude_pid}" 2>/dev/null || true
old_claude_pid=""
"${updater}" >/dev/null
# Nothing but the pinned release survives once nothing in-use holds older ones;
# a rollback re-downloads by reverting the manifest.
test ! -e "${test_root}/state/releases/claude/0.0.1"
test ! -e "${test_root}/state/releases/claude/2.1.0"
test -x "${test_root}/state/releases/claude/2.2.0/bin/claude"

marker="${test_root}/manifest-executed"
# shellcheck disable=SC2016
printf 'oops|npm|pkg|tool|1.2.3|$(touch %s)\n' "${marker}" >"${test_root}/malicious.tsv"
if VSWARM_TOOLING_MANIFEST="${test_root}/malicious.tsv" "${updater}"; then
  echo "malicious manifest unexpectedly passed validation" >&2
  exit 1
fi
test ! -e "${marker}"

find "${test_root}/state/releases/go" -depth -delete
unlink "${test_root}/bin/go"
unlink "${test_root}/bin/gofmt"
export FAKE_GO_CHECKSUM=aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa
if "${updater}"; then
  echo "reconcile unexpectedly accepted a mismatched Go checksum" >&2
  exit 1
fi
test ! -e "${test_root}/bin/go"

echo "vswarm-tooling reconciliation, strict manifest, and checksum tests passed"

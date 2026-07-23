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

# A real executable models a process using an older release. The updater must
# retain its release directory until the process exits.
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
if [[ "${1:-}" == view ]]; then
  case "${2:-}" in
    @anthropic-ai/claude-code) echo 9.0.0 ;;
    @openai/codex) echo 3.4.0 ;;
    *) exit 1 ;;
  esac
  exit 0
fi
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
  'https://go.dev/dl/?mode=json')
    printf '[{"version":"go1.26.5","stable":true,"files":[]}]\n'
    ;;
  'https://go.dev/dl/?mode=json&include=all')
    printf '[{"version":"go1.26.5","stable":true,"files":[{"filename":"go1.26.5.linux.%s.tar.gz","os":"linux","arch":"%s","kind":"archive","sha256":"%s"}]}]\n' "${FAKE_GO_ARCH}" "${FAKE_GO_ARCH}" "${FAKE_GO_CHECKSUM}"
    ;;
  "https://go.dev/dl/go1.26.5.linux.${FAKE_GO_ARCH}.tar.gz")
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

"${updater}" status all >"${test_root}/missing-status"
grep -E '^claude +installed=0\.0\.1 .* stale$' "${test_root}/missing-status"
grep -E '^codex +installed=missing .* stale$' "${test_root}/missing-status"

"${updater}" update all
test "$(readlink "${test_root}/bin/claude")" = \
  "${test_root}/state/releases/claude/2.1.0/bin/claude"
test -x "${test_root}/state/releases/claude/0.0.1/bin/claude"
"${test_root}/bin/codex" --version | grep -F '3.4.0'
"${test_root}/bin/go" version | grep -F 'go1.26.5'
"${test_root}/bin/gofmt" | grep -F 'gofmt fixture'

# A concurrent reconciliation must fail immediately instead of leaving a stale
# directory lock behind.
exec 8>"${test_root}/state/update.lock"
flock -n 8
if "${updater}" update codex; then
  echo "concurrent update unexpectedly acquired the lock" >&2
  exit 1
fi
flock -u 8

"${updater}" update claude --latest
grep -qx 'claude=9.0.0' "${test_root}/home/.config/vswarm-tooling/overrides.env"
"${updater}" status claude | grep -E 'selected=9\.0\.0 .*channel=latest +ahead$'

# Normal reconciliation preserves a selected newer channel.
"${updater}" update all
"${updater}" status all >"${test_root}/selected-status"
grep -E '^claude .* installed=9\.0\.0 .* ahead$' "${test_root}/selected-status"
test "$(grep -c ' current$' "${test_root}/selected-status")" -eq 2

kill "${old_claude_pid}"
wait "${old_claude_pid}" 2>/dev/null || true
old_claude_pid=""
"${updater}" rollback claude
test "$(readlink "${test_root}/bin/claude")" = \
  "${test_root}/state/releases/claude/2.1.0/bin/claude"
test ! -s "${test_root}/home/.config/vswarm-tooling/overrides.env"
test ! -e "${test_root}/state/releases/claude/0.0.1"
test -x "${test_root}/state/releases/claude/9.0.0/bin/claude"

# A manifest is parsed as data, never sourced as shell.
marker="${test_root}/manifest-executed"
# shellcheck disable=SC2016 # the command substitution must remain literal test data
printf 'oops|npm|pkg|tool|1.2.3|$(touch %s)\n' "${marker}" >"${test_root}/malicious.tsv"
if VSWARM_TOOLING_MANIFEST="${test_root}/malicious.tsv" "${updater}" status all; then
  echo "malicious manifest unexpectedly passed validation" >&2
  exit 1
fi
test ! -e "${marker}"

# A mismatched upstream checksum fails closed without selecting the release.
find "${test_root}/state/releases/go" -depth -delete
unlink "${test_root}/bin/go"
unlink "${test_root}/bin/gofmt"
export FAKE_GO_CHECKSUM=aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa
if "${updater}" update go; then
  echo "Go update unexpectedly accepted a mismatched checksum" >&2
  exit 1
fi
test ! -e "${test_root}/bin/go"

echo "vswarm-tooling lifecycle, strict manifest, and checksum tests passed"

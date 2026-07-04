#!/usr/bin/env bash
set -euo pipefail

# Import a machine's Claude Code project memory into a VibeSwarm workspace.
#
# Claude Code stores per-project memory at ~/.claude/projects/<encoded-cwd>/memory,
# where <encoded-cwd> is the project's absolute path with "/" turned into "-".
# That path is baked into the directory name, so memory copied verbatim to a
# machine with a different home directory never loads. This remaps the home-dir
# prefix of every project so the memory attaches under the workspace's home,
# then copies it in. Repo paths *below* the home dir must match on both sides
# (mirror your checkout layout in the workspace) for a given project to resolve.

usage() {
  cat <<'EOF'
import-claude-memory.sh — move Claude Code memory into a VibeSwarm workspace

USAGE
  scripts/import-claude-memory.sh --container <name> [options]
  scripts/import-claude-memory.sh --dest <home-dir>  [options]

TARGET (one required)
  --container <name>   docker cp into a running workspace (e.g. vswarm-alenhay)
  --dest <dir>         copy into a home directory on disk (a mounted home volume,
                       e.g. config/<tenant>/home) — for operator/Ansible use

OPTIONS
  --source-home <dir>  host home whose memory is imported   (default: $HOME)
  --target-home <dir>  home the memory should load under     (default: /home/ai-agent)
  --uid <n>            chown copied files to this uid:gid    (default: 1000)
  --match <glob>       only projects whose decoded path matches (default: *)
  --all                copy whole project dirs, not just memory/ (includes session
                       transcripts — larger, may contain conversation history)
  --rewrite-paths      also rewrite the source-home prefix *inside* .md files
  --dry-run            print what would be copied, change nothing
  -h, --help           this help

EXAMPLES
  # Your laptop, into your running container (needs docker access to the host):
  scripts/import-claude-memory.sh --container vswarm-alenhay

  # Operator: seed a tenant's home volume before `vswarm up`:
  scripts/import-claude-memory.sh --dest config/alenhay/home --match '*/Dislace/*'
EOF
}

encode() { printf '%s' "$1" | sed 's:/:-:g'; }

CONTAINER="" DEST="" SOURCE_HOME="${HOME}" TARGET_HOME="/home/ai-agent"
UID_GID="1000" MATCH="*" ALL=0 REWRITE=0 DRY=0

while [ $# -gt 0 ]; do
  case "$1" in
    --container) CONTAINER="$2"; shift 2 ;;
    --dest) DEST="$2"; shift 2 ;;
    --source-home) SOURCE_HOME="$2"; shift 2 ;;
    --target-home) TARGET_HOME="$2"; shift 2 ;;
    --uid) UID_GID="$2"; shift 2 ;;
    --match) MATCH="$2"; shift 2 ;;
    --all) ALL=1; shift ;;
    --rewrite-paths) REWRITE=1; shift ;;
    --dry-run) DRY=1; shift ;;
    -h|--help) usage; exit 0 ;;
    *) echo "unknown arg: $1" >&2; usage; exit 2 ;;
  esac
done

if [ -n "$CONTAINER" ] && [ -n "$DEST" ]; then
  echo "error: pass --container OR --dest, not both" >&2; exit 2
fi
if [ -z "$CONTAINER" ] && [ -z "$DEST" ]; then
  echo "error: one of --container or --dest is required" >&2; usage; exit 2
fi

PROJECTS="${SOURCE_HOME%/}/.claude/projects"
[ -d "$PROJECTS" ] || { echo "error: no memory found at $PROJECTS" >&2; exit 1; }

SRC_PREFIX="$(encode "${SOURCE_HOME%/}")"
DST_PREFIX="$(encode "${TARGET_HOME%/}")"

STAGE="$(mktemp -d)"
trap 'rm -rf "$STAGE"' EXIT

count=0
for proj in "$PROJECTS"/*/; do
  name="$(basename "$proj")"
  case "$name" in
    "${SRC_PREFIX}"|"${SRC_PREFIX}-"*) : ;;
    *) continue ;;
  esac
  [ "$ALL" -eq 1 ] || [ -d "${proj}memory" ] || continue

  decoded="${TARGET_HOME%/}$(printf '%s' "${name#"${SRC_PREFIX}"}" | sed 's:-:/:g')"
  # shellcheck disable=SC2254
  case "$decoded" in $MATCH) : ;; *) continue ;; esac

  remapped="${DST_PREFIX}${name#"${SRC_PREFIX}"}"
  out="$STAGE/$remapped"
  mkdir -p "$out"
  if [ "$ALL" -eq 1 ]; then
    cp -a "$proj". "$out/"
  else
    cp -a "${proj}memory" "$out/"
  fi

  if [ "$REWRITE" -eq 1 ]; then
    find "$out" -name '*.md' -type f -exec \
      sed -i "s#${SOURCE_HOME%/}#${TARGET_HOME%/}#g" {} +
  fi
  echo "  $name  ->  $remapped"
  count=$((count + 1))
done

[ "$count" -gt 0 ] || { echo "no matching projects under $SRC_PREFIX" >&2; exit 1; }
echo "$count project(s) staged"

if [ "$DRY" -eq 1 ]; then echo "(dry-run — nothing written)"; exit 0; fi

if [ -n "$CONTAINER" ]; then
  docker exec -u root "$CONTAINER" mkdir -p "${TARGET_HOME%/}/.claude/projects"
  for d in "$STAGE"/*/; do
    docker cp "$d" "$CONTAINER:${TARGET_HOME%/}/.claude/projects/"
  done
  docker exec -u root "$CONTAINER" chown -R "$UID_GID:$UID_GID" "${TARGET_HOME%/}/.claude"
  echo "imported into $CONTAINER:${TARGET_HOME%/}/.claude/projects"
else
  target="${DEST%/}/.claude/projects"
  mkdir -p "$target"
  cp -a "$STAGE"/. "$target/"
  if [ "$(id -u)" -eq 0 ]; then chown -R "$UID_GID:$UID_GID" "${DEST%/}/.claude"; fi
  echo "imported into $target"
fi

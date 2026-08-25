# VibeSwarm

VibeSwarm provides isolated browser-based development workspaces for trusted teams.

## Workspace tooling

The stock workspace includes t3, Claude Code, Codex, OpenCode, Bun, the
Infisical CLI, and Go. Tool versions
are pinned in one place — the manifest at `templates/tools.tsv.tmpl` — which is
rendered into `generated/image/tools.tsv` and bind-mounted read-only into every
tenant. There is no update command: a background reconciler in each workspace
converges to the mounted manifest (on shell open and periodically), installing
side-by-side releases and flipping atomic symlinks, so CLIs update without
rebuilding the image or recreating the container. Changing a version is a
one-line PR; rollback is reverting it.

Devs who want a different version than the fleet baseline just use the
provider's own flow (`claude update`, `bun upgrade`, `npm i -g …`): user-local
installs live ahead of `/usr/local/bin` on PATH and shadow the baseline until
removed.

See [DEPLOYMENT.md](DEPLOYMENT.md#workspace-tooling) for the manifest format,
reconciliation triggers, and guarantees.

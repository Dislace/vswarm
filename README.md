# VibeSwarm

VibeSwarm provides isolated browser-based development workspaces for trusted teams.

## Workspace tooling

The stock workspace ships t3 — the server the workspace *is* — plus git, gh,
node, python and a build toolchain. Agent CLIs are not vswarm's to manage:
install what you want with the provider's own flow (`npm i -g …`,
`bun upgrade`, `claude update`). User-local installs live in the tenant's own
work volume, ahead of `/usr/local/bin` on PATH, and survive container
recreates because the home directory is a volume rather than image state.

See [DEPLOYMENT.md](DEPLOYMENT.md#workspace-tooling) for the manifest format,
reconciliation triggers, and guarantees.

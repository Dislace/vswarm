# VibeSwarm

VibeSwarm provides isolated browser-based development workspaces for trusted teams.

## Workspace tooling

The stock workspace includes Claude Code, Codex, Bun, and Go. Each CLI is
installed as a side-by-side release and selected through an atomic symlink, so
it can be updated without rebuilding the image, recreating the container, or
interrupting a CLI process that is already running:

```bash
vswarm-tooling status all
vswarm-tooling update codex
vswarm-tooling update claude --latest
vswarm-tooling rollback claude
```

Normal updates use the reviewed versions baked into the workspace manifest.
An explicit `--latest` selection is recorded in the persistent workspace home;
normal reconciliation will not silently downgrade it. T3 remains image-managed
because its running server must restart to load a new release.

See [DEPLOYMENT.md](DEPLOYMENT.md#workspace-tooling) for the manifest format,
custom tool catalogs, update guarantees, and container-recreation behavior.

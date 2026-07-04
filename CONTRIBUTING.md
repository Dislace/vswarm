# Contributing to VibeSwarm

Thanks for your interest. VibeSwarm is a small, dependency-light Go CLI plus a
set of embedded templates. Contributions that keep it that way — simple,
auditable, secure-by-default — are very welcome.

## Development setup

Requirements: **Go 1.22+**. Docker is needed only to build/run the workspace
stack, not to build the CLI.

```bash
git clone https://github.com/dislace/vswarm
cd vswarm
make build        # compiles ./vswarm  (stdlib only — no module downloads)
```

## Before you open a PR

Run the same checks CI runs:

```bash
make fmtcheck     # gofmt -l must be empty
make vet          # go vet ./...
make build        # must compile
make lint         # shellcheck + hadolint on the templates (if installed)
```

CI (`.github/workflows/ci.yml`) enforces `gofmt`, `go vet`, `go build`,
`hadolint` on `templates/Dockerfile.tmpl`, and `shellcheck` on the shell
templates and `scripts/`.

## Conventions

- **No third-party Go dependencies.** The CLI is stdlib-only by design (no
  `go.sum`, offline `go build`). If a change seems to need a dependency, open an
  issue to discuss first — there is usually a small stdlib path.
- **Comments explain *why*, not *what*.** The code favours readability over
  narration; lengthy rationale belongs in the README or a doc, not inline.
- **Commit messages:** Conventional Commits (`feat:`, `fix:`, `docs:`,
  `chore:`…), imperative subject.
- **Security-sensitive changes** (auth, the proxy config, network isolation,
  container hardening) must keep the invariants in
  [THREAT-MODEL.md](THREAT-MODEL.md) intact and be called out in the PR. When in
  doubt, run `./vswarm doctor` against a live stack.

## Reporting security issues

Do **not** open a public issue for vulnerabilities. See
[SECURITY.md](SECURITY.md).

## License

By contributing you agree your contributions are licensed under the
[MIT License](LICENSE).

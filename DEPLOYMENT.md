# Deployment (operator contract)

VibeSwarm is deployment-agnostic. This repo ships the application (workspace
image, `vswarm` CLI, proxy/tunnel templates). **Host provisioning, secret
management, and the Cloudflare Access policy live outside this repo** — for us,
in the private `Dislace/ansible` repo.

`vswarm` is built to be driven non-interactively: every command is idempotent,
flag/env-driven, and exits non-zero on failure.

## What the deployment layer must provide

| Input | How | Notes |
| --- | --- | --- |
| Host with Docker + Compose v2 | provisioning role | `vswarm` shells out to `docker` / `docker compose` |
| `tenants.yaml` | template from vault/inventory | the single source of truth; schema in `tenants.example.yaml` |
| `.env` | template from vault | must set `VSWARM_TUNNEL_TOKEN`; optional `VSWARM_REGISTRY`, `COMPOSE_PROJECT_NAME` |
| Per-tenant `config/<name>/home/.ssh` keys | write from vault | `vswarm` creates the dirs (`0700`); place keys `0600` |
| Cloudflare Tunnel | dashboard/API | route hostname → `http://vswarm-proxy:8080` |
| Cloudflare Access policy | dashboard/API | bind to the hostname; allow only `tenants.yaml` emails |

`tenants.yaml` and `.env` are **consumed, not owned** by this repo — they are
gitignored, and the deployment layer templates the real ones.

### Workspace image overlay (optional)

To bake a deployment-specific toolchain into the workspace without forking
`templates/Dockerfile.tmpl`, ship a Dockerfile alongside `tenants.yaml` and
point `image_overlay:` at it:

```dockerfile
ARG VSWARM_BASE_IMAGE
FROM ${VSWARM_BASE_IMAGE}
RUN npm install -g bun && apt-get update && apt-get install -y --no-install-recommends jq \
 && rm -rf /var/lib/apt/lists/*
```

`vswarm build` builds the stock image under `<image>-base`, then layers the
overlay on top as the final `image:` tag. The overlay file's directory is its
build context. A deployment layer driving `docker build` itself follows the
same two-step contract.

### Dev postgres sidecar (optional, per tenant)

Opt a tenant in with `services: [postgres]` in `tenants.yaml`. `services` is an
inline flow list (the tenant block has no block-list form); unknown service
names are rejected at parse time. For each opted-in tenant, `vswarm up` runs:

- a container `vswarm-db-<name>` joined **only** to that tenant's network
  (`vswarm-net-<name>`), image from the top-level `db_image:` key (default
  `timescale/timescaledb:2.28.2-pg17`), memory-capped at 1g;
- a named volume `vswarm-dbdata-<name>` for `/var/lib/postgresql/data`, so the
  database survives container recreates.

`vswarm` mints a random postgres password per tenant at render time and writes
the connection contract into the tenant home as `~/.pg.env` (mode `0600`, uid
`1000` — same delivery model as `.infisical.env`). The password **persists**:
`~/.pg.env` is the source of truth, so re-renders/re-ups never rotate it (delete
the file to force a new one). The same password is passed to the db container as
`POSTGRES_PASSWORD`. `~/.pg.env` contents:

```sh
PGHOST=vswarm-db-<name>
PGPORT=5432
PGUSER=postgres
PGPASSWORD=<minted>
PGDATABASE=postgres
```

Apps run natively in the workspace (`bun run start:dev`) against it; reset with
`dropdb && createdb && bun run migration:run`.

`vswarm doctor` gains two invariants per postgres tenant: (a) no other tenant's
workspace can open a TCP connection to this tenant's db container, and (b) the
db container is attached to exactly its own tenant network.

## Commands the deployment layer runs

```bash
vswarm build           # build the workspace image (or pull from VSWARM_REGISTRY)
vswarm up              # render + start + provision all tenant tokens (idempotent)
vswarm doctor          # gate: exits non-zero if any isolation invariant fails
```

Reconcile on change (add/remove users) by re-templating `tenants.yaml` and
running `vswarm up` again, or targeted:

```bash
vswarm tenant add <email> <name>   # adds + starts + pairs just that tenant
vswarm tenant rm <name> --purge    # removes just that tenant
```

## Outputs / exit codes

- All commands: `0` on success, non-zero on failure (safe for `changed_when`/
  `failed_when`).
- `vswarm doctor`: `0` only if every invariant PASSes — use it as a deploy gate.
- Rendered artifacts land in `generated/` (gitignored; contain per-tenant tokens
  — treat as secret).

## Token rotation

Tenant T3 tokens are minted with `token_ttl` (default `30d`). Re-run
`vswarm pair <name>` (or `vswarm up`) before expiry — schedule it in the
deployment layer (e.g. a periodic Ansible run or cron).

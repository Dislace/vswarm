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
| Per-tenant credentials | stage a tree, `vswarm provision` | the deployment layer owns the key material; `vswarm` owns the path and the modes |
| Cloudflare Tunnel | dashboard/API | route hostname → `http://vswarm-proxy:8080` |
| Cloudflare Access policy | dashboard/API | bind to the hostname; allow only `tenants.yaml` emails |

`tenants.yaml` and `.env` are **consumed, not owned** by this repo — they are
gitignored, and the deployment layer templates the real ones.

## Tenant storage

Tenant state is split by **durability class**, one named volume per class, so
that the thing you have to move is small and the thing that is large does not
have to move at all.

| Volume | Mounted at | Contents | On a move |
| --- | --- | --- | --- |
| `vswarm-work-<name>` | `/home/ai-agent` | dotfiles, shell state, checkouts, uncommitted work, session history | the only volume worth copying |
| `vswarm-cache-<name>` | `/home/ai-agent/.cache` | npm, bun, go, pip caches | drop it; it refills |
| `vswarm-dbdata-<name>` | postgres data dir | dev database (opt-in) | copy if the data matters |

The cache volume earns its keep through environment, not through more mounts:
`XDG_CACHE_HOME`, `npm_config_cache`, `BUN_INSTALL_CACHE_DIR`, `GOMODCACHE`,
`GOCACHE` and `PIP_CACHE_DIR` all point inside it. Chasing each tool's default
path with its own mount would be fragile and endless; one mount plus those
variables covers the same ground. In practice this moves the majority of a
home onto the droppable volume.

`node_modules` is the exception — it lives inside checkouts and cannot be
redirected, so it stays on the work volume. `vswarm migrate` and any backup
should exclude it explicitly.

### Putting tenant state off-host

Set `storage:` in `tenants.yaml` (see `tenants.example.yaml`). The driver and
its options apply to the **durable** volumes only — work and postgres data.
Cache volumes are pinned to `local` regardless, because a cache on a remote
filesystem is slower than no cache at all.

Nothing else in the model is path-aware. There are no host paths in the
generated compose file for tenant data, which is what makes the driver the only
thing you have to change.

> Postgres on a network filesystem is its own trap. The sidecar is a dev
> convenience; if you move durable volumes to NFS, consider leaving the
> database local or accepting that it is disposable.

### Credential delivery

`vswarm` owns the path and the modes; the deployment layer owns the material.
Stage a directory that mirrors the tenant home and hand it over:

```bash
install -d -m 0700 stage/.ssh
install -m 0600 /path/to/key stage/.ssh/vswarm-admin
install -m 0600 /path/to/infisical.env stage/.infisical.env
vswarm provision <name> --from stage
```

`provision` copies the tree into the work volume through a throwaway container,
then enforces the contract regardless of what the staging tree said: everything
delivered is owned by uid 1000, `.ssh` is `0700`, files directly under `.ssh`
are `0600`, and any `*.env` at the home root is `0600`.

It also delivers `~/.pg.env` for postgres tenants on its own — `vswarm up` runs
it for every tenant, so a fresh workspace gets its database contract with no
extra step.

The postgres password now persists at `config/<name>/pg.password` (mode `0600`)
rather than inside the tenant home. Rendering needs to read it to emit
`POSTGRES_PASSWORD`, and reaching into tenant-owned storage to do that was
never right. Delete the file to force a new password.

### Migrating from a bind-mounted home

Earlier versions bind-mounted `./config/<name>/home`. To convert:

```bash
vswarm up                  # creates the volumes with the configured driver
docker compose -f generated/docker-compose.yml stop vswarm-<name>
vswarm migrate <name>      # copies the home in, dropping rebuildable caches
docker compose -f generated/docker-compose.yml start vswarm-<name>
vswarm doctor
```

The stop comes **after** `up`, not before: `up` creates the volumes but also
starts the container, and `migrate` refuses to run against a running one. The
workspace is on an empty home between `up` and `migrate`, so keep the window
short and expect the tenant to be logged out of it.

To convert one tenant at a time instead of the whole roster, render first and
recreate only that service — the others keep running on their existing
containers:

```bash
vswarm render
docker compose -f generated/docker-compose.yml up -d vswarm-<name>
docker compose -f generated/docker-compose.yml stop vswarm-<name>
vswarm migrate <name>
docker compose -f generated/docker-compose.yml start vswarm-<name>
```

`migrate` refuses to run against a running container, lifts the postgres
password out of the old `~/.pg.env` into `config/<name>/pg.password`, and
**leaves the source directory in place** — verify the workspace before you
delete anything. `--keep-derived` copies the caches too if you would rather
not re-warm them.

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

### Workspace tooling

The stock image uses `vswarm-tooling` for Claude Code, Codex, Bun, and Go.
Approved releases are installed side by side under `/opt/vswarm-tooling` and
selected through links in `/usr/local/bin`. Updating a tool verifies its
reported version before atomically switching the link. Old release trees are
kept while a process still refers to them; otherwise the current, previous,
and approved releases are retained.

Tenant commands:

```bash
vswarm-tooling status all
vswarm-tooling update claude
vswarm-tooling update codex --latest
vswarm-tooling update all --latest
vswarm-tooling rollback codex
```

The default paths require root, so the command re-executes through the
workspace's passwordless `sudo` grant. Updates are serialized with `flock`.
Npm packages use the registry's package-integrity verification and are checked
again by executing the staged CLI. Go archives are matched against the
filename and SHA-256 published by `go.dev` before extraction.

`--latest` records only the selected version in
`~/.config/vswarm-tooling/overrides.env`; the home directory is persistent but
installed releases live in the container filesystem. After recreating a
workspace, run `vswarm-tooling update all` to restore any selected newer
releases. An operator reconciliation can safely run the same command: approved
tools remain pinned and newer tenant selections are preserved.

The root-owned manifest is `/etc/vswarm-tooling/tools.tsv`. Each non-comment
line has six pipe-delimited fields:

```text
name|provider|package/source|primary binary|approved version|extra binaries
```

Supported providers are `npm` and `go`; extra binaries are a comma-separated
list or `-`. For example, a deployment overlay can add the Infisical CLI by
copying a replacement manifest and reconciling it:

```text
claude|npm|@anthropic-ai/claude-code|claude|2.1.215|-
codex|npm|@openai/codex|codex|0.144.6|-
bun|npm|bun|bun|1.3.14|-
go|go|go.dev|go|1.26.5|gofmt
infisical|npm|@infisical/cli|infisical|0.43.110|-
```

```dockerfile
ARG VSWARM_BASE_IMAGE
FROM ${VSWARM_BASE_IMAGE}
COPY tools.tsv /etc/vswarm-tooling/tools.tsv
RUN vswarm-tooling update all
```

The manifest is parsed strictly as data and is never sourced as shell.

### Dev postgres sidecar (optional, per tenant)

Opt a tenant in with `services: [postgres]` in `tenants.yaml`. `services` is an
inline flow list (the tenant block has no block-list form); unknown service
names are rejected at parse time. For each opted-in tenant, `vswarm up` runs:

- a container `vswarm-db-<name>` joined **only** to that tenant's network
  (`vswarm-net-<name>`), image from the top-level `db_image:` key (default
  `timescale/timescaledb:2.28.2-pg17`), memory-capped at 1g;
- a named volume `vswarm-dbdata-<name>` for `/var/lib/postgresql/data`, so the
  database survives container recreates.

`vswarm` mints a random postgres password per tenant at render time, persists it
at `config/<name>/pg.password` (mode `0600`) and delivers the connection
contract into the work volume as `~/.pg.env` (mode `0600`, uid `1000`) during
`vswarm provision`, which `up` runs for you. The password **persists**:
`config/<name>/pg.password` is the source of truth, so re-renders/re-ups never
rotate it (delete the file to force a new one). The same password is passed to
the db container as `POSTGRES_PASSWORD`. `~/.pg.env` contents:

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

### Admin host SSH access (optional, per tenant)

Mark a tenant with `admin: true` in `tenants.yaml` to grant it SSH access to the
host from inside its workspace. This is **split-ownership**: vswarm carries the
flag and enforces invariants, but **vswarm NEVER touches host ssh config or
`authorized_keys`** — minting and delivering the key is the deployment layer's
job (for us, the `vswarm` Ansible role).

Contract the deployment layer implements:

- A dedicated ed25519 keypair per admin tenant (not the tenant's git key, so
  revocation is independent and the sshd audit trail is clean).
- The **private** half is delivered to the well-known path `~/.ssh/vswarm-admin`
  inside the tenant home, by staging it and running `vswarm provision <name>
  --from <dir>`. `vswarm` applies mode `0600` and uid `1000`; the deployment
  layer never touches tenant storage directly, because after the volume split
  there is no host path for it to touch.
- The **public** half goes into the host user's `authorized_keys`, source-pinned
  to the tenant's own subnet (`from="172.31.<10+index>.0/24"`), so the key is
  useless anywhere but that workspace. Revocation = flip `admin` off and
  re-apply (the `authorized_keys` line is removed).

`vswarm doctor` gains two invariants:

- **(a)** no NON-admin tenant home contains a `~/.ssh/vswarm-admin` file — a
  stranded admin key on a tenant that lost the flag fails the gate;
- **(b)** every admin tenant's `~/.ssh/vswarm-admin` exists with mode `0600`.

Both are read from **inside** the workspace with `stat`, not from a host path.
That is forced by the volume split, and it is the stricter check anyway: it
verifies what the tenant actually sees rather than what the deployment layer
believes it wrote.

Usage from inside an admin workspace (the gateway is the tenant's own bridge
gateway, `172.31.<10+index>.1`, where index is the tenant's roster position):

```sh
ssh -i ~/.ssh/vswarm-admin ubuntu@172.31.10.1
```

## Commands the deployment layer runs

```bash
vswarm build                       # build the workspace image (or pull from VSWARM_REGISTRY)
vswarm up                          # render + start + provision + pair every tenant (idempotent)
vswarm provision <name> --from DIR # deliver staged credentials into the work volume
vswarm doctor                      # gate: exits non-zero if any isolation invariant fails
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

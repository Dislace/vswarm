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
| `.env` | template from vault | must set `VSWARM_TUNNEL_TOKEN`; optional `COMPOSE_PROJECT_NAME` |
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

### Host assets the workspaces share

Anything the host manages and every workspace reads — an operator CLI, a
service catalog — goes in `mounts:` rather than into the workspace image:

```yaml
mounts:
  - /opt/vswarm/cli:/opt/vendor-cli
```

Each entry is published read-only into every workspace container. Both paths
must be absolute and canonical, targets must be unique, and none may take,
shadow or sit under a path the workspace already mounts — the tenant home, the
cache, `/run`. `vswarm doctor` re-checks every declared mount inside each
running workspace.

Sources are otherwise unconstrained and Docker resolves them on the host, so a
source is as trusted as whoever writes `tenants.yaml`; keep them inside one
published directory. Docker creates a missing source as an empty root-owned
directory rather than failing, which is what `doctor` is for.

Baking those assets into the image instead is what makes them expensive:
rebuilding the image moves its id, `vswarm up` sees a new image and recreates
every workspace container, and every session running inside dies. A mount
updates in place and recreates nothing.

### Declared repos

The split makes the work volume small; declaring repos is what makes it
*rebuildable*. List them per tenant in `tenants.yaml`:

```yaml
repo_base: "git@github.com:"     # default; bare owner/name resolves against it
tenants:
  - email: alice@example.com
    name: alice
    repos: [Acme/api, Acme/web, https://github.com/other/thing.git]
```

`vswarm provision` writes the expanded list to `~/.config/vswarm/repos`, and
the workspace gets a `vswarm-repos` command:

```bash
vswarm-repos status   # what is declared, and what is actually present
vswarm-repos sync     # clone the missing ones
```

`sync` only ever clones what is absent. It never re-clones, resets or pulls an
existing checkout — uncommitted work is precisely what the rest of this design
exists to protect, so the one operation that could destroy it is not offered.

Cloning uses the tenant's own credentials inside their workspace, so nothing
about repo access moves into the deployment layer.

### Credential delivery

`vswarm` owns the path and the modes; the deployment layer owns the material.
Stage a directory that mirrors the tenant home and hand it over:

```bash
install -d -m 0700 stage/.ssh
install -m 0600 /path/to/key stage/.ssh/vswarm-admin
vswarm provision <name> --from stage
```

`provision` copies the tree into the work volume through a throwaway container,
then enforces the contract regardless of what the staging tree said: everything
delivered is owned by uid 1000, `.ssh` is `0700`, files directly under `.ssh`
are `0600`, and any `*.env` at the home root is `0600`.

**The staging tree is the desired state.** A path it holds is delivered; a path
vswarm delivered on an earlier run and the tree no longer holds is taken back.
Retiring a credential is therefore deleting it from the staging tree — the
deployment layer never carries a standing list of files that used to exist.

What makes that safe is that vswarm removes only from its own record: each
provision writes the list of paths it delivered to `~/.config/vswarm/provisioned`
inside the work volume, and only a path in that list is ever eligible for
removal. A file the tenant created is not in the list and cannot be taken. The
removal is not recursive either, so a provisioned path the tenant has since
replaced with a directory keeps its contents. A missing or unreadable list
means nothing is removed: the failure mode is a file left behind.

`--remove <rel-path>` still exists for whole trees vswarm never delivered — the
one-time cleanup of anything provisioned before the list existed. It is not for
standing use; a caller reaching for it repeatedly wants the staging tree instead.

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

### Installing a release

Tagging `vX.Y.Z` publishes both artifacts, and a deployment consumes them the
way it consumes any other pinned upstream — a version, a URL, a checksum:

| Artifact | Where | Pin with |
| --- | --- | --- |
| `vswarm-linux-amd64`, `vswarm-linux-arm64` | GitHub release `vX.Y.Z` | the release's `SHA256SUMS` asset |
| `ghcr.io/dislace/vswarm-workspace:vX.Y.Z` (linux/arm64) | GHCR | the digest in the release's `workspace-image.txt` asset |

`vswarm version` prints the tag the binary was built from, so a role can check
what is installed before fetching anything.

### The workspace image

The image is an **input**, not something a deployment produces. `image/` in
this repo is a plain committed build context; CI builds it and publishes
`ghcr.io/dislace/vswarm-workspace:<tag>`, and a host names that tag in
`image:`. `image:` is required — there is no default, because a host that
names none would run whatever the local daemon last tagged.

`vswarm render` does not write a build context and `vswarm build` only works
from a vswarm checkout, where it builds `./image` and tags it `image:`. A
deployment layer never calls `build`; it pulls.

To bake a deployment-specific toolchain in, build your own image `FROM` the
published one and put your tag in `image:`. There is no overlay mechanism to
learn: the config key already names any image you like.

### Workspace tooling

The image ships t3 and the base toolchain (git, gh, node, python3, build
essentials, uv, vim). It does **not** manage agent CLIs. Install them the
provider's own way from inside the workspace:

```bash
npm i -g @anthropic-ai/claude-code @openai/codex
```

`NPM_CONFIG_PREFIX` points at `~/.local`, which is on the tenant's work volume
and ahead of `/usr/local/bin` on PATH, so a hand-installed CLI persists across
container recreates and image bumps. Nothing in vswarm reconciles, prunes or
version-checks these; the operator owns them.

t3 itself is pinned in `image/Dockerfile` (`ARG T3_VERSION`, Renovate-tracked)
because the workspace cannot serve without it. Moving it is a new image.

### Dev postgres sidecar (optional, per tenant)

Opt a tenant in with `services: [postgres]` in `tenants.yaml`. `services` is an
inline flow list (the tenant block has no block-list form); unknown service
names are rejected at parse time. For each opted-in tenant, `vswarm up` runs:

- a container `vswarm-db-<name>` joined **only** to that tenant's network
  (`vswarm-net-<name>`), image from the top-level `db_image:` key (default
  `postgres:18.4`), memory-capped at 1g;
- a named volume `vswarm-dbdata-<name>` mounted at `/var/lib/postgresql`, so the
  database survives container recreates. PostgreSQL 18+ images keep their data
  cluster under `<major>/docker` inside that mount; `db_image:` overrides must
  be PostgreSQL 18+ compatible with this layout.

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

### Playwright sidecar (optional, per tenant)

Opt a tenant in with `services: [playwright]`. For each opted-in tenant,
`vswarm up` runs a stateless Chromium sidecar `vswarm-playwright-<name>` on the
tenant's network only (image from `playwright_image:`, default
`zenika/alpine-chrome:124`), exposing Chrome DevTools on port 9222. The
connection contract is delivered as `~/.playwright.env`:

```sh
CHROMIUM_CDP_URL=http://vswarm-playwright-<name>:9222
```

Use it with `playwright-core` (`npm i playwright-core` — no browser download)
via `chromium.connectOverCDP(process.env.CHROMIUM_CDP_URL)`. The sidecar is
stateless: pages live as long as the connection; keep long-lived browser state
in the workspace instead.

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
  to the tenant's own subnet (`from="172.31.<net_id>.0/24"`), so the key is
  useless anywhere but that workspace. Revocation = flip `admin` off and
  re-apply (the `authorized_keys` line is removed).

  Declare `net_id` per tenant in `tenants.yaml` so the octet is stated once and
  both sides read it. It is optional: a tenant without one keeps the old
  `10 + roster position`. Prefer declaring it, because with position the octet
  moves when a tenant earlier in the roster is removed, and the source pin then
  authorizes that key on a different tenant's subnet. vswarm refuses a `net_id`
  outside 10-254 or shared by two tenants.

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
vswarm up --json                    # render + start + provision + pair (idempotent)
vswarm provision <name> --from DIR  # make the work volume match the staging tree
vswarm doctor --wait=60s            # gate: non-zero if any isolation invariant fails
```

`vswarm build` is not one of these. A deployment pulls the published image.

## Outputs / exit codes

- All commands: `0` on success, non-zero on failure (safe for `changed_when`/
  `failed_when`).
- `vswarm doctor`: `0` only if every invariant PASSes — use it as a deploy gate.
  `--wait=<duration>` re-runs the whole set until it passes or the deadline
  expires, so the caller does not need a retry loop around it. Without it,
  doctor makes one pass, as before.
- `vswarm up --json` and `vswarm status --json` write a JSON document to
  **stdout** and move progress prose to stderr. `up` reports one entry per
  declared container:

```json
{
  "changed": true,
  "containers": [
    {"container": "vswarm-proxy", "action": "unchanged", "id": "…"},
    {"container": "vswarm-alice", "action": "recreated", "id": "…"},
    {"container": "vswarm-db-alice", "action": "created", "id": "…"}
  ]
}
```

  `action` is `created`, `recreated`, `unchanged` or `absent`, and `changed` is
  true for anything that is not `unchanged`. Use it directly for
  `changed_when`; capturing container ids before and after `up` and diffing
  them is what this replaces.
- Rendered artifacts land in `generated/` (gitignored; contain per-tenant tokens
  — treat as secret).

Reconcile on change (add/remove users) by re-templating `tenants.yaml` and
running `vswarm up` again, or targeted:

```bash
vswarm tenant add <email> <name>   # adds + starts + pairs just that tenant
vswarm tenant rm <name> --purge    # removes just that tenant
```

## Token rotation

Tenant T3 tokens are minted with `token_ttl` (default `30d`). Re-run
`vswarm pair <name>` (or `vswarm up`) before expiry — schedule it in the
deployment layer (e.g. a periodic Ansible run or cron).

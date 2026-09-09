# Threat model

VibeSwarm runs **untrusted, LLM-generated code** inside each tenant's container.
Assume a tenant container is hostile. The design limits what a compromised
tenant, or an unauthenticated attacker, can reach.

## Trust boundaries

1. **Cloudflare Access (edge)** — the real gatekeeper. It authenticates identity
   (GitHub OAuth or your IdP), enforces the allow-policy, and injects
   `Cf-Access-Authenticated-User-Email`. Nothing reaches the origin without
   passing it, with one deliberate exception: `OPTIONS` is configured to bypass
   Access (see [DEPLOYMENT.md](DEPLOYMENT.md#cors-preflights)), because a CORS
   preflight carries no credentials to authenticate and rejecting it locks out
   every browser-based client. A preflight is a question about what a later
   request would be allowed to do; it carries no body and returns none, and the
   request it precedes is authenticated normally.
2. **Cloudflare Tunnel (`cloudflared`)** — the *only* ingress to the origin. No
   host ports are published; the stack is not reachable from the public internet
   except through the Access-authenticated tunnel.
3. **angie proxy** — routes by the Access identity to that user's container and
   injects that user's T3 token. Fails closed (`403`) on unknown identity. It
   answers CORS preflights itself, before that gate, since a preflight has no
   identity to route on; the answer echoes what the preflight asked for and grants
   nothing, because the request that follows still arrives at the gate.
4. **T3 token (per tenant)** — a lock T3 puts on itself when network-reachable.
   VibeSwarm satisfies it on the user's behalf; it is not the primary identity
   control.

## Isolation invariants (verified by `vswarm doctor`)

- **Proxy unreachable by tenants.** angie is multi-homed onto every tenant
  network but binds its listener only to the edge-network address. A hostile
  tenant container therefore has no proxy port to connect to and cannot forge a
  routing header to reach another user's box.
- **Tenants cannot reach each other.** Each tenant is on its own Docker network
  shared only with the proxy.
- **No published host ports.** The tunnel is the sole ingress.
- **Container hardening.** Workspaces run as the non-root `ai-agent` user by
  default, each on its own network, with `pids`, `ulimits`, and CPU/memory
  limits.
- **SSH key perms.** Per-tenant `.ssh` is `0700`.
- **Declared mounts are read-only and off tenant state.** A `mounts:` entry is
  rendered `:ro` and rejected if its target takes, shadows, or sits under a path
  the workspace already occupies — the tenant home, the cache, the baked
  browsers, `/run`. `doctor` re-checks each one in the running container.

### Workspace privilege posture (dev-env default)

The default image ships `sudo` (passwordless for `ai-agent`) and a writable root
filesystem so the workspace behaves like a normal dev box (`apt install`, edit
`/etc`). This deliberately trades the stricter `cap_drop: ALL` +
`no-new-privileges` + read-only-rootfs posture for ergonomics, and assumes the
**tenants themselves are trusted** (they can already root their own container via
the agents they run). It does **not** weaken the boundaries that matter between
users: per-tenant network isolation, the unreachable-by-tenants proxy, resource
limits, and the Access identity gate all still hold — a tenant rooting its own
container cannot reach another tenant or the proxy.

If you are running genuinely hostile tenants, re-add to each workspace service in
`templates/docker-compose.yml.tmpl`: `read_only: true` (+ `tmpfs: [/tmp, /run]`),
`cap_drop: [ALL]`, `security_opt: [no-new-privileges:true]`, and drop `sudo` from
`image/Dockerfile`. The isolation invariants above are independent of
this choice.

Workspace CLIs are installed by hand, by the operator or the tenant, through
the workspace's own package managers. They are not a security boundary and
vswarm makes no claim about them: an npm install script runs with whatever
authority the tenant already holds inside their own container, including the
`sudo` grant. Remove `sudo` for hostile-tenant deployments.

## The header-trust assumption (and how to remove it)

v1 routes on the `Cf-Access-Authenticated-User-Email` header. This is trustworthy
**only because** the origin is reachable exclusively via the Access-authenticated
tunnel and the proxy is unreachable by tenants (above). If you cannot guarantee
that topology, enable cryptographic verification of the Access JWT:
`templates/njs/access-jwt.js` verifies the signed assertion (JWKS + `aud` + `iss`
+ `exp`) so identity no longer depends on network layout. Recommended for
production.

To enable it: set `TEAM_DOMAIN` and `AUD` in `templates/njs/access-jwt.js`, mount
it into the proxy, `js_import` it in `angie.conf`, gate `location /` with an
`auth_request` to the verifier, and route on `$vswarm_verified_email` instead of
the raw header. Requires an Angie build with njs + `ngx.fetch`.

## Known limitations (v1)

- **Open egress.** Tenant containers can reach the internet (needed for git,
  npm, agent APIs) — LLM-generated code could exfiltrate data. An egress
  allowlist is future work.
- **Shared host kernel.** Containers are not VMs; a kernel-level escape crosses
  the boundary. Run on a dedicated host; consider `userns-remap` and, for higher
  assurance, a VM/microVM per tenant.
- **Declared mounts are operator-trusted host paths.** `mounts:` sources are
  validated as canonical absolute paths but are not otherwise constrained, and
  symlinks are resolved by Docker on the host: an operator who points one at `/`
  or at the Docker socket publishes it read-only into every workspace. The same
  bytes are visible to every tenant, so a mount is a shared read channel, not a
  per-tenant one. Keep sources to a dedicated published directory.
- **T3 token scope.** v1 injects a session token with broad scopes. Scoping it
  down to client-only capabilities is planned. The blast radius is at least
  bounded in count: `pair` reconciles each tenant to exactly one vswarm-owned
  session and revokes the rest, so a broad token is one credential per tenant
  rather than one per deploy.
- **Preview automation reaches the viewer's browser, not the workspace.** t3
  routes an agent's `preview_*` calls to whichever desktop client is focused and
  runs them against that client's own webview — the browser on the operator's
  laptop, with the operator's cookies and session. This is upstream's design and
  it is what makes the agent and the person share one page, but it means the
  workspace boundary does not contain preview automation: `preview_evaluate` is
  arbitrary JavaScript on the machine holding the tab. The workspace's own
  preview host only answers when no client is focused. Treat an agent's preview
  access as equivalent to handing it the browser you are watching it in.

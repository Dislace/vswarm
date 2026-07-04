# Threat model

VibeSwarm runs **untrusted, LLM-generated code** inside each tenant's container.
Assume a tenant container is hostile. The design limits what a compromised
tenant, or an unauthenticated attacker, can reach.

## Trust boundaries

1. **Cloudflare Access (edge)** — the real gatekeeper. It authenticates identity
   (GitHub OAuth or your IdP), enforces the allow-policy, and injects
   `Cf-Access-Authenticated-User-Email`. Nothing reaches the origin without
   passing it.
2. **Cloudflare Tunnel (`cloudflared`)** — the *only* ingress to the origin. No
   host ports are published; the stack is not reachable from the public internet
   except through the Access-authenticated tunnel.
3. **angie proxy** — routes by the Access identity to that user's container and
   injects that user's T3 token. Fails closed (`403`) on unknown identity.
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
`templates/Dockerfile.tmpl`. The isolation invariants above are independent of
this choice.

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
- **T3 token scope.** v1 injects a session token with broad scopes. Scoping it
  down to client-only capabilities is planned.

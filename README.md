# VibeSwarm

Multi-tenant [T3 Code](https://t3.codes) behind a single URL, gated by Cloudflare Access.

Each user opens one hostname, logs in once with Cloudflare Access (e.g. GitHub OAuth), and their own T3 Code web workspace — editor, terminal, and coding agents — just opens. No pairing prompt, no Tailscale, no per-user URLs. Every user gets an isolated container; the `vswarm` CLI wires up routing and auth.

```
you.example.com
  → Cloudflare Access (the only login you see)
  → cloudflared (Cloudflare Tunnel; sole ingress)
  → angie proxy   route by identity → your container, inject your T3 token
  → vswarm-<you>:3773   t3 serve --mode web   (self-serves UI + API + WebSocket)
```

## How it works

- **T3 Code** (`t3 serve --mode web`) self-serves its full web UI same-origin, so one hostname behind Access is enough — no external client.
- Bound to a network, T3 locks itself (auth policy `remote-reachable`) and wants a one-time pairing token. That lock is about "is this random internet or an allowed client" — **not** which user.
- So VibeSwarm does the real gatekeeping at the edge with **Cloudflare Access** (identity + which container), and the **angie** proxy injects each tenant's T3 session token (`Authorization: Bearer`) so T3's lock opens silently. The user only ever sees the Access login.
- Routing key is the `Cf-Access-Authenticated-User-Email` header Access sets. Unknown identity → `403` (fail closed).

## Quick start

```bash
make build                 # compile the vswarm binary (needs Go 1.22+)
./vswarm init              # scaffold tenants.yaml, .env, config/
# edit tenants.yaml (domain + users) and .env (VSWARM_TUNNEL_TOKEN)
./vswarm build             # build the workspace image
./vswarm up                # start the stack + provision every tenant
./vswarm doctor            # verify isolation + config invariants
```

Add or remove users without touching anyone else:

```bash
./vswarm tenant add alex@example.com alex
./vswarm tenant rm alex --purge
./vswarm tenant ls
```

## Cloudflare setup (one time)

1. **Tunnel** — create a Cloudflare Tunnel, put its token in `.env` as `VSWARM_TUNNEL_TOKEN`. In the Zero Trust dashboard, point the public hostname (`you.example.com`) at `http://vswarm-proxy:8080`.
2. **Access** — add an Access application on that hostname. Identity provider: GitHub (or your IdP). Policy: allow only the emails in `tenants.yaml`. This is what authenticates users and injects `Cf-Access-Authenticated-User-Email`.

That's it — Access + the tunnel are the trust boundary; angie routes and injects tokens behind it.

## Commands

| command | does |
| --- | --- |
| `vswarm init` | scaffold `tenants.yaml`, `.env`, `config/` (idempotent) |
| `vswarm render` | `tenants.yaml` → `generated/` (compose + angie + image files) |
| `vswarm build` | build the workspace image |
| `vswarm up` / `down` | start / stop the whole stack; `up` also provisions tokens |
| `vswarm tenant add/rm/ls` | manage users (one line in `tenants.yaml`) |
| `vswarm pair <name>` | (re)mint a tenant's T3 token and inject it into angie |
| `vswarm status` / `logs` | inspect running stack |
| `vswarm doctor` | verify the security invariants machine-side |

## Customizing the workspace

The workspace image installs `t3`, `@anthropic-ai/claude-code`, and
`@openai/codex`, plus the GitHub CLI (`gh`) — each user runs `gh auth login`
once and the credentials persist on their home volume. To offer more agents
(OpenCode, Cursor), add them to the `npm install -g` line in
`templates/Dockerfile.tmpl` and `vswarm build`.

The image is a working dev box: `git`, `ripgrep`, `vim`, build tools, and
passwordless `sudo` on a writable root filesystem. That relaxes the strict
container hardening in favour of ergonomics and assumes tenants are trusted — see
[THREAT-MODEL.md](THREAT-MODEL.md#workspace-privilege-posture-dev-env-default)
for the trade-off and how to re-harden for hostile tenants.

## Security

These containers run untrusted, LLM-generated code — treat them as hostile. VibeSwarm isolates each tenant on its own Docker network, binds the proxy so tenants can't reach it, hardens the workspace containers, and fails closed on unknown identity. Read **[THREAT-MODEL.md](THREAT-MODEL.md)** before exposing this to real users, and **[SECURITY.md](SECURITY.md)** to report issues.

## Deployment

This repo is the application (image, CLI, proxy/tunnel templates). Provisioning the host, sourcing secrets, and creating the Cloudflare Access policy are handled separately (for us, in a private Ansible repo). See **[DEPLOYMENT.md](DEPLOYMENT.md)** for the operator contract.

## License

[MIT](LICENSE).

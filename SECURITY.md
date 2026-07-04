# Security policy

VibeSwarm exposes developer workspaces that run untrusted code. We take reports
seriously.

## Reporting a vulnerability

Please **do not** open a public issue for security problems. Instead, report
privately via GitHub Security Advisories ("Report a vulnerability" on the
Security tab) or email **contact@dislace.com**.

Include: what you found, how to reproduce it, and the impact. We aim to
acknowledge within 72 hours and to coordinate a fix and disclosure timeline with
you.

## Scope

In scope: identity/routing bypass, tenant isolation escape, token leakage,
proxy misconfiguration, container hardening gaps.

Out of scope (documented limitations — see [THREAT-MODEL.md](THREAT-MODEL.md)):
open tenant egress, shared-kernel container boundary, broad T3 token scope in v1.

## Hardening checklist for operators

- Enable Cloudflare Access on the hostname with a strict allow-policy.
- Keep the origin reachable **only** via the tunnel (no published ports).
- Run `vswarm doctor` after every change; treat any `FAIL` as blocking.
- Enable JWT verification (`templates/njs/access-jwt.js`) for production.
- Run on a dedicated host; consider Docker `userns-remap`.

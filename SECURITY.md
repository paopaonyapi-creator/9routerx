# Security Policy

9RouterX is an AI gateway: it terminates client traffic, holds provider
credentials and OAuth tokens in SQLite, and exposes a dashboard plus an HTTP
API on a known port. Vulnerabilities in auth, the proxy surface, the dashboard
API, or the updater are treated as security issues.

## Reporting a vulnerability

**Please do not open a public GitHub issue for security reports.**

1. **Preferred:** open a private vulnerability advisory on GitHub —
   <https://github.com/yanstd888/9routerx/security/advisories/new>. Only the
   maintainer sees the report until you choose to publish it.
2. **Email:** `awaia89jq@gmail.com` — 
   maintainer contact for the public
   release. Include `[security]` in the subject.

Please include, when possible:

- Affected version (`9routerx version`) and platform.
- A minimal reproduction (curl command, config, or dashboard flow).
- Impact assessment — what an attacker gains (auth bypass, credential
  extraction, SSRF, RCE, …).
- Whether the issue affects the default configuration (`PORT=20128`, local
  bind, default password flow) or only non-default setups.

What we ask you **not** to do: test against deployments you do not own, access
or modify other users' data, or exploit an issue beyond the minimum needed to
demonstrate it.

## Supported versions

| Version | Supported | Notes |
| --- | --- | --- |
| 1.0.0 (current `main`) | Yes | Security fixes land on `main` first and ship in the next release |
| 9router-go / pre-fork releases (≤ 1.9.15 legacy line) | No | Upstream fork history, archived in `docs/changelog/9router-go-legacy.md` |

Experimental/pre-release builds (`-exp.*`, Docker `:exp`) are supported on a
best-effort basis — report against them only if the issue does not exist on
the latest stable release.

## Response timeline

- **Acknowledgment:** within 72 hours of the report.
- **Triage & severity assignment:** within 7 days (CVSS-like severity:
  Critical / High / Medium / Low).
- **Fix target:** Critical/High within 30 days; Medium/Low within 90 days or
  the next scheduled release, whichever is sooner.
- **Coordination:** the reporter is kept informed via the advisory thread and
  is credited in the release notes (unless anonymity is requested).
- **Disclosure:** advisory published once a patched release is available —
  typically at most 90 days after the fix ships.

## Security-relevant defaults (quick reference)

- Dashboard login: `INITIAL_PASSWORD` or the local-only `123456` fallback —
  remote access **refuses** the default password (fresh-install takeover
  guard, CVE-2026-56679).
- Login rate limiting: 5 failed attempts lock the source; forwarded client-IP
  headers are only trusted from `TRUST_PROXY` / `TRUSTED_PROXIES` ranges (or a
  `9ROUTERX_PEER_TOKEN`-presenting front proxy).
- Provider URLs on loopback/private ranges are rejected unless
  `ALLOW_PRIVATE_PROVIDER_URLS` is set; web-session executors are gated behind
  `ENABLE_WEB_EXECUTORS=true` (off by default).
- The updater only fetches from `https://raw.githubusercontent.com/yanstd888/9routerx/main/version.json`
  and GitHub Releases for `yanstd888/9routerx` — report any other URL in a
  running binary as suspicious.

Full hardening guidance: [`README.md`](README.md#-dashboard-login--fresh-install)
(auth and remote access) and [`ARCHITECTURE.md`](ARCHITECTURE.md) (proxy
internals).

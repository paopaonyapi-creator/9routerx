---
title: Operations
order: 15
section: Contributing
---

# Operations

Everything an operator does after the first install: containers, tunnels, updates,
maintenance, and the background loops that keep a deployment healthy.

## Docker

```bash
docker run -d --name 9routerx --restart unless-stopped \
  -p 888:888 -v "$HOME/.9router:/data" \
  -e PORT=888 -e DATA_DIR=/data \
  -e INITIAL_PASSWORD=your-secure-password \
  yanstd888/9routerx:latest
```

`docker-compose.yml` in the repository root is the same shape if you prefer
compose.

Experimental builds are marked **Pre-release** and never offered to a normal
install; Docker Hub tags them `exp` (`yanstd888/9routerx:exp`), never `latest`.

## Tunnels

The dashboard can expose the gateway through a tunnel, which is how you reach it
without opening a port:

```text
GET  /api/tunnel/status
POST /api/tunnel/enable
POST /api/tunnel/disable
GET  /api/tunnel/tailscale-check
POST /api/tunnel/tailscale-enable
POST /api/tunnel/tailscale-disable
```

Tailscale in a container needs the `NET_ADMIN` capability and a TUN device — see
`docker-compose.yml` for the flags.

## Self-update

```bash
9routerx update        # check and install
9routerx version       # current version plus an update check
```

Or from the dashboard: `POST /api/version/update` and
`POST /api/version/auto-update` (admin). `AUTO_UPDATE=true` turns the background
checker on. `releases/latest` always points at the stable version, so an
auto-update never installs an experimental build over a stable one.

### Update banner and channels

When a newer release exists, the dashboard shows an update banner
(`web/src/components/UpdateBanner.svelte`). What the banner offers depends on
how this build was installed — `updater.DetectDistChannel()` classifies it:

| Channel | Detected when | Banner offers |
|---|---|---|
| `binary` | a packaged release binary | "Update now" link to the release (only when hosted on the trusted release host), plus the per-OS asset name and sha256 when present. `9routerx update` remains the install path |
| `npm` | installed through the npm package (`9ROUTERX_NPM_INSTALL=1`) | the `npm i -g 9routerx@latest` command with a copy button |
| `dev` | built from source with no version metadata | info line and release link only |

"Skip this version" (binary and npm installs) stores the version in the browser
and stops the banner for it. A check that fails — 404, timeout, malformed
manifest, rate limit — degrades silently: the status endpoint answers with the
last cached result and `stale: true`, and the cause is classified into the
structured log and the check record. The full contract, including the pinned
manifest host, lives in
[`docs/updater-contract.md`](https://github.com/yanstd888/9routerx/blob/main/docs/updater-contract.md).

## Database maintenance

All behind a dashboard session or CLI token:

```text
GET  /api/settings/database          Export
POST /api/settings/database          Import
GET  /api/settings/database/stats
POST /api/settings/database/prune           Prune old rows
POST /api/settings/database/prune-usage     Prune usage history
POST /api/settings/database/vacuum          VACUUM
POST /api/settings/proxy-test               Test the outbound proxy
POST /api/settings/logo                     White-label logo upload
POST /api/settings/currency/refresh         Refresh exchange rates
```

Usage-history pruning keeps a **carryover** per dimension, so spend that has
already been counted is not billed twice after the rows it came from are pruned.
The invariant is tested: node spend is identical before and after a prune, and a
quota reset is not re-banked.

## Background loops

These run inside the gateway and need no external cron:

| Loop | What it does |
|---|---|
| Gemini Web session renewal | Rotates the `__Secure-1PSIDTS` cookie so a signed-in account keeps reaching paid models |
| Background catalog sync | Refreshes the model catalog from upstream |
| Auto-ping scheduler | Pings providers through the real dispatch path on a schedule, keeping OAuth fresh and quota current |
| Model health loop | Probes models and disables the ones that stop answering |
| Usage retention loop | Prunes old usage rows and folds the carryover |

The auto-ping scheduler deliberately sends its pings through the *same* handler
instance the data plane uses, so a ping resolves credentials, refreshes OAuth, and
picks an executor the way real traffic does.

## Headroom

`/headroom/*` manages the token-compression proxy that sits in front of a CLI tool:

```text
POST /headroom/start | stop | restart
GET  /headroom/status
GET  /headroom/extras
POST /headroom/extras
DELETE /headroom/extras
GET  /headroom/proxy
```

## Metrics

`GET /api/metrics` serves Prometheus. Provider-scoped rate-limit rejections carry
`scope="provider"` and key-scoped ones `scope="api_key"`, so a dashboard alert can
tell "this endpoint is saturated" from "this caller is abusive".

## Observability

- `GET /debug/traces` — p50/p95 latency per provider+model
- `GET /api/translator/console-logs/stream` — live log with a level filter
- `PPROF_ENABLED=true` — exposes `/debug/pprof/*` behind admin auth. Keep it off
  on untrusted networks.

## Server timeouts

The listener's read-header, idle, and max-header-byte limits are compiled in
rather than configurable. Tune the proxy in front of the gateway instead.

## Further reading

- [`ARCHITECTURE.md`](https://github.com/yanstd888/9routerx/blob/main/ARCHITECTURE.md)
- [`DATABASE.md`](https://github.com/yanstd888/9routerx/blob/main/DATABASE.md)

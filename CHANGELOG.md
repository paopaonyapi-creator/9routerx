# Changelog

All notable changes to **9RouterX** are documented in this file. The format
follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/) and this
project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

Release history up to the fork point is archived in the upstream repo's
[CHANGELOG.md](https://github.com/luqman-v1/9router-go/blob/main/CHANGELOG.md).

## [Unreleased]

### Changed

- Release sections carry the `v` prefix (`## [v1.0.0]`), matching what the
  release workflow's notes extraction and the dashboard changelog both expect;
  the pending-fragment splice now lands under this Unreleased heading instead
  of above the file title.

## [v1.0.0] - 2026-11-06

Forked from 9router-go (luqman-v1), upstream decolua/9router — attribution and thanks.

### Added

- **Per-key governance** — daily cost limit, request limit, IP whitelist, per-key system prompt, endpoint permissions, and policy templates applied across multiple keys (serenhope columns).
- **Token saver suite** — RTK input compression plus anti-slop injector, context squeezer, tool-call fallback, JSON guard, and Responses compact verification.
- **Output styles** — Caveman and Ponytail directive modes for compressed output.
- **Security event log** — per-event security log with alerting, TLS stealth hardening, and audit log v2.
- **Notifications** — notification feed, Telegram release notifications, and multi-target webhooks.
- **i18n** — Indonesian/English dashboard localization.
- **Free-tier catalog** — free-tier provider catalog entries with usage/cost tracking, plus custom credits tracking.
- **Routing strategies** — cache-optimized, auto, quota-weighted, and reset-aware combo routing strategies.
- **3-tier circuit breaker** — strike-breaker quota handling across subscription, cheap, and free tiers.
- **GCF compression** — pre-compressed `.br`/`.gz` dashboard asset serving.
- **Teams billing** — multi-seat/team billing support.
- **MCP server and A2A** — MCP (Model Context Protocol) server endpoint and agent-to-agent endpoints.
- **Config bundle** — import/export of configuration bundles.
- **Web executors** — ChatGPT web executor and browser-based provider executors.
- **Dashboard phase 4** — API key metrics, search/filter, bulk create, success rate and loop detection.
- **9RouterX rebranding** — full rebrand across README, wiki, skills, and docs; binary `9routerx` (`9routerx-tray` for the tray companion); module path `9routerx/proxy`; repository `github.com/yanstd888/9routerx`; wiki `yanstd888.github.io/9routerx`.

### Changed

- **BREAKING**: environment variables are renamed from `NINEROUTER_*` / `9ROUTER_*` to the `9ROUTERX_*` prefix (e.g. `9ROUTERX_URL`, `9ROUTERX_KEY`). Migration: rename the variables in your `.env`, shell profile, and deployment manifests; legacy spellings are still accepted for one release via the `internal/envcompat` fallback with a deprecation warning, and are dropped afterwards.
- **BREAKING**: product name `9router-go` becomes **9RouterX** — repository links now point at `github.com/yanstd888/9routerx`, the binary and CLI commands are `9routerx`, the module path is `9routerx/proxy`, and installers are fetched from `raw.githubusercontent.com/yanstd888/9routerx`.
- **BREAKING**: default gateway port `20130` → `888`. Migration: set `PORT=20130` in your `.env` to keep the old port.

[1.0.0]: https://github.com/yanstd888/9routerx/releases/tag/v1.0.0

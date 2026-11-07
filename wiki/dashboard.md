---
title: Dashboard
order: 5
section: Using the Gateway
---

# Dashboard

The dashboard is native Svelte 5, embedded in the binary — no separate Node.js
process at runtime. Every page below is a real sidebar entry.

## Sidebar entries

The sidebar is organised into three groups: the main entries, an
**Advanced Features** group, and a **System** group. Every entry can be hidden or
reordered from Settings; a fresh install hides only **Plans** and **Persona**.

| Entry | What it does |
|---|---|
| Endpoint & Key | Where to point your CLI tool, and the API keys it uses |
| Providers | Connect providers, browse models, manage custom nodes |
| Combo & Vision Adapter | Fallback chains, round-robin, sticky, fusion, capacity |
| Usage | Live usage, history, per-request breakdown |
| Quota Tracker | Per-provider quota and reset schedules |
| Token Saver | RTK, Caveman, Ponytail, ADHD, and the test benches |
| CLI Tools | Which installed CLI tools the gateway has written config for |
| Security | Credential vault, guardrails, and the enforcement panel |
| Security Log | The per-event security log the gateway records on |
| Audit Log | Compliance trail of dashboard actions (logins, key changes, updates) |
| Persona | Agent prompt presets, per-model binding *(hidden by default)* |
| Free Tier | Free-tier provider catalog with usage and cost tracking |
| Web Executors | Browser-based provider executors and their session health |
| Webhooks | Multi-target push notifications with per-target test pings |
| Plans | Reusable policy templates *(hidden by default)* |
| Media Providers | Accordion: Embedding, Text to Image, Text to Speech, Speech to Text, Video, System One, Web Fetch & Search |
| Proxy Pools | Bind a proxy to each API key connection |
| Skills | Operator skill blocks per scope |
| Console Log | Live log with level filters and search |
| Settings | Profile, display, savers, branding, sidebar layout |

## Usage views

The Usage page switches between several views of the same ledger
(`web/src/components/analytics/UsageDashboard.svelte`):

| View | Component |
|---|---|
| Summary KPIs | `SummaryKpiCards.svelte` |
| Chart | `UsageChart.svelte` |
| Breakdown table | `UsageBreakdownTable.svelte` |
| Provider topology | `ProviderTopologyCard.svelte` |
| Activity heatmap | `ActivityHeatmap.svelte` |
| Combo flow | `ComboFlowCard.svelte` |
| Node health | `NodeHealthTable.svelte` |
| Request details | `RequestDetailsTab.svelte` |

## Sidebar layout is yours

The sidebar is not fixed. Three controls, all in Settings:

- **Per-entry visibility** — show or hide any row without touching code
  (`web/src/lib/navVisibility.svelte.ts`). Settings itself can never be hidden.
- **Arrangement** — reorder rows inside their container, up/down or drag-and-drop
  (`web/src/lib/orderList.ts`). A drop on a row from another container is
  refused rather than silently misplaced.
- **Presets** — one click fills the hide list:

| Preset | Hides |
|---|---|
| `default` | Plans, Persona |
| `advanced` | nothing |
| `simple` | media surfaces, CLI Tools, Skills, Console Log, Plans |
| `super-lite` | everything except Endpoint & Key, Providers, Combos, Usage, Settings, Security |

A preset writes the hide list only. The arrangement stays where you put it,
because "what is on the sidebar" and "in which order" are different questions.

## Quota and model health

- **Quota table** (`components/quota/QuotaTable.svelte`) shows per-provider
  remaining quota, with Codex reset credits and GLM reset cards where the
  provider has them.
- **Auto-ping schedule** (`components/quota/AutoPingScheduleModal.svelte`) sets
  a background schedule that pings a provider through the real dispatch path, so
  OAuth refreshes and quota stays current without traffic.
- **Model health** (`components/settings/ModelHealthPanel.svelte`) shows what the
  automatic health loop found, and lets you re-enable a model it disabled.

## Compression and cache analytics

Two pages read the same ledger:

- **Compression Analytics** (`CompressionAnalyticsView.svelte`) — tokens saved,
  savings ratio, mode breakdown (`rtk`, `caveman`, `adhd`, `ponytail`, `stacked`).
- **Cache Analytics** (`CacheAnalyticsView.svelte`) — semantic cache hit rate and
  the entries behind it.

## Appearance

A theme toggle in the top bar switches between light and dark; the choice is
stored per browser and applied again on the next visit. Both themes share one
palette, so a visitor who opens the docs and then the dashboard sees one
product, not two tools.

## Screenshots

The [Screenshots](screenshots.html) page shows the current dashboard — every
page, in both themes, at desktop and mobile widths, captured from a build of
this release.

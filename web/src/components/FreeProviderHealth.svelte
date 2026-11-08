<!-- 9RouterX — source port and modifications by https://github.com/yanstd888/ — https://t.me/aiclub_id -->
<script lang="ts">
  // Free Provider Health: what the free-tier fallback chain sees, per
  // provider. A provider the chain would skip (cooling) shows red with the
  // time its wait ends; a provider the chain deprioritises (flaky) shows
  // amber; everything else is green. The numbers come from the same tracker
  // the request path records into, so the panel cannot drift from the gate.
  import { api, type FreeProviderHealthRow } from '../api/client'
  import Badge from '../lib/ui/Badge.svelte'
  import Button from '../lib/ui/Button.svelte'
  import Card from '../lib/ui/Card.svelte'
  import { notifications } from '../lib/notifications'
  import { RotateCcw, ShieldCheck } from 'lucide-svelte'

  const POLL_MS = 30_000

  let rows = $state<FreeProviderHealthRow[]>([])
  let loaded = $state(false)
  let failed = $state(false)
  let resetting = $state(false)
  let confirmReset = $state(false)

  const coolingCount = $derived(rows.filter((r) => r.status === 'cooling').length)
  const flakyCount = $derived(rows.filter((r) => r.status === 'flaky').length)
  // Rows worth showing: every provider with data or a wait, then the ones
  // the catalogue documents. A provider with no traffic and no cooldown is
  // sorted in, but the table hides it on small viewports via the regime
  // column rather than dropping it.
  const visible = $derived(
    [...rows].sort((a, b) => {
      const rank = (s: string) => (s === 'cooling' ? 0 : s === 'flaky' ? 1 : 2)
      if (rank(a.status) !== rank(b.status)) return rank(a.status) - rank(b.status)
      return a.provider.localeCompare(b.provider)
    }),
  )

  async function load() {
    try {
      const res = await api.getFreeProvidersHealth()
      rows = res.providers ?? []
      loaded = true
      failed = false
    } catch {
      failed = true
    }
  }

  async function doReset() {
    resetting = true
    try {
      const res = await api.resetFreeProvidersHealth()
      notifications.success(`Cleared ${res.clearedNow} cooldown(s)`)
      confirmReset = false
      await load()
    } catch {
      notifications.error('Reset failed')
    } finally {
      resetting = false
    }
  }

  $effect(() => {
    void load()
    const timer = setInterval(() => void load(), POLL_MS)
    return () => clearInterval(timer)
  })

  // "cooldown until 00:05" — the local wall clock the wait ends at, plus a
  // relative reading when it is under an hour away.
  function coolingLabel(row: FreeProviderHealthRow): string {
    if (row.coolingOpenEnded || !row.coolingUntil) return 'until success (no auto reset)'
    const until = new Date(row.coolingUntil)
    if (Number.isNaN(until.getTime())) return 'cooling'
    const hhmm = until.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' })
    const mins = Math.round((until.getTime() - Date.now()) / 60_000)
    if (mins > 0 && mins < 60) return `until ${hhmm} (~${mins}m)`
    return `until ${hhmm}`
  }

  function rateLabel(row: FreeProviderHealthRow): string {
    if (row.successRatePct < 0) return 'no data'
    return `${Math.round(row.successRatePct)}%`
  }

  function dotClass(status: string): string {
    switch (status) {
      case 'cooling':
        return 'bg-red-500'
      case 'flaky':
        return 'bg-amber-400'
      default:
        return 'bg-emerald-500'
    }
  }
</script>

<Card>
  <div class="flex flex-col sm:flex-row sm:items-center sm:justify-between gap-3">
    <div class="flex items-center gap-2">
      <ShieldCheck class="w-4 h-4 text-brand-500" aria-hidden="true" />
      <h2 class="text-sm font-semibold text-text-main">Free Provider Health</h2>
      {#if loaded && coolingCount > 0}
        <Badge tone="danger" dot>{coolingCount} cooling</Badge>
      {/if}
      {#if loaded && flakyCount > 0}
        <Badge tone="warning" dot>{flakyCount} flaky</Badge>
      {/if}
    </div>
    <div class="flex items-center gap-2">
      {#if confirmReset}
        <Button size="sm" variant="danger" disabled={resetting} onclick={doReset}>
          <RotateCcw class="w-3.5 h-3.5" aria-hidden="true" />
          Confirm clear
        </Button>
        <Button size="sm" variant="ghost" onclick={() => (confirmReset = false)}>Cancel</Button>
      {:else}
        <Button size="sm" variant="ghost" disabled={coolingCount === 0} onclick={() => (confirmReset = true)}>
          <RotateCcw class="w-3.5 h-3.5" aria-hidden="true" />
          Reset cooldowns
        </Button>
      {/if}
    </div>
  </div>

  <p class="text-xs text-text-muted mt-1">
    What the free-tier fallback chain sees. Red = skipped while its quota wait
    runs out; amber = flaky over the last 24h; green = healthy. Polls every 30s.
  </p>

  {#if loaded && visible.length > 0}
    <div class="overflow-x-auto mt-3">
      <table class="glyph-table w-full text-left text-xs min-w-[560px]">
        <caption class="sr-only">Free provider health: status, success rate, latency, recent refusals, cooldown</caption>
        <thead>
          <tr class="text-text-muted border-b border-border-subtle">
            <th scope="col" class="py-2 pr-3 font-medium">Provider</th>
            <th scope="col" class="py-2 pr-3 font-medium">Success (24h)</th>
            <th scope="col" class="py-2 pr-3 font-medium hidden md:table-cell">p95 latency</th>
            <th scope="col" class="py-2 pr-3 font-medium hidden md:table-cell">429 / 403</th>
            <th scope="col" class="py-2 pr-3 font-medium hidden lg:table-cell">Regime</th>
            <th scope="col" class="py-2 font-medium">Status</th>
          </tr>
        </thead>
        <tbody>
          {#each visible as row (row.provider)}
            <tr class="border-b border-border-subtle hover:bg-surface-2 transition-colors">
              <td class="py-2 pr-3">
                <span class="inline-flex items-center gap-2">
                  <span class={`inline-block w-2 h-2 rounded-full ${dotClass(row.status)}`} aria-hidden="true"></span>
                  <span class="font-medium text-text-main">{row.provider}</span>
                  {#if row.freeModels > 0}
                    <span class="text-text-muted">({row.freeModels} free)</span>
                  {/if}
                </span>
              </td>
              <td class="py-2 pr-3 font-mono text-text-main whitespace-nowrap">{rateLabel(row)}</td>
              <td class="py-2 pr-3 font-mono text-text-muted whitespace-nowrap hidden md:table-cell">
                {row.p95LatencyMs > 0 ? `${Math.round(row.p95LatencyMs)} ms` : '—'}
              </td>
              <td class="py-2 pr-3 font-mono text-text-muted whitespace-nowrap hidden md:table-cell">
                {row.recent429} / {row.recent403}
              </td>
              <td class="py-2 pr-3 hidden lg:table-cell">
                {#if row.regime}
                  <Badge tone="info">{row.regime}</Badge>
                {:else}
                  <span class="text-text-subtle">—</span>
                {/if}
              </td>
              <td class="py-2 whitespace-nowrap">
                {#if row.status === 'cooling'}
                  <span class="text-red-400 font-medium">{coolingLabel(row)}</span>
                {:else if row.status === 'flaky'}
                  <span class="text-amber-400 font-medium">flaky</span>
                {:else}
                  <span class="text-emerald-400">healthy</span>
                {/if}
              </td>
            </tr>
          {/each}
        </tbody>
      </table>
    </div>
  {:else if loaded}
    <p class="text-xs text-text-subtle mt-3">No free provider traffic recorded yet.</p>
  {:else if failed}
    <p class="text-[11px] text-text-muted mt-3 border border-border-subtle rounded-lg px-3 py-2">
      Health data is stale — the last refresh failed. It retries every 30 seconds.
    </p>
  {:else}
    <p class="text-xs text-text-subtle mt-3">Loading…</p>
  {/if}
</Card>

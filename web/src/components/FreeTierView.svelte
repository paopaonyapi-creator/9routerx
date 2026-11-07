<!-- 9RouterX — source port and modifications by https://github.com/yanstd888/ — https://t.me/aiclub_id -->
<script lang="ts">
  import Badge from '../lib/ui/Badge.svelte'
  import Card from '../lib/ui/Card.svelte'
  import { api, type FreeTierLiveRow } from '../api/client'
  import {
    CATALOG_CURATED_AT,
    FREE_TIER_CATALOG,
    REGIME_LABELS,
    TOKENS_PER_REQUEST_ASSUMPTION,
    TOS_LABELS,
    combinedSteadyMonthlyTokens,
    formatTokenEstimate,
    type FreeTierTos,
  } from '../lib/freeTierCatalog'

  // The TOS badge colors carry the verdict: green means the tier is safe to
  // build on, amber means the terms need reading (data training, rotation),
  // red means the terms themselves are the problem. Color is never the only
  // signal — every badge prints its label too.
  const tosTones: Record<FreeTierTos, 'success' | 'warning' | 'danger'> = {
    ok: 'success',
    caution: 'warning',
    avoid: 'danger',
  }

  const headline = $derived(combinedSteadyMonthlyTokens())

  // FASE 10.1 live tracking. The curated catalog (evidence, TOS, terms) stays
  // static; what the gateway has actually served this period comes from
  // /api/free-tier/live, keyed by the provider id each row routes through.
  // Polled every 60s — usage counters move slowly enough that a faster poll
  // would only spend the small host's CPU redrawing the same numbers.
  const LIVE_POLL_MS = 60_000

  let liveRows = $state<Record<string, FreeTierLiveRow>>({})
  let liveLoaded = $state(false)
  let liveFailed = $state(false)

  async function loadLive() {
    try {
      const res = await api.getFreeTierLive()
      const next: Record<string, FreeTierLiveRow> = {}
      for (const row of res.providers ?? []) next[row.provider] = row
      liveRows = next
      liveLoaded = true
      liveFailed = false
    } catch {
      // Keep the last good counters on screen; a failed poll must not blank a
      // table the operator is reading. The cell falls back to "—" only when
      // nothing has ever loaded.
      liveFailed = true
    }
  }

  $effect(() => {
    void loadLive()
    const timer = setInterval(() => void loadLive(), LIVE_POLL_MS)
    return () => clearInterval(timer)
  })

  function liveFor(providerId?: string): FreeTierLiveRow | undefined {
    return providerId ? liveRows[providerId] : undefined
  }
</script>

<div class="max-w-4xl mx-auto space-y-6">
  <Card>
    <div class="flex flex-col sm:flex-row sm:items-end sm:justify-between gap-3">
      <div>
        <p class="text-xs font-medium text-text-muted uppercase tracking-wide">
          Combined steady-monthly free tokens
        </p>
        <p class="text-3xl font-bold text-text-main mt-1">
          {formatTokenEstimate(headline)}
          <span class="text-sm font-medium text-text-muted">tokens / month</span>
        </p>
        <p class="text-xs text-text-muted mt-2 max-w-xl leading-relaxed">
          Recurring tiers only, each capacity pool counted once at its largest
          member. One-off credits and tiers without a published token cap are
          left out rather than guessed. Request-day caps convert at
          {TOKENS_PER_REQUEST_ASSUMPTION.toLocaleString()} tokens per request.
        </p>
      </div>
      <Badge tone="primary" size="lg">Curated {CATALOG_CURATED_AT}</Badge>
    </div>
  </Card>

  <div class="overflow-x-auto">
    <table class="glyph-table w-full text-left text-xs min-w-[760px]">
      <caption class="sr-only">
        Provider free tiers with monthly token estimates, live usage this period,
        reset regime and terms-of-service verdict
      </caption>
      <thead>
        <tr class="text-text-muted border-b border-border-subtle">
          <th scope="col" class="py-2.5 pr-3 font-medium">Provider</th>
          <th scope="col" class="py-2.5 pr-3 font-medium hidden md:table-cell">Models</th>
          <th scope="col" class="py-2.5 pr-3 font-medium">Est. tokens / month</th>
          <th scope="col" class="py-2.5 pr-3 font-medium">Used this period</th>
          <th scope="col" class="py-2.5 pr-3 font-medium hidden md:table-cell">Regime</th>
          <th scope="col" class="py-2.5 font-medium">Terms of service</th>
        </tr>
      </thead>
      <tbody>
        {#each FREE_TIER_CATALOG as row (row.poolKey)}
          {@const live = liveFor(row.providerId)}
          <tr class="border-b border-border-subtle align-top hover:bg-surface-2 transition-colors">
            <td class="free-provider-cell py-3 pr-3">
              <span class="font-semibold text-text-main text-[13px]">{row.provider}</span>
              <p class="text-text-muted mt-1 leading-relaxed max-w-[240px]">{row.notes}</p>
            </td>
            <td data-label="Models" class="py-3 pr-3 text-text-muted whitespace-nowrap hidden md:table-cell">{row.models.length}</td>
            <td data-label="Est. tokens / month" class="py-3 pr-3 font-mono font-bold text-text-main whitespace-nowrap">
              {row.monthlyTokens > 0 ? formatTokenEstimate(row.monthlyTokens) : 'unpublished'}
            </td>
            <td data-label="Used this period" class="py-3 pr-3 whitespace-nowrap">
              {#if live}
                {#if live.low}
                  <Badge tone="danger" dot>
                    {live.remainingPct < 1 ? 'exhausted' : `${Math.floor(live.remainingPct)}% left`}
                  </Badge>
                {:else}
                  <span class="font-mono text-text-main">
                    {formatTokenEstimate(live.used)}
                    {#if live.monthlyQuota > 0}
                      <span class="text-text-muted">/ {formatTokenEstimate(live.monthlyQuota)}</span>
                    {/if}
                  </span>
                {/if}
              {:else if row.providerId}
                <span class="text-text-subtle">{liveLoaded ? 'no usage' : '…'}</span>
              {:else}
                <span class="text-text-subtle">—</span>
              {/if}
            </td>
            <td data-label="Regime" class="py-3 pr-3 hidden md:table-cell">
              <Badge tone="info">{REGIME_LABELS[row.freeType]}</Badge>
            </td>
            <td data-label="Terms of service" class="py-3">
              <Badge tone={tosTones[row.tos]} dot>{TOS_LABELS[row.tos]}</Badge>
            </td>
          </tr>
        {/each}
      </tbody>
    </table>
  </div>

  {#if liveFailed && liveLoaded}
    <p class="text-[11px] text-text-muted border border-border-subtle rounded-lg px-3 py-2">
      Live usage is stale — the last refresh failed and the numbers on screen are
      the most recent ones served. It retries every 60 seconds.
    </p>
  {/if}

  <p class="text-[11px] text-text-muted leading-relaxed">
    Curated {CATALOG_CURATED_AT} — verify limits with the provider before relying
    on them. Estimates convert published request caps at
    {TOKENS_PER_REQUEST_ASSUMPTION.toLocaleString()} tokens per request; a tier
    whose cap the provider does not publish shows no number at all. The
    <span class="font-code">used this period</span> column is tracked live by the
    gateway (60s refresh) and badges a tier that drops under 10% of its quota.
  </p>
</div>

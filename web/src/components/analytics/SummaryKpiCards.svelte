<!-- 9RouterX — all source code port and modification by https://github.com/yanstd888/ — https://t.me/aiclub_id -->
<script lang="ts">
  // Same six KPIs and the same data flow the panel always had; the visual is
  // now the tremor-style KpiCard (/* adapted from tremor (Apache-2.0) */) on
  // the shadcn foundation (TASK-9routerX-shadcn-dashboard §1). The optional
  // `trend` series (the usage chart buckets the parent already reads) is the
  // only delta/sparkline source: badges render only where a series honestly
  // exists — the API exposes no per-bucket in/out split, so the token cards
  // stay clean numbers.
  import { fmtTokens } from '../../lib/compactNumber'
  import { costIn } from '../../lib/currency.svelte'
  import KpiCard from '../../lib/components/dashboard/KpiCard.svelte'
  import { bucketDelta, type TrendBucket } from './trend'
  import { type StatsData } from './types'

  let { stats = {}, trend = [] }: { stats?: StatsData; trend?: TrendBucket[] } = $props()

  function fmtSuccessRate(rate?: number | null): string {
    if (rate == null) return '—'
    return `${rate.toFixed(1)}%`
  }

  function rateColor(rate?: number | null): string {
    if (rate == null) return 'text-text-muted'
    if (rate >= 95) return 'text-success'
    if (rate >= 80) return 'text-warning'
    return 'text-red-500'
  }

  function fmtSuccessSubtext(s: StatsData): string {
    const total = s.totalRequests ?? 0
    if (total === 0) return 'No requests recorded'
    const errors = s.totalErrors ?? 0
    const successful = total - errors
    return `${successful.toLocaleString()}/${total.toLocaleString()} success`
  }

  // The cost delta and its sparkline come straight from the bucket series.
  const costDelta = $derived(bucketDelta(trend.map((b) => b.cost || 0)))
  const costSeries = $derived(trend.map((b) => b.cost || 0))
</script>

<div class="grid min-w-0 grid-cols-2 gap-3 sm:grid-cols-2 md:grid-cols-3 lg:grid-cols-6 sm:gap-4">
  <KpiCard label="Total Requests" value={fmtTokens(stats.totalRequests)} title={fmtTokens(stats.totalRequests)} />
  <KpiCard
    label="Success Rate"
    value={fmtSuccessRate(stats.successRate)}
    title={fmtSuccessRate(stats.successRate)}
    accentClass={rateColor(stats.successRate)}
    sub={fmtSuccessSubtext(stats)}
  />
  <KpiCard
    label="Total Input Tokens"
    value={fmtTokens(stats.totalPromptTokens)}
    title={fmtTokens(stats.totalPromptTokens)}
    accentClass="text-primary"
  />
  <KpiCard
    label="Cached Tokens"
    value={fmtTokens(stats.totalCachedTokens)}
    title={fmtTokens(stats.totalCachedTokens)}
    accentClass="text-info"
  />
  <KpiCard
    label="Output Tokens"
    value={fmtTokens(stats.totalCompletionTokens)}
    title={fmtTokens(stats.totalCompletionTokens)}
    accentClass="text-success"
  />
  <KpiCard
    label="Est. Cost"
    value="~{costIn(stats.totalCost ?? 0)}"
    title="Estimated, not actual"
    accentClass="text-warning"
    delta={costDelta}
    series={costSeries}
    sub="Estimated, not actual"
  />
</div>

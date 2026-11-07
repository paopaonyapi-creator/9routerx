<!-- 9RouterX — all source code port and modification by https://github.com/yanstd888/ — https://t.me/aiclub_id -->
<script lang="ts">
  // /* via shadcn-svelte (MIT) */ — bar + donut grid over the usage stats the
  // Overview already holds (stats.byProvider): requests per provider as bars,
  // token share as a donut. Zero new endpoints; aggregation is frontend-side
  // (TASK-9routerX-shadcn-dashboard §2).
  import { BarChart, PieChart, Text } from 'layerchart'
  import * as Chart from '$lib/components/ui/chart/index.js'
  import { fmtTokens } from '$lib/compactNumber'
  import { providerDisplayName, type StatsData, type UsageItem } from '../../../components/analytics/types'

  interface Props {
    stats?: StatsData
    /** Custom provider nodes, so a synthetic id renders as its configured name. */
    providerNodes?: { id: string; name?: string }[]
  }

  let { stats = {}, providerNodes = [] }: Props = $props()

  // The shadcn chart slots, in the order the series use them. Chart.Style
  // writes --color-{key} per theme; index.css points those at askglyph hues.
  const SLOT_COLORS = [
    'var(--chart-1)',
    'var(--chart-2)',
    'var(--chart-3)',
    'var(--chart-4)',
    'var(--chart-5)',
  ]
  const OTHER_COLOR = 'var(--muted-foreground)'

  // Ranked provider rows, the tail folded into "other" so both charts stay
  // readable no matter how many providers the gateway serves.
  interface ProviderRow {
    name: string
    requests: number
    tokens: number
    color: string
  }

  const rows = $derived.by((): ProviderRow[] => {

  // Integer x ticks for the requests bar chart: counts are whole numbers, so
  // the axis steps 0..max by 1 when the max is small (<=8) and by a whole
  // fifth otherwise — never 0.2 / 1.4.
  const maxRequests = $derived(Math.max(1, ...rows.map((r) => Number(r.requests) || 0)))
  const barXTicks = $derived.by(() => {
    const max = maxRequests
    if (max <= 8) return Array.from({ length: max + 1 }, (_, i) => i)
    const step = Math.ceil(max / 5)
    const ticks: number[] = []
    for (let v = 0; v <= max + step / 2; v += step) ticks.push(v)
    if (ticks[ticks.length - 1] < max) ticks.push(max)
    return ticks
  })
    const source: Record<string, UsageItem> = stats.byProvider || {}
    const entries = Object.entries(source)
      .map(([id, item]) => ({
        id,
        name: providerDisplayName(id, providerNodes),
        requests: item.requests || 0,
        tokens: (item.promptTokens || 0) + (item.completionTokens || 0),
      }))
      .filter((r) => r.requests > 0 || r.tokens > 0)
      .sort((a, b) => b.tokens - a.tokens || b.requests - a.requests)
    const kept = entries.slice(0, 5)
    const tail = entries.slice(5)
    const out: ProviderRow[] = kept.map((r, i) => ({
      name: r.name,
      requests: r.requests,
      tokens: r.tokens,
      color: SLOT_COLORS[i % SLOT_COLORS.length],
    }))
    if (tail.length > 0) {
      out.push({
        name: 'other',
        requests: tail.reduce((s, r) => s + r.requests, 0),
        tokens: tail.reduce((s, r) => s + r.tokens, 0),
        color: OTHER_COLOR,
      })
    }
    return out
  })

  const barConfig = $derived(
    Object.fromEntries(rows.map((r) => [r.name, { label: r.name, color: r.color }]))
  ) satisfies Chart.ChartConfig

  const donutConfig = $derived(
    Object.fromEntries(rows.map((r) => [r.name, { label: r.name, color: r.color }]))
  ) satisfies Chart.ChartConfig

  const totalTokens = $derived(rows.reduce((s, r) => s + r.tokens, 0))
  const hasData = $derived(rows.length > 0)
</script>

{#if hasData}
  <div class="grid min-w-0 grid-cols-1 gap-4 lg:grid-cols-2" data-slot="provider-breakdown">
    <!-- Requests per provider. A horizontal band reads long provider names
         without rotating labels; the bar color follows each provider's slot. -->
    <div class="glyph-surface flex min-w-0 flex-col p-4 sm:p-5">
      <div class="mb-3 flex min-w-0 flex-col">
        <h3 class="font-headline text-sm font-bold text-text-main">Requests by provider</h3>
        <p class="font-body text-xs text-text-muted">Selected window, live totals</p>
      </div>
      <Chart.Container config={barConfig} class="h-[280px] w-full">
        <BarChart
          data={rows}
          orientation="horizontal"
          bandPadding={0.25}
          x="requests"
          y="name"
          series={[{ key: 'requests', label: 'Requests', color: 'var(--chart-1)' }]}
          props={{
            /* Request counts are integers — fractional ticks (0.2 / 1.4) read
               as nonsense. Tick values are computed as whole numbers from the
               data max, and the format rounds as a belt-and-braces net. */
            xAxis: { ticks: barXTicks, format: (v: number) => Math.round(v).toLocaleString() },
            yAxis: { format: (v: string) => (v.length > 14 ? v.slice(0, 13) + '…' : v) },
          }}
        >
          {#snippet tooltip()}
            <Chart.Tooltip indicator="line" hideLabel={false} />
          {/snippet}
        </BarChart>
      </Chart.Container>
    </div>

    <!-- Token share donut with the tremor center total. -->
    <div class="glyph-surface flex min-w-0 flex-col p-4 sm:p-5">
      <div class="mb-3 flex min-w-0 flex-col">
        <h3 class="font-headline text-sm font-bold text-text-main">Token share</h3>
        <p class="font-body text-xs text-text-muted">Input + output tokens by provider</p>
      </div>
      <Chart.Container config={donutConfig} class="mx-auto aspect-square max-h-[260px] w-full">
        <PieChart
          data={rows}
          key="name"
          value="tokens"
          c="color"
          innerRadius={60}
          padding={28}
          props={{ pie: { motion: 'tween' } }}
        >
          {#snippet aboveMarks()}
            <Text
              value={fmtTokens(totalTokens)}
              textAnchor="middle"
              verticalAnchor="middle"
              class="fill-foreground font-bold"
              dy={3}
            />
            <Text
              value="tokens"
              textAnchor="middle"
              verticalAnchor="middle"
              class="fill-muted-foreground! text-muted-foreground"
              dy={20}
            />
          {/snippet}
          {#snippet tooltip()}
            <Chart.Tooltip hideLabel />
          {/snippet}
        </PieChart>
      </Chart.Container>
    </div>
  </div>
{/if}

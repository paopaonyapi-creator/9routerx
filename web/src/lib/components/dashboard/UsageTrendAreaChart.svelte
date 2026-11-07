<!-- 9RouterX — all source code port and modification by https://github.com/yanstd888/ — https://t.me/aiclub_id -->
<script lang="ts">
  // /* via shadcn-svelte (MIT) */ — adapted from the registry block
  // chart-area-gradient (LayerChart + Chart.Container + Chart.Tooltip), fed by
  // the EXISTING /api/usage/chart response pivoted in ./trend.ts. The bucket
  // labels are plain strings (HH:MM or MMM D), so x uses a point scale.
  import { scalePoint } from 'd3-scale'
  import { Area, AreaChart, LinearGradient } from 'layerchart'
  import * as Chart from '$lib/components/ui/chart/index.js'
  import { fmtTokens } from '$lib/compactNumber'
  import {
    topFamilies,
    seriesRows,
    OTHER_COLOR,
    type TrendBucket,
  } from '../../../components/analytics/trend'

  interface Props {
    buckets?: TrendBucket[]
    /** Loading state of the parent's read; the panel shows a quiet spinner. */
    loading?: boolean
  }

  let { buckets = [], loading = false }: Props = $props()

  const families = $derived(topFamilies(buckets))
  const rows = $derived(seriesRows(buckets, families))
  const hasData = $derived(buckets.length >= 2 && rows.some((r) => families.some((f) => (r[f.family] as number) > 0)))

  // Reactive: the families are derived from the buckets, and the config must
  // carry a color for each key the stacked series names (Chart.Style writes
  // --color-{key} per theme from it).
  const chartConfig = $derived({
    ...Object.fromEntries(
      families.map((f) => [
        f.family,
        { label: f.family, color: f.color === OTHER_COLOR ? 'var(--muted-foreground)' : f.color },
      ])
    ),
  }) satisfies Chart.ChartConfig

  const xScale = scalePoint<string>().padding(0.15)
</script>

<div class="flex min-h-[260px] min-w-0 flex-col" data-slot="usage-trend-area">
  {#if loading}
    <div class="flex items-center justify-center py-12 text-text-muted text-xs">
      <span class="material-symbols-outlined animate-spin text-[20px] mr-2" aria-hidden="true">progress_activity</span>
      Loading chart...
    </div>
  {:else if !hasData}
    <div class="flex min-h-[260px] items-center justify-center text-xs text-text-muted">
      {buckets.length === 0 ? 'No usage recorded in this period.' : 'Not enough buckets to draw a trend yet.'}
    </div>
  {:else}
    <Chart.Container config={chartConfig} class="h-[300px] w-full [&_.lc-legend-swatch-button]:max-w-44 [&_.lc-legend-swatch-button_span]:min-w-0 [&_.lc-legend-swatch-button_span]:truncate">
      <AreaChart
        data={rows}
        x="label"
        xScale={xScale}
        yPadding={[0, 20]}
        axis="x"
        legend
        seriesLayout="stack"
        series={families.map((f) => ({
          key: f.family,
          label: f.family,
          color: f.color === OTHER_COLOR ? 'var(--muted-foreground)' : f.color,
        }))}
        props={{
          xAxis: {
            format: (v: string) => v,
            ticks: Math.min(8, rows.length),
          },
          yAxis: { format: (v: number) => fmtTokens(v) },
        }}
      >
        {#snippet tooltip()}
          <Chart.Tooltip indicator="dot" labelFormatter={(v: string) => v} />
        {/snippet}
        {#snippet marks({ context })}
          {#each context.series.visibleSeries as s (s.key)}
            <LinearGradient stops={[s.color ?? '', 'color-mix(in lch, ' + s.color + ' 10%, transparent)']} vertical>
              {#snippet children({ gradient })}
                <Area seriesKey={s.key} fillOpacity={0.15} line={{ class: 'stroke-1' }} {...s.props} fill={gradient} />
              {/snippet}
            </LinearGradient>
          {/each}
        {/snippet}
      </AreaChart>
    </Chart.Container>
  {/if}
</div>

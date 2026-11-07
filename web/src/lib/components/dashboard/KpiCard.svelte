<!-- 9RouterX — all source code port and modification by https://github.com/yanstd888/ — https://t.me/aiclub_id -->
<script lang="ts">
  // /* adapted from tremor (Apache-2.0) */ — KPI card with the delta badge
  // (▲/▼) and the mini area sparkline. Presentational only: every input is a
  // prop, so the panel that renders it keeps its own data flow untouched.
  interface Props {
    label: string
    value: string
    /** Hover title on the value (truncation rescue, same as the old cards). */
    title?: string
    sub?: string
    /** Percentage change; null/undefined hides the badge (tremor: no comparison, no badge). */
    delta?: number | null
    /** In-window series behind the value; renders the sparkline when non-empty. */
    series?: number[]
    /** Text color class for the value (the panel's existing accent per KPI). */
    accentClass?: string
  }

  let { label, value, title = '', sub = '', delta = null, series = [], accentClass = 'text-text-main' }: Props = $props()

  const hasDelta = $derived(delta !== null && delta !== undefined && Number.isFinite(delta))
  const deltaUp = $derived((delta ?? 0) >= 0)
  // The badge rounds away vanishing changes: |Δ| < 0.05% renders as flat.
  const deltaLabel = $derived(
    hasDelta && Math.abs(delta as number) >= 0.05
      ? `${Math.abs(delta as number).toFixed(1)}%`
      : '0%'
  )
  const flat = $derived(hasDelta && Math.abs(delta as number) < 0.05)

  const SPARK_W = 120
  const SPARK_H = 36
  // Sparkline geometry: one point per value, padded so the stroke never clips.
  const sparkPoints = $derived.by(() => {
    if (series.length < 2) return []
    const max = Math.max(...series)
    const min = Math.min(...series)
    const span = max - min || 1
    const step = SPARK_W / (series.length - 1)
    return series.map((v, i) => ({
      x: i * step,
      y: 2 + (1 - (v - min) / span) * (SPARK_H - 4),
    }))
  })
  const sparkLine = $derived(sparkPoints.map((p) => `${p.x.toFixed(1)},${p.y.toFixed(1)}`).join(' L '))
  // The tremor gradient fill: the area under the line fades to nothing —
  // the top stop carries ~15% of the color.
  const sparkArea = $derived(
    sparkPoints.length > 1
      ? `M ${sparkLine} L ${SPARK_W},${SPARK_H} L 0,${SPARK_H} Z`
      : ''
  )
  const uid = $props.id()
</script>

<div
  class="glyph-surface glyph-lift flex min-w-0 flex-col gap-1 px-4 py-3"
  data-slot="kpi-card"
>
  <div class="flex items-center justify-between gap-2">
    <span class="text-text-muted text-sm uppercase font-semibold truncate">{label}</span>
    {#if hasDelta}
      <span
        class="inline-flex shrink-0 items-center gap-0.5 rounded-full px-1.5 py-0.5 text-[10px] font-semibold font-code {flat
          ? 'bg-surface-3 text-text-muted'
          : deltaUp
            ? 'bg-success/10 text-success'
            : 'bg-danger/10 text-danger'}"
        title="Second half of the window vs the first"
      >
        {#if !flat}
          <span aria-hidden="true">{deltaUp ? '▲' : '▼'}</span>
        {/if}
        {deltaLabel}
      </span>
    {/if}
  </div>
  <span class="truncate text-2xl font-bold font-code {accentClass}" {title}>{value}</span>
  {#if sparkPoints.length > 1}
    <svg
      viewBox="0 0 {SPARK_W} {SPARK_H}"
      class="mt-1 h-9 w-full {accentClass}"
      preserveAspectRatio="none"
      role="img"
      aria-label="{label} trend over the window"
    >
      <defs>
        <linearGradient id="spark-{uid}" x1="0" y1="0" x2="0" y2="1">
          <stop offset="0%" stop-color="currentColor" stop-opacity="0.15" />
          <stop offset="100%" stop-color="currentColor" stop-opacity="0" />
        </linearGradient>
      </defs>
      <path d={sparkArea} fill="url(#spark-{uid})" />
      <path
        d="M {sparkLine}"
        fill="none"
        stroke="currentColor"
        stroke-width="1.5"
        vector-effect="non-scaling-stroke"
      />
    </svg>
    {#if sub}
      <span class="text-[10px] text-text-muted truncate">{sub}</span>
    {/if}
  {:else if sub}
    <span class="text-[10px] text-text-muted truncate">{sub}</span>
  {:else}
    <span class="text-[10px] invisible select-none leading-none">&nbsp;</span>
  {/if}
</div>

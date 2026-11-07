<!-- 9RouterX — this file is part of a port maintained by https://github.com/yanstd888/ — https://t.me/aiclub_id -->
<script lang="ts">
  import { onMount } from 'svelte'
  import { api, type SecurityLogEntry, type SecurityLogSummary } from '../../api/client'
  import Badge from '../../lib/ui/Badge.svelte'
  import Button from '../../lib/ui/Button.svelte'
  import Card from '../../lib/ui/Card.svelte'
  import ConfirmModal from '../../lib/ui/ConfirmModal.svelte'
  import {
    Activity,
    AlertOctagon,
    AlertTriangle,
    Filter,
    KeyRound,
    RefreshCw,
    ShieldAlert,
    Trash2,
  } from 'lucide-svelte'

  let events = $state<SecurityLogEntry[]>([])
  let summary = $state<SecurityLogSummary>({
    counts: { critical: 0, warn: 0, info: 0 },
    classifications: { breach: 0, suspicious: 0, normal: 0 },
    logins: { success: 0, failed: 0, viaApiKey: 0 },
    total: 0,
  })
  let isLoading = $state(false)
  let error = $state<string | null>(null)
  let severityFilter = $state<string>('')
  let limit = $state<number>(100)
  let isClearOpen = $state(false)
  let isClearing = $state(false)

  async function loadLogs() {
    isLoading = true
    error = null
    try {
      const res = await api.getSecurityLogs(limit, severityFilter || undefined)
      events = res.events || []
      summary = res.summary || {
        counts: { critical: 0, warn: 0, info: 0 },
        classifications: { breach: 0, suspicious: 0, normal: 0 },
        logins: { success: 0, failed: 0, viaApiKey: 0 },
        total: 0,
      }
    } catch (err) {
      error = err instanceof Error ? err.message : String(err)
    } finally {
      isLoading = false
    }
  }

  async function handleClear() {
    isClearing = true
    try {
      await api.clearSecurityLogs()
      isClearOpen = false
      await loadLogs()
    } catch (err) {
      error = err instanceof Error ? err.message : String(err)
    } finally {
      isClearing = false
    }
  }

  function formatTime(iso: string): string {
    if (!iso) return ''
    try {
      const d = new Date(iso)
      return d.toLocaleString()
    } catch {
      return iso
    }
  }

  function severityBadgeTone(sev?: string): 'danger' | 'warning' | 'info' | 'neutral' {
    switch ((sev || '').toLowerCase()) {
      case 'critical':
        return 'danger'
      case 'warn':
        return 'warning'
      case 'info':
        return 'info'
      default:
        return 'neutral'
    }
  }

  function classificationBadgeTone(cls?: string): 'danger' | 'warning' | 'success' | 'neutral' {
    switch ((cls || '').toLowerCase()) {
      case 'breach':
        return 'danger'
      case 'suspicious':
        return 'warning'
      case 'normal':
        return 'success'
      default:
        return 'neutral'
    }
  }

  onMount(() => {
    loadLogs()
  })
</script>

<div class="space-y-6">
  <!-- Headline summary cards -->
  <div class="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-4 gap-4">
    <Card class="p-4 flex items-center justify-between bg-surface border border-border-subtle">
      <div>
        <p class="text-xs font-medium text-text-muted">Total Events</p>
        <p class="text-2xl font-bold text-text-main mt-1">{summary.total}</p>
        <p class="text-[11px] text-text-muted mt-0.5">Retained audit trail</p>
      </div>
      <div class="p-2.5 rounded-xl bg-surface-2 text-text-muted">
        <Activity class="w-5 h-5" />
      </div>
    </Card>

    <Card class="p-4 flex items-center justify-between bg-surface border border-red-500/20 bg-red-500/5">
      <div>
        <p class="text-xs font-medium text-red-500">Breach / Critical</p>
        <p class="text-2xl font-bold text-red-500 mt-1">{summary.classifications.breach ?? 0}</p>
        <p class="text-[11px] text-red-400 mt-0.5">High-severity probe signals</p>
      </div>
      <div class="p-2.5 rounded-xl bg-red-500/10 text-red-500">
        <AlertOctagon class="w-5 h-5" />
      </div>
    </Card>

    <Card class="p-4 flex items-center justify-between bg-surface border border-amber-500/20 bg-amber-500/5">
      <div>
        <p class="text-xs font-medium text-amber-500">Suspicious</p>
        <p class="text-2xl font-bold text-amber-500 mt-1">{summary.classifications.suspicious ?? 0}</p>
        <p class="text-[11px] text-amber-400 mt-0.5">Refusals, auth failures, 401/403</p>
      </div>
      <div class="p-2.5 rounded-xl bg-amber-500/10 text-amber-500">
        <AlertTriangle class="w-5 h-5" />
      </div>
    </Card>

    <Card class="p-4 flex items-center justify-between bg-surface border border-border-subtle">
      <div>
        <p class="text-xs font-medium text-text-muted">Sign-in Activity</p>
        <p class="text-lg font-bold text-text-main mt-1">
          {summary.logins.success} <span class="text-xs font-normal text-text-muted">ok</span> /
          <span class="text-red-500">{summary.logins.failed}</span> <span class="text-xs font-normal text-text-muted">fail</span>
        </p>
        <p class="text-[11px] text-text-muted mt-0.5">{summary.logins.viaApiKey} via API key</p>
      </div>
      <div class="p-2.5 rounded-xl bg-surface-2 text-text-muted">
        <KeyRound class="w-5 h-5" />
      </div>
    </Card>
  </div>

  <!-- Filter & Action toolbar -->
  <Card class="p-4 bg-surface border border-border-subtle">
    <div class="flex flex-col sm:flex-row items-stretch sm:items-center justify-between gap-3">
      <div class="flex flex-wrap items-center gap-2">
        <div class="flex items-center gap-1.5 text-xs text-text-muted mr-1">
          <Filter class="w-3.5 h-3.5" />
          <span>Severity:</span>
        </div>
        {#each [
          { id: '', label: 'All' },
          { id: 'critical', label: 'Critical' },
          { id: 'warn', label: 'Warn' },
          { id: 'info', label: 'Info' },
        ] as opt}
          <button
            type="button"
            onclick={() => {
              severityFilter = opt.id
              loadLogs()
            }}
            class="px-2.5 py-1 rounded-lg text-xs font-medium transition cursor-pointer {severityFilter === opt.id
              ? 'bg-primary text-white shadow-sm'
              : 'bg-surface-2 text-text-muted hover:text-text-main'}"
          >
            {opt.label}
          </button>
        {/each}

        <div class="hidden sm:block h-4 w-px bg-border-subtle mx-1"></div>

        <select
          bind:value={limit}
          onchange={loadLogs}
          class="bg-surface-2 border border-border-subtle rounded-lg px-2.5 py-1 text-xs text-text-main focus:outline-none focus:border-primary"
        >
          <option value={50}>50 entries</option>
          <option value={100}>100 entries</option>
          <option value={200}>200 entries</option>
          <option value={500}>500 entries</option>
        </select>
      </div>

      <div class="flex items-center gap-2">
        <Button
          variant="secondary"
          size="sm"
          disabled={isLoading}
          onclick={loadLogs}
          class="flex items-center gap-1.5"
        >
          <RefreshCw class="w-3.5 h-3.5 {isLoading ? 'animate-spin' : ''}" />
          <span>Reload</span>
        </Button>

        <Button
          variant="danger"
          size="sm"
          disabled={isLoading || events.length === 0}
          onclick={() => (isClearOpen = true)}
          class="flex items-center gap-1.5"
        >
          <Trash2 class="w-3.5 h-3.5" />
          <span>Clear Log</span>
        </Button>
      </div>
    </div>
  </Card>

  {#if error}
    <div class="p-3.5 rounded-xl border border-red-500/30 bg-red-500/10 text-red-500 text-xs">
      {error}
    </div>
  {/if}

  <!-- Events Table -->
  <Card class="bg-surface border border-border-subtle overflow-hidden">
    {#if events.length === 0}
      <div class="p-12 text-center text-text-muted">
        <ShieldAlert class="w-8 h-8 mx-auto mb-2 opacity-40" />
        <p class="text-sm font-medium">No security events recorded</p>
        <p class="text-xs text-text-muted/70 mt-1">
          {#if severityFilter}
            No events match the selected severity filter.
          {:else}
            Authentication attempts, rejected probes, and access refusals will appear here.
          {/if}
        </p>
      </div>
    {:else}
      <div class="overflow-x-auto">
        <table class="glyph-table w-full text-left text-xs font-body">
          <thead class="bg-surface-2 border-b border-border-subtle text-text-muted font-medium uppercase text-[10px] tracking-wider">
            <tr>
              <th class="px-4 py-3">Time</th>
              <th class="px-4 py-3">Severity</th>
              <th class="px-4 py-3 hidden md:table-cell">Class</th>
              <th class="px-4 py-3">Type / Label</th>
              <th class="px-4 py-3 hidden md:table-cell">Actor / IP</th>
              <th class="px-4 py-3 hidden md:table-cell">Request</th>
              <th class="px-4 py-3">Status</th>
              <th class="px-4 py-3">Detail</th>
            </tr>
          </thead>
          <tbody class="divide-y divide-border-subtle/60">
            {#each events as event (event.id)}
              <tr class="hover:bg-surface-2/60 transition-colors {event.classification === 'breach' ? 'bg-red-500/[0.03]' : ''}">
                <td data-label="Time" class="px-4 py-3 whitespace-nowrap font-code text-[11px] text-text-subtle">
                  {formatTime(event.at)}
                </td>
                <td data-label="Severity" class="px-4 py-3 whitespace-nowrap">
                  <Badge tone={severityBadgeTone(event.severity)} size="sm">
                    {event.severity}
                  </Badge>
                </td>
                <td data-label="Class" class="px-4 py-3 whitespace-nowrap hidden md:table-cell">
                  <Badge tone={classificationBadgeTone(event.classification)} size="sm">
                    {event.classification || 'normal'}
                  </Badge>
                </td>
                <td data-label="Type / Label" class="px-4 py-3 whitespace-nowrap">
                  <div class="font-medium text-text-main">{event.label || event.type}</div>
                  <div class="text-[10px] font-code text-text-muted">{event.type}</div>
                </td>
                <td data-label="Actor / IP" class="px-4 py-3 whitespace-nowrap hidden md:table-cell">
                  <div class="font-code text-text-main">{event.ip}</div>
                  {#if event.actor}
                    <div class="text-[10px] text-text-muted truncate max-w-[140px]" title={event.actor}>
                      {event.actor}
                    </div>
                  {/if}
                </td>
                <td data-label="Request" class="px-4 py-3 hidden md:table-cell">
                  <div class="flex items-center gap-1.5 font-code text-[11px]">
                    {#if event.method}
                      <span class="font-bold text-text-muted">{event.method}</span>
                    {/if}
                    <span class="text-text-main truncate max-w-[200px]" title={event.path}>{event.path || '-'}</span>
                  </div>
                </td>
                <td data-label="Status" class="px-4 py-3 whitespace-nowrap font-code text-[11px]">
                  {#if event.status}
                    <span class="{event.status >= 400 ? 'text-red-500 font-bold' : 'text-green-500'}">
                      {event.status}
                    </span>
                  {:else}
                    <span class="text-text-muted">-</span>
                  {/if}
                </td>
                <td data-label="Detail" class="px-4 py-3 text-text-muted break-words max-w-[260px] text-[11px]">
                  {event.detail || '-'}
                </td>
              </tr>
            {/each}
          </tbody>
        </table>
      </div>
    {/if}
  </Card>

  <ConfirmModal
    isOpen={isClearOpen}
    title="Clear Security Log"
    message="Are you sure you want to delete all recorded security events? This action cannot be undone."
    confirmText="Clear Log"
    variant="danger"
    loading={isClearing}
    onConfirm={handleClear}
    onClose={() => (isClearOpen = false)}
  />
</div>

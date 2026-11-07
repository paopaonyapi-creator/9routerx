<!-- 9RouterX — source port and modifications by https://github.com/yanstd888/ — https://t.me/aiclub_id -->
<script lang="ts">
  import { onMount } from 'svelte'
  import { getAuthHeaders } from '../../api/client'
  import Badge from '../../lib/ui/Badge.svelte'
  import Button from '../../lib/ui/Button.svelte'
  import Card from '../../lib/ui/Card.svelte'
  import { Download, Filter, RefreshCw, ScrollText } from 'lucide-svelte'
  import { t } from '../../lib/i18n.svelte'

  // FASE 10.6 compliance audit log view. Reads GET /api/audit — the same
  // handler the security-log view sits beside — and downloads the CSV flavor of
  // the same query, so an export never disagrees with the table above it.

  interface AuditLogEntry {
    id?: number
    timestamp: string
    actor: string
    action: string
    target?: string
    before?: string
    after?: string
    ip?: string
  }

  let entries = $state<AuditLogEntry[]>([])
  let isLoading = $state(false)
  let isExporting = $state(false)
  let error = $state<string | null>(null)
  let actorFilter = $state('')
  let actionFilter = $state('')
  let limit = $state<number>(100)

  function auditQuery(): string {
    const params = new URLSearchParams()
    params.set('limit', String(limit))
    if (actorFilter.trim()) params.set('actor', actorFilter.trim())
    if (actionFilter.trim()) params.set('action', actionFilter.trim())
    return params.toString()
  }

  async function loadLogs() {
    isLoading = true
    error = null
    try {
      const res = await fetch(`/api/audit?${auditQuery()}`, { headers: getAuthHeaders() })
      if (!res.ok) throw new Error(`HTTP ${res.status}`)
      const data = (await res.json()) as { entries?: AuditLogEntry[] }
      entries = data.entries ?? []
    } catch (err) {
      error = err instanceof Error ? err.message : String(err)
    } finally {
      isLoading = false
    }
  }

  // The CSV endpoint is behind the dashboard auth, so the download goes through
  // an authenticated fetch and a blob URL rather than a plain href.
  async function exportCsv() {
    isExporting = true
    error = null
    try {
      const res = await fetch(`/api/audit?${auditQuery()}&format=csv`, { headers: getAuthHeaders() })
      if (!res.ok) throw new Error(`HTTP ${res.status}`)
      const url = URL.createObjectURL(await res.blob())
      const a = document.createElement('a')
      a.href = url
      a.download = `9routerx-audit-${new Date().toISOString().slice(0, 10)}.csv`
      a.click()
      URL.revokeObjectURL(url)
    } catch (err) {
      error = err instanceof Error ? err.message : String(err)
    } finally {
      isExporting = false
    }
  }

  function formatTime(iso: string): string {
    if (!iso) return ''
    try {
      return new Date(iso).toLocaleString()
    } catch {
      return iso
    }
  }

  function detailsOf(e: AuditLogEntry): string {
    return e.after || e.before || '-'
  }

  function actionTone(action: string): 'danger' | 'warning' | 'success' | 'info' | 'neutral' {
    if (action.endsWith('.delete') || action === 'auth.login_failed') return 'danger'
    if (action.endsWith('.create')) return 'success'
    if (action.endsWith('.update') || action.endsWith('.toggle')) return 'info'
    if (action === 'auth.login') return 'neutral'
    return 'warning'
  }

  onMount(() => {
    loadLogs()
  })
</script>

<div class="space-y-6">
  <!-- Filter & export toolbar -->
  <Card class="p-4 bg-surface border border-border-subtle">
    <div class="flex flex-col sm:flex-row items-stretch sm:items-center justify-between gap-3">
      <div class="flex flex-wrap items-center gap-2">
        <div class="flex items-center gap-1.5 text-xs text-text-muted mr-1">
          <Filter class="w-3.5 h-3.5" />
          <span>{t('Filter')}:</span>
        </div>
        <input
          type="text"
          bind:value={actorFilter}
          placeholder={t('Actor')}
          aria-label={t('Actor')}
          class="bg-surface-2 border border-border-subtle rounded-lg px-2.5 py-1.5 text-xs text-text-main placeholder:text-text-muted focus:outline-none focus:border-primary w-36"
        />
        <input
          type="text"
          bind:value={actionFilter}
          placeholder={t('Action')}
          aria-label={t('Action')}
          class="bg-surface-2 border border-border-subtle rounded-lg px-2.5 py-1.5 text-xs text-text-main placeholder:text-text-muted focus:outline-none focus:border-primary w-44 font-code"
        />

        <div class="hidden sm:block h-4 w-px bg-border-subtle mx-1"></div>

        <select
          bind:value={limit}
          onchange={loadLogs}
          aria-label={t('Limit')}
          class="bg-surface-2 border border-border-subtle rounded-lg px-2.5 py-1.5 text-xs text-text-main focus:outline-none focus:border-primary"
        >
          <option value={50}>50 {t('entries')}</option>
          <option value={100}>100 {t('entries')}</option>
          <option value={200}>200 {t('entries')}</option>
          <option value={500}>500 {t('entries')}</option>
          <option value={1000}>1000 {t('entries')}</option>
        </select>

        <Button
          variant="secondary"
          size="sm"
          disabled={isLoading}
          onclick={loadLogs}
          class="flex items-center gap-1.5"
        >
          <RefreshCw class="w-3.5 h-3.5 {isLoading ? 'animate-spin' : ''}" />
          <span>{t('Reload')}</span>
        </Button>
      </div>

      <Button
        variant="secondary"
        size="sm"
        disabled={isExporting || isLoading}
        onclick={exportCsv}
        class="flex items-center gap-1.5"
      >
        <Download class="w-3.5 h-3.5" />
        <span>{t('Export CSV')}</span>
      </Button>
    </div>
  </Card>

  {#if error}
    <div class="p-3.5 rounded-xl border border-red-500/30 bg-red-500/10 text-red-500 text-xs">
      {error}
    </div>
  {/if}

  <!-- Entries table -->
  <Card class="bg-surface border border-border-subtle overflow-hidden">
    {#if entries.length === 0}
      <div class="p-12 text-center text-text-muted">
        <ScrollText class="w-8 h-8 mx-auto mb-2 opacity-40" />
        <p class="text-sm font-medium">{t('No audit entries recorded')}</p>
        <p class="text-xs text-text-muted/70 mt-1">
          {#if actorFilter || actionFilter}
            {t('No entries match the current filters.')}
          {:else}
            {t('Dashboard mutations (keys, connections, settings, logins) will appear here.')}
          {/if}
        </p>
      </div>
    {:else}
      <div class="overflow-x-auto">
        <table class="glyph-table w-full text-left text-xs font-body">
          <thead class="bg-surface-2 border-b border-border-subtle text-text-muted font-medium uppercase text-[10px] tracking-wider">
            <tr>
              <th class="px-4 py-3">{t('Time')}</th>
              <th class="px-4 py-3">{t('Action')}</th>
              <th class="px-4 py-3 hidden md:table-cell">{t('Target')}</th>
              <th class="px-4 py-3 hidden md:table-cell">{t('Actor')}</th>
              <th class="px-4 py-3">{t('Details')}</th>
            </tr>
          </thead>
          <tbody class="divide-y divide-border-subtle/60">
            {#each entries as entry (entry.id ?? entry.timestamp)}
              <tr class="hover:bg-surface-2/60 transition-colors">
                <td data-label="Time" class="px-4 py-3 whitespace-nowrap font-code text-[11px] text-text-subtle">
                  {formatTime(entry.timestamp)}
                </td>
                <td data-label="Action" class="px-4 py-3 whitespace-nowrap">
                  <Badge tone={actionTone(entry.action)} size="sm">{entry.action}</Badge>
                </td>
                <td data-label="Target" class="px-4 py-3 whitespace-nowrap font-code text-[11px] text-text-main hidden md:table-cell">
                  {entry.target || '-'}
                </td>
                <td data-label="Actor" class="px-4 py-3 whitespace-nowrap hidden md:table-cell">
                  <div class="font-code text-text-main">{entry.actor}</div>
                  {#if entry.ip}
                    <div class="text-[10px] font-code text-text-muted">{entry.ip}</div>
                  {/if}
                </td>
                <td data-label="Details" class="px-4 py-3 font-code text-[11px] text-text-muted break-words max-w-[320px]">
                  <span class="block truncate" title={detailsOf(entry)}>{detailsOf(entry)}</span>
                </td>
              </tr>
            {/each}
          </tbody>
        </table>
      </div>
      <div class="px-4 py-2.5 border-t border-border-subtle text-[11px] text-text-muted">
        {t('Showing the most recent')} {entries.length} {t('entries')} — {t('raise the limit to page further')}
      </div>
    {/if}
  </Card>
</div>

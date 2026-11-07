<!-- 9RouterX — derived from the work of https://github.com/yanstd888/ — port and modifications by the maintainers — https://t.me/aiclub_id -->
<script lang="ts">
  import Badge from '../../lib/ui/Badge.svelte'
  import Button from '../../lib/ui/Button.svelte'
  import Card from '../../lib/ui/Card.svelte'
  import ConfirmModal from '../../lib/ui/ConfirmModal.svelte'
  import Input from '../../lib/ui/Input.svelte'
  import Toggle from '../../lib/ui/Toggle.svelte'
  import { Plus, RefreshCw } from 'lucide-svelte'
  import { api, type WebhookTarget, type WebhookTargetInput } from '../../api/client'
  import { t } from '../../lib/i18n.svelte'

  // FASE 10.4 webhook fan-out dashboard. The destinations and the fan-out
  // itself live server-side (internal/webhooks); this view drives the CRUD and
  // the per-target test ping.

  const TARGET_TYPES = ['telegram', 'discord', 'slack', 'generic']

  let rows = $state<WebhookTarget[]>([])
  let loaded = $state(false)
  let failed = $state(false)

  // The form doubles as the add form and the editor: editingId set means the
  // next submit is a PUT onto that row.
  let editingId = $state<string | null>(null)
  let formType = $state('discord')
  let formUrl = $state('')
  let formFilter = $state('')
  let saving = $state(false)
  let formError = $state('')

  let toggling = $state<Record<string, boolean>>({})
  let testing = $state<Record<string, boolean>>({})
  let testResults = $state<Record<string, { ok: boolean; error?: string } | undefined>>({})
  let deleting = $state<WebhookTarget | null>(null)
  let deletingBusy = $state(false)

  async function load() {
    try {
      rows = await api.getWebhookTargets()
      loaded = true
      failed = false
    } catch {
      failed = true
    }
  }

  $effect(() => {
    void load()
  })

  function resetForm() {
    editingId = null
    formType = 'discord'
    formUrl = ''
    formFilter = ''
    formError = ''
  }

  function startEdit(row: WebhookTarget) {
    editingId = row.id
    formType = row.type
    formUrl = row.url
    formFilter = row.filterEvents === '*' ? '' : commaList(row.filterEvents)
    formError = ''
  }

  // filterEvents is stored as "*" or a JSON array; the form takes a plain
  // comma list because that is what an operator types. Parse failures of a
  // stored value fall back to showing it verbatim.
  function commaList(filterEvents: string): string {
    try {
      const parsed: unknown = JSON.parse(filterEvents)
      if (Array.isArray(parsed) && parsed.every((v) => typeof v === 'string')) {
        return (parsed as string[]).join(', ')
      }
    } catch {}
    return filterEvents
  }

  // toFilterEvents inverts commaList: empty stays "*" (subscribe to all), a
  // comma list becomes the JSON array the API validates. Whitespace and a
  // trailing comma are tolerated.
  function toFilterEvents(raw: string): string {
    const parts = raw
      .split(',')
      .map((p) => p.trim())
      .filter(Boolean)
    if (parts.length === 0) return '*'
    return JSON.stringify(parts)
  }

  async function save() {
    saving = true
    formError = ''
    const body: WebhookTargetInput = {
      type: formType,
      url: formUrl,
      filterEvents: toFilterEvents(formFilter),
    }
    try {
      if (editingId) {
        await api.updateWebhookTarget(editingId, body)
      } else {
        await api.createWebhookTarget(body)
      }
      resetForm()
      await load()
    } catch (err) {
      formError = String(err)
    } finally {
      saving = false
    }
  }

  async function toggle(row: WebhookTarget, enabled: boolean) {
    toggling[row.id] = true
    try {
      const next = await api.updateWebhookTarget(row.id, { enabled })
      rows = rows.map((r) => (r.id === row.id ? next : r))
    } catch {
      // The next load reconciles the switch with the stored value.
    } finally {
      toggling[row.id] = false
    }
  }

  async function runTest(row: WebhookTarget) {
    testing[row.id] = true
    try {
      testResults[row.id] = await api.testWebhookTarget(row.id)
    } catch (err) {
      // A thrown fetch (404 unknown id, 400 unconfigured target) reads the
      // same way a destination refusing the ping.
      testResults[row.id] = { ok: false, error: String(err) }
    } finally {
      testing[row.id] = false
    }
  }

  async function confirmDelete() {
    if (!deleting) return
    deletingBusy = true
    try {
      await api.deleteWebhookTarget(deleting.id)
      if (editingId === deleting.id) resetForm()
      deleting = null
      await load()
    } catch {
      // The list keeps the row; the next load reconciles.
    } finally {
      deletingBusy = false
    }
  }

  function maskUrl(url: string): string {
    try {
      const parsed = new URL(url)
      const tail = url.slice(parsed.origin.length)
      const shown = tail.length > 14 ? tail.slice(0, 14) + '…' : tail || '/'
      return parsed.origin + shown
    } catch {
      return url.slice(0, 30) + (url.length > 30 ? '…' : '')
    }
  }

  function isConfigured(row: WebhookTarget): boolean {
    return row.url.trim() !== ''
  }
</script>

<div class="max-w-4xl mx-auto space-y-6">
  <Card>
    <div class="flex items-center justify-between gap-3">
      <div>
        <p class="text-xs font-medium text-text-muted uppercase tracking-wide">
          {t('Webhook targets')}
        </p>
        <p class="text-sm text-text-muted mt-1 leading-relaxed max-w-xl">
          {t(
            'Push destinations for gateway notifications — quota pressure, expired keys, security alerts. One event fans out to every enabled target subscribed to it, at most once per minute per event type.'
          )}
        </p>
      </div>
      <Button variant="ghost" size="sm" onclick={() => void load()} title={t('Refresh')}>
        <RefreshCw class="h-4 w-4" aria-hidden="true" />
      </Button>
    </div>
  </Card>

  {#if failed && !loaded}
    <p class="text-[11px] text-text-muted border border-border-subtle rounded-lg px-3 py-2">
      {t('Could not load the webhook targets — the last refresh failed.')}
    </p>
  {/if}

  <div class="space-y-3">
    {#each rows as row (row.id)}
      {@const result = testResults[row.id]}
      <Card>
        <div class="space-y-3">
          <div class="flex items-start justify-between gap-3">
            <div class="min-w-0">
              <div class="flex items-center gap-2">
                <Badge tone="info">{row.type}</Badge>
                {#if !isConfigured(row)}
                  <Badge tone="warning">{t('Not configured')}</Badge>
                {/if}
              </div>
              <p class="text-[11px] text-text-muted font-code mt-1 truncate" title={t('Destination URL is masked; edit to see it')}>
                {maskUrl(row.url)}
              </p>
            </div>
            <Toggle
              checked={row.enabled}
              disabled={toggling[row.id]}
              label={t('Enable webhook target')}
              size="sm"
              onChange={(value) => void toggle(row, value)}
            />
          </div>

          <p class="text-[11px] text-text-muted">
            <span class="font-medium">{t('Events')}</span>:
            {row.filterEvents === '*' ? t('All events') : commaList(row.filterEvents)}
          </p>

          <div class="flex flex-wrap items-center gap-2">
            <Button variant="secondary" size="sm" onclick={() => startEdit(row)}>
              {t('Edit')}
            </Button>
            <Button variant="danger" size="sm" onclick={() => (deleting = row)}>
              {t('Delete')}
            </Button>
            <Button
              variant="secondary"
              size="sm"
              disabled={testing[row.id]}
              onclick={() => void runTest(row)}
            >
              {testing[row.id] ? t('Sending…') : t('Send test')}
            </Button>
            {#if result}
              {#if result.ok}
                <Badge tone="success">{t('Delivered')}</Badge>
              {:else}
                <Badge tone="danger">{t('Failed')}</Badge>
              {/if}
            {/if}
          </div>

          {#if result && !result.ok && result.error}
            <p class="text-[11px] text-text-muted font-mono break-all">{result.error}</p>
          {/if}
        </div>
      </Card>
    {/each}
  </div>

  {#if loaded && rows.length === 0}
    <p class="text-[11px] text-text-muted border border-border-subtle rounded-lg px-3 py-2">
      {t('No webhook targets yet — add one below to start receiving notifications.')}
    </p>
  {/if}

  <Card>
    <div class="space-y-3">
      <div class="flex items-center gap-2">
        <Plus class="h-4 w-4 text-text-muted" aria-hidden="true" />
        <p class="font-semibold text-text-main text-[13px]">
          {editingId ? t('Edit webhook target') : t('Add webhook target')}
        </p>
      </div>

      <div class="grid gap-3 sm:grid-cols-2">
        <label class="block">
          <span class="text-xs text-text-muted">{t('Type')}</span>
          <select
            class="mt-1 w-full rounded-lg border border-border-subtle bg-transparent px-3 py-2 text-sm text-text-main"
            bind:value={formType}
          >
            {#each TARGET_TYPES as typ (typ)}
              <option value={typ}>{typ}</option>
            {/each}
          </select>
        </label>
        <Input
          label={t('URL')}
          placeholder="https://discord.com/api/webhooks/…"
          bind:value={formUrl}
          error={formError}
        />
        <Input
          label={t('Events (comma-separated, empty = all)')}
          placeholder="notification.quota, notification.security"
          bind:value={formFilter}
          class="sm:col-span-2"
        />
      </div>

      <div class="flex items-center gap-2">
        <Button variant="primary" size="sm" disabled={saving || !formUrl.trim()} onclick={() => void save()}>
          {saving ? t('Saving…') : editingId ? t('Save changes') : t('Add target')}
        </Button>
        {#if editingId}
          <Button variant="ghost" size="sm" onclick={resetForm}>{t('Cancel')}</Button>
        {/if}
      </div>
    </div>
  </Card>

  <ConfirmModal
    isOpen={deleting !== null}
    onClose={() => (deleting = null)}
    onConfirm={() => void confirmDelete()}
    title={t('Delete webhook target')}
    message={t('Stop sending notifications to this destination? The target is removed immediately.')}
    confirmText={t('Delete')}
    cancelText={t('Cancel')}
    variant="danger"
    loading={deletingBusy}
  />
</div>

<!-- 9RouterX — derived from the work of https://github.com/yanstd888/ — port and modifications by the maintainers — https://t.me/aiclub_id -->
<script lang="ts">
  import Badge from '../../lib/ui/Badge.svelte'
  import Button from '../../lib/ui/Button.svelte'
  import Card from '../../lib/ui/Card.svelte'
  import Toggle from '../../lib/ui/Toggle.svelte'
  import { TriangleAlert, RefreshCw } from 'lucide-svelte'
  import { api, type WebExecStatus, type WebExecCheckResult } from '../../api/client'
  import { t } from '../../lib/i18n.svelte'

  // FASE 10.0 web-executor dashboard. The pool and its 15-minute health job
  // live server-side (internal/webexec); this view draws one card per provider
  // from GET /api/webexec and drives the toggle + manual test endpoints.

  const LIVE_POLL_MS = 60_000

  let rows = $state<WebExecStatus[]>([])
  let loaded = $state(false)
  let failed = $state(false)

  // One in-flight/test result per provider, keyed the way the endpoints
  // address them. A failed session is a 200 with ok=false, so the error badge
  // carries the accounts' own messages rather than a thrown fetch.
  let toggling = $state<Record<string, boolean>>({})
  let testing = $state<Record<string, boolean>>({})
  let testResults = $state<Record<string, WebExecCheckResult | undefined>>({})

  async function load() {
    try {
      const res = await api.getWebExec()
      rows = res.providers ?? []
      loaded = true
      failed = false
    } catch {
      // Keep the last good cards on screen, like the other live views.
      failed = true
    }
  }

  $effect(() => {
    void load()
    const timer = setInterval(() => void load(), LIVE_POLL_MS)
    return () => clearInterval(timer)
  })

  async function toggle(row: WebExecStatus, enabled: boolean) {
    toggling[row.provider] = true
    try {
      const next = await api.toggleWebExec(row.provider, enabled)
      rows = rows.map((r) => (r.provider === row.provider ? next : r))
    } catch {
      // The next poll reconciles the switch with the stored settings.
    } finally {
      toggling[row.provider] = false
    }
  }

  async function runTest(row: WebExecStatus) {
    testing[row.provider] = true
    try {
      testResults[row.provider] = await api.testWebExec(row.provider)
    } catch (err) {
      // A thrown fetch (503 when the health job is off, 400 for an unknown
      // provider) is surfaced the same way a failing session is.
      testResults[row.provider] = {
        provider: row.provider,
        accounts: 0,
        ok: false,
        failures: [{ accountId: '', ok: false, error: String(err) }],
      }
    } finally {
      testing[row.provider] = false
    }
  }

  const PROVIDER_LABELS: Record<string, string> = {
    'deepseek-web': 'DeepSeek',
    'gemini-web': 'Gemini',
    'chatgpt-web': 'ChatGPT',
    'muse-web': 'Muse',
  }

  function providerLabel(provider: string): string {
    return PROVIDER_LABELS[provider] ?? provider
  }

  function sessionTone(row: WebExecStatus): 'success' | 'warning' | 'danger' | 'neutral' {
    if (!row.enabled) return 'neutral'
    return row.sessionValid ? 'success' : 'warning'
  }

  function sessionLabel(row: WebExecStatus): string {
    if (!row.enabled) return t('Disabled')
    return row.sessionValid ? t('Session valid') : t('Session unverified / cooling down')
  }

  function formatWhen(rfc3339?: string): string {
    if (!rfc3339) return '—'
    const parsed = new Date(rfc3339)
    return Number.isNaN(parsed.getTime()) ? rfc3339 : parsed.toLocaleString()
  }
</script>

<div class="max-w-4xl mx-auto space-y-6">
  <!-- Persistent compliance banner: never dismissible, it is the feature's
       one mandatory surface. -->
  <div
    class="flex items-start gap-3 rounded-lg border border-amber-400/50 bg-amber-500/10 px-4 py-3 text-amber-800 dark:text-amber-300"
    role="alert"
  >
    <TriangleAlert class="mt-0.5 h-4 w-4 shrink-0" aria-hidden="true" />
    <div class="text-xs leading-relaxed">
      <p class="font-semibold">{t('Web executors replay browser sessions')}</p>
      <p class="mt-0.5 text-amber-800/80 dark:text-amber-300/80">
        {t(
          'These executors reuse cookies from a real browser login against providers whose terms of service may prohibit automated access. Every executor ships OFF — enabling one is your decision and your responsibility.'
        )}
      </p>
    </div>
  </div>

  <Card>
    <div class="flex items-center justify-between gap-3">
      <div>
        <p class="text-xs font-medium text-text-muted uppercase tracking-wide">
          {t('Web executor session pool')}
        </p>
        <p class="text-sm text-text-muted mt-1 leading-relaxed max-w-xl">
          {t(
            'One card per web-session executor. A 15-minute server job revalidates stored sessions and refreshes the rate budget; the switch and the health check below drive the same settings the job reads.'
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
      {t('Could not load the web executor status — the last refresh failed. It retries every 60 seconds.')}
    </p>
  {/if}

  <div class="grid gap-4 sm:grid-cols-2">
    {#each rows as row (row.provider)}
      {@const result = testResults[row.provider]}
      <Card>
        <div class="space-y-3">
          <div class="flex items-start justify-between gap-3">
            <div>
              <p class="font-semibold text-text-main text-[13px]">{providerLabel(row.provider)}</p>
              <p class="text-[11px] text-text-muted font-code">{row.provider}</p>
            </div>
            <Toggle
              checked={row.enabled}
              disabled={toggling[row.provider]}
              label={t('Enable') + ' ' + providerLabel(row.provider)}
              size="sm"
              onChange={(value) => void toggle(row, value)}
            />
          </div>

          <div class="flex flex-wrap gap-2">
            <Badge tone={sessionTone(row)} dot>{sessionLabel(row)}</Badge>
            <Badge tone="info">
              {row.rateLimitRemaining} {t('req/min left')}
            </Badge>
          </div>

          <dl class="text-[11px] text-text-muted space-y-1">
            <div class="flex justify-between gap-2">
              <dt>{t('Last used')}</dt>
              <dd class="text-text-main font-mono">{formatWhen(row.lastUsed)}</dd>
            </div>
          </dl>

          <div class="flex items-center gap-2">
            <Button
              variant="secondary"
              size="sm"
              disabled={testing[row.provider]}
              onclick={() => void runTest(row)}
            >
              {testing[row.provider] ? t('Checking…') : t('Run health check')}
            </Button>
            {#if result}
              {#if result.ok}
                <Badge tone="success">{t('OK')} · {result.accounts} {t('accounts')}</Badge>
              {:else}
                <Badge tone="danger">{t('Failed')}</Badge>
              {/if}
            {/if}
          </div>

          {#if result && !result.ok}
            <ul class="text-[11px] text-text-muted space-y-0.5">
              {#each result.failures ?? [] as failure (failure.accountId)}
                <li class="font-mono truncate" title={failure.error}>
                  {failure.accountId || '—'}: {failure.error}
                </li>
              {/each}
              {#if !result.failures?.length}
                <li>{t('The health job is not running — start the server job before testing.')}</li>
              {/if}
            </ul>
          {/if}
        </div>
      </Card>
    {/each}
  </div>

  {#if loaded && rows.length === 0}
    <p class="text-[11px] text-text-muted border border-border-subtle rounded-lg px-3 py-2">
      {t('No web executors are registered in this build.')}
    </p>
  {/if}
</div>

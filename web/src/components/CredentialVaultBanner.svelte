<!-- 9RouterX — all source code port and modification by https://github.com/yanstd888/ — https://t.me/aiclub_id -->
<script lang="ts">
  import { onMount } from 'svelte'
  import { ShieldAlert } from 'lucide-svelte'
  import { api } from '../api/client'
  import { t } from '../lib/i18n.svelte'

  /**
   * The credential-vault state. The vault is optional: on an install without
   * ROUTER_MASTER_KEY, provider credentials and browser sessions are written to
   * SQLite in the clear (internal/vault, internal/sessionvault). That is a
   * deliberate degradation, so the operator is told rather than left to
   * discover it from the schema.
   *
   * Renders nothing while the status is unknown — a failed read must not become
   * a fake warning, the same rule the TLS-stealth banner follows.
   */
  let status = $state<{ enabled: boolean } | null>(null)

  onMount(async () => {
    try {
      status = await api.getVaultStatus()
    } catch {
      status = null
    }
  })
</script>

{#if status && !status.enabled}
  <div
    class="mb-4 flex items-start gap-3 rounded-lg border border-amber-400/50 bg-amber-500/10 px-4 py-3 text-amber-800 dark:text-amber-300"
    role="alert"
  >
    <ShieldAlert class="mt-0.5 h-4 w-4 shrink-0" aria-hidden="true" />
    <div class="text-xs leading-relaxed">
      <p class="font-semibold">{t('Credential encryption: OFF')}</p>
      <p class="mt-0.5 text-amber-800/80 dark:text-amber-300/80">
        {t(
          'Provider credentials and browser sessions are stored in the database unencrypted. Set ROUTER_MASTER_KEY and restart the gateway to seal them.'
        )}
      </p>
    </div>
  </div>
{/if}

<!-- 9RouterX — all source code port and modification by https://github.com/yanstd888/ — https://t.me/aiclub_id -->
<script lang="ts">
  import { onMount } from 'svelte'
  import { TriangleAlert } from 'lucide-svelte'
  import { api } from '../api/client'

  /**
   * FASE 10.7 TLS stealth compliance banner. The server answers
   * /api/tls-stealth/status with the profile and the legal notice; while the
   * flag is on this banner renders permanently — there is no dismiss, because
   * the notice is the feature's one mandatory surface (it repeats daily in the
   * notification feed too). A failed status read renders nothing: an endpoint
   * that is down must not become a fake warning.
   */
  let status = $state<{ enabled: boolean; activeProfile: string; legalNotice: string } | null>(null)

  onMount(async () => {
    try {
      status = await api.getTlsStealthStatus()
    } catch {
      status = null
    }
  })
</script>

{#if status?.enabled}
  <div
    class="mb-4 flex items-start gap-3 rounded-lg border border-amber-400/50 bg-amber-500/10 px-4 py-3 text-amber-800 dark:text-amber-300"
    role="alert"
  >
    <TriangleAlert class="mt-0.5 h-4 w-4 shrink-0" aria-hidden="true" />
    <div class="text-xs leading-relaxed">
      <p class="font-semibold">
        TLS stealth is enabled{status.activeProfile ? ` (profile: ${status.activeProfile})` : ''}
      </p>
      <p class="mt-0.5 text-amber-800/80 dark:text-amber-300/80">{status.legalNotice}</p>
    </div>
  </div>
{/if}

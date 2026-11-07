<!-- 9RouterX — all source code port and modification by https://github.com/yanstd888/ — https://t.me/aiclub_id -->
<script lang="ts">
  import { onMount } from 'svelte'
  import { api, type UpdateCheckResponse } from '../api/client'
  import { copyToClipboard } from '../lib/clipboard'
  import {
    NPM_PACKAGE_CMD,
    assetForUserAgent,
    isVersionSkipped,
    releaseUrlTrust,
    skipVersion
  } from '../lib/updateBanner'

  /**
   * Update banner (TASK-9routerX updater A): renders when the cached
   * /api/update/check reports a newer release AND the operator has not
   * skipped that version. One check per dashboard session — the component
   * mounts once in the app shell, and the server answers from its cache.
   *
   * Channel behaviors (docs/updater-contract.md):
   *  - binary → "Update now" opens the release page (only ever a
   *    github.com/yanstd888 URL) and shows the per-OS asset name + checksum.
   *  - npm → the static upgrade command with a copy button.
   *  - dev → information plus the release link.
   *
   * There is deliberately NO auto-download and NO self-replace here — the
   * banner is information only; `9routerx update` (UpdateModal) stays the
   * install path. Remote strings (notes, versions, URLs) render as escaped
   * text; nothing from the release feed is ever HTML.
   */
  let check = $state<UpdateCheckResponse | null>(null)
  let dismissed = $state(false)
  let copied = $state(false)

  onMount(async () => {
    try {
      const result = await api.getUpdateCheck()
      // A rollback, an equal version, or a skipped release renders nothing —
      // the banner is strictly a "newer release exists" surface.
      if (result?.hasUpdate && !isVersionSkipped(result.latestVersion)) {
        check = result
      }
    } catch {
      // A failed check renders nothing: the endpoint degrades gracefully and
      // so must the UI. The server already logged the cause.
      check = null
    }
  })

  const trustedRelease = $derived(releaseUrlTrust(check?.releaseUrl) === 'trusted')
  const assetName = $derived(
    typeof navigator !== 'undefined' ? assetForUserAgent(navigator.userAgent) : ''
  )

  function dismiss() {
    dismissed = true
  }

  function skipThisVersion() {
    if (check?.latestVersion) skipVersion(check.latestVersion)
    dismissed = true
  }

  function openRelease() {
    // Checked again at click time: the trust decision never lives in markup.
    if (!trustedRelease || !check?.releaseUrl) return
    window.open(check.releaseUrl, '_blank', 'noopener,noreferrer')
  }

  async function copyNpmCommand() {
    const ok = await copyToClipboard(NPM_PACKAGE_CMD)
    if (ok) {
      copied = true
      setTimeout(() => {
        copied = false
      }, 2000)
    }
  }
</script>

{#if check && !dismissed}
  <div
    class="mb-4 flex flex-col sm:flex-row sm:items-start gap-3 rounded-lg border border-amber-400/50 bg-amber-500/10 px-4 py-3 text-amber-800 dark:text-amber-300"
    role="status"
  >
    <span class="material-symbols-outlined mt-0.5 text-[18px] shrink-0" aria-hidden="true">upgrade</span>

    <div class="flex-1 min-w-0 text-xs leading-relaxed">
      <p class="font-semibold">
        New version available: v{check.latestVersion}
        {#if check.channel === 'dev'}
          <span class="ml-1 font-code text-[10px] uppercase text-text-muted border border-border-subtle rounded px-1.5 py-0.5">dev build</span>
        {/if}
      </p>

      {#if check.releaseNotes}
        <p class="mt-0.5 text-amber-800/80 dark:text-amber-300/80 line-clamp-2 whitespace-pre-line">
          {check.releaseNotes}
        </p>
      {/if}

      {#if check.channel === 'binary'}
        <p class="mt-1 font-code text-[11px] break-all">
          <span class="text-amber-800/70 dark:text-amber-300/70">asset:</span>
          {assetName}
          {#if check.sha256}
            <span class="block break-all text-amber-800/70 dark:text-amber-300/70">
              sha256: <span class="break-all">{check.sha256}</span>
            </span>
          {/if}
        </p>
      {:else if check.channel === 'npm'}
        <div class="mt-1.5 flex flex-wrap items-center gap-2 min-w-0">
          <code class="font-mono text-[11px] px-2 py-1 rounded bg-surface border border-border-subtle text-text-main break-all select-all">
            {NPM_PACKAGE_CMD}
          </code>
          <button
            type="button"
            onclick={copyNpmCommand}
            class="px-2.5 py-1.5 text-[11px] rounded-lg bg-surface hover:bg-surface-2 border border-border-subtle text-text-main transition-colors cursor-pointer flex items-center gap-1"
          >
            <span class="material-symbols-outlined text-[14px]" aria-hidden="true">content_copy</span>
            <span>{copied ? 'Copied!' : 'Copy'}</span>
          </button>
        </div>
      {/if}
    </div>

    <div class="flex flex-wrap sm:flex-nowrap items-center gap-2 shrink-0 w-full sm:w-auto">
      {#if check.channel === 'binary' && trustedRelease}
        <button
          type="button"
          onclick={openRelease}
          class="flex-1 sm:flex-none justify-center px-3.5 py-2 text-xs font-semibold rounded-lg bg-amber-500 hover:bg-amber-600 text-white transition-colors cursor-pointer flex items-center gap-1.5"
        >
          <span class="material-symbols-outlined text-[15px]" aria-hidden="true">download</span>
          <span>Update now</span>
        </button>
      {:else if check.channel !== 'npm' && trustedRelease}
        <a
          href={check.releaseUrl}
          target="_blank"
          rel="noopener noreferrer"
          class="flex-1 sm:flex-none justify-center px-3.5 py-2 text-xs font-semibold rounded-lg bg-surface hover:bg-surface-2 border border-border-subtle text-text-main transition-colors cursor-pointer flex items-center gap-1.5"
        >
          <span class="material-symbols-outlined text-[15px]" aria-hidden="true">open_in_new</span>
          <span>View release</span>
        </a>
      {/if}

      {#if check.channel !== 'dev'}
        <button
          type="button"
          onclick={skipThisVersion}
          class="flex-1 sm:flex-none justify-center px-3 py-2 text-xs rounded-lg text-text-muted hover:text-text-main hover:bg-surface-2 transition-colors cursor-pointer"
        >
          Skip this version
        </button>
      {/if}

      <button
        type="button"
        onclick={dismiss}
        class="p-1.5 rounded-lg text-text-muted hover:text-text-main hover:bg-surface-2 transition-colors cursor-pointer"
        aria-label="Dismiss update banner"
      >
        <span class="material-symbols-outlined text-[18px]" aria-hidden="true">close</span>
      </button>
    </div>
  </div>
{/if}

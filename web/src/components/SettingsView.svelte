<!-- 9RouterX — derived from the work of https://github.com/yanstd888/ — port and modifications by the maintainers — https://t.me/aiclub_id -->
<script lang="ts">
  import { notifications } from '../lib/notifications'
  import {
    Check,
    Copy,
    Database,
    Download,
    Key,
    Loader2,
    Lock,
    RefreshCw,
    Save,
    Shield,
    Upload,
    Zap
  } from 'lucide-svelte'
  import { onMount } from 'svelte'
  import { api, type Settings, type UpdateCheckResponse } from '../api/client'
  import Checkbox from '../lib/ui/Checkbox.svelte'
  import ModelHealthPanel from './settings/ModelHealthPanel.svelte'

  let {
    settings = {},
    onRefresh
  }: {
    settings: Settings
    onRefresh: () => void
  } = $props()

  let formData = $state<Settings>({})
  let isSaving = $state(false)
  let resetProvider = $state('antigravity')
  let isResetting = $state(false)
  // Detected distribution channel (binary | npm | dev), read from the update
  // check the engine caches — one cheap cached request when the page opens.
  let updateInfo = $state<UpdateCheckResponse | null>(null)

  $effect(() => {
    formData = { ...settings }
  })

  onMount(async () => {
    try {
      updateInfo = await api.getUpdateCheck()
    } catch {
      updateInfo = null
    }
  })

  async function handleSave() {
    try {
      isSaving = true
      await api.updateSettings(formData)
      onRefresh()
      notifications.success('System configuration and token savers updated successfully!')
    } catch (err) {
      notifications.error(`Failed to save settings: ${err instanceof Error ? err.message : String(err)}`)
    } finally {
      isSaving = false
    }
  }

  async function handleResetHealth() {
    try {
      isResetting = true
      await api.resetHealth(resetProvider)
      notifications.success(`Health state and rate-limit locks for '${resetProvider}' have been reset.`)
    } catch (err) {
      notifications.error(`Failed to reset health: ${err instanceof Error ? err.message : String(err)}`)
    } finally {
      isResetting = false
    }
  }
</script>

<div class="space-y-6">
  <!-- Page header -->
  <div class="flex flex-col sm:flex-row sm:items-end justify-between gap-4">
    <div class="space-y-1.5">
      <div class="flex items-center gap-2">
        <span class="font-code text-[10px] uppercase tracking-wider text-brand-500 px-2 py-0.5 rounded bg-brand-500/10 border border-brand-500/25 font-bold">
          System Control
        </span>
      </div>
      <h1 class="font-headline text-2xl sm:text-3xl font-bold text-text-main tracking-tight">
        Local Mode & Gateway Configuration
      </h1>
      <p class="font-body text-xs sm:text-sm text-text-muted max-w-2xl leading-relaxed">
        Manage persistent SQLite state, master auth credentials, and load routing algorithms.
      </p>
    </div>

    <button
      type="button"
      onclick={handleSave}
      disabled={isSaving}
      class="flex items-center gap-1.5 px-4 py-2 rounded-lg bg-brand-500 hover:bg-brand-600 text-white font-body text-xs font-bold shadow-md shadow-brand-500/25 transition cursor-pointer"
    >
      {#if isSaving}
        <Loader2 class="w-3.5 h-3.5 animate-spin" />
      {:else}
        <Save class="w-3.5 h-3.5" />
      {/if}
      <span>Save System State</span>
    </button>
  </div>

  <!-- 3 Main Configuration Cards (Stitch Design) -->
  <div class="grid grid-cols-1 lg:grid-cols-2 gap-4">
    <!-- Card 1: Runtime Storage -->
    <div class="p-6 rounded-xl bg-surface border border-border space-y-4 shadow-xl flex flex-col justify-between">
      <div class="space-y-3">
        <div class="flex items-center justify-between">
          <div class="flex items-center gap-2">
            <Database class="w-4 h-4 text-success" />
            <h3 class="font-headline text-sm font-bold text-text-main">Runtime Storage</h3>
          </div>
          <span class="font-code text-[10px] text-success bg-success/10 px-2 py-0.5 rounded border border-success/20">
            Server managed
          </span>
        </div>

        <div class="p-3 rounded-lg bg-bg border border-border space-y-1 font-code text-xs">
          <div class="text-[10px] text-text-muted uppercase">Default Database Location</div>
          <div class="text-info font-semibold">~/.9router/db/data.sqlite</div>
          <div class="text-[10px] text-text-subtle pt-1">SQLite WAL • Configurable with DB_PATH or DATA_DIR</div>
          <div class="text-[10px] text-text-subtle pt-1">No automatic versioned schema migration; initialize a fresh database from a compatible upstream schema.</div>
        </div>
      </div>

      <div class="flex items-center gap-2 pt-2">
        <button
          type="button"
          class="flex-1 flex items-center justify-center gap-1.5 py-2 rounded-lg bg-surface-2 hover:bg-surface-3 text-text-main font-body text-xs font-semibold border border-border transition cursor-pointer"
        >
          <Download class="w-3.5 h-3.5 text-info" />
          <span>Download Backup</span>
        </button>

        <button
          type="button"
          class="flex-1 flex items-center justify-center gap-1.5 py-2 rounded-lg bg-surface-2 hover:bg-surface-3 text-text-main font-body text-xs font-semibold border border-border transition cursor-pointer"
        >
          <Upload class="w-3.5 h-3.5 text-brand-400" />
          <span>Import Backup</span>
        </button>
      </div>
    </div>

    <!-- Card 2: Routing Strategy & Token Saver Engines -->
    <div class="p-6 rounded-xl bg-surface border border-border space-y-4 shadow-xl">
      <div class="flex items-center justify-between">
        <div class="flex items-center gap-2">
          <Zap class="w-4 h-4 text-brand-500" />
          <h3 class="font-headline text-sm font-bold text-text-main">Routing Strategy & Token Saver</h3>
        </div>
        <span class="font-code text-[10px] text-info bg-info/10 px-2 py-0.5 rounded border border-info/20">
          Active Engine
        </span>
      </div>

      <div class="space-y-3 font-body text-xs">
        <!-- RTK -->
        <div class="flex items-center justify-between p-3 rounded-lg bg-bg border border-border">
          <div>
            <div class="font-bold text-text-main flex items-center gap-1.5">
              <span>RTK Compression</span>
              <span class="text-[9px] px-1.5 py-0.2 rounded bg-success/15 text-success font-code">60-80% Savings</span>
            </div>
            <div class="text-[11px] text-text-muted">Filters repetitive CLI, build, and git output</div>
          </div>
          <Checkbox
            checked={!!formData.rtkEnabled}
            tone="success"
            onchange={(v) => (formData.rtkEnabled = v)}
          />
        </div>

        <!-- Caveman -->
        <div class="flex items-center justify-between p-3 rounded-lg bg-bg border border-border">
          <div>
            <div class="font-bold text-text-main">Caveman Terse Output</div>
            <div class="text-[11px] text-text-muted">Instructs model to reply in concise, zero-filler language</div>
          </div>
          <Checkbox
            checked={!!formData.cavemanEnabled}
            onchange={(v) => (formData.cavemanEnabled = v)}
          />
        </div>

        <!-- Ponytail -->
        <div class="flex items-center justify-between p-3 rounded-lg bg-bg border border-border">
          <div>
            <div class="font-bold text-text-main">Ponytail Code Style</div>
            <div class="text-[11px] text-text-muted">Enforces pragmatic, minimal boilerplate code style</div>
          </div>
          <Checkbox
            checked={!!formData.ponytailEnabled}
            onchange={(v) => (formData.ponytailEnabled = v)}
          />
        </div>

        <!-- ADHD -->
        <div class="flex items-center justify-between p-3 rounded-lg bg-bg border border-border">
          <div>
            <div class="font-bold text-text-main">I have ADHD (Action-First)</div>
            <div class="text-[11px] text-text-muted">Action-first output: command/code first, bounded steps, no fluff</div>
          </div>
          <Checkbox
            checked={!!formData.adhdEnabled}
            onchange={(v) => (formData.adhdEnabled = v)}
          />
        </div>
      </div>
    </div>
  </div>

  <!-- Bottom Row: Security & Rate Limit Reset -->
  <div class="grid grid-cols-1 lg:grid-cols-2 gap-4">
    <!-- Security & Master Access -->
    <div class="p-6 rounded-xl bg-surface border border-border space-y-4 shadow-xl">
      <div class="flex items-center justify-between">
        <div class="flex items-center gap-2">
          <Shield class="w-4 h-4 text-info" />
          <h3 class="font-headline text-sm font-bold text-text-main">Security & Master Access</h3>
        </div>
        <div class="flex items-center gap-2 font-code text-xs">
          <span class="text-text-muted">Require Login:</span>
          <Checkbox
            checked={!!formData.requireApiKey}
            onchange={(v) => (formData.requireApiKey = v)}
          />
        </div>
      </div>

      <p class="font-body text-xs text-text-muted">
        When enabled, client requests to <code class="font-code text-info">/v1/chat/completions</code> must provide a valid Bearer token from the CLI & Remote Access table.
      </p>
    </div>

    <!-- Updates: "Check updates" toggle + detected distribution channel -->
    <div class="p-6 rounded-xl bg-surface border border-border space-y-4 shadow-xl">
      <div class="flex items-center justify-between">
        <div class="flex items-center gap-2">
          <span class="material-symbols-outlined text-[16px] text-brand-500">system_update_alt</span>
          <h3 class="font-headline text-sm font-bold text-text-main">Updates</h3>
        </div>
        {#if updateInfo?.channel}
          <span
            class="font-code text-[10px] px-2 py-0.5 rounded border uppercase {'text-info bg-info/10 border-info/20'}"
            title="How this build was installed (binary, npm, or built from source)"
          >
            channel: {updateInfo.channel}
          </span>
        {/if}
      </div>

      <div class="flex items-center justify-between gap-2">
        <span class="font-body text-xs text-text-main">Check updates</span>
        <Checkbox
          checked={formData.updateCheckEnabled ?? true}
          onchange={(v) => (formData.updateCheckEnabled = v)}
        />
      </div>

      <p class="font-body text-xs text-text-muted">
        Ask the 9RouterX release feed once per dashboard session and every 6 hours in the background
        (UPDATE_CHECK_INTERVAL). Off stops the version banner; installing updates stays a manual
        <code class="font-code text-info">9routerx update</code>.
      </p>
    </div>

    <!-- Health & Rate Limit Cache Reset -->
    <div class="p-6 rounded-xl bg-surface border border-border space-y-4 shadow-xl">
      <div class="flex items-center gap-2">
        <RefreshCw class="w-4 h-4 text-brand-400" />
        <h3 class="font-headline text-sm font-bold text-text-main">Failover Health Cache Reset</h3>
      </div>

      <p class="font-body text-xs text-text-muted">
        If an upstream credential hit HTTP 429 and was placed in cooldown, clear its lockout state manually:
      </p>

      <div class="flex items-center gap-2">
        <select
          bind:value={resetProvider}
          class="flex-1 px-3 py-2 rounded-lg bg-bg border border-border font-code text-xs text-text-main focus:outline-none focus:border-brand-500"
        >
          <option value="antigravity">antigravity (Google AI)</option>
          <option value="freebuff">freebuff (Codebuff)</option>
          <option value="clinepass">clinepass</option>
          <option value="deepseek">deepseek</option>
          <option value="groq">groq</option>
        </select>

        <button
          type="button"
          onclick={handleResetHealth}
          disabled={isResetting}
          class="px-4 py-2 rounded-lg bg-surface-2 hover:bg-surface-3 text-text-main font-body text-xs font-bold flex items-center gap-1.5 transition cursor-pointer border border-border"
        >
          {#if isResetting}
            <Loader2 class="w-3.5 h-3.5 animate-spin text-info" />
          {:else}
            <Check class="w-3.5 h-3.5 text-success" />
          {/if}
          <span>Reset Cooldown</span>
        </button>
      </div>
    </div>
    <!-- Card 3: Model Health -->
    <div class="p-6 rounded-xl bg-surface border border-border space-y-4 shadow-xl lg:col-span-2">
      <div class="flex items-center gap-2">
        <span class="material-symbols-outlined text-[16px] text-brand-500">monitor_heart</span>
        <h3 class="font-headline text-sm font-bold text-text-main">Model Health</h3>
        <span class="font-code text-[10px] text-text-muted bg-surface-2 px-2 py-0.5 rounded border border-border">
          automatic
        </span>
      </div>
      <ModelHealthPanel />
    </div>
  </div>
</div>

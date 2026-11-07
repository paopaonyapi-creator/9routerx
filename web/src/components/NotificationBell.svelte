<!-- 9RouterX — ported & modified by https://github.com/yanstd888/ — https://t.me/aiclub_id -->
<script lang="ts">
  import { onMount } from 'svelte'
  import { api, type NotificationItem } from '../api/client'
  import { Bell, Check, Trash2, AlertTriangle, AlertOctagon, Info } from 'lucide-svelte'

  let isOpen = $state(false)
  let items = $state<NotificationItem[]>([])
  let unread = $state(0)
  let dropdownRef: HTMLDivElement | null = $state(null)

  async function loadNotifications() {
    try {
      const res = await api.getNotifications(20)
      items = res.notifications || []
      unread = res.unread || 0
    } catch {
      // Background poll failure must not break UI
    }
  }

  async function markAllRead() {
    unread = 0
    items = items.map((n) => ({ ...n, read: true }))
    try {
      const res = await api.postNotifications({ action: 'read', all: true })
      if (typeof res.unread === 'number') {
        unread = res.unread
      }
    } catch {
      loadNotifications()
    }
  }

  async function clearAll() {
    items = []
    unread = 0
    try {
      const res = await api.postNotifications({ action: 'clear', all: true })
      if (typeof res.unread === 'number') {
        unread = res.unread
      }
    } catch {
      loadNotifications()
    }
  }

  async function markReadOne(n: NotificationItem) {
    items = items.map((x) => (x.id === n.id ? { ...x, read: true } : x))
    if (unread > 0) unread--
    try {
      const res = await api.postNotifications({ action: 'read', id: n.id })
      if (typeof res.unread === 'number') {
        unread = res.unread
      }
    } catch {
      loadNotifications()
    }
  }

  async function dismissOne(n: NotificationItem) {
    items = items.filter((x) => x.id !== n.id)
    if (!n.read && unread > 0) unread--
    try {
      const res = await api.postNotifications({ action: 'clear', id: n.id })
      if (typeof res.unread === 'number') {
        unread = res.unread
      }
    } catch {
      loadNotifications()
    }
  }

  function formatTime(iso: string): string {
    if (!iso) return ''
    try {
      const d = new Date(iso)
      return d.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' })
    } catch {
      return iso
    }
  }

  function severityColor(sev: string): { icon: typeof Info; text: string; bg: string } {
    switch (sev) {
      case 'critical':
      case 'error':
        return { icon: AlertOctagon, text: 'text-red-500', bg: 'bg-red-500/10' }
      case 'warn':
      case 'warning':
        return { icon: AlertTriangle, text: 'text-amber-500', bg: 'bg-amber-500/10' }
      default:
        return { icon: Info, text: 'text-primary', bg: 'bg-primary/10' }
    }
  }

  onMount(() => {
    loadNotifications()
    const timer = setInterval(loadNotifications, 30000)

    function handleClickOutside(event: MouseEvent) {
      if (isOpen && dropdownRef && !dropdownRef.contains(event.target as Node)) {
        isOpen = false
      }
    }
    window.addEventListener('click', handleClickOutside)

    return () => {
      clearInterval(timer)
      window.removeEventListener('click', handleClickOutside)
    }
  })
</script>

<div class="relative" bind:this={dropdownRef}>
  <button
    type="button"
    onclick={(e) => {
      e.stopPropagation()
      isOpen = !isOpen
      if (isOpen) loadNotifications()
    }}
    aria-label="Notifications ({unread} unread)"
    data-tip="Notifications ({unread} unread)"
    class="x9-tip x9-tip-br relative flex items-center justify-center size-8 rounded-lg border border-border bg-surface-2 text-text-muted transition-colors hover:border-brand-500/40 hover:text-text-main cursor-pointer"
  >
    <Bell class="h-[18px] w-[18px]" />
    {#if unread > 0}
      <span
        class="absolute -top-1 -right-1 flex h-4 min-w-[16px] items-center justify-center rounded-full bg-red-500 px-1 text-[9px] font-bold text-white shadow-sm animate-pulse"
      >
        {unread > 99 ? '99+' : unread}
      </span>
    {/if}
  </button>

  {#if isOpen}
    <div
      class="absolute right-0 mt-2 w-80 sm:w-96 rounded-2xl border border-border bg-surface shadow-2xl z-50 overflow-hidden font-body animate-in fade-in zoom-in-95"
    >
      <!-- Header -->
      <div class="flex items-center justify-between border-b border-border px-4 py-3 bg-surface-2">
        <div class="flex items-center gap-2">
          <span class="text-xs font-semibold text-text-main">Notifications</span>
          {#if unread > 0}
            <span class="px-1.5 py-0.5 rounded-full bg-red-500/10 text-red-500 text-[10px] font-bold">
              {unread} new
            </span>
          {/if}
        </div>
        <div class="flex items-center gap-2">
          {#if unread > 0}
            <button
              type="button"
              onclick={markAllRead}
              class="text-[11px] text-primary hover:underline cursor-pointer"
            >
              Mark all read
            </button>
          {/if}
          {#if items.length > 0}
            <button
              type="button"
              onclick={clearAll}
              class="text-[11px] text-text-muted hover:text-danger cursor-pointer ml-1"
              title="Clear all notifications"
            >
              Clear all
            </button>
          {/if}
        </div>
      </div>

      <!-- Feed list -->
      <div class="max-h-80 overflow-y-auto divide-y divide-border/60">
        {#if items.length === 0}
          <div class="py-10 text-center text-text-muted">
            <Bell class="w-6 h-6 mx-auto mb-2 opacity-30" />
            <p class="text-xs font-medium">Nothing to report</p>
            <p class="text-[11px] text-text-subtle mt-0.5">Quota warnings and system alerts will appear here</p>
          </div>
        {:else}
          {#each items as item (item.id)}
            {@const style = severityColor(item.severity)}
            {@const IconComponent = style.icon}
            <div
              class="flex items-start justify-between gap-2.5 p-3.5 transition-colors hover:bg-surface-2/60 {item.read
                ? 'opacity-65'
                : 'bg-primary/[0.02]'}"
            >
              <div class="flex items-start gap-2.5 min-w-0 flex-1">
                <div class="p-1.5 rounded-lg {style.bg} {style.text} shrink-0 mt-0.5">
                  <IconComponent class="w-3.5 h-3.5" />
                </div>
                <div class="min-w-0 flex-1">
                  <div class="flex items-center gap-1.5">
                    <p class="text-xs font-semibold text-text-main truncate leading-tight">
                      {item.title}
                    </p>
                    {#if !item.read}
                      <span class="size-1.5 rounded-full bg-primary shrink-0"></span>
                    {/if}
                  </div>
                  {#if item.body}
                    <p class="text-[11px] text-text-muted mt-1 leading-snug break-words">
                      {item.body}
                    </p>
                  {/if}
                  <p class="text-[10px] text-text-subtle mt-1 font-code">
                    {formatTime(item.at)}
                  </p>
                </div>
              </div>

              <!-- Item Actions -->
              <div class="flex items-center gap-1 shrink-0 pt-0.5">
                {#if !item.read}
                  <button
                    type="button"
                    onclick={() => markReadOne(item)}
                    title="Mark as read"
                    class="p-1 rounded text-text-muted hover:text-text-main hover:bg-surface-3 transition cursor-pointer"
                  >
                    <Check class="w-3.5 h-3.5" />
                  </button>
                {/if}
                <button
                  type="button"
                  onclick={() => dismissOne(item)}
                  title="Dismiss notification"
                  class="p-1 rounded text-text-muted hover:text-danger hover:bg-danger/10 transition cursor-pointer"
                >
                  <Trash2 class="w-3.5 h-3.5" />
                </button>
              </div>
            </div>
          {/each}
        {/if}
      </div>
    </div>
  {/if}
</div>

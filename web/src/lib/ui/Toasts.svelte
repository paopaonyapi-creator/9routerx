<!-- 9RouterX — all source code port and modification by https://github.com/yanstd888/ — https://t.me/aiclub_id -->
<script lang="ts">
  // Port of the toast stack in upstream DashboardLayout.js.
  import { onMount } from 'svelte'
  import { notifications, type NotificationType } from '../notifications'
  import { routingBurst } from '../routingBurst'

  function toastStyle(type: NotificationType): { wrapper: string; icon: string } {
    if (type === 'success') {
      return {
        wrapper: 'text-[var(--app-success)]',
        icon: 'check_circle',
      }
    }
    if (type === 'error') {
      return {
        wrapper: 'text-[var(--app-danger)]',
        icon: 'error',
      }
    }
    if (type === 'warning') {
      return {
        wrapper: 'text-[var(--app-warning)]',
        icon: 'warning',
      }
    }
    return {
      wrapper: 'text-[var(--app-primary)]',
      icon: 'info',
    }
  }

  // Glyph spec §5.4: on a phone the toast is full-width at the bottom and
  // auto-dismisses after 4s. The store keeps its own timers for the desktop
  // durations; this effect only schedules the existing removeNotification at
  // the 4s mark when the viewport is a phone. Local bookkeeping only — no
  // feature state touched.
  onMount(() => {
    if (!window.matchMedia('(max-width: 767px)').matches) return
    const scheduled = new Set<number>()
    const unsubscribe = notifications.subscribe((list) => {
      for (const n of list) {
        if (scheduled.has(n.id)) continue
        scheduled.add(n.id)
        setTimeout(() => notifications.removeNotification(n.id), 4000)
      }
    })
    return () => {
      unsubscribe()
      scheduled.clear()
    }
  })

  // Live routing flow (TASK-9routerX-anim-bg 2.5.2), wiring point 1 of 2:
  // every toast show fires a background data burst (no coords — the burst
  // picks a route itself; the AsciiRoutingBg component consumes this).
  onMount(() => {
    const seen = new Set<number>()
    const unsubscribe = notifications.subscribe((list) => {
      for (const n of list) {
        if (seen.has(n.id)) continue
        seen.add(n.id)
        routingBurst()
      }
    })
    return unsubscribe
  })
</script>

<!-- Bottom-right glass cards (spec §4); on a phone full-width at the bottom
     edge (spec §5.4). The card look lives on the .glyph-toast class in
     index.css. The stack itself is pointer-transparent: it spans the whole
     bottom strip (full width on a phone, 380px on desktop) exactly where
     modal footers and table action rows sit, so a visible toast must not
     make those dead for its 4-5s lifetime — the cards take the clicks, the
     layer between and around them lets them through. -->
<div
  class="pointer-events-none fixed bottom-4 left-4 right-4 sm:left-auto sm:right-4 sm:w-[min(92vw,380px)] z-[80] flex flex-col gap-2"
>
  {#each $notifications as n (n.id)}
    {@const style = toastStyle(n.type)}
    <div class="glyph-toast pointer-events-auto px-3 py-2 {style.wrapper}">
      <div class="flex items-start gap-2">
        <span class="material-symbols-outlined text-[18px] leading-5">{style.icon}</span>
        <div class="min-w-0 flex-1">
          {#if n.title}<p class="text-xs font-semibold mb-0.5">{n.title}</p>{/if}
          <p class="text-xs font-code whitespace-pre-wrap break-words">{n.message}</p>
        </div>
        {#if n.dismissible}
          <button
            type="button"
            onclick={() => notifications.removeNotification(n.id)}
            class="text-current/70 hover:text-current cursor-pointer"
            aria-label="Dismiss notification"
          >
            <span class="material-symbols-outlined text-[16px]">close</span>
          </button>
        {/if}
      </div>
      <!-- Thin accent drain line: the bar reads the duration the store already
           carries (markup only — no timer logic added); a toast with duration 0
           never auto-dismisses, so it gets no bar. -->
      {#if n.duration > 0}
        <span
          class="glyph-toast-progress"
          aria-hidden="true"
          style="--toast-dur:{n.duration}ms"
        ></span>
      {/if}
    </div>
  {/each}
</div>

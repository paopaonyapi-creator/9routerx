// 9RouterX — ported & modified by https://github.com/yanstd888/ — https://t.me/aiclub_id
/**
 * Tiny module-level event bus for the "Live Routing Flow" background
 * (spec TASK-9routerX-anim-bg 2.5.2). UI actions call `routingBurst()` to
 * make the ASCII background fire one fast pulse plus a small glyph spray;
 * the background component subscribes with `onRoutingBurst`.
 *
 * Coordinates are optional viewport px. Without them the background picks a
 * route itself (e.g. toast shows have no meaningful click position).
 *
 * Wiring points (kept to two by design):
 *   - web/src/lib/ui/Toasts.svelte  — every toast show fires a burst.
 *   - web/src/components/LoginView.svelte — successful login submit.
 */

type BurstListener = (x?: number, y?: number) => void

const listeners = new Set<BurstListener>()

/** Fire a routing burst. Safe to call before the background mounts (no-op). */
export function routingBurst(x?: number, y?: number): void {
  for (const fn of listeners) {
    try {
      fn(x, y)
    } catch {
      // A broken listener must never break the UI action that fired it.
    }
  }
}

/** Subscribe from the background component. Returns an unsubscribe fn. */
export function onRoutingBurst(fn: BurstListener): () => void {
  listeners.add(fn)
  return () => {
    listeners.delete(fn)
  }
}

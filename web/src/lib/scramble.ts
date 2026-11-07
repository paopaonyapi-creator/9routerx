// 9RouterX — ported & modified by https://github.com/yanstd888/ — https://t.me/aiclub_id
/**
 * Text-scramble headline decode (spec TASK-9routerX-anim-bg 2.5.4).
 *
 * `use:scramble` on a headline: on first appearance the text nodes cycle
 * random mono glyphs (≤500ms total) and then settle on the final text. One
 * shot — no continuous loop, and a timer-based settle net guarantees the
 * authored text even if no animation frame ever fires again (hidden pages
 * pause rAF entirely, which used to freeze the headline mid-scramble).
 * Respects prefers-reduced-motion (renders the final text immediately,
 * untouched).
 *
 * Structure-safe: only text node VALUES are rewritten, element children
 * (e.g. the accent span inside the login headline) are preserved.
 *
 * Re-fire: pass the current text as the parameter
 * (`use:scramble={{ text: displayTitle }}`); when it changes the action
 * decodes once more. Plain `use:scramble` fires once per mount.
 */

import type { ActionReturn } from 'svelte/action'

const GLYPHS = 'x%@o8s#>'
/* Decode window ≤500ms: screenshots and fast readers hit the headline well
   inside a second, so a 600-900ms cycle read as a garbled headline. */
const MIN_MS = 280
const MAX_MS = 480
/* The settle net fires just past the decode window. rAF is paused outright
   on hidden pages (background tabs, offscreen headless captures), so a
   mid-scramble frame could otherwise stay on screen forever; timers still
   run there, so the headline always lands on its authored text. */
const SETTLE_GRACE_MS = 120

export interface ScrambleOptions {
  /** Target text. Supply it to re-decode when the headline text changes. */
  text?: string
}

export function scramble(node: HTMLElement, opts?: ScrambleOptions): ActionReturn<ScrambleOptions> {
  if (typeof window === 'undefined') return {}

  const reduced = window.matchMedia('(prefers-reduced-motion: reduce)').matches

  // The text nodes this action owns, with their settled originals.
  let targets: { node: Text; original: string }[] = []
  let raf = 0
  let settleTimer: ReturnType<typeof setTimeout> | undefined
  let settled = ''

  function collect(): void {
    targets = []
    const walker = document.createTreeWalker(node, NodeFilter.SHOW_TEXT)
    let current = walker.nextNode()
    while (current) {
      const text = current as Text
      // Skip whitespace-only nodes left by template indentation.
      if (text.nodeValue && text.nodeValue.trim()) {
        targets.push({ node: text, original: text.nodeValue })
      }
      current = walker.nextNode()
    }
  }

  function stop(): void {
    if (raf) {
      cancelAnimationFrame(raf)
      raf = 0
    }
    if (settleTimer !== undefined) {
      clearTimeout(settleTimer)
      settleTimer = undefined
    }
  }

  // Land exactly on the authored text and tear the animation down (no loop).
  function settle(): void {
    stop()
    for (const t of targets) t.node.nodeValue = t.original
  }

  function run(): void {
    stop()
    collect()
    if (!targets.length || reduced) return
    const duration = MIN_MS + Math.random() * (MAX_MS - MIN_MS)
    const start = performance.now()
    settleTimer = setTimeout(settle, duration + SETTLE_GRACE_MS)
    const step = (now: number) => {
      const progress = Math.min(1, (now - start) / duration)
      for (const t of targets) {
        const original = t.original
        let out = ''
        for (let i = 0; i < original.length; i++) {
          const ch = original[i]
          // Spaces and chars already "decoded" (probabilistically, so the
          // reveal ripples left to right) pass through; the rest cycle.
          if (ch === ' ' || Math.random() < progress * progress) {
            out += ch
          } else {
            out += GLYPHS[(Math.random() * GLYPHS.length) | 0]
          }
        }
        t.node.nodeValue = out
      }
      if (progress < 1) {
        raf = requestAnimationFrame(step)
      } else {
        settle()
      }
    }
    raf = requestAnimationFrame(step)
  }

  settled = opts?.text ?? node.textContent ?? ''
  run()

  return {
    update(next?: ScrambleOptions) {
      // Trust the PARAMETER, not the DOM: Svelte 5 may run this action
      // update before the text binding flushes, so a DOM read here captures
      // the OLD title and settle() would then freeze it forever (the stale
      // headline bug). Write the target into the owned text node first.
      const target = next?.text ?? node.textContent ?? ''
      if (target !== settled) {
        settled = target
        const walker = document.createTreeWalker(node, NodeFilter.SHOW_TEXT)
        let current = walker.nextNode()
        while (current) {
          const text = current as Text
          if (text.nodeValue && text.nodeValue.trim()) {
            text.nodeValue = target
            break
          }
          current = walker.nextNode()
        }
        run()
      }
    },
    destroy() {
      stop()
    },
  }
}

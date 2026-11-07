<!-- 9RouterX — all source code port and modification by https://github.com/yanstd888/ — https://t.me/aiclub_id -->
<script lang="ts">
  // Custom checkbox (TASK-9routerX-ui-polish §2): tick pops in with a 120ms
  // scale from 0.6 to 1. adapted from uiverse.io/sssynk/tall-octopus-55 (MIT),
  // repainted to the theme tokens — every color here is a variable, so the box
  // follows dark and light without a second component.
  //
  // The real <input> stays in the tree and is merely visually hidden, so
  // keyboard toggling, focus and screen-reader announcements are the browser's
  // own; the styled box is aria-hidden decoration beside it.
  let {
    checked = false,
    disabled = false,
    label = '',
    title = '',
    /** Paints the tick in a status tone instead of the accent. */
    tone = 'accent',
    onchange
  }: {
    checked?: boolean
    disabled?: boolean
    label?: string
    title?: string
    tone?: 'accent' | 'success' | 'danger' | 'warning'
    onchange?: (value: boolean) => void
  } = $props()

  const toneVar: Record<string, string> = {
    accent: 'var(--app-primary)',
    success: 'var(--app-success)',
    danger: 'var(--app-danger)',
    warning: 'var(--app-warning)'
  }
</script>

<label
  class="x9-check inline-flex items-center gap-2 select-none {disabled
    ? 'opacity-60 cursor-not-allowed'
    : 'cursor-pointer'}"
  {title}
>
  <input
    type="checkbox"
    class="x9-check-input"
    {disabled}
    checked={checked}
    onchange={(e) => onchange?.(e.currentTarget.checked)}
  />
  <span
    class="x9-check-box"
    aria-hidden="true"
    style:--check-tone={toneVar[tone] ?? toneVar.accent}
  >
    <!-- Inline check: no emoji, no icon-font dependency at 10px. -->
    <svg viewBox="0 0 12 12" fill="none" aria-hidden="true">
      <path
        d="M2.5 6.5L5 9L9.5 3.5"
        stroke="currentColor"
        stroke-width="2"
        stroke-linecap="round"
        stroke-linejoin="round"
      />
    </svg>
  </span>
  {#if label}
    <span class="text-sm text-text-main leading-tight {disabled ? 'cursor-not-allowed' : ''}">
      {label}
    </span>
  {/if}
</label>

<style>
  /* The native input is the interaction surface; it is shrunk to nothing but
     keeps focus (the ring draws on the styled box through :focus-visible +). */
  .x9-check-input {
    position: absolute;
    width: 1px;
    height: 1px;
    margin: 0;
    opacity: 0;
  }

  /* Rest: recessed track box with a hairline border — the light-theme trap
     (invisible white inset / track invisible on the card) is avoided by the
     border carrying the contrast, not the fill. */
  .x9-check-box {
    display: inline-flex;
    align-items: center;
    justify-content: center;
    flex: none;
    width: 16px;
    height: 16px;
    border-radius: var(--9x-radius-sm);
    background: var(--app-surface-2);
    border: 1px solid var(--app-border);
    box-shadow: var(--9x-inset-rest);
    color: transparent;
    transition:
      background-color var(--9x-transition),
      border-color var(--9x-transition),
      box-shadow var(--9x-transition);
  }

  /* The check pops: scale .6 -> 1 in 120ms (spec §2 checkboxes/radio). */
  .x9-check-box svg {
    width: 10px;
    height: 10px;
    transform: scale(0.6);
    opacity: 0;
    transition:
      transform 120ms var(--9x-ease),
      opacity 120ms var(--9x-ease);
  }
  .x9-check-input:checked + .x9-check-box {
    background: var(--check-tone, var(--app-primary));
    border-color: transparent;
    /* The tick strokes with currentColor: fill-level ink, the same token the
       glyph buttons use on accent/danger/success fills (readable in both
       themes — white on the deep light accent, near-black on the dark one). */
    color: var(--9x-accent-ink);
  }
  .x9-check-input:checked + .x9-check-box svg {
    transform: scale(1);
    opacity: 1;
  }
  /* Focus: the keyboard indicator lands on the styled box (the input itself is
     1px), ringed with the accent token so it reads in both themes. */
  .x9-check-input:focus-visible + .x9-check-box {
    outline: 2px solid var(--app-primary);
    outline-offset: 2px;
  }
  @media (hover: hover) {
    .x9-check:hover:not(.x9-check:has(.x9-check-input:disabled)) .x9-check-box {
      border-color: var(--check-tone, var(--app-primary));
    }
  }

  @media (prefers-reduced-motion: reduce) {
    .x9-check-box,
    .x9-check-box svg {
      transition: none;
    }
  }
</style>

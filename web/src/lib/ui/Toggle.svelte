<!-- 9RouterX — all source code port and modification by https://github.com/yanstd888/ — https://t.me/aiclub_id -->
<script lang="ts">
  // Port of decolua/9router src/shared/components/Toggle.js (switch)
  let {
    checked = false,
    disabled = false,
    label = 'Toggle',
    title = '',
    size = 'md',
    onChange,
    onchange
  }: {
    checked?: boolean
    disabled?: boolean
    label?: string
    title?: string
    size?: 'sm' | 'md'
    onChange?: (value: boolean) => void
    onchange?: (value: boolean) => void
  } = $props()
</script>

<!--
  Switch (TASK-9routerX-ui-polish §2). adapted from uiverse.io/alexruix/splendid-liger-23
  (MIT) — inset track + raised thumb — repainted to the theme tokens: the OFF
  track keeps a hairline border so it never dissolves into a light card (the
  light-theme trap from spec §1.5.3), the ON track is the accent, and the thumb
  springs between stops on the shared glyph ease.
-->
<button
  type="button"
  role="switch"
  aria-checked={checked}
  aria-label={label}
  {title}
  {disabled}
  onclick={() => {
    const fn = onChange || onchange
    fn?.(!checked)
  }}
  class="relative inline-flex shrink-0 {size === 'sm' ? 'h-4 w-7' : 'h-5 w-9'} items-center rounded-full border transition-[background-color,border-color,box-shadow] duration-[180ms] ease-[var(--9x-ease)] cursor-pointer disabled:opacity-50 disabled:cursor-not-allowed {checked
    ? 'bg-brand-500 shadow-[inset_0_1px_3px_rgba(0,0,0,0.25)]'
    : 'bg-surface-3 shadow-[var(--9x-inset-rest)]'}"
>
  <span
    class="inline-block {size === 'sm' ? 'h-3 w-3' : 'h-3.5 w-3.5'} transform rounded-full bg-white shadow-[var(--9x-shadow-sm)] transition-transform duration-[180ms] ease-[var(--9x-ease)] {checked
      ? (size === 'sm' ? 'translate-x-[13px]' : 'translate-x-[18px]')
      : 'translate-x-[2px]'}"
  ></span>
</button>

<!-- 9RouterX — derived from the work of https://github.com/yanstd888/ — port and modifications by the maintainers — https://t.me/aiclub_id -->
<script lang="ts">
  // Custom radio (TASK-9routerX-ui-polish §2): the dot pops in with the same
  // 120ms scale the checkbox uses. adapted from uiverse.io/sssynk/tall-octopus-55
  // (MIT) — the same tick-pop family, round. Every color is a theme token, so
  // dark and light come from the same component.
  //
  // Like Checkbox, the native input stays in the tree (visually hidden) and
  // carries the real interaction: keyboard arrows between same-name radios,
  // focus, and screen-reader semantics are the browser's own.
  let {
    checked = false,
    disabled = false,
    label = '',
    title = '',
    name = '',
    value = '',
    onchange
  }: {
    checked?: boolean
    disabled?: boolean
    label?: string
    title?: string
    /** Radio group name; radios sharing a name arrow-key between each other. */
    name?: string
    value?: string
    onchange?: (value: boolean) => void
  } = $props()
</script>

<label
  class="x9-radio inline-flex items-center gap-2 select-none {disabled
    ? 'opacity-60 cursor-not-allowed'
    : 'cursor-pointer'}"
  {title}
>
  <input
    type="radio"
    class="x9-radio-input"
    {disabled}
    {name}
    {value}
    checked={checked}
    onchange={() => onchange?.(true)}
  />
  <span class="x9-radio-box" aria-hidden="true">
    <span class="x9-radio-dot"></span>
  </span>
  {#if label}
    <span class="text-sm text-text-main leading-tight {disabled ? 'cursor-not-allowed' : ''}">
      {label}
    </span>
  {/if}
</label>

<style>
  .x9-radio-input {
    position: absolute;
    width: 1px;
    height: 1px;
    margin: 0;
    opacity: 0;
  }

  /* Rest: hairline ring on the surface; the border carries the contrast so the
     control never disappears against a light card. */
  .x9-radio-box {
    display: inline-flex;
    align-items: center;
    justify-content: center;
    flex: none;
    width: 16px;
    height: 16px;
    border-radius: 999px;
    background: var(--app-surface-2);
    border: 1px solid var(--app-border);
    box-shadow: var(--9x-inset-rest);
    transition:
      border-color var(--9x-transition),
      box-shadow var(--9x-transition);
  }

  /* The dot pops: scale .6 -> 1 in 120ms (spec §2). */
  .x9-radio-dot {
    width: 8px;
    height: 8px;
    border-radius: 999px;
    background: var(--app-primary);
    transform: scale(0.6);
    opacity: 0;
    transition:
      transform 120ms var(--9x-ease),
      opacity 120ms var(--9x-ease);
  }
  .x9-radio-input:checked + .x9-radio-box {
    border-color: var(--app-primary);
  }
  .x9-radio-input:checked + .x9-radio-box .x9-radio-dot {
    transform: scale(1);
    opacity: 1;
  }
  .x9-radio-input:focus-visible + .x9-radio-box {
    outline: 2px solid var(--app-primary);
    outline-offset: 2px;
  }
  @media (hover: hover) {
    .x9-radio:hover:not(.x9-radio:has(.x9-radio-input:disabled)) .x9-radio-box {
      border-color: var(--app-primary);
    }
  }

  @media (prefers-reduced-motion: reduce) {
    .x9-radio-dot {
      transition: none;
    }
  }
</style>

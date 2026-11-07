<!-- 9RouterX — source port and modifications by https://github.com/yanstd888/ — https://t.me/aiclub_id -->
<script lang="ts">
  // Port of decolua/9router src/shared/components/Input.js
  let {
    label = '',
    type = 'text',
    placeholder = '',
    value = $bindable(''),
    error = '',
    hint = '',
    icon = '',
    disabled = false,
    required = false,
    min = '',
    max = '',
    class: klass = '',
    inputClass = '',
    /**
     * Optional field id. Set it and the label points at the input, so clicking the
     * label focuses the field and a screen reader announces the pair; without one
     * the label is decoration.
     */
    id = ''
  }: {
    label?: string
    type?: string
    placeholder?: string
    /**
     * The field's value. A number is legitimate: Svelte binds `<input
     * type="number">` as a number, so declaring only `string` described the seed
     * and not what the field actually holds after a keystroke.
     */
    value?: string | number
    error?: string
    hint?: string
    icon?: string
    disabled?: boolean
    required?: boolean
    min?: string
    max?: string
    class?: string
    inputClass?: string
    id?: string
  } = $props()
</script>

<div class="flex flex-col gap-1.5 {klass}">
  {#if label}
    <label class="text-sm font-medium text-text-main" for={id || undefined}>
      {label}
      {#if required}<span class="text-red-500 ml-1">*</span>{/if}
    </label>
  {/if}

  <div class="relative">
    {#if icon}
      <div class="absolute inset-y-0 left-0 flex items-center pl-3 pointer-events-none text-text-muted">
        <span class="material-symbols-outlined text-[20px]">{icon}</span>
      </div>
    {/if}
    <input
      id={id || undefined}
      {type}
      {placeholder}
      {disabled}
      {min}
      {max}
      bind:value
      class="w-full py-2.5 px-3 text-sm text-text-main bg-surface-2 rounded-[10px] border border-border-subtle shadow-[var(--9x-inset-rest)] placeholder-text-muted/70 focus:outline-none focus:border-primary focus:shadow-[var(--9x-ring),var(--9x-inset-rest)] transition-[border-color,box-shadow,background-color] duration-[180ms] ease-[var(--9x-ease)] disabled:opacity-60 disabled:bg-surface-3 disabled:cursor-not-allowed text-[16px] sm:text-sm {icon ? 'pl-10' : ''} {error ? 'ring-1 ring-red-500 focus:border-red-500/60 focus:shadow-[0_0_0_3px_color-mix(in_srgb,var(--app-danger)_18%,transparent),var(--9x-inset-rest)] border-red-500/40' : ''} {inputClass}"
    />
  </div>

  {#if error}
    <p class="text-xs text-red-500 flex items-center gap-1">
      <span class="material-symbols-outlined text-[14px]">error</span>
      {error}
    </p>
  {:else if hint}
    <p class="text-xs text-text-muted">{hint}</p>
  {/if}
</div>

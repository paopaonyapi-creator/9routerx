<!-- 9RouterX — source port and modifications by https://github.com/yanstd888/ — https://t.me/aiclub_id -->
<script lang="ts">
  // Pagination for a paged list: the page label on the left, the prev/next
  // pair on the right. Both lists on the jailbreak page used to carry this
  // markup twice, so a change to the button treatment had to be made twice.
  interface Props {
    page: number
    pageCount: number
    total: number
    noun?: string
    onPage: (page: number) => void
  }
  let { page, pageCount, total, noun = 'preset', onPage }: Props = $props()
</script>

<div class="flex flex-wrap items-center justify-between gap-2 pt-2 border-t border-border-subtle">
  <span class="text-[11px] text-text-subtle">
    Page {page} of {pageCount} · {total} {noun}{total === 1 ? '' : 's'}
  </span>
  <div class="flex items-center gap-2">
    <!-- Pagination arrows (polish pass): the glyph surface buttons get their
         hover lift / active press from the shared depth rules in index.css;
         these classes only add the rest-state shadow token and a readable
         disabled state. -->
    <button
      type="button"
      onclick={() => onPage(Math.max(1, page - 1))}
      disabled={page <= 1}
      aria-label="Previous page"
      class="px-3 py-1.5 bg-surface-3 border border-border-subtle shadow-[var(--9x-shadow-sm)] text-text-main text-sm font-medium disabled:opacity-40 disabled:shadow-none disabled:cursor-not-allowed"
    >
      ‹
    </button>
    <button
      type="button"
      onclick={() => onPage(Math.min(pageCount, page + 1))}
      disabled={page >= pageCount}
      aria-label="Next page"
      class="px-3 py-1.5 bg-surface-3 border border-border-subtle shadow-[var(--9x-shadow-sm)] text-text-main text-sm font-medium disabled:opacity-40 disabled:shadow-none disabled:cursor-not-allowed"
    >
      ›
    </button>
  </div>
</div>

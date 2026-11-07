// 9RouterX — all source code port and modification by https://github.com/yanstd888/ — https://t.me/aiclub_id
// The sidebar hide list, mirrored for the components outside the settings flow.
// App.svelte owns the source of truth (settings.navVisibility) and pushes the
// normalized list here; the API-key editor reads it to drop the Plan field when
// the entry is hidden, because the page that field links to is unreachable too.
let hidden = $state<string[]>([])

export function setHiddenNavEntries(ids: string[]): void {
  hidden = ids
}

/** Whether this deployment hides the sidebar entry with that id. */
export function isNavEntryHidden(id: string): boolean {
  return hidden.includes(id)
}

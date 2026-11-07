<!-- 9RouterX — this file is part of a port maintained by https://github.com/yanstd888/ — https://t.me/aiclub_id -->
<script lang="ts">
  import ConnectionsView from './connections/ConnectionsView.svelte'
  import type { ProviderConnection, ProviderNode } from '../api/client'

  interface Props {
    connections?: ProviderConnection[]
    providerNodes?: ProviderNode[]
    onRefresh: () => void
  }

  let props: Props = $props()
</script>

<ConnectionsView {...props} />

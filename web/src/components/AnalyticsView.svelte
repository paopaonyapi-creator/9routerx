<!-- 9RouterX — all source code port and modification by https://github.com/yanstd888/ — https://t.me/aiclub_id -->
<script lang="ts">
  import AnalyticsView from './analytics/AnalyticsView.svelte'
  import type { ProviderConnection, ProviderNode } from '../api/client'

  interface Props {
    connections?: ProviderConnection[]
    providerNodes?: ProviderNode[]
  }

  let props: Props = $props()
</script>

<AnalyticsView {...props} />

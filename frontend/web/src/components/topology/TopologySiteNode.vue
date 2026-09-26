<script setup lang="ts">
import { computed } from 'vue'
import AppLink from '../../navigation/AppLink.vue'
import { scopeOf, usePage } from '../../navigation/page'
import type { Site } from '../../domain/fleet'
import AppIcon from '../AppIcon.vue'
defineOptions({ inheritAttrs: false })
const props = defineProps<{
  data: { siteId: string }
  sites: Site[]
  tenantName: (siteId: string) => string
}>()
const page = usePage()
const site = computed(() =>
  props.sites.find((item) => item.id === props.data.siteId),
)
</script>

<template>
  <div
    v-if="site"
    :class="['topology-site-node', { solo: sites.length === 1 }]"
  >
    <header v-if="sites.length > 1">
      <span
        ><strong>{{ site.name }}</strong
        ><small>{{ site.location }} · {{ tenantName(site.id) }}</small></span
      >
      <AppLink
        class="topology-site-link nodrag"
        :to="{
          path: '/devices',
          query: { ...scopeOf(page.location.value), site: site.id },
        }"
        :aria-label="`Devices at ${site.name}`"
        ><AppIcon name="arrow"
      /></AppLink>
    </header>
  </div>
</template>

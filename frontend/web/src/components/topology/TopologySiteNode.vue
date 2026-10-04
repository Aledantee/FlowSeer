<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import AppLink from '../../navigation/AppLink.vue'
import { scopeOf, usePage } from '../../navigation/page'
import type { Site } from '../../domain/fleet'
import AppIcon from '../AppIcon.vue'
import { aiTarget, useAiSlot } from '../../ai'
import type { AiTarget } from '../../ai'
import { useFormat } from '../../i18n/format'
defineOptions({ inheritAttrs: false })
const props = defineProps<{
  data: { siteId: string }
  sites: Site[]
  tenantName: (siteId: string) => string
}>()
const { t } = useI18n({ useScope: 'global' })
const format = useFormat()
const page = usePage()
const slot = useAiSlot()
const site = computed(() =>
  props.sites.find((item) => item.id === props.data.siteId),
)
const target = computed<AiTarget | undefined>(() => {
  const current = site.value
  if (!current) return undefined
  return aiTarget({
    slot,
    view: 'topology',
    kind: 'site',
    entityId: current.id,
    label: current.name,
    context: {
      name: current.name,
      location: current.location,
      tenant: props.tenantName(current.id),
    },
  })
})
</script>

<template>
  <div
    v-if="site"
    v-ai-target="target"
    :class="['topology-site-node', { solo: sites.length === 1 }]"
  >
    <header v-if="sites.length > 1">
      <span
        ><strong translate="no">{{ site.name }}</strong
        ><small translate="no">{{
          format.facts([site.location, tenantName(site.id)])
        }}</small></span
      >
      <AppLink
        class="topology-site-link nodrag"
        :to="{
          path: '/devices',
          query: { ...scopeOf(page.location.value), site: site.id },
        }"
        :aria-label="t('view.topology.siteDevices', { site: site.name })"
        ><AppIcon name="arrow"
      /></AppLink>
    </header>
  </div>
</template>

<style scoped>
.topology-site-node {
  width: 100%;
  height: 100%;
  border: 1px dashed var(--input);
  border-radius: var(--radius-panel);
  background: color-mix(in srgb, var(--card) 55%, transparent);
}
.topology-site-node.solo {
  border-color: transparent;
  background: transparent;
}
.topology-site-node header {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 12px;
  padding: 16px 18px;
}
.topology-site-node strong,
.topology-site-node small {
  display: block;
}
.topology-site-node strong {
  font-size: var(--text-md);
  font-weight: 600;
}
.topology-site-node small {
  margin-top: 4px;
  font-size: var(--text-xs);
  color: var(--muted-foreground);
}
.topology-site-link {
  display: grid;
  place-items: center;
  width: 28px;
  height: 28px;
  border-radius: var(--radius-control);
  color: var(--muted-foreground);
}
.topology-site-link:hover {
  background: var(--hover);
  color: var(--accent-foreground);
}
.topology-site-link :deep(svg),
.topology-site-link svg {
  width: 15px;
}
</style>

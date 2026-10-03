<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { Handle, Position } from '@vue-flow/core'
import { UiStatusBadge } from '../../ui'
import DeviceIcon from '../DeviceIcon.vue'
import { aiTarget, useAiSlot } from '../../ai'
import type { AiTarget } from '../../ai'
import { useFormat } from '../../i18n/format'
import { useLabels } from '../../i18n/labels'
import { useTopologyLive } from './live'
defineOptions({ inheritAttrs: false })
const props = defineProps<{ data: { deviceId: string } }>()
const { t, n } = useI18n({ useScope: 'global' })
const format = useFormat()
const labels = useLabels()
const live = useTopologyLive()
const device = computed(() => live.device(props.data.deviceId))
const slot = useAiSlot()
const target = computed<AiTarget | undefined>(() => {
  const current = device.value
  if (!current) return undefined
  return aiTarget({
    slot,
    view: 'topology',
    kind: 'device',
    entityId: current.id,
    label: current.name,
    context: {
      name: current.name,
      kind: current.kind,
      health: current.health,
      address: current.address,
      role: current.role,
      clients: n(current.clients, 'integer'),
      throughput: format.rate(current.throughput),
    },
  })
})
const selected = computed(
  () =>
    live.selection.value !== undefined &&
    live.selection.value.kind !== 'link' &&
    live.selection.value.id === props.data.deviceId,
)
</script>

<template>
  <div
    v-if="device"
    v-ai-target="target"
    :class="[
      'topology-node',
      device.health.toLowerCase(),
      { selected: selected, peeked: live.highlighted.value === data.deviceId },
    ]"
    :aria-label="
      t('view.topology.nodeLabel', {
        name: device.name,
        kind: device.kind,
        health: labels.health(device.health),
      })
    "
  >
    <Handle type="target" :position="Position.Top" :connectable="false" />
    <DeviceIcon :role="device.role" />
    <span class="topology-node-text">
      <strong translate="no">{{ device.name }}</strong>
      <small translate="no">{{
        format.facts([device.kind, device.address])
      }}</small>
    </span>
    <UiStatusBadge
      v-if="device.health !== 'Healthy'"
      class="topology-node-status"
      :status="device.health"
      size="sm"
    />
    <span class="topology-node-stats">
      <span v-if="device.role === 'access-point'">{{
        format.counted('view.common.clients', device.clients)
      }}</span
      ><span>{{ format.rate(device.throughput) }}</span>
    </span>
    <Handle type="source" :position="Position.Bottom" :connectable="false" />
  </div>
</template>

<style scoped>
.topology-node {
  position: relative;
  height: 86px;
  display: grid;
  grid-template-columns: auto minmax(0, 1fr) auto;
  align-items: center;
  gap: 10px;
  width: 212px;
  padding: 10px 12px;
  border: 1px solid var(--border);
  border-radius: var(--radius-panel);
  background: var(--card);
  color: var(--foreground);
  box-shadow: var(--shadow-xs);
  transition:
    border-color 120ms ease,
    background 120ms ease;
}
.topology-node:hover,
.topology-node.selected {
  border-color: var(--info-border);
  background: var(--hover);
}
.topology-node.peeked {
  border-color: var(--accent-foreground);
  box-shadow: 0 0 0 3px
    color-mix(in srgb, var(--accent-foreground) 25%, transparent);
}
.topology-node :deep(.device-icon) {
  color: var(--accent-foreground);
}
.topology-node.degraded :deep(.device-icon) {
  border-color: var(--warning-border);
  color: var(--warning-foreground);
}
.topology-node.offline :deep(.device-icon) {
  border-color: var(--danger-border);
  color: var(--danger-foreground);
}
.topology-node-text {
  min-width: 0;
}
/* The badge sits on the top border so a long status word, such as the
   German one for degraded, leaves the device name its full column. */
.topology-node-status {
  position: absolute;
  top: -9px;
  right: 10px;
}
.topology-node-text strong,
.topology-node-text small {
  display: block;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.topology-node-text strong {
  font-size: var(--text-sm);
  font-weight: 550;
}
.topology-node-text small {
  margin-top: 3px;
  font-size: var(--text-2xs);
  color: var(--muted-foreground);
}
.topology-node-stats {
  grid-column: 1 / -1;
  display: flex;
  justify-content: space-between;
  gap: 10px;
  padding-top: 8px;
  border-top: 1px solid var(--border);
  font-size: var(--text-2xs);
  color: var(--muted-foreground);
}
.topology-node-stats span:only-child {
  margin-left: auto;
}
</style>

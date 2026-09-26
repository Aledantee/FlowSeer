<script setup lang="ts">
import { computed } from 'vue'
import { Handle, Position } from '@vue-flow/core'
import DeviceIcon from '../DeviceIcon.vue'
import { useTopologyLive } from './live'
defineOptions({ inheritAttrs: false })
const props = defineProps<{ data: { deviceId: string } }>()
const live = useTopologyLive()
const device = computed(() => live.device(props.data.deviceId))
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
    :class="[
      'topology-node',
      device.health.toLowerCase(),
      { selected: selected, peeked: live.highlighted.value === data.deviceId },
    ]"
    :aria-label="`${device.name}, ${device.kind}, ${device.health}`"
  >
    <Handle type="target" :position="Position.Top" :connectable="false" />
    <DeviceIcon :role="device.role" />
    <span class="topology-node-text">
      <strong>{{ device.name }}</strong>
      <small>{{ device.kind }} · {{ device.address }}</small>
    </span>
    <i class="topology-status" :title="device.health" aria-hidden="true"></i>
    <span class="topology-node-stats">
      <span v-if="device.role === 'access-point'"
        >{{ device.clients }} clients</span
      ><span>{{ device.throughput }} Mbps</span>
    </span>
    <Handle type="source" :position="Position.Bottom" :connectable="false" />
  </div>
</template>

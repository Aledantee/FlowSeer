<script setup lang="ts">
import { computed } from 'vue'
import type { Device } from '../domain/fleet'
import type { Port } from '../domain/telemetry'
const props = defineProps<{
  ports: Port[]
  device: (id: string) => Device | undefined
}>()
const emit = defineEmits<{
  neighbor: [deviceId: string]
  port: [name: string]
}>()
const active = computed(() =>
  props.ports.filter((port) => port.status === 'Up'),
)
function speed(mbps: number | undefined) {
  if (!mbps) return ''
  return mbps >= 1000 ? `${mbps / 1000}G` : `${mbps}M`
}
function describe(port: Port) {
  const far = port.neighborId
    ? props.device(port.neighborId)?.name
    : port.endpoint
  return [port.name, port.status, speed(port.speed), far]
    .filter(Boolean)
    .join(' · ')
}
</script>

<template>
  <div class="device-ports">
    <ol class="port-map" :aria-label="`${ports.length} ports`">
      <li v-for="port in ports" :key="port.name">
        <button
          :class="['port', port.status.toLowerCase(), { poe: port.poe }]"
          :title="describe(port)"
          :aria-label="describe(port)"
          @click="emit('port', port.name)"
        ></button>
      </li>
    </ol>
    <p class="port-summary">
      {{ active.length }} of {{ ports.length }} up<template
        v-if="ports.some((port) => port.poe)"
      >
        ·
        {{ ports.reduce((sum, port) => sum + (port.poe ?? 0), 0) }} W
        PoE</template
      >
    </p>
    <ul v-if="active.length" class="port-list">
      <li v-for="port in active" :key="port.name">
        <button class="port-name mono" @click="emit('port', port.name)">
          {{ port.name }}
        </button>
        <button
          v-if="port.neighborId"
          class="port-neighbor"
          @click="emit('neighbor', port.neighborId)"
        >
          {{ device(port.neighborId)?.name }}
        </button>
        <span v-else class="port-endpoint">{{ port.endpoint }}</span>
        <span class="port-rate"
          >{{ speed(port.speed) }} · {{ port.throughput }} Mbps</span
        >
      </li>
    </ul>
  </div>
</template>

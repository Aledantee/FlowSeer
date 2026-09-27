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
    <p class="port-summary text-xs text-muted-foreground mt-2">
      {{ active.length }} of {{ ports.length }} up<template
        v-if="ports.some((port) => port.poe)"
      >
        ·
        {{ ports.reduce((sum, port) => sum + (port.poe ?? 0), 0) }} W
        PoE</template
      >
    </p>
    <ul
      v-if="active.length"
      class="port-list grid gap-1.5 m-0 p-0 list-none text-sm mt-3"
    >
      <li
        v-for="port in active"
        :key="port.name"
        class="grid grid-cols-[78px_1fr_auto] items-center gap-2.5"
      >
        <button
          class="port-name font-mono justify-self-start p-0 border-0 bg-transparent text-foreground text-left hover:text-accent-foreground hover:underline cursor-pointer"
          @click="emit('port', port.name)"
        >
          {{ port.name }}
        </button>
        <button
          v-if="port.neighborId"
          class="port-neighbor justify-self-start p-0 border-0 bg-transparent text-accent-foreground text-sm font-semibold text-left hover:underline cursor-pointer"
          @click="emit('neighbor', port.neighborId)"
        >
          {{ device(port.neighborId)?.name }}
        </button>
        <span
          v-else
          class="port-endpoint text-xs text-muted-foreground tabular-nums"
          >{{ port.endpoint }}</span
        >
        <span class="port-rate text-xs text-muted-foreground tabular-nums"
          >{{ speed(port.speed) }} · {{ port.throughput }} Mbps</span
        >
      </li>
    </ul>
  </div>
</template>

<style scoped>
.port-map {
  display: grid;
  align-items: start;
  grid-template-columns: repeat(auto-fill, minmax(14px, 1fr));
  gap: 4px;
  margin: 0;
  padding: 0;
  list-style: none;
}
.port-map button {
  display: block;
  width: 100%;
  padding: 0;
  cursor: pointer;
}
.port-map button:hover {
  border-color: var(--accent-foreground);
}
.port {
  aspect-ratio: 1;
  border: 1px solid var(--input);
  border-radius: 3px;
  background: transparent;
}
.port.up {
  border-color: var(--success-foreground);
  background: color-mix(in srgb, var(--success-foreground) 55%, transparent);
}
.port.disabled {
  border-style: dashed;
  opacity: 0.5;
}
.port.poe {
  box-shadow: inset 0 -3px 0 var(--accent);
}
</style>

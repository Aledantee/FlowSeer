<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref } from 'vue'
import AppLink from '../../navigation/AppLink.vue'
import { scopeOf, usePage } from '../../navigation/page'
import AppIcon from '../AppIcon.vue'
import DeviceIcon from '../DeviceIcon.vue'
import TrafficSparkline from '../TrafficSparkline.vue'
import { UiMeter, UiScrollArea, UiStatusBadge } from '../../ui'
import DevicePorts from '../DevicePorts.vue'
import {
  linkDetailsOf,
  portDetailsOf,
  portsOf,
  radiosOf,
  telemetryOf,
} from '../../domain/telemetry'
import { useTopologyLive } from './live'
const props = defineProps<{
  history: Record<string, number[]>
  siteName: (id: string) => string
}>()
const live = useTopologyLive()
const page = usePage()
const device = computed(() =>
  live.selection.value?.kind === 'device'
    ? live.device(live.selection.value.id)
    : undefined,
)
const port = computed(() => {
  const current = live.selection.value
  if (current?.kind !== 'port') return undefined
  const owner = live.device(current.id)
  const details = owner
    ? portDetailsOf(live.fleet.value, owner, current.port)
    : undefined
  return owner && details ? { owner, details } : undefined
})
const link = computed(() =>
  live.selection.value?.kind === 'link'
    ? live.link(live.selection.value.id)
    : undefined,
)
const ends = computed(() =>
  link.value
    ? [live.device(link.value.sourceId), live.device(link.value.targetId)]
    : [],
)
// A port facing a managed device carries that link's traffic history.
const samples = computed(() => {
  const key = port.value
    ? port.value.details.link?.id
    : live.selection.value?.id
  return key ? (props.history[key] ?? []) : []
})
const telemetry = computed(() =>
  device.value ? telemetryOf(device.value) : undefined,
)
const ports = computed(() =>
  device.value ? portsOf(live.fleet.value, device.value) : [],
)
const details = computed(() =>
  link.value ? linkDetailsOf(live.fleet.value, link.value) : undefined,
)
const radios = computed(() => (device.value ? radiosOf(device.value) : []))
const now = ref(Date.now())
let clock: ReturnType<typeof setInterval> | undefined
onMounted(() => (clock = setInterval(() => (now.value = Date.now()), 30_000)))
onUnmounted(() => clearInterval(clock))
function uptime(bootedAt: number | undefined) {
  if (bootedAt === undefined) return 'Down'
  const minutes = Math.max(0, Math.floor((now.value - bootedAt) / 60_000))
  const days = Math.floor(minutes / 1440)
  const hours = Math.floor((minutes % 1440) / 60)
  return days ? `${days}d ${hours}h` : `${hours}h ${minutes % 60}m`
}
function ago(at: number) {
  const hours = Math.max(0, Math.floor((now.value - at) / 3_600_000))
  return hours >= 24 ? `${Math.floor(hours / 24)}d ago` : `${hours}h ago`
}
function rate(mbps: number | undefined) {
  if (!mbps) return '—'
  return mbps >= 1000 ? `${mbps / 1000} Gbps` : `${mbps} Mbps`
}
function deviceLink(id: string) {
  return { path: `/devices/${id}`, query: scopeOf(page.location.value) }
}
</script>

<template>
  <aside
    v-if="device || link || port"
    class="topology-inspector"
    aria-live="polite"
    :aria-label="
      device
        ? `${device.name} details`
        : port
          ? `${port.owner.name} ${port.details.port.name} details`
          : 'Link details'
    "
  >
    <header>
      <template v-if="device">
        <DeviceIcon :role="device.role" />
        <span
          ><strong>{{ device.name }}</strong
          ><small
            >{{ device.kind }} · {{ siteName(device.siteId) }}</small
          ></span
        >
      </template>
      <template v-else-if="port">
        <button
          class="icon-button"
          :aria-label="`Back to ${port.owner.name}`"
          @click="live.select({ kind: 'device', id: port.owner.id })"
        >
          <AppIcon name="back" />
        </button>
        <span
          ><strong class="mono">{{ port.details.port.name }}</strong
          ><small
            >{{ port.owner.name }} · {{ port.details.mode }} port</small
          ></span
        >
      </template>
      <span v-else-if="link"
        ><strong>{{ ends[0]?.name }} → {{ ends[1]?.name }}</strong
        ><small
          >{{ link.medium }} · {{ link.capacity / 1000 }} Gbps</small
        ></span
      >
      <button
        class="icon-button"
        aria-label="Close details"
        @click="live.select(undefined)"
      >
        <AppIcon name="close" />
      </button>
    </header>
    <UiScrollArea class="flex-1 min-h-0" viewport-class="inspector-viewport">
      <template v-if="device && telemetry">
        <dl>
          <div>
            <dt>Status</dt>
            <dd><UiStatusBadge :status="device.health" /></dd>
          </div>
          <div>
            <dt>Uptime</dt>
            <dd>{{ uptime(telemetry.bootedAt) }}</dd>
          </div>
          <div>
            <dt>IP address</dt>
            <dd class="mono">{{ device.address }}</dd>
          </div>
          <div>
            <dt>Traffic</dt>
            <dd>{{ device.throughput }} Mbps</dd>
          </div>
          <div>
            <dt>Model</dt>
            <dd>{{ telemetry.model }}</dd>
          </div>
          <div>
            <dt>Firmware</dt>
            <dd>{{ telemetry.firmware }}</dd>
          </div>
        </dl>
        <section class="topology-inspector-section">
          <h3>Traffic, live</h3>
          <TrafficSparkline
            :values="samples"
            :label="`Traffic over the last ${samples.length} samples`"
          />
        </section>
        <section
          v-if="telemetry.bootedAt !== undefined"
          class="topology-inspector-section"
        >
          <h3>Resources</h3>
          <UiMeter label="CPU" :value="telemetry.cpu" />
          <UiMeter
            label="Memory"
            :value="telemetry.memory"
            :detail="`${telemetry.memoryTotal / 1024} GB`"
          />
          <p class="topology-inspector-note">
            {{ telemetry.temperature }} °C · serial
            <span class="mono">{{ telemetry.serial }}</span>
          </p>
        </section>
        <section v-if="radios.length" class="topology-inspector-section">
          <h3>Radios</h3>
          <ul class="radio-list">
            <li v-for="radio in radios" :key="radio.band">
              <strong>{{ radio.band }}</strong
              ><span>Channel {{ radio.channel }}</span
              ><span>{{ radio.clients }} clients</span>
            </li>
          </ul>
        </section>
        <section class="topology-inspector-section">
          <h3>Ports</h3>
          <DevicePorts
            :ports="ports"
            :device="live.device"
            @neighbor="live.select({ kind: 'device', id: $event })"
            @port="live.select({ kind: 'port', id: device.id, port: $event })"
          />
        </section>
      </template>
      <template v-else-if="link && details">
        <ul class="link-ends">
          <li v-for="(end, index) in ends" :key="index">
            <button
              v-if="end"
              class="port-neighbor"
              @click="live.select({ kind: 'device', id: end.id })"
            >
              {{ end.name }}
            </button>
            <button
              v-if="end && (index ? details.targetPort : details.sourcePort)"
              class="port-name mono"
              @click="
                live.select({
                  kind: 'port',
                  id: end.id,
                  port:
                    (index ? details.targetPort : details.sourcePort)?.name ??
                    '',
                })
              "
            >
              {{ (index ? details.targetPort : details.sourcePort)?.name }}
            </button>
            <span v-else class="mono">—</span>
            <span
              :class="[
                'port-state',
                (index
                  ? details.targetPort
                  : details.sourcePort
                )?.status.toLowerCase(),
              ]"
              >{{
                (index ? details.targetPort : details.sourcePort)?.status
              }}</span
            >
          </li>
        </ul>
        <dl>
          <div>
            <dt>Status</dt>
            <dd><UiStatusBadge :status="link.health" /></dd>
          </div>
          <div>
            <dt>Speed</dt>
            <dd>{{ link.capacity / 1000 }} Gbps · {{ details.duplex }}</dd>
          </div>
          <div>
            <dt>Downstream</dt>
            <dd>{{ details.down }} Mbps</dd>
          </div>
          <div>
            <dt>Upstream</dt>
            <dd>{{ details.up }} Mbps</dd>
          </div>
          <div>
            <dt>Latency</dt>
            <dd>
              {{ link.health === 'Offline' ? '—' : `${details.latency} ms` }}
            </dd>
          </div>
          <div>
            <dt>CRC errors</dt>
            <dd>{{ details.errors }}</dd>
          </div>
          <div>
            <dt>Medium</dt>
            <dd>{{ link.medium }}</dd>
          </div>
          <div>
            <dt>MTU</dt>
            <dd>{{ details.mtu }}</dd>
          </div>
          <div class="wide">
            <dt>VLANs</dt>
            <dd>{{ details.vlans }}</dd>
          </div>
        </dl>
        <section class="topology-inspector-section">
          <h3>Traffic, live</h3>
          <TrafficSparkline
            :values="samples"
            :label="`Traffic over the last ${samples.length} samples`"
          />
        </section>
        <section class="topology-inspector-section">
          <UiMeter
            label="Utilization"
            :value="details.utilization"
            :detail="`of ${link.capacity.toLocaleString()} Mbps`"
          />
        </section>
      </template>

      <template v-if="port">
        <dl>
          <div>
            <dt>Status</dt>
            <dd>
              <span
                :class="['port-state', port.details.port.status.toLowerCase()]"
                >{{ port.details.port.status }}</span
              >
            </dd>
          </div>
          <div>
            <dt>Speed</dt>
            <dd>
              {{ rate(port.details.port.speed)
              }}<template v-if="port.details.duplex">
                · {{ port.details.duplex }}</template
              >
            </dd>
          </div>
          <div class="wide">
            <dt>Connected to</dt>
            <dd>
              <button
                v-if="port.details.port.neighborId"
                class="port-neighbor"
                @click="
                  live.select({
                    kind: 'device',
                    id: port.details.port.neighborId,
                  })
                "
              >
                {{ live.device(port.details.port.neighborId)?.name }}
              </button>
              <template v-else>{{
                port.details.port.endpoint ?? 'Nothing'
              }}</template>
            </dd>
          </div>
          <div>
            <dt>Received</dt>
            <dd>{{ port.details.rx }} Mbps</dd>
          </div>
          <div>
            <dt>Sent</dt>
            <dd>{{ port.details.tx }} Mbps</dd>
          </div>
          <div>
            <dt>PoE</dt>
            <dd>
              {{ port.details.port.poe ? `${port.details.port.poe} W` : 'Off' }}
            </dd>
          </div>
          <div>
            <dt>Errors</dt>
            <dd>{{ port.details.errors }}</dd>
          </div>
          <div>
            <dt>VLANs</dt>
            <dd>{{ port.details.vlans }}</dd>
          </div>
          <div>
            <dt>MTU</dt>
            <dd>{{ port.details.mtu }}</dd>
          </div>
          <div>
            <dt>MAC address</dt>
            <dd class="mono">{{ port.details.mac }}</dd>
          </div>
          <div>
            <dt>Last change</dt>
            <dd>{{ ago(port.details.lastChange) }}</dd>
          </div>
        </dl>
        <section v-if="port.details.link" class="topology-inspector-section">
          <h3>Traffic, live</h3>
          <TrafficSparkline
            :values="samples"
            :label="`Traffic over the last ${samples.length} samples`"
          />
        </section>
      </template>

      <footer>
        <template v-if="device">
          <AppLink class="panel-link" :to="deviceLink(device.id)"
            >Open device <AppIcon name="arrow"
          /></AppLink>
          <AppLink
            v-if="device.clients"
            class="panel-link"
            :to="{
              path: '/clients',
              query: { ...scopeOf(page.location.value), ap: device.id },
            }"
            >Clients <AppIcon name="arrow"
          /></AppLink>
        </template>
        <AppLink
          v-else-if="port"
          class="panel-link"
          :to="deviceLink(port.owner.id)"
          >Open {{ port.owner.name }} <AppIcon name="arrow"
        /></AppLink>
        <template v-else>
          <AppLink
            v-for="end in ends.filter((item) => item !== undefined)"
            :key="end.id"
            class="panel-link"
            :to="deviceLink(end.id)"
            >{{ end.name }} <AppIcon name="arrow"
          /></AppLink>
        </template>
      </footer>
    </UiScrollArea>
  </aside>
</template>

<style scoped>
.topology-inspector {
  position: absolute;
  top: 14px;
  right: 14px;
  z-index: 5;
  width: 340px;
  max-width: calc(100% - 28px);
  display: flex;
  flex-direction: column;
  max-height: calc(100% - 28px);
  overflow: hidden;
  border: 1px solid var(--border);
  border-radius: var(--radius-panel);
  background: color-mix(in srgb, var(--card) 92%, transparent);
  backdrop-filter: blur(16px);
  box-shadow: var(--shadow-lg);
}
.topology-inspector header {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 14px 12px 12px 16px;
  border-bottom: 1px solid var(--border);
}
.topology-inspector header > span:not(.device-icon) {
  flex: 1;
  min-width: 0;
}
.topology-inspector header strong,
.topology-inspector header small {
  display: block;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.topology-inspector header strong {
  font-size: var(--text-base);
  font-weight: 600;
}
.topology-inspector header small {
  margin-top: 3px;
  font-size: var(--text-xs);
  color: var(--muted-foreground);
}
.topology-inspector dl {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: 14px;
  margin: 0;
  padding: 14px 16px;
}
.topology-inspector dt {
  font-size: var(--text-xs);
  color: var(--muted-foreground);
}
.topology-inspector dd {
  margin: 4px 0 0;
  font-size: var(--text-base);
  font-weight: 550;
}
.topology-inspector dl .wide {
  grid-column: 1 / -1;
}
.topology-inspector-section {
  display: grid;
  gap: 10px;
  padding: 14px 16px;
  border-top: 1px solid var(--border);
}
.topology-inspector-section h3 {
  font-size: var(--text-xs);
  font-weight: 500;
  color: var(--muted-foreground);
}
.topology-inspector-note {
  font-size: var(--text-xs);
  color: var(--muted-foreground);
}
.resource-meter {
  display: grid;
  grid-template-columns: 1fr auto;
  gap: 5px 8px;
  font-size: var(--text-sm);
}
.resource-meter-label {
  color: var(--muted-foreground);
}
.resource-meter-value {
  white-space: nowrap;
  font-weight: 550;
  font-variant-numeric: tabular-nums;
}
.resource-meter-value small {
  font-weight: 400;
  color: var(--muted-foreground);
}
.resource-meter-track {
  grid-column: 1 / -1;
  height: 5px;
  overflow: hidden;
  border-radius: 999px;
  background: var(--subtle);
}
.resource-meter-track i {
  display: block;
  height: 100%;
  border-radius: inherit;
  background: var(--accent-foreground);
  transition: width 300ms ease;
}
.resource-meter.warning .resource-meter-track i {
  background: var(--warning-foreground);
}
.resource-meter.critical .resource-meter-track i {
  background: var(--danger-foreground);
}
.radio-list,
.port-list,
.link-ends {
  display: grid;
  gap: 6px;
  margin: 0;
  padding: 0;
  list-style: none;
  font-size: var(--text-sm);
}
.radio-list li,
.port-list li,
.link-ends li {
  display: grid;
  grid-template-columns: 1fr auto auto;
  align-items: center;
  gap: 10px;
}
.radio-list span,
.port-rate,
.port-endpoint {
  font-size: var(--text-xs);
  color: var(--muted-foreground);
  font-variant-numeric: tabular-nums;
}
.link-ends {
  padding: 14px 16px 0;
}
.port-state {
  font-size: var(--text-xs);
  color: var(--success-foreground);
}
.port-state.down {
  color: var(--danger-foreground);
}
.topology-inspector footer {
  display: flex;
  flex-wrap: wrap;
  gap: 8px 16px;
  padding: 12px 16px;
  border-top: 1px solid var(--border);
}
.panel-link {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  font-size: var(--text-sm);
  color: var(--accent-foreground);
}
.panel-link:hover {
  text-decoration: underline;
}

.panel-link :deep(svg) {
  width: 14px;
}

.topology-inspector header .icon-button {
  display: grid;
  place-items: center;
  width: 26px;
  height: 26px;
  padding: 0;
  border: 0;
  border-radius: var(--radius-control);
  background: transparent;
  color: var(--muted-foreground);
  cursor: pointer;
  transition:
    background-color 90ms ease-out,
    color 90ms ease-out;
}

.topology-inspector header .icon-button:hover {
  background: var(--hover);
  color: var(--accent-foreground);
}

.topology-inspector header .icon-button :deep(svg) {
  width: 14px;
  height: 14px;
}

@media (max-width: 560px) {
  .topology-inspector header .icon-button {
    min-width: 44px;
    min-height: 44px;
  }
}

.mono {
  font-family: var(--font-mono);
}
</style>

<script setup lang="ts">
import ScrollArea from '../ScrollArea.vue'
import { computed, onMounted, onUnmounted, ref } from 'vue'
import AppLink from '../../navigation/AppLink.vue'
import { scopeOf, usePage } from '../../navigation/page'
import AppIcon from '../AppIcon.vue'
import DeviceIcon from '../DeviceIcon.vue'
import TrafficSparkline from '../TrafficSparkline.vue'
import StatusBadge from '../StatusBadge.vue'
import DevicePorts from '../DevicePorts.vue'
import ResourceMeter from '../ResourceMeter.vue'
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
    <ScrollArea viewport-class="inspector-viewport">
      <template v-if="device && telemetry">
        <dl>
          <div>
            <dt>Status</dt>
            <dd><StatusBadge :status="device.health" /></dd>
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
          <ResourceMeter label="CPU" :percent="telemetry.cpu" />
          <ResourceMeter
            label="Memory"
            :percent="telemetry.memory"
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
            <dd><StatusBadge :status="link.health" /></dd>
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
          <ResourceMeter
            label="Utilization"
            :percent="details.utilization"
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
    </ScrollArea>
  </aside>
</template>

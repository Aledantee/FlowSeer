<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref } from 'vue'
import { I18nT, useI18n } from 'vue-i18n'
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
import { useFormat } from '../../i18n/format'
import { useLabels } from '../../i18n/labels'
import { useTopologyLive } from './live'
const props = defineProps<{
  history: Record<string, number[]>
  siteName: (id: string) => string
}>()
const { t, n } = useI18n({ useScope: 'global' })
const format = useFormat()
const labels = useLabels()
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
const label = computed(() => {
  if (device.value) {
    return t('view.topologyInspector.deviceLabel', { name: device.value.name })
  }
  if (port.value) {
    return t('view.topologyInspector.portLabel', {
      device: port.value.owner.name,
      port: port.value.details.port.name,
    })
  }
  return t('view.topologyInspector.linkLabel')
})
const samplesLabel = computed(() =>
  format.counted('view.topologyInspector.trafficSamples', samples.value.length),
)
const now = ref(Date.now())
let clock: ReturnType<typeof setInterval> | undefined
onMounted(() => (clock = setInterval(() => (now.value = Date.now()), 30_000)))
onUnmounted(() => clearInterval(clock))
function uptime(bootedAt: number | undefined) {
  if (bootedAt === undefined) return t('view.topologyInspector.uptimeDown')
  const minutes = Math.max(0, Math.floor((now.value - bootedAt) / 60_000))
  const days = Math.floor(minutes / 1440)
  const hours = Math.floor((minutes % 1440) / 60)
  return days
    ? t('view.topologyInspector.duration.daysHours', {
        days: n(days, 'integer'),
        hours: n(hours, 'integer'),
      })
    : t('view.topologyInspector.duration.hoursMinutes', {
        hours: n(hours, 'integer'),
        minutes: n(minutes % 60, 'integer'),
      })
}
function lastChange(at: number) {
  return format.ago(Math.max(0, (now.value - at) / 60_000))
}
function speedOf(mbps: number | undefined) {
  return mbps ? format.rate(mbps, true) : t('view.common.noReading')
}
// The port facing one end of the selected link, source first.
function endPort(index: number) {
  return index ? details.value?.targetPort : details.value?.sourcePort
}
function endStatus(index: number) {
  const status = endPort(index)?.status
  return status ? labels.portStatus(status) : ''
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
    :aria-label="label"
  >
    <header>
      <template v-if="device">
        <DeviceIcon :role="device.role" />
        <span
          ><strong translate="no">{{ device.name }}</strong
          ><small translate="no">{{
            format.facts([device.kind, siteName(device.siteId)])
          }}</small></span
        >
      </template>
      <template v-else-if="port">
        <button
          class="icon-button"
          :aria-label="
            t('view.topologyInspector.back', { name: port.owner.name })
          "
          @click="live.select({ kind: 'device', id: port.owner.id })"
        >
          <AppIcon name="back" />
        </button>
        <span
          ><strong class="mono" translate="no">{{
            port.details.port.name
          }}</strong
          ><small
            ><span translate="no">{{ port.owner.name }}</span
            >{{ t('view.common.factSeparator')
            }}{{
              t('view.topologyInspector.portMode', {
                mode: labels.mode(port.details.mode),
              })
            }}</small
          ></span
        >
      </template>
      <span v-else-if="link"
        ><strong translate="no">{{
          t('view.topologyInspector.linkEnds', {
            source: ends[0]?.name ?? '',
            target: ends[1]?.name ?? '',
          })
        }}</strong
        ><small>{{
          format.facts([
            labels.medium(link.medium),
            format.rate(link.capacity, true),
          ])
        }}</small></span
      >
      <button
        class="icon-button"
        :aria-label="t('view.topologyInspector.close')"
        @click="live.select(undefined)"
      >
        <AppIcon name="close" />
      </button>
    </header>
    <UiScrollArea class="flex-1 min-h-0" viewport-class="inspector-viewport">
      <template v-if="device && telemetry">
        <dl>
          <div>
            <dt>{{ t('view.common.status') }}</dt>
            <dd><UiStatusBadge :status="device.health" /></dd>
          </div>
          <div>
            <dt>{{ t('view.topologyInspector.fields.uptime') }}</dt>
            <dd>{{ uptime(telemetry.bootedAt) }}</dd>
          </div>
          <div>
            <dt>{{ t('view.common.ipAddress') }}</dt>
            <dd class="mono" translate="no">{{ device.address }}</dd>
          </div>
          <div>
            <dt>{{ t('view.common.columns.traffic') }}</dt>
            <dd>{{ format.rate(device.throughput) }}</dd>
          </div>
          <div>
            <dt>{{ t('view.topologyInspector.fields.model') }}</dt>
            <dd translate="no">{{ telemetry.model }}</dd>
          </div>
          <div>
            <dt>{{ t('view.topologyInspector.fields.firmware') }}</dt>
            <dd translate="no">{{ telemetry.firmware }}</dd>
          </div>
        </dl>
        <section class="topology-inspector-section">
          <h3>{{ t('view.topologyInspector.sections.trafficLive') }}</h3>
          <TrafficSparkline :values="samples" :label="samplesLabel" />
        </section>
        <section
          v-if="telemetry.bootedAt !== undefined"
          class="topology-inspector-section"
        >
          <h3>{{ t('view.topologyInspector.sections.resources') }}</h3>
          <UiMeter
            :label="t('view.topologyInspector.meters.cpu')"
            :value="telemetry.cpu"
          />
          <UiMeter
            :label="t('view.topologyInspector.meters.memory')"
            :value="telemetry.memory"
            :detail="format.quantity(telemetry.memoryTotal / 1024, 'gb')"
          />
          <p class="topology-inspector-note">
            {{ format.quantity(telemetry.temperature, 'celsius')
            }}{{ t('view.common.factSeparator') }}
            <I18nT
              scope="global"
              tag="span"
              keypath="view.topologyInspector.serial"
            >
              <template #serial>
                <span class="mono" translate="no">{{ telemetry.serial }}</span>
              </template>
            </I18nT>
          </p>
        </section>
        <section v-if="radios.length" class="topology-inspector-section">
          <h3>{{ t('view.topologyInspector.sections.radios') }}</h3>
          <ul class="radio-list">
            <li v-for="radio in radios" :key="radio.band">
              <strong>{{ labels.band(radio.band) }}</strong
              ><span>{{
                t('view.topologyInspector.channel', {
                  channel: n(radio.channel, 'integer'),
                })
              }}</span
              ><span>{{
                format.counted('view.common.clients', radio.clients)
              }}</span>
            </li>
          </ul>
        </section>
        <section class="topology-inspector-section">
          <h3>{{ t('view.topologyInspector.sections.ports') }}</h3>
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
              translate="no"
              @click="live.select({ kind: 'device', id: end.id })"
            >
              {{ end.name }}
            </button>
            <button
              v-if="end && endPort(index)"
              class="port-name mono"
              translate="no"
              @click="
                live.select({
                  kind: 'port',
                  id: end.id,
                  port: endPort(index)?.name ?? '',
                })
              "
            >
              {{ endPort(index)?.name }}
            </button>
            <span v-else class="mono">{{ t('view.common.noReading') }}</span>
            <span
              :class="['port-state', endPort(index)?.status.toLowerCase()]"
              >{{ endStatus(index) }}</span
            >
          </li>
        </ul>
        <dl>
          <div>
            <dt>{{ t('view.common.status') }}</dt>
            <dd><UiStatusBadge :status="link.health" /></dd>
          </div>
          <div>
            <dt>{{ t('view.topologyInspector.fields.speed') }}</dt>
            <dd>
              {{
                format.facts([
                  format.rate(link.capacity, true),
                  labels.duplex(details.duplex),
                ])
              }}
            </dd>
          </div>
          <div>
            <dt>{{ t('view.topologyInspector.fields.downstream') }}</dt>
            <dd>{{ format.rate(details.down) }}</dd>
          </div>
          <div>
            <dt>{{ t('view.topologyInspector.fields.upstream') }}</dt>
            <dd>{{ format.rate(details.up) }}</dd>
          </div>
          <div>
            <dt>{{ t('view.topologyInspector.fields.latency') }}</dt>
            <dd>
              {{
                link.health === 'Offline'
                  ? t('view.common.noReading')
                  : format.quantity(details.latency, 'ms')
              }}
            </dd>
          </div>
          <div>
            <dt>{{ t('view.topologyInspector.fields.crcErrors') }}</dt>
            <dd>{{ n(details.errors, 'integer') }}</dd>
          </div>
          <div>
            <dt>{{ t('view.topologyInspector.fields.medium') }}</dt>
            <dd>{{ labels.medium(link.medium) }}</dd>
          </div>
          <div>
            <dt>{{ t('view.topologyInspector.fields.mtu') }}</dt>
            <dd>{{ details.mtu }}</dd>
          </div>
          <div class="wide">
            <dt>{{ t('view.topologyInspector.fields.vlans') }}</dt>
            <dd>{{ details.vlans }}</dd>
          </div>
        </dl>
        <section class="topology-inspector-section">
          <h3>{{ t('view.topologyInspector.sections.trafficLive') }}</h3>
          <TrafficSparkline :values="samples" :label="samplesLabel" />
        </section>
        <section class="topology-inspector-section">
          <UiMeter
            :label="t('view.topologyInspector.meters.utilization')"
            :value="details.utilization"
            :detail="
              t('view.topologyInspector.capacityDetail', {
                capacity: format.rate(link.capacity),
              })
            "
          />
        </section>
      </template>

      <template v-if="port">
        <dl>
          <div>
            <dt>{{ t('view.common.status') }}</dt>
            <dd>
              <span
                :class="['port-state', port.details.port.status.toLowerCase()]"
                >{{ labels.portStatus(port.details.port.status) }}</span
              >
            </dd>
          </div>
          <div>
            <dt>{{ t('view.topologyInspector.fields.speed') }}</dt>
            <dd>
              {{
                format.facts([
                  speedOf(port.details.port.speed),
                  port.details.duplex && labels.duplex(port.details.duplex),
                ])
              }}
            </dd>
          </div>
          <div class="wide">
            <dt>{{ t('view.topologyInspector.fields.connectedTo') }}</dt>
            <dd>
              <button
                v-if="port.details.port.neighborId"
                class="port-neighbor"
                translate="no"
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
                port.details.port.endpoint ??
                t('view.topologyInspector.nothing')
              }}</template>
            </dd>
          </div>
          <div>
            <dt>{{ t('view.topologyInspector.fields.received') }}</dt>
            <dd>{{ format.rate(port.details.rx) }}</dd>
          </div>
          <div>
            <dt>{{ t('view.topologyInspector.fields.sent') }}</dt>
            <dd>{{ format.rate(port.details.tx) }}</dd>
          </div>
          <div>
            <dt>{{ t('view.topologyInspector.fields.poe') }}</dt>
            <dd>
              {{
                port.details.port.poe
                  ? format.quantity(port.details.port.poe, 'w')
                  : t('view.topologyInspector.off')
              }}
            </dd>
          </div>
          <div>
            <dt>{{ t('view.topologyInspector.fields.errors') }}</dt>
            <dd>{{ n(port.details.errors, 'integer') }}</dd>
          </div>
          <div>
            <dt>{{ t('view.topologyInspector.fields.vlans') }}</dt>
            <dd>{{ port.details.vlans }}</dd>
          </div>
          <div>
            <dt>{{ t('view.topologyInspector.fields.mtu') }}</dt>
            <dd>{{ port.details.mtu }}</dd>
          </div>
          <div>
            <dt>{{ t('view.common.macAddress') }}</dt>
            <dd class="mono" translate="no">{{ port.details.mac }}</dd>
          </div>
          <div>
            <dt>{{ t('view.topologyInspector.fields.lastChange') }}</dt>
            <dd>{{ lastChange(port.details.lastChange) }}</dd>
          </div>
        </dl>
        <section v-if="port.details.link" class="topology-inspector-section">
          <h3>{{ t('view.topologyInspector.sections.trafficLive') }}</h3>
          <TrafficSparkline :values="samples" :label="samplesLabel" />
        </section>
      </template>

      <footer>
        <template v-if="device">
          <AppLink class="panel-link" :to="deviceLink(device.id)"
            >{{ t('view.topologyInspector.openDevice') }} <AppIcon name="arrow"
          /></AppLink>
          <AppLink
            v-if="device.clients"
            class="panel-link"
            :to="{
              path: '/clients',
              query: { ...scopeOf(page.location.value), ap: device.id },
            }"
            >{{ labels.page('clients') }} <AppIcon name="arrow"
          /></AppLink>
        </template>
        <AppLink
          v-else-if="port"
          class="panel-link"
          :to="deviceLink(port.owner.id)"
          ><I18nT
            scope="global"
            tag="span"
            keypath="view.topologyInspector.openNamed"
          >
            <template #name>
              <span translate="no">{{ port.owner.name }}</span>
            </template>
          </I18nT>
          <AppIcon name="arrow"
        /></AppLink>
        <template v-else>
          <AppLink
            v-for="end in ends.filter((item) => item !== undefined)"
            :key="end.id"
            class="panel-link"
            :to="deviceLink(end.id)"
            ><span translate="no">{{ end.name }}</span> <AppIcon name="arrow"
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

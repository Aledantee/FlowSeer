<script setup lang="ts">
import { computed, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import AppLink from './navigation/AppLink.vue'
import UiAiTarget from './ui/ai/UiAiTarget.vue'
import { scopeOf, usePage } from './navigation/page'
import {
  UiAiSummary,
  UiCard,
  UiScrollArea,
  UiSegmentedMeter,
  UiStatusBadge,
  UiTable,
  UiTableBody,
  UiTableCell,
  UiTableHead,
  UiTableHeader,
  UiTableRow,
} from './ui'
import TrafficChart from './components/TrafficChart.vue'
import { aiTarget, useAiSlot } from './ai'
import type { AiTarget } from './ai'
import { useFormat } from './i18n/format'
import { useLabels } from './i18n/labels'
import type { Device, Site } from './domain/fleet'
import {
  healthCounts,
  rankSites,
  scopedEvents,
  siteRollups,
  trafficHistory,
} from './domain/overview'
import type { SiteRollup } from './domain/overview'

const props = defineProps<{
  scope: Device[]
  sites: Site[]
  site?: Site
  tenantName: (siteId: string) => string
}>()

const { t, n, locale } = useI18n({ useScope: 'global' })
const format = useFormat()
const labels = useLabels()
const page = usePage()
const slot = useAiSlot()

type TrafficRange = 6 | 24
const selectedRange = ref<TrafficRange>(24)

// An hour of the day as a time, for `clock`. The date is arbitrary.
const atHour = (hour: number) => new Date(2000, 0, 1, hour)
const separator = computed(() => t('view.common.factSeparator'))
const peakReading = computed(() =>
  t('view.dashboard.peakContext', {
    rate: format.rate(peak.value.mbps, true),
    time: format.clock(atHour(peak.value.hour)),
  }),
)
const chartLabel = computed(() =>
  props.site
    ? t('view.dashboard.chartLabelSite', { site: props.site.name })
    : t('view.dashboard.chartLabelAll'),
)

const viewTarget = computed(() =>
  aiTarget({
    slot,
    view: 'dashboard',
    kind: 'view',
    entityId: props.site ? props.site.id : 'all',
    label: props.site
      ? t('view.dashboard.targetLabel', { site: props.site.name })
      : t('view.dashboard.targetLabelAll'),
    context: {
      scope: props.site
        ? t('view.dashboard.scopeSite', {
            name: props.site.name,
            location: props.site.location,
          })
        : t('view.common.allSites'),
      devices: n(props.scope.length, 'integer'),
      health: labels.healthLine(counts.value),
      attention: attention.value
        .map((device) =>
          t('view.dashboard.attentionItem', {
            name: device.name,
            health: labels
              .health(device.health)
              .toLocaleLowerCase(locale.value),
          }),
        )
        .join(t('view.dashboard.listSeparator')),
      peak: peakReading.value,
    },
  }),
)
function attentionTarget(device: Device): AiTarget {
  return aiTarget({
    slot,
    view: 'dashboard',
    kind: 'attention-device',
    entityId: device.id,
    label: device.name,
    context: {
      name: device.name,
      kind: device.kind,
      health: device.health,
      site: siteNameOf(device.siteId),
      reason: reason(device),
    },
  })
}
function roleTarget(device: Device): AiTarget {
  return aiTarget({
    slot,
    view: 'dashboard',
    kind: 'role-device',
    entityId: device.id,
    label: device.name,
    context: {
      name: device.name,
      kind: device.kind,
      health: device.health,
      address: device.address,
      site: siteNameOf(device.siteId),
    },
  })
}
function siteTarget(rollup: SiteRollup): AiTarget {
  return aiTarget({
    slot,
    view: 'dashboard',
    kind: 'site',
    entityId: rollup.site.id,
    label: rollup.site.name,
    context: {
      name: rollup.site.name,
      location: rollup.site.location,
      tenant: props.tenantName(rollup.site.id),
      health: labels.healthLine(rollup.health),
    },
  })
}
const chartTarget = computed(() =>
  aiTarget({
    slot,
    view: 'dashboard',
    kind: 'chart',
    entityId: 'traffic',
    label: t('view.dashboard.trafficTitle'),
    context: {
      scope: props.site ? props.site.name : t('view.common.allSites'),
      peak: peakReading.value,
    },
  }),
)

function siteNameOf(id: string) {
  return (
    props.sites.find((item) => item.id === id)?.name ??
    t('view.common.unknownSite')
  )
}
function deviceTo(id: string) {
  return { path: `/devices/${id}`, query: scopeOf(page.location.value) }
}

// Focusing a site keeps the dashboard and changes its scope.
function siteTo(siteId: string) {
  return {
    path: page.location.value.path,
    query: { ...scopeOf(page.location.value), site: siteId || undefined },
  }
}

const endHour = new Date().getHours()
const counts = computed(() => healthCounts(props.scope))
const healthyPercent = computed(() => {
  if (props.scope.length === 0) return ''
  const ratio = counts.value.Healthy / props.scope.length
  return new Intl.NumberFormat(locale.value, {
    style: 'percent',
    minimumFractionDigits: 1,
    maximumFractionDigits: 1,
  }).format(ratio)
})

const history = computed(() =>
  trafficHistory(props.scope, endHour, selectedRange.value),
)
const peak = computed(() =>
  history.value.reduce((top, point) => (point.mbps > top.mbps ? point : top), {
    hour: 0,
    mbps: 0,
  }),
)
const currentThroughput = computed(() =>
  props.scope
    .filter((device) => device.health !== 'Offline')
    .reduce((sum, device) => sum + device.throughput, 0),
)

const severity = { Offline: 0, Degraded: 1, Healthy: 2 }
const attention = computed(() =>
  props.scope
    .filter((device) => device.health !== 'Healthy')
    .sort(
      (a, b) =>
        severity[a.health] - severity[b.health] || a.name.localeCompare(b.name),
    ),
)

// The newest warning or critical event is the reason shown beside a device;
// the full list lives in device details.
function reason(device: Device) {
  const event = feed.value.find(
    (item) => item.deviceId === device.id && item.severity !== 'info',
  )
  return format.facts([
    event?.summary ?? t('view.dashboard.noEventExplains'),
    device.health === 'Offline'
      ? t('view.dashboard.lastAnswered', {
          age: format.ago(device.lastSeenMinutes),
        })
      : event && format.ago(event.minutesAgo),
  ])
}

const feed = computed(() => scopedEvents(props.scope))
const rollups = computed(() => rankSites(siteRollups(props.scope, props.sites)))
const devicesById = computed(
  () => new Map(props.scope.map((device) => [device.id, device])),
)
const roles = computed(() => {
  const groups = new Map<string, Device[]>()
  for (const device of props.scope)
    groups.set(device.kind, [...(groups.get(device.kind) ?? []), device])
  return [...groups.entries()]
})
</script>

<template>
  <UiAiTarget :ai="viewTarget" class="dashboard dashboard-container w-full">
    <div class="dashboard-grid">
      <!-- 1. AI Summary -->
      <UiCard
        as="section"
        class="area-summary overflow-hidden min-w-0"
        aria-labelledby="summary-title"
      >
        <template #header>
          <div>
            <h2
              id="summary-title"
              class="text-xs font-mono uppercase tracking-wider font-semibold text-foreground"
            >
              {{ t('view.dashboard.aiSummary') }}
            </h2>
            <p class="text-xs text-muted-foreground mt-0.5">
              <I18nT
                scope="global"
                :keypath="
                  site
                    ? 'view.dashboard.summaryForSite'
                    : 'view.dashboard.summaryForAll'
                "
              >
                <template #site>
                  <span translate="no">{{ site?.name }}</span>
                </template>
              </I18nT>
            </p>
          </div>
        </template>
        <UiAiSummary :target="viewTarget" />
      </UiCard>

      <!-- 2. Fleet Health -->
      <UiCard
        as="section"
        class="area-health overflow-hidden min-w-0"
        aria-labelledby="fleet-health-title"
      >
        <template #header>
          <div>
            <h3
              id="fleet-health-title"
              class="text-xs font-mono uppercase tracking-wider font-semibold text-foreground"
            >
              {{ t('view.dashboard.fleetHealth') }}
            </h3>
            <p class="text-xs text-muted-foreground mt-0.5">
              {{
                t(
                  'view.dashboard.monitoredDevices',
                  { count: n(scope.length, 'integer') },
                  scope.length,
                )
              }}
            </p>
          </div>
        </template>
        <div v-if="scope.length" class="flex flex-col gap-3">
          <div class="flex items-baseline gap-2">
            <span class="text-display font-mono font-semibold text-foreground">
              {{ n(counts.Healthy, 'integer') }}
            </span>
            <span class="text-xs text-muted-foreground">
              <strong class="font-medium text-foreground block">
                {{ t('ui.segmentedMeter.healthy') }}
              </strong>
              <small class="text-2xs text-muted-foreground block">
                {{
                  t('view.dashboard.ofTotal', {
                    total: n(scope.length, 'integer'),
                  })
                }}
              </small>
            </span>
          </div>

          <UiSegmentedMeter :counts="counts" />

          <div
            class="flex justify-between items-center text-xs text-muted-foreground font-mono"
          >
            <span class="inline-flex items-center gap-1.5">
              <i
                class="w-2 h-2 rounded-full bg-warning-foreground inline-block shrink-0"
                aria-hidden="true"
              />
              <span>{{ t('ui.segmentedMeter.degraded') }}</span>
              <strong class="font-semibold text-foreground ml-1">{{
                n(counts.Degraded, 'integer')
              }}</strong>
            </span>
            <span class="inline-flex items-center gap-1.5">
              <i
                class="w-2 h-2 rounded-full bg-danger-foreground inline-block shrink-0"
                aria-hidden="true"
              />
              <span>{{ t('ui.segmentedMeter.offline') }}</span>
              <strong class="font-semibold text-foreground ml-1">{{
                n(counts.Offline, 'integer')
              }}</strong>
            </span>
          </div>

          <p
            v-if="healthyPercent"
            class="text-xs text-muted-foreground pt-1 border-t border-border"
          >
            <strong class="font-mono font-semibold text-foreground">
              {{
                t('view.dashboard.healthyPercent', { percent: healthyPercent })
              }}
            </strong>
          </p>
        </div>
        <div v-else>
          <p class="text-xs text-muted-foreground">
            {{ t('view.dashboard.noDevices') }}
          </p>
        </div>
      </UiCard>

      <!-- 3. Network Traffic -->
      <UiCard
        as="section"
        class="area-traffic overflow-hidden min-w-0"
        aria-labelledby="traffic-title"
      >
        <template #header>
          <div class="flex items-start justify-between gap-3 w-full">
            <div>
              <h3
                id="traffic-title"
                class="text-xs font-mono uppercase tracking-wider font-semibold text-foreground"
              >
                {{ t('view.dashboard.networkTraffic') }}
              </h3>
              <p class="text-xs text-muted-foreground mt-0.5">
                {{
                  site
                    ? t('view.dashboard.trafficSubtitleSite', {
                        site: site.name,
                      })
                    : t('view.dashboard.trafficSubtitleAll')
                }}
              </p>
            </div>
            <div
              role="group"
              :aria-label="t('view.dashboard.trafficRange')"
              class="inline-flex rounded-control border border-border p-0.5 bg-card"
            >
              <button
                type="button"
                class="px-2 py-0.5 text-xs font-mono rounded-sm transition-colors"
                :class="
                  selectedRange === 6
                    ? 'bg-primary text-primary-foreground font-semibold'
                    : 'text-muted-foreground hover:text-foreground'
                "
                :aria-pressed="selectedRange === 6"
                @click="selectedRange = 6"
              >
                {{ t('view.dashboard.trafficRange6h') }}
              </button>
              <button
                type="button"
                class="px-2 py-0.5 text-xs font-mono rounded-sm transition-colors"
                :class="
                  selectedRange === 24
                    ? 'bg-primary text-primary-foreground font-semibold'
                    : 'text-muted-foreground hover:text-foreground'
                "
                :aria-pressed="selectedRange === 24"
                @click="selectedRange = 24"
              >
                {{ t('view.dashboard.trafficRange24h') }}
              </button>
            </div>
          </div>
        </template>
        <div class="flex flex-col gap-2">
          <div
            class="flex items-baseline justify-between flex-wrap gap-4 px-3 pt-1"
          >
            <div class="flex items-baseline gap-4">
              <span
                class="text-display font-mono font-semibold text-foreground"
              >
                {{ format.rate(currentThroughput, true) }}
              </span>
              <div class="pl-3 border-l border-border text-xs">
                <span
                  class="text-2xs font-mono uppercase text-muted-foreground block"
                >
                  {{ t('view.dashboard.peak') }}
                </span>
                <strong class="font-mono font-semibold text-foreground">
                  {{ format.rate(peak.mbps, true) }}
                </strong>
              </div>
            </div>
            <div
              class="flex items-center gap-1.5 text-xs text-muted-foreground"
            >
              <span
                class="inline-block w-3 h-0.5 rounded-full"
                style="background-color: var(--chart-1)"
                aria-hidden="true"
              />
              <span class="font-mono uppercase text-2xs">{{
                t('view.dashboard.throughput')
              }}</span>
            </div>
          </div>
          <TrafficChart
            :ai="chartTarget"
            :points="history"
            :label="chartLabel"
          />
          <div
            class="flex items-center justify-between text-2xs text-muted-foreground px-3 pt-1 border-t border-border"
          >
            <span>
              {{
                t('view.dashboard.hourSamples', {
                  unit:
                    currentThroughput >= 1000 || peak.mbps >= 1000
                      ? t('view.common.units.gbps')
                      : t('view.common.units.mbps'),
                })
              }}
            </span>
            <span>
              {{ t('view.dashboard.showingHours', { count: selectedRange }) }}
            </span>
          </div>
        </div>
      </UiCard>

      <!-- 4. Device Roles -->
      <UiCard
        as="section"
        class="area-roles overflow-hidden min-w-0"
        aria-labelledby="roles-summary-title"
      >
        <template #header>
          <div>
            <h3
              id="roles-summary-title"
              class="text-xs font-mono uppercase tracking-wider font-semibold text-foreground"
            >
              {{ t('view.dashboard.deviceRoles') }}
            </h3>
            <p class="text-xs text-muted-foreground mt-0.5">
              {{
                site
                  ? t('view.dashboard.siteComposition')
                  : t('view.dashboard.fleetComposition')
              }}
            </p>
          </div>
        </template>
        <div v-if="roles.length" class="flex flex-col gap-3">
          <div
            v-for="[kind, members] in roles"
            :key="kind"
            class="flex flex-col gap-1"
          >
            <div class="flex items-center justify-between text-xs">
              <span class="text-foreground font-medium">{{ kind }}</span>
              <strong class="font-mono font-semibold text-foreground">
                {{ n(members.length, 'integer') }}
              </strong>
            </div>
            <div class="h-1 w-full bg-subtle rounded-control overflow-hidden">
              <div
                class="h-full bg-accent"
                :style="{
                  width: `${scope.length ? Math.min(100, Math.round((members.length / scope.length) * 100)) : 0}%`,
                }"
              />
            </div>
          </div>
        </div>
        <p v-else class="text-xs text-muted-foreground">
          {{ t('view.dashboard.noDevices') }}
        </p>
      </UiCard>

      <!-- 5. Needs Attention -->
      <UiCard
        as="section"
        class="area-attention overflow-hidden min-w-0"
        aria-labelledby="attention-title"
      >
        <template #header>
          <div class="flex items-start justify-between gap-3 w-full">
            <div>
              <h2
                id="attention-title"
                class="text-xs font-mono uppercase tracking-wider font-semibold text-foreground"
              >
                {{ t('view.common.needsAttention') }}
              </h2>
              <p class="text-xs text-muted-foreground mt-0.5">
                {{ t('view.dashboard.orderedBySeverity') }}
              </p>
            </div>
            <span
              v-if="attention.length"
              class="text-2xs bg-warning-surface text-warning-foreground border border-warning-border px-1.5 py-0.5 rounded-control font-mono"
            >
              {{ n(attention.length, 'integer') }}
            </span>
          </div>
        </template>
        <div>
          <ul
            v-if="attention.length"
            class="list-none m-0 p-0 flex flex-col gap-1"
          >
            <UiAiTarget
              v-for="device in attention"
              :key="device.id"
              as="li"
              :ai="attentionTarget(device)"
            >
              <AppLink
                class="flex items-center justify-between gap-3 py-2 px-2.5 -mx-2.5 rounded hover:bg-hover text-left"
                :to="deviceTo(device.id)"
              >
                <span>
                  <strong
                    translate="no"
                    class="text-xs font-medium text-foreground block"
                  >
                    {{ device.name }}
                  </strong>
                  <small class="text-2xs text-muted-foreground mt-0.5 block">
                    {{ device.kind }}
                    <template v-if="!site">
                      {{ separator }}
                      <span translate="no">
                        {{ sites.find((s) => s.id === device.siteId)?.name }}
                      </span>
                    </template>
                  </small>
                  <span class="block mt-1 text-xs text-foreground">
                    {{ reason(device) }}
                  </span>
                </span>
                <UiStatusBadge :status="device.health" />
              </AppLink>
            </UiAiTarget>
          </ul>
          <p v-else-if="scope.length" class="text-xs text-muted-foreground">
            {{ t('view.dashboard.allHealthy') }}
          </p>
          <p v-else class="text-xs text-muted-foreground">
            {{ t('view.dashboard.noDevices') }}
          </p>
        </div>
      </UiCard>

      <!-- 6. Sites (All Sites view) or Site Devices (Site-scoped view) -->
      <UiCard
        v-if="!site"
        as="section"
        class="area-sites overflow-hidden min-w-0"
        aria-labelledby="sites-title"
      >
        <template #header>
          <div>
            <h2
              id="sites-title"
              class="text-xs font-mono uppercase tracking-wider font-semibold text-foreground flex items-center gap-2"
            >
              <span>{{ labels.page('sites') }}</span>
              <span
                class="text-2xs bg-subtle px-1.5 py-0.5 rounded-control text-muted-foreground font-mono"
              >
                {{ n(sites.length, 'integer') }}
              </span>
            </h2>
            <p class="text-xs text-muted-foreground mt-0.5">
              {{ t('view.dashboard.selectSite') }}
            </p>
          </div>
        </template>
        <UiScrollArea axis="x" viewport-class="table-scroll">
          <UiTable>
            <UiTableHeader>
              <UiTableRow>
                <UiTableHead>{{ t('view.common.columns.site') }}</UiTableHead>
                <UiTableHead class="w-[28%]">
                  {{ t('view.common.columns.health') }}
                </UiTableHead>
                <UiTableHead align="numeric">
                  {{ t('view.common.columns.devices') }}
                </UiTableHead>
                <UiTableHead class="max-[560px]:hidden" align="numeric">
                  {{ t('view.common.columns.clients') }}
                </UiTableHead>
                <UiTableHead class="max-[560px]:hidden" align="numeric">
                  {{ t('view.common.columns.traffic') }}
                </UiTableHead>
              </UiTableRow>
            </UiTableHeader>
            <UiTableBody>
              <UiTableRow
                v-for="rollup in rollups"
                :key="rollup.site.id"
                :ai="siteTarget(rollup)"
              >
                <UiTableCell>
                  <AppLink
                    class="inline-block text-left group"
                    :to="siteTo(rollup.site.id)"
                  >
                    <strong
                      translate="no"
                      class="font-medium text-foreground group-hover:text-accent-foreground block"
                    >
                      {{ rollup.site.name }}
                    </strong>
                    <small class="text-2xs text-muted-foreground block">
                      {{ rollup.site.location }}{{ separator }}
                      <span translate="no">
                        {{ tenantName(rollup.site.id) }}
                      </span>
                    </small>
                  </AppLink>
                </UiTableCell>
                <UiTableCell class="w-[28%]">
                  <UiSegmentedMeter :counts="rollup.health" />
                  <small class="text-2xs text-muted-foreground block mt-1">
                    {{ labels.healthLine(rollup.health) }}
                  </small>
                </UiTableCell>
                <UiTableCell align="numeric">
                  {{
                    n(
                      rollup.health.Healthy +
                        rollup.health.Degraded +
                        rollup.health.Offline,
                      'integer',
                    )
                  }}
                </UiTableCell>
                <UiTableCell class="max-[560px]:hidden" align="numeric">
                  {{ n(rollup.clients, 'integer') }}
                </UiTableCell>
                <UiTableCell class="max-[560px]:hidden" align="numeric">
                  <I18nT scope="global" keypath="view.common.valueWithUnit">
                    <template #value>
                      {{ n(rollup.throughput, 'decimal') }}
                    </template>
                    <template #unit>
                      <span class="text-2xs text-muted-foreground">
                        {{ t('view.common.units.mbps') }}
                      </span>
                    </template>
                  </I18nT>
                </UiTableCell>
              </UiTableRow>
            </UiTableBody>
          </UiTable>
        </UiScrollArea>
      </UiCard>

      <UiCard
        v-else
        as="section"
        class="area-sites overflow-hidden min-w-0"
        aria-labelledby="roles-title"
      >
        <template #header>
          <div class="flex items-start justify-between gap-3 w-full">
            <div>
              <h2
                id="roles-title"
                translate="no"
                class="text-xs font-mono uppercase tracking-wider font-semibold text-foreground"
              >
                {{ site.name }}
              </h2>
              <p class="text-xs text-muted-foreground mt-0.5">
                {{ site.location }}{{ separator }}
                <span translate="no">{{ tenantName(site.id) }}</span>
              </p>
            </div>
            <AppLink
              class="text-xs text-accent-foreground p-1 hover:underline"
              :to="siteTo('')"
            >
              {{ t('view.common.allSites') }}
            </AppLink>
          </div>
        </template>
        <div
          class="grid grid-cols-[repeat(auto-fill,minmax(220px,1fr))] gap-x-6 gap-y-4"
        >
          <div v-for="[kind, members] in roles" :key="kind">
            <h3
              class="text-xs font-medium text-muted-foreground mt-2 mb-1.5 font-mono"
            >
              {{ kind }}
            </h3>
            <UiAiTarget
              v-for="device in members"
              :key="device.id"
              as-child
              :ai="roleTarget(device)"
            >
              <AppLink
                class="flex items-center justify-between gap-3 py-2 px-2.5 -mx-2.5 rounded hover:bg-hover text-left"
                :to="deviceTo(device.id)"
              >
                <span>
                  <strong
                    translate="no"
                    class="text-xs font-medium text-foreground block"
                  >
                    {{ device.name }}
                  </strong>
                  <small class="text-2xs text-muted-foreground mt-0.5 block">
                    <span translate="no">{{ device.address }}</span>
                    <template v-if="device.clients">
                      {{ separator }}
                      {{
                        format.counted('view.common.clients', device.clients)
                      }}
                    </template>
                  </small>
                </span>
                <UiStatusBadge :status="device.health" />
              </AppLink>
            </UiAiTarget>
          </div>
        </div>
      </UiCard>

      <!-- 7. Recent Events -->
      <UiCard
        as="section"
        class="area-events overflow-hidden min-w-0"
        aria-labelledby="events-title"
      >
        <template #header>
          <div>
            <h2
              id="events-title"
              class="text-xs font-mono uppercase tracking-wider font-semibold text-foreground"
            >
              {{ t('view.dashboard.events') }}
            </h2>
            <p class="text-xs text-muted-foreground mt-0.5">
              {{ t('view.dashboard.eventsPeriod') }}
            </p>
          </div>
        </template>
        <ol v-if="feed.length" class="list-none m-0 p-0">
          <li
            v-for="event in feed"
            :key="event.id"
            class="flex gap-3 py-2.5 border-t border-border first:border-t-0"
          >
            <i
              aria-hidden="true"
              class="shrink-0 w-2 h-2 mt-1 rounded-full"
              :class="{
                'bg-warning-border': event.severity === 'warning',
                'bg-danger-border': event.severity === 'critical',
                'bg-info-border':
                  event.severity !== 'warning' && event.severity !== 'critical',
              }"
            />
            <div>
              <strong class="text-xs font-medium text-foreground block">
                {{ event.summary }}
              </strong>
              <small class="text-2xs text-muted-foreground mt-0.5 block">
                <span class="text-sm text-foreground font-medium">
                  {{ labels.severity(event.severity) }}
                </span>
                {{ separator }}
                <template v-if="devicesById.get(event.deviceId)">
                  <AppLink
                    translate="no"
                    class="text-accent-foreground underline underline-offset-2"
                    :to="deviceTo(event.deviceId)"
                  >
                    {{ devicesById.get(event.deviceId)?.name }}
                  </AppLink>
                  {{ separator }}
                </template>
                {{ format.ago(event.minutesAgo) }}
              </small>
            </div>
          </li>
        </ol>
        <p v-else class="text-xs text-muted-foreground">
          {{ t('view.dashboard.eventsNone') }}
        </p>
      </UiCard>
    </div>
  </UiAiTarget>
</template>

<style scoped>
.dashboard-container {
  container-type: inline-size;
  width: 100%;
}

.dashboard-grid {
  display: grid;
  grid-template-columns: 1fr;
  gap: 16px;
  padding-bottom: 2rem;
}

@container (min-width: 640px) and (max-width: 999px) {
  .dashboard-grid {
    grid-template-columns: minmax(0, 1fr) minmax(0, 1fr);
    grid-template-areas:
      'summary summary'
      'traffic traffic'
      'health roles'
      'attention events'
      'sites sites';
  }

  .area-summary {
    grid-area: summary;
  }
  .area-traffic {
    grid-area: traffic;
  }
  .area-health {
    grid-area: health;
  }
  .area-roles {
    grid-area: roles;
  }
  .area-attention {
    grid-area: attention;
  }
  .area-events {
    grid-area: events;
  }
  .area-sites {
    grid-area: sites;
  }
}

@container (min-width: 1000px) {
  .dashboard-grid {
    grid-template-columns: minmax(0, 210px) minmax(0, 1fr) minmax(0, 274px);
    grid-template-areas:
      'summary summary summary'
      'health traffic traffic'
      'roles attention events'
      'sites sites events';
  }

  .area-summary {
    grid-area: summary;
  }
  .area-health {
    grid-area: health;
  }
  .area-traffic {
    grid-area: traffic;
  }
  .area-roles {
    grid-area: roles;
  }
  .area-attention {
    grid-area: attention;
  }
  .area-sites {
    grid-area: sites;
  }
  .area-events {
    grid-area: events;
  }
}
</style>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import AppLink from './navigation/AppLink.vue'
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

// An hour of the day as a time, for `clock`. The date is arbitrary.
const atHour = (hour: number) => new Date(2000, 0, 1, hour)
const separator = computed(() => t('view.common.factSeparator'))
const peakReading = computed(() =>
  t('view.dashboard.peakContext', {
    rate: format.rate(peak.value.mbps),
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
const history = computed(() => trafficHistory(props.scope, endHour))
const peak = computed(() =>
  history.value.reduce((top, point) => (point.mbps > top.mbps ? point : top), {
    hour: 0,
    mbps: 0,
  }),
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
  <div
    v-ai-target="viewTarget"
    class="dashboard grid grid-cols-[minmax(0,2fr)_minmax(0,1fr)] max-[1150px]:grid-cols-1 gap-6 pb-8"
  >
    <UiCard
      as="section"
      class="col-span-full overflow-hidden min-w-0"
      aria-labelledby="summary-title"
    >
      <template #header>
        <div>
          <h2
            id="summary-title"
            class="text-base font-semibold text-foreground"
          >
            {{ t('view.dashboard.aiSummary') }}
          </h2>
          <p class="text-xs text-muted-foreground mt-1">
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

    <UiCard
      as="section"
      class="overflow-hidden min-w-0"
      aria-labelledby="health-title"
    >
      <template #header>
        <div>
          <h2 id="health-title" class="text-base font-semibold text-foreground">
            {{ t('view.common.needsAttention') }}
          </h2>
          <p class="text-xs text-muted-foreground mt-1">
            {{
              t(
                'view.dashboard.attentionCount',
                {
                  count: n(attention.length, 'integer'),
                  total: n(scope.length, 'integer'),
                },
                scope.length,
              )
            }}
          </p>
        </div>
      </template>
      <div>
        <ul v-if="attention.length" class="list-none m-0 p-0">
          <li
            v-for="device in attention"
            :key="device.id"
            v-ai-target="attentionTarget(device)"
          >
            <AppLink
              class="flex items-center justify-between gap-3 py-2 px-2.5 -mx-2.5 rounded hover:bg-hover text-left"
              :to="deviceTo(device.id)"
            >
              <span>
                <strong
                  translate="no"
                  class="text-xs font-medium text-foreground block"
                  >{{ device.name }}</strong
                >
                <small class="text-2xs text-muted-foreground mt-0.5 block">
                  {{ device.kind }}
                  <template v-if="!site">
                    {{ separator
                    }}<span translate="no">{{
                      sites.find((s) => s.id === device.siteId)?.name
                    }}</span>
                  </template>
                </small>
                <span class="block mt-1 text-xs text-foreground">{{
                  reason(device)
                }}</span>
              </span>
              <UiStatusBadge :status="device.health" />
            </AppLink>
          </li>
        </ul>
        <p v-else-if="scope.length" class="text-xs text-muted-foreground">
          {{ t('view.dashboard.allHealthy') }}
        </p>
        <p v-else class="text-xs text-muted-foreground">
          {{ t('view.dashboard.noDevices') }}
        </p>
        <h3 class="text-xs font-medium text-muted-foreground mt-5 mb-1.5">
          {{ t('view.dashboard.healthAcross') }}
        </h3>
        <UiSegmentedMeter :counts="counts" legend />
      </div>
    </UiCard>

    <UiCard
      as="section"
      class="max-[800px]:hidden overflow-hidden min-w-0"
      aria-labelledby="traffic-title"
    >
      <template #header>
        <div class="flex items-start justify-between gap-3 w-full">
          <div>
            <h2
              id="traffic-title"
              class="text-base font-semibold text-foreground"
            >
              {{ t('view.dashboard.trafficTitle') }}
            </h2>
            <p class="text-xs text-muted-foreground mt-1">
              <I18nT scope="global" keypath="view.dashboard.peakSummary">
                <template #rate>
                  <strong class="text-foreground font-semibold">{{
                    format.rate(peak.mbps)
                  }}</strong>
                </template>
                <template #time>{{ format.clock(atHour(peak.hour)) }}</template>
              </I18nT>
            </p>
          </div>
          <span class="text-xs text-muted-foreground">{{
            t('view.common.units.mbps')
          }}</span>
        </div>
      </template>
      <TrafficChart
        v-ai-target="chartTarget"
        :points="history"
        :label="chartLabel"
      />
    </UiCard>

    <UiCard
      v-if="!site"
      as="section"
      class="overflow-hidden min-w-0"
      aria-labelledby="sites-title"
    >
      <template #header>
        <div>
          <h2 id="sites-title" class="text-base font-semibold text-foreground">
            {{ labels.page('sites') }}
            <span
              class="text-xs bg-subtle px-1.5 py-0.5 rounded text-muted-foreground ml-1.5 font-medium"
              >{{ n(sites.length, 'integer') }}</span
            >
          </h2>
          <p class="text-xs text-muted-foreground mt-1">
            {{ t('view.dashboard.selectSite') }}
          </p>
        </div>
      </template>
      <UiScrollArea axis="x" viewport-class="table-scroll">
        <UiTable>
          <UiTableHeader>
            <UiTableRow>
              <UiTableHead>{{ t('view.common.columns.site') }}</UiTableHead>
              <UiTableHead class="w-[28%]">{{
                t('view.common.columns.health')
              }}</UiTableHead>
              <UiTableHead align="numeric">{{
                t('view.common.columns.devices')
              }}</UiTableHead>
              <UiTableHead class="max-[560px]:hidden" align="numeric">{{
                t('view.common.columns.clients')
              }}</UiTableHead>
              <UiTableHead class="max-[560px]:hidden" align="numeric">{{
                t('view.common.columns.traffic')
              }}</UiTableHead>
            </UiTableRow>
          </UiTableHeader>
          <UiTableBody>
            <UiTableRow
              v-for="rollup in rollups"
              :key="rollup.site.id"
              v-ai-target="siteTarget(rollup)"
            >
              <UiTableCell>
                <AppLink
                  class="inline-block text-left group"
                  :to="siteTo(rollup.site.id)"
                >
                  <strong
                    translate="no"
                    class="font-medium text-foreground group-hover:text-accent-foreground block"
                    >{{ rollup.site.name }}</strong
                  >
                  <small class="text-2xs text-muted-foreground block">
                    {{ rollup.site.location }}{{ separator
                    }}<span translate="no">{{
                      tenantName(rollup.site.id)
                    }}</span>
                  </small>
                </AppLink>
              </UiTableCell>
              <UiTableCell class="w-[28%]">
                <UiSegmentedMeter :counts="rollup.health" />
                <small class="text-2xs text-muted-foreground block mt-1">{{
                  labels.healthLine(rollup.health)
                }}</small>
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
              <UiTableCell class="max-[560px]:hidden" align="numeric">{{
                n(rollup.clients, 'integer')
              }}</UiTableCell>
              <UiTableCell class="max-[560px]:hidden" align="numeric">
                <I18nT scope="global" keypath="view.common.valueWithUnit">
                  <template #value>{{
                    n(rollup.throughput, 'decimal')
                  }}</template>
                  <template #unit>
                    <span class="text-2xs text-muted-foreground">{{
                      t('view.common.units.mbps')
                    }}</span>
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
      class="overflow-hidden min-w-0"
      aria-labelledby="roles-title"
    >
      <template #header>
        <div class="flex items-start justify-between gap-3 w-full">
          <div>
            <h2
              id="roles-title"
              translate="no"
              class="text-base font-semibold text-foreground"
            >
              {{ site.name }}
            </h2>
            <p class="text-xs text-muted-foreground mt-1">
              {{ site.location }}{{ separator
              }}<span translate="no">{{ tenantName(site.id) }}</span>
            </p>
          </div>
          <AppLink
            class="text-xs text-accent-foreground p-1 hover:underline"
            :to="siteTo('')"
            >{{ t('view.common.allSites') }}</AppLink
          >
        </div>
      </template>
      <div class="grid grid-cols-[repeat(auto-fill,minmax(220px,1fr))] gap-x-6">
        <div v-for="[kind, members] in roles" :key="kind">
          <h3 class="text-xs font-medium text-muted-foreground mt-2 mb-1.5">
            {{ kind }}
          </h3>
          <AppLink
            v-for="device in members"
            :key="device.id"
            v-ai-target="roleTarget(device)"
            class="flex items-center justify-between gap-3 py-2 px-2.5 -mx-2.5 rounded hover:bg-hover text-left"
            :to="deviceTo(device.id)"
          >
            <span>
              <strong
                translate="no"
                class="text-xs font-medium text-foreground block"
                >{{ device.name }}</strong
              >
              <small class="text-2xs text-muted-foreground mt-0.5 block">
                <span translate="no">{{ device.address }}</span>
                <template v-if="device.clients">
                  {{ separator
                  }}{{ format.counted('view.common.clients', device.clients) }}
                </template>
              </small>
            </span>
            <UiStatusBadge :status="device.health" />
          </AppLink>
        </div>
      </div>
    </UiCard>

    <UiCard
      as="section"
      class="overflow-hidden min-w-0"
      aria-labelledby="events-title"
    >
      <template #header>
        <div>
          <h2 id="events-title" class="text-base font-semibold text-foreground">
            {{ t('view.dashboard.events') }}
          </h2>
          <p class="text-xs text-muted-foreground mt-1">
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
            <strong class="text-xs font-medium text-foreground block">{{
              event.summary
            }}</strong>
            <small class="text-2xs text-muted-foreground mt-0.5 block">
              <span class="text-sm text-foreground font-medium">{{
                labels.severity(event.severity)
              }}</span>
              {{ separator }}
              <template v-if="devicesById.get(event.deviceId)">
                <AppLink
                  translate="no"
                  class="text-accent-foreground hover:underline"
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
</template>

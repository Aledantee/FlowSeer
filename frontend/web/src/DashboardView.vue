<script setup lang="ts">
import { computed } from 'vue'
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
import type { Device, Site } from './domain/fleet'
import {
  formatAgo,
  healthCounts,
  healthLine,
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

const page = usePage()
const slot = useAiSlot()

const viewTarget = computed(() =>
  aiTarget({
    slot,
    view: 'dashboard',
    kind: 'view',
    entityId: props.site ? props.site.id : 'all',
    label: `Dashboard · ${props.site ? props.site.name : 'all sites'}`,
    context: {
      scope: props.site
        ? `${props.site.name}, ${props.site.location}`
        : 'All sites',
      devices: String(props.scope.length),
      health: healthLine(counts.value),
      attention: attention.value
        .map((device) => `${device.name} (${device.health.toLowerCase()})`)
        .join(', '),
      peak: `${peak.value.mbps} Mbps at ${String(peak.value.hour).padStart(2, '0')}:00`,
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
      health: healthLine(rollup.health),
    },
  })
}
const chartTarget = computed(() =>
  aiTarget({
    slot,
    view: 'dashboard',
    kind: 'chart',
    entityId: 'traffic',
    label: 'Traffic, last 24 hours',
    context: {
      scope: props.site ? props.site.name : 'All sites',
      peak: `${peak.value.mbps} Mbps at ${String(peak.value.hour).padStart(2, '0')}:00`,
    },
  }),
)

function siteNameOf(id: string) {
  return props.sites.find((item) => item.id === id)?.name ?? 'Unknown site'
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
const severityLabel = { critical: 'Critical', warning: 'Warning', info: 'Info' }
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
  const parts = [event?.summary ?? 'No event explains this yet']
  if (device.health === 'Offline')
    parts.push(`last answered ${formatAgo(device.lastSeenMinutes)}`)
  else if (event) parts.push(formatAgo(event.minutesAgo))
  return parts.join(' · ')
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
            AI summary
          </h2>
          <p class="text-xs text-muted-foreground mt-1">
            Health, attention, and traffic for
            {{ site ? site.name : 'all sites' }}
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
          <h2
            id="health-title"
            class="text-base font-semibold text-foreground"
            v-text="'Needs attention'"
          ></h2>
          <p class="text-xs text-muted-foreground mt-1">
            {{ attention.length }} of {{ scope.length }} devices
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
                <strong class="text-xs font-medium text-foreground block">{{
                  device.name
                }}</strong>
                <small class="text-2xs text-muted-foreground mt-0.5 block">
                  {{ device.kind }}
                  <template v-if="!site">
                    ·
                    {{
                      sites.find((s) => s.id === device.siteId)?.name
                    }}</template
                  >
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
          Every device in this scope is healthy.
        </p>
        <p v-else class="text-xs text-muted-foreground">
          No devices in this scope.
        </p>
        <h3 class="text-xs font-medium text-muted-foreground mt-5 mb-1.5">
          Health across the scope
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
              Traffic, last 24 hours
            </h2>
            <p class="text-xs text-muted-foreground mt-1">
              Aggregate device throughput · peak
              <strong class="text-foreground font-semibold"
                >{{ peak.mbps }} Mbps</strong
              >
              at {{ String(peak.hour).padStart(2, '0') }}:00
            </p>
          </div>
          <span class="text-xs text-muted-foreground">Mbps</span>
        </div>
      </template>
      <TrafficChart
        v-ai-target="chartTarget"
        :points="history"
        :label="`Hourly aggregate traffic for ${site ? site.name : 'all sites in scope'}`"
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
            Sites
            <span
              class="text-xs bg-subtle px-1.5 py-0.5 rounded text-muted-foreground ml-1.5 font-medium"
              >{{ sites.length }}</span
            >
          </h2>
          <p class="text-xs text-muted-foreground mt-1">
            Select a site to focus the dashboard on it.
          </p>
        </div>
      </template>
      <UiScrollArea axis="x" viewport-class="table-scroll">
        <UiTable>
          <UiTableHeader>
            <UiTableRow>
              <UiTableHead>Site</UiTableHead>
              <UiTableHead class="w-[28%]">Health</UiTableHead>
              <UiTableHead align="numeric">Devices</UiTableHead>
              <UiTableHead class="max-[560px]:hidden" align="numeric"
                >Clients</UiTableHead
              >
              <UiTableHead class="max-[560px]:hidden" align="numeric"
                >Traffic</UiTableHead
              >
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
                    class="font-medium text-foreground group-hover:text-accent-foreground block"
                    >{{ rollup.site.name }}</strong
                  >
                  <small class="text-2xs text-muted-foreground block">
                    {{ rollup.site.location }} ·
                    {{ tenantName(rollup.site.id) }}
                  </small>
                </AppLink>
              </UiTableCell>
              <UiTableCell class="w-[28%]">
                <UiSegmentedMeter :counts="rollup.health" />
                <small class="text-2xs text-muted-foreground block mt-1">{{
                  healthLine(rollup.health)
                }}</small>
              </UiTableCell>
              <UiTableCell align="numeric">
                {{
                  rollup.health.Healthy +
                  rollup.health.Degraded +
                  rollup.health.Offline
                }}
              </UiTableCell>
              <UiTableCell class="max-[560px]:hidden" align="numeric">{{
                rollup.clients
              }}</UiTableCell>
              <UiTableCell class="max-[560px]:hidden" align="numeric">
                {{ rollup.throughput }}
                <span class="text-2xs text-muted-foreground">Mbps</span>
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
              class="text-base font-semibold text-foreground"
            >
              {{ site.name }}
            </h2>
            <p class="text-xs text-muted-foreground mt-1">
              {{ site.location }} · {{ tenantName(site.id) }}
            </p>
          </div>
          <AppLink
            class="text-xs text-accent-foreground p-1 hover:underline"
            :to="siteTo('')"
            >All sites</AppLink
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
              <strong class="text-xs font-medium text-foreground block">{{
                device.name
              }}</strong>
              <small class="text-2xs text-muted-foreground mt-0.5 block">
                {{ device.address }}
                <template v-if="device.clients">
                  · {{ device.clients }} clients</template
                >
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
            Recent events
          </h2>
          <p class="text-xs text-muted-foreground mt-1">
            Last 24 hours in this scope
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
                severityLabel[event.severity]
              }}</span>
              ·
              <AppLink
                v-if="devicesById.get(event.deviceId)"
                class="text-accent-foreground hover:underline"
                :to="deviceTo(event.deviceId)"
              >
                {{ devicesById.get(event.deviceId)?.name }}
              </AppLink>
              · {{ formatAgo(event.minutesAgo) }}
            </small>
          </div>
        </li>
      </ol>
      <p v-else class="text-xs text-muted-foreground">
        No events in the last 24 hours.
      </p>
    </UiCard>
  </div>
</template>

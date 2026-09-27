<script setup lang="ts">
import { computed } from 'vue'
import AppIcon from './components/AppIcon.vue'
import AppLink from './navigation/AppLink.vue'
import { scopeOf, usePage } from './navigation/page'
import {
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
import type { Device, Site } from './domain/fleet'
import {
  formatAgo,
  healthCounts,
  scopedEvents,
  siteRollups,
  trafficHistory,
} from './domain/overview'
const props = defineProps<{
  scope: Device[]
  sites: Site[]
  site?: Site
  tenantName: (siteId: string) => string
}>()
const page = usePage()
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
const attention = computed(() =>
  props.scope
    .filter((device) => device.health !== 'Healthy')
    .sort((a, b) =>
      a.health === 'Offline' ? -1 : b.health === 'Offline' ? 1 : 0,
    ),
)
const feed = computed(() => scopedEvents(props.scope))
const rollups = computed(() => siteRollups(props.scope, props.sites))
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
    class="grid grid-cols-[minmax(0,2fr)_minmax(0,1fr)] max-[1150px]:grid-cols-1 gap-6 pb-8"
  >
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
        :points="history"
        :label="`Hourly aggregate traffic for ${site ? site.name : 'all sites in scope'}`"
      />
    </UiCard>

    <UiCard
      as="section"
      class="overflow-hidden min-w-0"
      aria-labelledby="health-title"
    >
      <template #header>
        <div>
          <h2 id="health-title" class="text-base font-semibold text-foreground">
            Device health
          </h2>
          <p class="text-xs text-muted-foreground mt-1">
            {{ scope.length }} devices monitored
          </p>
        </div>
      </template>
      <div>
        <UiSegmentedMeter :counts="counts" legend />
        <h3 class="text-xs font-medium text-muted-foreground mt-5 mb-1.5">
          Needs attention
        </h3>
        <ul v-if="attention.length" class="list-none m-0 p-0">
          <li v-for="device in attention" :key="device.id">
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
              </span>
              <UiStatusBadge :status="device.health" />
            </AppLink>
          </li>
        </ul>
        <p v-else class="text-xs text-muted-foreground">
          Every device in this scope is healthy.
        </p>
      </div>
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
              <UiTableHead class="w-[28%] max-[800px]:hidden"
                >Health</UiTableHead
              >
              <UiTableHead align="numeric">Devices</UiTableHead>
              <UiTableHead align="numeric">Clients</UiTableHead>
              <UiTableHead align="numeric">Traffic</UiTableHead>
              <UiTableHead><span class="sr-only">Open</span></UiTableHead>
            </UiTableRow>
          </UiTableHeader>
          <UiTableBody>
            <UiTableRow v-for="rollup in rollups" :key="rollup.site.id">
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
              <UiTableCell class="w-[28%] max-[800px]:hidden">
                <UiSegmentedMeter :counts="rollup.health" />
              </UiTableCell>
              <UiTableCell align="numeric">
                {{
                  rollup.health.Healthy +
                  rollup.health.Degraded +
                  rollup.health.Offline
                }}
              </UiTableCell>
              <UiTableCell align="numeric">{{ rollup.clients }}</UiTableCell>
              <UiTableCell align="numeric">
                {{ rollup.throughput }}
                <span class="text-2xs text-muted-foreground">Mbps</span>
              </UiTableCell>
              <UiTableCell>
                <AppLink
                  class="inline-flex items-center justify-center p-1 rounded hover:bg-hover text-muted-foreground hover:text-foreground"
                  :aria-label="`Focus on ${rollup.site.name}`"
                  :to="siteTo(rollup.site.id)"
                >
                  <AppIcon name="arrow" />
                </AppLink>
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
              <AppLink
                v-if="devicesById.get(event.deviceId)"
                class="text-accent-foreground hover:underline"
                :to="deviceTo(event.deviceId)"
              >
                {{ devicesById.get(event.deviceId)?.name }}
              </AppLink>
              · {{ formatAgo(event.minutesAgo) }}
              <span class="sr-only">, severity {{ event.severity }}</span>
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

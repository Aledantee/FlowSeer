<script setup lang="ts">
import ScrollArea from './components/ScrollArea.vue'
import { computed } from 'vue'
import AppIcon from './components/AppIcon.vue'
import AppLink from './navigation/AppLink.vue'
import { scopeOf, usePage } from './navigation/page'
import HealthBar from './components/HealthBar.vue'
import { UiStatusBadge } from './ui'
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
  <div class="dashboard">
    <section class="dash-panel dash-traffic" aria-labelledby="traffic-title">
      <header class="dash-heading">
        <div>
          <h2 id="traffic-title">Traffic, last 24 hours</h2>
          <p>
            Aggregate device throughput · peak
            <strong>{{ peak.mbps }} Mbps</strong> at
            {{ String(peak.hour).padStart(2, '0') }}:00
          </p>
        </div>
        <span class="dash-unit">Mbps</span>
      </header>
      <TrafficChart
        :points="history"
        :label="`Hourly aggregate traffic for ${site ? site.name : 'all sites in scope'}`"
      />
    </section>

    <section class="dash-panel dash-health" aria-labelledby="health-title">
      <header class="dash-heading">
        <div>
          <h2 id="health-title">Device health</h2>
          <p>{{ scope.length }} devices monitored</p>
        </div>
      </header>
      <div class="dash-body">
        <HealthBar :counts="counts" legend />
        <h3 class="dash-subheading">Needs attention</h3>
        <ul v-if="attention.length" class="dash-list">
          <li v-for="device in attention" :key="device.id">
            <AppLink class="dash-row" :to="deviceTo(device.id)">
              <span
                ><strong>{{ device.name }}</strong
                ><small
                  >{{ device.kind
                  }}<template v-if="!site">
                    ·
                    {{
                      sites.find((s) => s.id === device.siteId)?.name
                    }}</template
                  ></small
                ></span
              >
              <UiStatusBadge :status="device.health" />
            </AppLink>
          </li>
        </ul>
        <p v-else class="dash-empty">Every device in this scope is healthy.</p>
      </div>
    </section>

    <section
      v-if="!site"
      class="dash-panel dash-sites"
      aria-labelledby="sites-title"
    >
      <header class="dash-heading">
        <div>
          <h2 id="sites-title">
            Sites <span class="dash-count">{{ sites.length }}</span>
          </h2>
          <p>Select a site to focus the dashboard on it.</p>
        </div>
      </header>
      <ScrollArea axis="x" viewport-class="table-scroll">
        <table class="dash-table">
          <thead>
            <tr>
              <th>Site</th>
              <th class="dash-health-col">Health</th>
              <th class="numeric">Devices</th>
              <th class="numeric">Clients</th>
              <th class="numeric">Traffic</th>
              <th><span class="sr-only">Open</span></th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="rollup in rollups" :key="rollup.site.id">
              <td>
                <AppLink class="dash-site" :to="siteTo(rollup.site.id)">
                  <strong>{{ rollup.site.name }}</strong
                  ><small
                    >{{ rollup.site.location }} ·
                    {{ tenantName(rollup.site.id) }}</small
                  >
                </AppLink>
              </td>
              <td class="dash-health-col">
                <HealthBar :counts="rollup.health" />
              </td>
              <td class="numeric">
                {{
                  rollup.health.Healthy +
                  rollup.health.Degraded +
                  rollup.health.Offline
                }}
              </td>
              <td class="numeric">{{ rollup.clients }}</td>
              <td class="numeric traffic">
                {{ rollup.throughput }} <span>Mbps</span>
              </td>
              <td>
                <AppLink
                  class="icon-button"
                  :aria-label="`Focus on ${rollup.site.name}`"
                  :to="siteTo(rollup.site.id)"
                >
                  <AppIcon name="arrow" />
                </AppLink>
              </td>
            </tr>
          </tbody>
        </table>
      </ScrollArea>
    </section>

    <section v-else class="dash-panel dash-sites" aria-labelledby="roles-title">
      <header class="dash-heading">
        <div>
          <h2 id="roles-title">{{ site.name }}</h2>
          <p>{{ site.location }} · {{ tenantName(site.id) }}</p>
        </div>
        <AppLink class="dash-link" :to="siteTo('')">All sites</AppLink>
      </header>
      <div class="dash-roles">
        <div v-for="[kind, members] in roles" :key="kind" class="dash-role">
          <h3 class="dash-subheading">{{ kind }}</h3>
          <AppLink
            v-for="device in members"
            :key="device.id"
            class="dash-row"
            :to="deviceTo(device.id)"
          >
            <span
              ><strong>{{ device.name }}</strong
              ><small
                >{{ device.address
                }}<template v-if="device.clients">
                  · {{ device.clients }} clients</template
                ></small
              ></span
            >
            <UiStatusBadge :status="device.health" />
          </AppLink>
        </div>
      </div>
    </section>

    <section class="dash-panel dash-events" aria-labelledby="events-title">
      <header class="dash-heading">
        <div>
          <h2 id="events-title">Recent events</h2>
          <p>Last 24 hours in this scope</p>
        </div>
      </header>
      <ol v-if="feed.length" class="dash-feed">
        <li v-for="event in feed" :key="event.id" :class="event.severity">
          <i aria-hidden="true"></i>
          <div>
            <strong>{{ event.summary }}</strong>
            <small>
              <AppLink
                v-if="devicesById.get(event.deviceId)"
                class="dash-inline"
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
      <p v-else class="dash-empty">No events in the last 24 hours.</p>
    </section>
  </div>
</template>

<style src="./dashboard.css"></style>

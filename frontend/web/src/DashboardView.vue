<script setup lang="ts">
import { computed } from 'vue'
import AppIcon from './components/AppIcon.vue'
import HealthBar from './components/HealthBar.vue'
import StatusBadge from './components/StatusBadge.vue'
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
const emit = defineEmits<{ open: [device: Device]; site: [siteId: string] }>()

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
function openById(id: string) {
  const device = devicesById.value.get(id)
  if (device) emit('open', device)
}
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
            <button class="dash-row" @click="$emit('open', device)">
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
              <StatusBadge :status="device.health" />
            </button>
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
      <div class="table-scroll">
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
                <button
                  class="dash-site"
                  @click="$emit('site', rollup.site.id)"
                >
                  <strong>{{ rollup.site.name }}</strong
                  ><small
                    >{{ rollup.site.location }} ·
                    {{ tenantName(rollup.site.id) }}</small
                  >
                </button>
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
                <button
                  class="icon-button"
                  :aria-label="`Focus on ${rollup.site.name}`"
                  @click="$emit('site', rollup.site.id)"
                >
                  <AppIcon name="arrow" />
                </button>
              </td>
            </tr>
          </tbody>
        </table>
      </div>
    </section>

    <section v-else class="dash-panel dash-sites" aria-labelledby="roles-title">
      <header class="dash-heading">
        <div>
          <h2 id="roles-title">{{ site.name }}</h2>
          <p>{{ site.location }} · {{ tenantName(site.id) }}</p>
        </div>
        <button class="dash-link" @click="$emit('site', '')">All sites</button>
      </header>
      <div class="dash-roles">
        <div v-for="[kind, members] in roles" :key="kind" class="dash-role">
          <h3 class="dash-subheading">{{ kind }}</h3>
          <button
            v-for="device in members"
            :key="device.id"
            class="dash-row"
            @click="$emit('open', device)"
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
            <StatusBadge :status="device.health" />
          </button>
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
              <button
                v-if="devicesById.get(event.deviceId)"
                class="dash-inline"
                @click="openById(event.deviceId)"
              >
                {{ devicesById.get(event.deviceId)?.name }}
              </button>
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

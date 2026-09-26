<script setup lang="ts">
import ScrollArea from './components/ScrollArea.vue'
import { computed, defineAsyncComponent, ref, watch } from 'vue'
import DashboardView from './DashboardView.vue'
import DeviceView from './DeviceView.vue'
import ClientsView from './ClientsView.vue'
import { UiMetricCard, UiStatusBadge, UiTooltip } from './ui'
import AppIcon from './components/AppIcon.vue'
import DeviceIcon from './components/DeviceIcon.vue'
import AppLink from './navigation/AppLink.vue'
import { scopeOf, usePage } from './navigation/page'
import { useWorkspace } from './navigation/workspace'
import { useMotionFeedback } from './motion/useMotionFeedback'
import { sites, tenants, tenantIds, filterDevices } from './domain/fleet'
import type { Device } from './domain/fleet'

// Vue Flow and the ELK layout engine are most of the bundle and only the
// topology page needs them, so they load when it first opens.
const TopologyGraph = defineAsyncComponent(
  () => import('./components/topology/TopologyGraph.vue'),
)
const page = usePage()
const workspace = useWorkspace()
const { fleet, message, reassign, siteName, tenantName, peek, sideDeviceId } =
  workspace
// Notices show in whichever pane the user is working in.
const noticeHere = computed(
  () => (workspace.activePane.value === 'main') === page.primary,
)
const { play } = useMotionFeedback()
const notice = ref<HTMLElement>()
const ascending = ref(true)
const view = page.view
const query = page.query
const selected = computed(() =>
  fleet.value.find((device) => device.id === page.deviceId.value),
)
const title = computed(
  () =>
    ({
      dashboard: 'Dashboard',
      devices: 'Devices',
      clients: 'Clients',
      sites: 'Sites',
      topology: 'Topology',
      device: selected.value?.name ?? 'Unknown device',
    })[view.value],
)
const scope = computed(() =>
  filterDevices(fleet.value, query('tenant'), query('site'), '', ''),
)
const filtered = computed(() =>
  filterDevices(
    fleet.value,
    query('tenant'),
    query('site'),
    query('search'),
    query('health'),
  ).sort((a, b) =>
    ascending.value
      ? a.name.localeCompare(b.name)
      : b.name.localeCompare(a.name),
  ),
)
const visibleSites = computed(() =>
  sites.filter(
    (site) =>
      tenantIds(query('tenant')).includes(site.tenantId) &&
      (!query('site') || site.id === query('site')),
  ),
)
const scopeSummary = computed(() => {
  const site = sites.find((item) => item.id === query('site'))
  if (site) return `${site.name} · ${site.location}`
  const tenant = tenants.find((item) => item.id === query('tenant'))
  const count = visibleSites.value.length
  return `${count} ${count === 1 ? 'site' : 'sites'} across ${tenant ? tenant.name : 'all tenants'}`
})
const healthy = computed(
  () => scope.value.filter((device) => device.health === 'Healthy').length,
)
const clients = computed(() =>
  scope.value.reduce((sum, device) => sum + device.clients, 0),
)
const throughput = computed(() =>
  scope.value.reduce((sum, device) => sum + device.throughput, 0),
)
const allowedSites = computed(() =>
  sites.filter(
    (site) =>
      site.tenantId ===
      sites.find((item) => item.id === selected.value?.siteId)?.tenantId,
  ),
)
watch(
  message,
  (value) => {
    if (value && page.primary)
      play(notice.value, {
        opacity: [0.6, 1],
        transform: ['translateY(-4px)', 'none'],
      })
  },
  { flush: 'post' },
)
async function setQuery(key: string, value: string) {
  try {
    await page.go(
      {
        query: {
          ...page.location.value.query,
          [key]: value,
          ...(key === 'tenant' ? { site: undefined } : {}),
        },
      },
      { replace: true },
    )
  } catch {
    message.value = 'Could not update this view. Try again.'
  }
}
async function resetFilters() {
  try {
    await page.go({ path: '/devices', query: {} }, { replace: true })
  } catch {
    message.value = 'Could not reset filters. Try again.'
  }
}
function valueOf(event: Event): string {
  return event.target instanceof HTMLInputElement ||
    event.target instanceof HTMLSelectElement
    ? event.target.value
    : ''
}
function deviceLink(device: Device) {
  return {
    path: `/devices/${device.id}`,
    query: scopeOf(page.location.value),
  }
}
// Peeking opens a device beside this list; the arrow keys then step through
// the list as it is filtered and sorted now.
async function peekDevice(device: Device) {
  peek.value = filtered.value.map((item) => item.id)
  await workspace.follow(page, deviceLink(device), { beside: true })
}
function rememberList(event: MouseEvent) {
  if (event.shiftKey) peek.value = filtered.value.map((item) => item.id)
}
</script>

<template>
  <TopologyGraph
    v-if="view === 'topology'"
    :focus="query('focus')"
    class="topology-canvas"
    :fleet="fleet"
    :sites="visibleSites"
    :site-name="siteName"
    :tenant-name="tenantName"
  />
  <template v-else-if="view === 'device'">
    <div v-if="message && noticeHere" role="status" class="notice">
      {{ message
      }}<button aria-label="Dismiss notification" @click="message = ''">
        <AppIcon name="close" />
      </button>
    </div>
    <DeviceView
      v-if="selected"
      :device="selected"
      :fleet="fleet"
      :allowed-sites="allowedSites"
      :site-name="siteName"
      :tenant-name="tenantName"
      @reassign="reassign(selected.id, $event)"
    />
    <div v-else class="empty">
      <AppIcon name="search" />
      <h3>This device does not exist</h3>
      <p>It may have been removed from the fleet.</p>
      <AppLink :to="{ path: '/devices' }">Back to devices</AppLink>
    </div>
  </template>
  <template v-else>
    <div class="page-heading">
      <div>
        <span class="eyebrow">NETWORK OPERATIONS</span>
        <h1>{{ title }}</h1>
        <p>
          {{
            view === 'dashboard'
              ? scopeSummary
              : view === 'devices'
                ? 'Monitor health and keep your fleet connected.'
                : view === 'clients'
                  ? 'Every client your access points report, and where it connects.'
                  : 'A clear view of every location in your network.'
          }}
        </p>
      </div>
    </div>
    <div v-if="message && noticeHere" ref="notice" role="status" class="notice">
      {{ message
      }}<button aria-label="Dismiss notification" @click="message = ''">
        <AppIcon name="close" />
      </button>
    </div>
    <section class="metrics" aria-label="Fleet summary">
      <UiMetricCard
        label="Devices in scope"
        :value="scope.length"
        unit="devices"
      >
        <template #icon><AppIcon name="devices" /></template>
        Across {{ visibleSites.length }}
        {{ visibleSites.length === 1 ? 'site' : 'sites' }}
      </UiMetricCard>
      <UiMetricCard
        label="Fleet health"
        :value="scope.length ? Math.round((healthy / scope.length) * 100) : 0"
        unit="%"
      >
        <template #icon><AppIcon name="pulse" /></template>
        <b>{{ healthy }} healthy</b> · {{ scope.length - healthy }} need
        attention
      </UiMetricCard>
      <UiMetricCard label="Connected clients" :value="clients">
        <template #icon><AppIcon name="topology" /></template>
        Reported by access points
      </UiMetricCard>
      <UiMetricCard label="Device traffic" :value="throughput" unit="Mbps">
        <template #icon><AppIcon name="pulse" /></template>
        Updates every 2.5s
      </UiMetricCard>
    </section>
    <template v-if="view === 'devices'">
      <div
        v-if="scope.some((device) => device.health !== 'Healthy')"
        class="attention"
      >
        <span class="attention-icon">!</span>
        <div>
          <strong
            >{{ scope.length - healthy }}
            {{ scope.length - healthy === 1 ? 'device needs' : 'devices need' }}
            attention</strong
          ><span>Review degraded or offline devices in the current scope.</span>
        </div>
        <button @click="setQuery('health', query('health') ? '' : 'attention')">
          {{ query('health') ? 'Show all devices' : 'Review devices'
          }}<AppIcon name="arrow" />
        </button>
      </div>
      <section class="inventory" aria-labelledby="inventory-title">
        <div class="section-heading">
          <div>
            <h2 id="inventory-title">
              Device inventory <span>{{ scope.length }}</span>
            </h2>
          </div>
        </div>
        <div class="toolbar">
          <label class="search"
            ><AppIcon name="search" /><input
              :value="query('search')"
              placeholder="Search name, type, or IP address…"
              aria-label="Search devices"
              @input="setQuery('search', valueOf($event))" /></label
          ><label class="status-filter"
            ><span>Status</span
            ><select
              :value="query('health')"
              aria-label="Filter by status"
              @change="setQuery('health', valueOf($event))"
            >
              <option value="">All statuses</option>
              <option value="attention">Needs attention</option>
              <option>Healthy</option>
              <option>Degraded</option>
              <option>Offline</option>
            </select></label
          ><span class="results"
            >{{ filtered.length }}
            {{ filtered.length === 1 ? 'result' : 'results' }}</span
          >
        </div>
        <ul
          v-if="filtered.length"
          class="mobile-devices"
          aria-label="Device status"
        >
          <li v-for="device in filtered" :key="device.id">
            <AppLink
              :to="deviceLink(device)"
              :aria-label="`View status for ${device.name}`"
            >
              <strong>{{ device.name }}</strong>
              <UiStatusBadge :status="device.health" />
              <small
                >{{ siteName(device.siteId) }} · {{ device.address }}</small
              >
              <span class="mobile-device-action"
                >View status <AppIcon name="arrow"
              /></span>
            </AppLink>
          </li>
        </ul>
        <ScrollArea axis="x" viewport-class="table-scroll">
          <table>
            <thead>
              <tr>
                <th :aria-sort="ascending ? 'ascending' : 'descending'">
                  <button class="sort-button" @click="ascending = !ascending">
                    Device name {{ ascending ? '↑' : '↓' }}
                  </button>
                </th>
                <th>Status</th>
                <th>Site / tenant</th>
                <th>IP address</th>
                <th class="numeric">Clients</th>
                <th class="numeric">Traffic</th>
                <th><span class="sr-only">Details</span></th>
              </tr>
            </thead>
            <tbody @click.capture="rememberList">
              <tr
                v-for="device in filtered"
                :key="device.id"
                :class="{
                  peeked: page.primary && sideDeviceId === device.id,
                }"
              >
                <td>
                  <AppLink class="device-button" :to="deviceLink(device)">
                    <DeviceIcon :role="device.role" /><span
                      ><strong>{{ device.name }}</strong
                      ><small>{{ device.kind }}</small></span
                    >
                  </AppLink>
                </td>
                <td>
                  <UiStatusBadge :status="device.health" />
                </td>
                <td>
                  <span class="site-name">{{ siteName(device.siteId) }}</span
                  ><small>{{ tenantName(device.siteId) }}</small>
                </td>
                <td class="mono">{{ device.address }}</td>
                <td class="numeric">{{ device.clients || '—' }}</td>
                <td class="numeric traffic">
                  {{ device.throughput }} <span>Mbps</span>
                </td>
                <td class="row-actions">
                  <UiTooltip
                    label="Peek beside"
                    hint="Shift-click a name does the same; ↑ ↓ step through the list, Esc closes."
                  >
                    <button
                      class="icon-button"
                      :aria-label="`Peek at ${device.name} beside this list`"
                      @click="peekDevice(device)"
                    >
                      <AppIcon name="panel-right" />
                    </button>
                  </UiTooltip>
                  <AppLink
                    class="icon-button"
                    :to="deviceLink(device)"
                    :aria-label="`Details for ${device.name}`"
                  >
                    <AppIcon name="arrow" />
                  </AppLink>
                </td>
              </tr>
            </tbody>
          </table>
          <div v-if="!filtered.length" class="empty">
            <AppIcon name="search" />
            <h3>No devices match this view</h3>
            <p>Try a different search, status, or site.</p>
            <button @click="resetFilters">Reset all filters</button>
          </div>
        </ScrollArea>
        <footer class="table-footer">
          <span
            >Showing {{ filtered.length }} of {{ scope.length }} devices</span
          >
        </footer>
      </section>
    </template>
    <ClientsView
      v-else-if="view === 'clients'"
      :scope="scope"
      :site-name="siteName"
    />
    <DashboardView
      v-else-if="view === 'dashboard'"
      :scope="scope"
      :sites="visibleSites"
      :site="sites.find((site) => site.id === query('site'))"
      :tenant-name="tenantName"
    />
    <section v-else-if="view === 'sites'" class="site-grid" aria-label="Sites">
      <article v-for="site in visibleSites" :key="site.id" class="site-card">
        <span class="site-symbol"><AppIcon name="sites" /></span
        ><span class="eyebrow">{{ site.location }}</span>
        <h2>{{ site.name }}</h2>
        <p>{{ tenantName(site.id) }}</p>
        <div class="site-stats">
          <strong
            >{{
              fleet.filter((device) => device.siteId === site.id).length
            }}
            devices</strong
          ><span
            >{{
              fleet.filter(
                (device) =>
                  device.siteId === site.id && device.health !== 'Healthy',
              ).length
            }}
            need attention</span
          >
        </div>
        <AppLink
          :to="{
            path: '/devices',
            query: { tenant: query('tenant'), site: site.id },
          }"
          >View devices <AppIcon name="arrow"
        /></AppLink>
      </article>
    </section>
  </template>
</template>

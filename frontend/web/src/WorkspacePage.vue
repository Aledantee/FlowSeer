<script setup lang="ts">
import { computed, defineAsyncComponent, ref, watch } from 'vue'
import DashboardView from './DashboardView.vue'
import DeviceView from './DeviceView.vue'
import ClientsView from './ClientsView.vue'
import {
  UiEmptyState,
  UiInput,
  UiMetricCard,
  UiScrollArea,
  UiSelect,
  UiStatusBadge,
  UiTable,
  UiTableBody,
  UiTableCell,
  UiTableHead,
  UiTableHeader,
  UiTableRow,
  UiTooltip,
} from './ui'
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
const statusOptions = [
  { value: 'all', label: 'All statuses' },
  { value: 'attention', label: 'Needs attention' },
  { value: 'Healthy', label: 'Healthy' },
  { value: 'Degraded', label: 'Degraded' },
  { value: 'Offline', label: 'Offline' },
]
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
    <div
      v-if="message && noticeHere"
      role="status"
      class="flex items-center justify-between gap-3 px-3.5 py-2.5 bg-card border border-border rounded-panel text-xs text-foreground mb-6 shadow-sm"
    >
      {{ message }}
      <button
        aria-label="Dismiss notification"
        class="p-1 text-muted-foreground hover:text-foreground rounded cursor-pointer"
        @click="message = ''"
      >
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
    <UiEmptyState
      v-else
      title="This device does not exist"
      description="It may have been removed from the fleet."
    >
      <template #icon>
        <AppIcon name="search" />
      </template>
      <template #actions>
        <AppLink
          class="text-xs text-accent-foreground hover:underline"
          :to="{ path: '/devices' }"
        >
          Back to devices
        </AppLink>
      </template>
    </UiEmptyState>
  </template>
  <template v-else>
    <div class="flex justify-between items-center gap-5 mb-6">
      <div>
        <span
          class="block text-2xs font-semibold tracking-wider text-muted-foreground mb-1.5 uppercase"
        >
          NETWORK OPERATIONS
        </span>
        <h1 class="text-2xl font-bold text-foreground">{{ title }}</h1>
        <p class="text-sm text-muted-foreground mt-1.5">
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
    <div
      v-if="message && noticeHere"
      ref="notice"
      role="status"
      class="flex items-center justify-between gap-3 px-3.5 py-2.5 bg-card border border-border rounded-panel text-xs text-foreground mb-6 shadow-sm"
    >
      {{ message }}
      <button
        aria-label="Dismiss notification"
        class="p-1 text-muted-foreground hover:text-foreground rounded cursor-pointer"
        @click="message = ''"
      >
        <AppIcon name="close" />
      </button>
    </div>
    <section
      class="grid grid-cols-4 max-[1150px]:grid-cols-2 max-[640px]:grid-cols-1 gap-4 mb-6"
      aria-label="Fleet summary"
    >
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
        class="flex items-center p-3.5 px-4 bg-warning-surface border border-warning-border rounded-panel mb-6 gap-3.5 text-xs text-foreground"
      >
        <span
          class="w-4.5 h-4.5 border border-warning-foreground text-warning-foreground font-semibold rounded-full grid place-items-center text-xs shrink-0"
        >
          !
        </span>
        <div>
          <strong class="font-semibold text-xs">
            {{ scope.length - healthy }}
            {{ scope.length - healthy === 1 ? 'device needs' : 'devices need' }}
            attention
          </strong>
          <span class="text-xs text-warning-foreground ml-2.5">
            Review degraded or offline devices in the current scope.
          </span>
        </div>
        <button
          class="ml-auto inline-flex items-center gap-2 text-xs p-1 text-warning-foreground hover:underline whitespace-nowrap cursor-pointer"
          @click="setQuery('health', query('health') ? '' : 'attention')"
        >
          {{ query('health') ? 'Show all devices' : 'Review devices' }}
          <AppIcon name="arrow" />
        </button>
      </div>
      <section
        class="bg-card border border-border rounded-panel overflow-hidden shadow-xs"
        aria-labelledby="inventory-title"
      >
        <div class="px-6 py-5 pb-4 flex items-center justify-between gap-3">
          <div>
            <h2
              id="inventory-title"
              class="text-base font-semibold text-foreground"
            >
              Device inventory
              <span
                class="text-xs bg-subtle px-1.5 py-0.5 rounded text-muted-foreground ml-1.5 font-medium"
              >
                {{ scope.length }}
              </span>
            </h2>
          </div>
        </div>
        <div class="flex items-center gap-3 px-6 pb-5">
          <div class="relative flex items-center w-[340px]">
            <AppIcon
              name="search"
              class="absolute left-2.5 w-3.5 h-3.5 text-muted-foreground pointer-events-none z-10"
            />
            <UiInput
              :model-value="query('search')"
              placeholder="Search name, type, or IP address…"
              aria-label="Search devices"
              class="pl-8"
              @update:model-value="setQuery('search', $event)"
            />
          </div>
          <div class="w-44">
            <UiSelect
              :model-value="query('health') || 'all'"
              :options="statusOptions"
              aria-label="Filter by status"
              @update:model-value="
                setQuery('health', $event === 'all' ? '' : $event)
              "
            />
          </div>
          <span class="ml-auto text-xs text-muted-foreground">
            {{ filtered.length }}
            {{ filtered.length === 1 ? 'result' : 'results' }}
          </span>
        </div>
        <ul
          v-if="filtered.length"
          class="max-[560px]:grid hidden max-[560px]:list-none max-[560px]:m-0 max-[560px]:p-0 max-[560px]:px-3.5 border-t border-border"
          aria-label="Device status"
        >
          <li
            v-for="device in filtered"
            :key="device.id"
            class="border-t border-border first:border-t-0"
          >
            <AppLink
              :to="deviceLink(device)"
              :aria-label="`View status for ${device.name}`"
              class="grid grid-cols-[1fr_auto] gap-2.5 w-full py-4 text-left"
            >
              <strong
                class="font-semibold text-foreground self-center break-words"
              >
                {{ device.name }}
              </strong>
              <UiStatusBadge :status="device.health" />
              <small class="text-xs text-muted-foreground">
                {{ siteName(device.siteId) }} · {{ device.address }}
              </small>
              <span
                class="inline-flex items-center gap-1 text-xs text-accent-foreground"
              >
                View status <AppIcon name="arrow" />
              </span>
            </AppLink>
          </li>
        </ul>
        <UiScrollArea axis="x" viewport-class="table-scroll max-[560px]:hidden">
          <UiTable>
            <UiTableHeader>
              <UiTableRow>
                <UiTableHead
                  sortable
                  :sort-direction="ascending ? 'ascending' : 'descending'"
                  @sort="ascending = !ascending"
                >
                  Device name
                </UiTableHead>
                <UiTableHead>Status</UiTableHead>
                <UiTableHead>Site / tenant</UiTableHead>
                <UiTableHead>IP address</UiTableHead>
                <UiTableHead align="numeric">Clients</UiTableHead>
                <UiTableHead align="numeric">Traffic</UiTableHead>
                <UiTableHead><span class="sr-only">Details</span></UiTableHead>
              </UiTableRow>
            </UiTableHeader>
            <UiTableBody @click.capture="rememberList">
              <UiTableRow
                v-for="device in filtered"
                :key="device.id"
                :class="{
                  peeked: page.primary && sideDeviceId === device.id,
                }"
              >
                <UiTableCell>
                  <AppLink
                    class="inline-flex items-center gap-2.5 text-left group"
                    :to="deviceLink(device)"
                  >
                    <DeviceIcon :role="device.role" />
                    <span>
                      <strong
                        class="font-semibold text-foreground group-hover:text-accent-foreground block text-xs"
                      >
                        {{ device.name }}
                      </strong>
                      <small class="text-2xs text-muted-foreground block">
                        {{ device.kind }}
                      </small>
                    </span>
                  </AppLink>
                </UiTableCell>
                <UiTableCell>
                  <UiStatusBadge :status="device.health" />
                </UiTableCell>
                <UiTableCell>
                  <span class="font-medium text-foreground block text-xs">
                    {{ siteName(device.siteId) }}
                  </span>
                  <small class="text-2xs text-muted-foreground block">
                    {{ tenantName(device.siteId) }}
                  </small>
                </UiTableCell>
                <UiTableCell mono>{{ device.address }}</UiTableCell>
                <UiTableCell align="numeric">
                  {{ device.clients || '—' }}
                </UiTableCell>
                <UiTableCell align="numeric" class="traffic">
                  {{ device.throughput }}
                  <span class="text-2xs text-muted-foreground">Mbps</span>
                </UiTableCell>
                <UiTableCell>
                  <div class="flex items-center gap-1 justify-end">
                    <UiTooltip
                      label="Peek beside"
                      hint="Shift-click a name does the same; ↑ ↓ step through the list, Esc closes."
                    >
                      <button
                        class="inline-flex items-center justify-center p-1.5 rounded hover:bg-hover text-muted-foreground hover:text-foreground cursor-pointer"
                        :aria-label="`Peek at ${device.name} beside this list`"
                        @click="peekDevice(device)"
                      >
                        <AppIcon name="panel-right" />
                      </button>
                    </UiTooltip>
                    <AppLink
                      class="inline-flex items-center justify-center p-1.5 rounded hover:bg-hover text-muted-foreground hover:text-foreground"
                      :to="deviceLink(device)"
                      :aria-label="`Details for ${device.name}`"
                    >
                      <AppIcon name="arrow" />
                    </AppLink>
                  </div>
                </UiTableCell>
              </UiTableRow>
            </UiTableBody>
          </UiTable>
          <UiEmptyState
            v-if="!filtered.length"
            title="No devices match this view"
            description="Try a different search, status, or site."
          >
            <template #icon>
              <AppIcon name="search" />
            </template>
            <template #actions>
              <button
                class="text-xs text-accent-foreground hover:underline cursor-pointer"
                @click="resetFilters"
              >
                Reset all filters
              </button>
            </template>
          </UiEmptyState>
        </UiScrollArea>
        <footer
          class="px-6 py-4 border-t border-border text-xs text-muted-foreground"
        >
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
    <section
      v-else-if="view === 'sites'"
      class="grid grid-cols-2 max-[800px]:grid-cols-1 gap-4.5"
      aria-label="Sites"
    >
      <article
        v-for="site in visibleSites"
        :key="site.id"
        class="p-6 border border-border rounded-panel bg-card"
      >
        <span
          class="grid place-items-center w-9.5 h-9.5 bg-info-surface text-accent-foreground rounded-panel mb-5"
        >
          <AppIcon name="sites" />
        </span>
        <span
          class="text-2xs font-semibold tracking-wider text-muted-foreground uppercase block"
        >
          {{ site.location }}
        </span>
        <h2 class="text-xl font-bold text-foreground my-1.5">
          {{ site.name }}
        </h2>
        <p class="text-xs text-muted-foreground">{{ tenantName(site.id) }}</p>
        <div class="flex justify-between my-6 mb-5 text-xs text-foreground">
          <strong>
            {{ fleet.filter((device) => device.siteId === site.id).length }}
            devices
          </strong>
          <span class="text-muted-foreground">
            {{
              fleet.filter(
                (device) =>
                  device.siteId === site.id && device.health !== 'Healthy',
              ).length
            }}
            need attention
          </span>
        </div>
        <AppLink
          class="border-t border-border pt-3.5 flex justify-between text-accent-foreground text-xs hover:underline"
          :to="{
            path: '/devices',
            query: { tenant: query('tenant'), site: site.id },
          }"
        >
          View devices <AppIcon name="arrow" />
        </AppLink>
      </article>
    </section>
  </template>
</template>

<script setup lang="ts">
import { computed, nextTick, onMounted, onUnmounted, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useMotionFeedback } from './motion/useMotionFeedback'
import ComponentsView from './ComponentsView.vue'
import DashboardView from './DashboardView.vue'
import UiButton from './components/UiButton.vue'
import StatusBadge from './components/StatusBadge.vue'
import MetricCard from './components/MetricCard.vue'
import AppIcon from './components/AppIcon.vue'
import AccountMenu from './components/AccountMenu.vue'
import ReportBugButton from './components/ReportBugButton.vue'
import HelpButton from './components/HelpButton.vue'
import ThemeSwitcher from './components/ThemeSwitcher.vue'
import TenantSwitcher from './components/TenantSwitcher.vue'
import ScopeSwitcher from './components/ScopeSwitcher.vue'
import {
  devices,
  integrations,
  sites,
  tenants,
  tenantIds,
  filterDevices,
  moveDevice,
  pollDevice,
} from './domain/fleet'
import type { Device } from './domain/fleet'
import { events, formatAgo } from './domain/overview'
const { play, cancel } = useMotionFeedback()
// The Components workspace is a design tool for the team, not an operator
// page, so production builds leave it out of navigation.
const showComponents = import.meta.env.DEV
const workspace = ref<HTMLElement>()
const sidebar = ref<HTMLElement>()
const navigation = ref<HTMLElement>()
const mainShell = ref<HTMLElement>()
const topbar = ref<HTMLElement>()
let topbarObserver: ResizeObserver | undefined
onMounted(() => {
  topbarObserver = new ResizeObserver(() => {
    if (topbar.value)
      mainShell.value?.style.setProperty(
        '--topbar-height',
        `${topbar.value.offsetHeight}px`,
      )
  })
  if (topbar.value) topbarObserver.observe(topbar.value)
})
onUnmounted(() => topbarObserver?.disconnect())
const notice = ref<HTMLElement>()
const route = useRoute()
const router = useRouter()
const fleet = ref(devices.map((device) => ({ ...device })))
const sidebarCollapsed = ref(false)
const tick = ref(0)
const message = ref('')
interface Move {
  deviceId: string
  name: string
  from: string
  to: string
  observed: boolean
}
const move = ref<Move>()
const detail = ref<HTMLDialogElement>()
const selected = ref<Device>()
const destination = ref('')
type SortKey = 'status' | 'name' | 'site'
// Problems sort first by default so an operator never scrolls past healthy
// devices to find the one that needs attention.
const sortKey = ref<SortKey>('status')
const ascending = ref(true)
const severity = { Offline: 0, Degraded: 1, Healthy: 2 }
function sortBy(key: SortKey) {
  ascending.value = sortKey.value === key ? !ascending.value : true
  sortKey.value = key
}
const view = computed(() => String(route.params.view || 'dashboard'))
const title = computed(
  () =>
    ({
      dashboard: 'Dashboard',
      devices: 'Devices',
      sites: 'Sites',
      topology: 'Topology',
      components: 'Components',
    })[view.value] || 'Dashboard',
)
watch(view, async (page) => {
  const previous = navigation.value
    ?.querySelector('.active')
    ?.getBoundingClientRect()
  await nextTick()
  if (view.value !== page || !previous) return
  const highlight =
    navigation.value?.querySelector<HTMLElement>('.nav-highlight')
  if (!highlight || !highlight.getClientRects().length) return
  const current = highlight.getBoundingClientRect()
  play(
    highlight,
    {
      transform: [
        `translate(${previous.left - current.left}px, ${previous.top - current.top}px)`,
        'none',
      ],
      width: [`${previous.width}px`, `${current.width}px`],
    },
    0.14,
  )
})
watch(
  () => [view.value, query('tenant'), query('site')],
  () => play(workspace.value, { opacity: [0.85, 1] }, 0.12),
  { flush: 'post' },
)
watch(
  () => message.value || move.value?.deviceId,
  (value) => {
    if (value)
      play(notice.value, {
        opacity: [0.6, 1],
        transform: ['translateY(-4px)', 'none'],
      })
  },
  { flush: 'post' },
)
async function toggleSidebar() {
  if (!sidebar.value || !mainShell.value) return
  cancel(sidebar.value)
  cancel(mainShell.value)
  const width = getComputedStyle(sidebar.value).width
  const margin = getComputedStyle(mainShell.value).marginLeft
  sidebarCollapsed.value = !sidebarCollapsed.value
  await nextTick()
  if (window.matchMedia('(min-width: 801px)').matches) {
    play(sidebar.value, {
      width: [width, getComputedStyle(sidebar.value).width],
    })
    play(mainShell.value, {
      marginLeft: [margin, getComputedStyle(mainShell.value).marginLeft],
    })
  } else if (!sidebarCollapsed.value) {
    play(
      sidebar.value.querySelector('nav') ?? undefined,
      { opacity: [0.6, 1] },
      0.1,
    )
  }
}
function query(key: string): string {
  const value = route.query[key]
  return typeof value === 'string' ? value : ''
}
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
  ).sort((a, b) => {
    const order =
      sortKey.value === 'status'
        ? severity[a.health] - severity[b.health]
        : sortKey.value === 'site'
          ? siteName(a.siteId).localeCompare(siteName(b.siteId))
          : 0
    return (ascending.value ? 1 : -1) * (order || a.name.localeCompare(b.name))
  }),
)
const scopedSites = computed(() =>
  sites.filter((site) => tenantIds(query('tenant')).includes(site.tenantId)),
)
const visibleSites = computed(() =>
  scopedSites.value.filter(
    (site) => !query('site') || site.id === query('site'),
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
async function setQuery(key: string, value: string) {
  try {
    await router.replace({
      query: {
        ...route.query,
        [key]: value || undefined,
        ...(key === 'tenant' ? { site: undefined } : {}),
      },
    })
  } catch {
    message.value = 'Could not update this view. Try again.'
  }
}
// Clearing filters keeps the tenant and site: widening scope is a separate,
// visible choice in the breadcrumb.
async function clearFilters() {
  try {
    await router.replace({
      query: { ...route.query, search: undefined, health: undefined },
    })
  } catch {
    message.value = 'Could not clear the filters. Try again.'
  }
}
// A tenant or site in the URL that no longer resolves is an error, not an
// empty scope: an empty scope would report every device as healthy.
const scopeError = computed(() => {
  if (query('tenant') && !tenants.some((item) => item.id === query('tenant')))
    return {
      text: 'This tenant does not exist or is not available to you.',
      action: 'Show all tenants',
      key: 'tenant',
    }
  if (
    query('site') &&
    !scopedSites.value.some((item) => item.id === query('site'))
  )
    return {
      text: 'This site does not exist in the selected tenant.',
      action: 'Show all sites',
      key: 'site',
    }
  return undefined
})
async function focusSite(id: string) {
  await setQuery('site', id)
  workspace.value?.scrollTo({ top: 0 })
}
function valueOf(event: Event): string {
  return event.target instanceof HTMLInputElement ||
    event.target instanceof HTMLSelectElement
    ? event.target.value
    : ''
}
function siteName(id: string) {
  return sites.find((site) => site.id === id)?.name || 'Unknown site'
}
function tenantName(siteId: string) {
  return (
    tenants.find(
      (tenant) =>
        tenant.id === sites.find((site) => site.id === siteId)?.tenantId,
    )?.name || 'Unknown tenant'
  )
}
const selectedIssues = computed(() =>
  events
    .filter(
      (event) =>
        event.deviceId === selected.value?.id && event.severity !== 'info',
    )
    .sort((a, b) => a.minutesAgo - b.minutesAgo),
)
function integration(id: string) {
  return integrations.find((item) => item.id === id)
}
const polling = ref(false)
const pollResult = ref('')
let pollTimer: ReturnType<typeof setTimeout> | undefined
function stopPoll() {
  clearTimeout(pollTimer)
  polling.value = false
  pollResult.value = ''
}
// The fixture answers after a short delay so the panel shows the difference
// between a poll that was sent and one that was observed.
function poll() {
  const device = selected.value
  if (!device || polling.value) return
  polling.value = true
  pollResult.value = ''
  pollTimer = setTimeout(() => {
    const updated = pollDevice(device)
    fleet.value = fleet.value.map((item) =>
      item.id === updated.id ? updated : item,
    )
    if (selected.value?.id === updated.id) selected.value = updated
    polling.value = false
    pollResult.value =
      updated.lastSeenMinutes === 0
        ? `Answered just now. Still ${updated.health.toLowerCase()}.`
        : `Still not answering. Last answer ${formatAgo(updated.lastSeenMinutes)}.`
  }, 1200)
}
async function openDevice(device: Device) {
  stopPoll()
  selected.value = device
  destination.value = device.siteId
  await nextTick()
  detail.value?.showModal()
  play(
    detail.value,
    { opacity: [0.75, 1], transform: ['translateX(12px)', 'none'] },
    0.16,
  )
}
let moveTimer: ReturnType<typeof setTimeout> | undefined
// A move is reported as done only once the fixture observes the device at its
// new site; until then the device stays where it was.
function startMove(device: Device, siteId: string) {
  const updated = moveDevice(device, siteId)
  clearTimeout(moveTimer)
  message.value = ''
  move.value = {
    deviceId: device.id,
    name: device.name,
    from: device.siteId,
    to: siteId,
    observed: false,
  }
  moveTimer = setTimeout(() => {
    fleet.value = fleet.value.map((item) =>
      item.id === updated.id
        ? { ...updated, throughput: item.throughput }
        : item,
    )
    if (selected.value?.id === updated.id)
      selected.value = fleet.value.find((item) => item.id === updated.id)
    if (move.value?.deviceId === updated.id) move.value.observed = true
  }, 1200)
}
function undoMove() {
  const last = move.value
  const device = fleet.value.find((item) => item.id === last?.deviceId)
  if (!last || !device) return
  startMove(device, last.from)
}
function dismissNotice() {
  message.value = ''
  if (move.value?.observed) move.value = undefined
}
function reassign() {
  if (!selected.value) return
  try {
    startMove(selected.value, destination.value)
    detail.value?.close()
  } catch (error: unknown) {
    message.value =
      error instanceof Error ? error.message : 'Could not assign site.'
  }
}
let timer: ReturnType<typeof setInterval> | undefined
onMounted(() => {
  timer = setInterval(() => {
    tick.value++
    for (const [index, device] of fleet.value.entries()) {
      if (device.health !== 'Offline')
        device.throughput = Math.max(
          1,
          device.throughput + ((tick.value + index) % 5) - 2,
        )
    }
  }, 2500)
})
onUnmounted(() => {
  clearInterval(timer)
  clearTimeout(pollTimer)
  clearTimeout(moveTimer)
})
</script>

<template>
  <div class="shell" :class="{ 'sidebar-collapsed': sidebarCollapsed }">
    <a class="skip-link" href="#main">Skip to main content</a>
    <aside id="workspace-sidebar" ref="sidebar" class="sidebar brand-glow">
      <div
        class="product-brand"
        role="img"
        aria-label="FlowSeer"
        title="FlowSeer"
      >
        <svg
          class="flowseer-mark"
          viewBox="0 0 32 32"
          fill="none"
          aria-hidden="true"
        >
          <g transform="translate(16 16) skewX(-13) translate(-16 -16)">
            <rect
              x="5.5"
              y="4.5"
              width="5"
              height="23"
              rx="1.5"
              fill="var(--cyan)"
            />
            <rect
              x="13.5"
              y="0"
              width="5"
              height="32"
              rx="1.5"
              fill="var(--cyan)"
            />
            <rect
              x="21.5"
              y="7.5"
              width="5"
              height="17"
              rx="1.5"
              fill="var(--coral)"
            />
          </g>
        </svg>
        <span>FlowSeer</span>
      </div>
      <nav ref="navigation" aria-label="Main navigation">
        <RouterLink
          v-for="item in [
            'dashboard',
            'devices',
            'sites',
            'topology',
            ...(showComponents ? ['components'] : []),
          ]"
          :key="item"
          :aria-label="
            item.charAt(0).toUpperCase() +
            item.slice(1) +
            (item === 'devices' ? `, ${fleet.length}` : '')
          "
          :title="item.charAt(0).toUpperCase() + item.slice(1)"
          :to="{ path: `/${item}`, query: route.query }"
          :class="{
            active: view === item,
            'desktop-navigation': item === 'topology' || item === 'components',
          }"
          :aria-current="view === item ? 'page' : undefined"
          ><span
            v-if="view === item"
            class="nav-highlight"
            aria-hidden="true"
          ></span
          ><AppIcon :name="item" /><span>{{
            item.charAt(0).toUpperCase() + item.slice(1)
          }}</span
          ><span v-if="item === 'devices'" class="nav-count">{{
            fleet.length
          }}</span></RouterLink
        >
      </nav>
      <button
        class="sidebar-toggle"
        type="button"
        aria-controls="workspace-sidebar"
        :aria-expanded="!sidebarCollapsed"
        :aria-label="sidebarCollapsed ? 'Expand sidebar' : 'Collapse sidebar'"
        :title="sidebarCollapsed ? 'Expand sidebar' : 'Collapse sidebar'"
        @click="toggleSidebar"
      >
        <svg
          viewBox="0 0 16 16"
          aria-hidden="true"
          fill="none"
          stroke="currentColor"
          stroke-width="1.5"
        >
          <path d="m10 4-4 4 4 4" />
        </svg>
      </button>
    </aside>
    <div ref="mainShell" class="main-shell">
      <span class="main-notch brand-glow" aria-hidden="true"></span>
      <header ref="topbar" class="topbar">
        <span class="topbar-glass brand-glow" aria-hidden="true"></span>
        <div class="topbar-start">
          <nav class="breadcrumb" aria-label="Breadcrumb">
            <template v-if="tenants.length > 1">
              <TenantSwitcher
                :tenants="tenants"
                :selected="query('tenant')"
                @change="setQuery('tenant', $event)"
              />
              <span class="breadcrumb-separator" aria-hidden="true">/</span>
            </template>
            <strong>{{ title }}</strong>
            <span
              v-if="view !== 'components'"
              class="breadcrumb-separator"
              aria-hidden="true"
              >/</span
            >
            <div v-if="view !== 'components'" class="breadcrumb-scope">
              Viewing
              <ScopeSwitcher
                label="Site scope"
                :selected="query('site')"
                :options="[
                  { value: '', label: 'All sites' },
                  ...scopedSites.map((site) => ({
                    value: site.id,
                    label: site.name,
                  })),
                ]"
                @change="setQuery('site', $event)"
              />
            </div>
          </nav>
        </div>
        <div class="topbar-tools">
          <ThemeSwitcher />
          <HelpButton />
          <ReportBugButton />
          <AccountMenu />
        </div>
      </header>
      <main id="main" ref="workspace" tabindex="-1">
        <ComponentsView v-if="view === 'components'" />
        <template v-else>
          <div class="page-heading">
            <div>
              <h1>{{ title }}</h1>
              <p v-if="!scopeError">
                {{ scopeSummary
                }}<template v-if="view === 'topology'"
                  >. Links are illustrative until topology is
                  discovered.</template
                ><template v-if="view === 'devices' && scope.length > healthy">
                  · {{ scope.length - healthy }} need attention</template
                >
              </p>
            </div>
          </div>
          <div class="notice-region" role="status">
            <div v-if="message || move" ref="notice" class="notice">
              <span v-if="message">{{ message }}</span>
              <span v-else-if="move && !move.observed"
                >Moving {{ move.name }} from {{ siteName(move.from) }} to
                {{ siteName(move.to) }}…</span
              >
              <span v-else-if="move"
                >{{ move.name }} is now at {{ siteName(move.to) }} (was
                {{ siteName(move.from) }}).</span
              >
              <button
                v-if="!message && move?.observed"
                class="notice-action"
                @click="undoMove"
              >
                Undo</button
              ><button
                v-if="message || move?.observed"
                aria-label="Dismiss notification"
                @click="dismissNotice"
              >
                <AppIcon name="close" />
              </button>
            </div>
          </div>
          <div v-if="scopeError" class="scope-error" role="alert">
            <h2>Scope not found</h2>
            <p>{{ scopeError.text }}</p>
            <UiButton @click="setQuery(scopeError.key, '')">{{
              scopeError.action
            }}</UiButton>
          </div>
          <template v-else>
            <section
              v-if="view === 'dashboard'"
              class="metrics"
              aria-label="Fleet summary"
            >
              <MetricCard
                label="Devices in scope"
                :value="scope.length"
                unit="devices"
                icon="devices"
              >
                Across {{ visibleSites.length }}
                {{ visibleSites.length === 1 ? 'site' : 'sites' }}
              </MetricCard>
              <MetricCard
                label="Fleet health"
                :value="
                  scope.length ? Math.round((healthy / scope.length) * 100) : 0
                "
                unit="%"
                icon="pulse"
              >
                <b>{{ healthy }} healthy</b> · {{ scope.length - healthy }} need
                attention
              </MetricCard>
              <MetricCard
                label="Connected clients"
                :value="clients"
                icon="topology"
                >Reported by access points</MetricCard
              >
              <MetricCard
                label="Device traffic"
                :value="throughput"
                unit="Mbps"
                icon="pulse"
                >Updates every 2.5s</MetricCard
              >
            </section>
            <template v-if="view === 'devices'">
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
                      <option value="attention">
                        Needs attention ({{ scope.length - healthy }})
                      </option>
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
                    <button
                      :aria-label="`View status for ${device.name}`"
                      @click="openDevice(device)"
                    >
                      <strong>{{ device.name }}</strong>
                      <StatusBadge :status="device.health" />
                      <small
                        >{{ siteName(device.siteId) }} ·
                        {{ device.address }}</small
                      >
                      <span class="mobile-device-action"
                        >View status <AppIcon name="arrow"
                      /></span>
                    </button>
                  </li>
                </ul>
                <div class="table-scroll">
                  <table>
                    <thead>
                      <tr>
                        <th
                          v-for="column in [
                            { key: 'name', label: 'Device name' },
                            { key: 'status', label: 'Status' },
                            { key: 'site', label: 'Site / tenant' },
                          ] as const"
                          :key="column.key"
                          :aria-sort="
                            sortKey === column.key
                              ? ascending
                                ? 'ascending'
                                : 'descending'
                              : undefined
                          "
                        >
                          <button
                            class="sort-button"
                            @click="sortBy(column.key)"
                          >
                            {{ column.label
                            }}<span
                              v-if="sortKey === column.key"
                              aria-hidden="true"
                              >{{ ascending ? ' ↑' : ' ↓' }}</span
                            >
                          </button>
                        </th>
                        <th>IP address</th>
                        <th class="numeric">Clients</th>
                        <th class="numeric">Traffic</th>
                      </tr>
                    </thead>
                    <tbody>
                      <tr v-for="device in filtered" :key="device.id">
                        <td>
                          <button
                            class="device-button"
                            @click="openDevice(device)"
                          >
                            <span class="device-icon"
                              ><AppIcon
                                :name="
                                  device.kind.includes('AP')
                                    ? 'access-point'
                                    : 'devices'
                                " /></span
                            ><span
                              ><strong>{{ device.name }}</strong
                              ><small>{{ device.kind }}</small></span
                            >
                          </button>
                        </td>
                        <td>
                          <StatusBadge :status="device.health" />
                        </td>
                        <td>
                          <span class="site-name">{{
                            siteName(device.siteId)
                          }}</span
                          ><small>{{ tenantName(device.siteId) }}</small>
                        </td>
                        <td class="mono">{{ device.address }}</td>
                        <td class="numeric">{{ device.clients || '—' }}</td>
                        <td class="numeric traffic">
                          <template v-if="device.health === 'Offline'"
                            >—<span class="sr-only"
                              >no data while offline</span
                            ></template
                          ><template v-else
                            >{{ device.throughput }} <span>Mbps</span></template
                          >
                        </td>
                      </tr>
                    </tbody>
                  </table>
                  <div v-if="!filtered.length" class="empty">
                    <AppIcon name="search" />
                    <h3>No devices match this view</h3>
                    <p>Try a different search, status, or site.</p>
                    <button @click="clearFilters">
                      Clear search and status
                    </button>
                  </div>
                </div>
                <footer class="table-footer">
                  <span
                    >Showing {{ filtered.length }} of
                    {{ scope.length }} devices</span
                  >
                </footer>
              </section>
            </template>
            <DashboardView
              v-else-if="view === 'dashboard'"
              :scope="scope"
              :sites="visibleSites"
              :site="sites.find((site) => site.id === query('site'))"
              :tenant-name="tenantName"
              @open="openDevice"
              @site="focusSite"
            />
            <section
              v-else-if="view === 'sites'"
              class="site-grid"
              aria-label="Sites"
            >
              <article
                v-for="site in visibleSites"
                :key="site.id"
                class="site-card"
              >
                <span class="site-symbol"><AppIcon name="sites" /></span
                ><small class="site-location">{{ site.location }}</small>
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
                          device.siteId === site.id &&
                          device.health !== 'Healthy',
                      ).length
                    }}
                    need attention</span
                  >
                </div>
                <RouterLink
                  :to="{
                    path: '/devices',
                    query: { tenant: query('tenant'), site: site.id },
                  }"
                  >View devices <AppIcon name="arrow"
                /></RouterLink>
              </article>
            </section>
            <section v-else class="topology-panel">
              <div class="section-heading">
                <div>
                  <h2>Site connections</h2>
                </div>
              </div>
              <div class="topology-grid">
                <article
                  v-for="site in visibleSites"
                  :key="site.id"
                  class="topology-site"
                >
                  <h3>{{ site.name }}</h3>
                  <p>{{ tenantName(site.id) }}</p>
                  <div class="connection-tree">
                    <button
                      v-for="device in fleet.filter(
                        (item) =>
                          item.siteId === site.id && !item.kind.includes('AP'),
                      )"
                      :key="device.id"
                      :class="['node', device.health.toLowerCase()]"
                      @click="openDevice(device)"
                    >
                      <AppIcon name="devices" /><strong>{{
                        device.name
                      }}</strong
                      ><StatusBadge :status="device.health" />
                    </button>
                    <div
                      class="node-branch"
                      :style="{
                        '--branches': fleet.filter(
                          (item) =>
                            item.siteId === site.id && item.kind.includes('AP'),
                        ).length,
                      }"
                    >
                      <button
                        v-for="device in fleet.filter(
                          (item) =>
                            item.siteId === site.id && item.kind.includes('AP'),
                        )"
                        :key="device.id"
                        :class="['node', device.health.toLowerCase()]"
                        @click="openDevice(device)"
                      >
                        <AppIcon name="access-point" /><strong>{{
                          device.name
                        }}</strong
                        ><StatusBadge :status="device.health" />
                      </button>
                    </div>
                  </div>
                </article>
              </div>
            </section>
          </template>
        </template>
      </main>
    </div>
    <dialog
      ref="detail"
      class="device-dialog"
      aria-labelledby="detail-title"
      @close="stopPoll"
    >
      <template v-if="selected"
        ><div class="dialog-heading">
          <span class="dialog-scope"
            >{{ tenantName(selected.siteId) }} /
            {{ siteName(selected.siteId) }}</span
          ><button
            class="icon-button"
            aria-label="Close device details"
            @click="detail?.close()"
          >
            <AppIcon name="close" />
          </button>
        </div>
        <h2 id="detail-title">{{ selected.name }}</h2>
        <p class="device-meta">
          {{ selected.kind }} · <span class="mono">{{ selected.address }}</span>
        </p>
        <div class="device-status">
          <StatusBadge :status="selected.health" /><span
            >Last answered {{ formatAgo(selected.lastSeenMinutes) }}</span
          >
        </div>
        <section class="device-section" aria-labelledby="issues-title">
          <h3 id="issues-title">
            {{
              selected.health === 'Healthy'
                ? 'No open issues'
                : 'Why it needs attention'
            }}
          </h3>
          <ol v-if="selectedIssues.length" class="device-issues">
            <li
              v-for="event in selectedIssues"
              :key="event.id"
              :class="event.severity"
            >
              <i aria-hidden="true"></i>
              <div>
                <strong>{{ event.summary }}</strong
                ><small
                  >{{ formatAgo(event.minutesAgo) }}
                  <span class="sr-only">, severity {{ event.severity }}</span>
                </small>
              </div>
            </li>
          </ol>
          <p v-else-if="selected.health === 'Healthy'">
            Every path to this device answered its last poll.
          </p>
          <p v-else>No event explains this status yet.</p>
          <div v-if="selected.health !== 'Healthy'" class="device-poll">
            <UiButton variant="primary" :disabled="polling" @click="poll">
              {{ polling ? 'Polling…' : 'Poll now' }}
            </UiButton>
            <p role="status">{{ pollResult }}</p>
          </div>
        </section>
        <section class="device-section" aria-labelledby="paths-title">
          <h3 id="paths-title">How FlowSeer reaches it</h3>
          <ul class="device-paths">
            <li
              v-for="binding in selected.bindings"
              :key="binding.integrationId"
            >
              <span
                ><strong>{{ integration(binding.integrationId)?.name }}</strong
                ><small>{{
                  integration(binding.integrationId)?.kind
                }}</small></span
              ><span
                :class="['reachability', binding.reachability.toLowerCase()]"
                ><strong>{{ binding.reachability }}</strong
                ><small
                  >checked {{ formatAgo(binding.observedMinutesAgo) }}</small
                ></span
              >
            </li>
          </ul>
          <dl>
            <div>
              <dt>Lifecycle</dt>
              <dd>{{ selected.lifecycle }}</dd>
            </div>
            <div>
              <dt>Clients</dt>
              <dd>
                {{ selected.health === 'Offline' ? '—' : selected.clients }}
              </dd>
            </div>
          </dl>
        </section>
        <details class="device-move">
          <summary>Move to another site</summary>
          <form @submit.prevent="reassign">
            <p>
              A device belongs to one site. Moving it replaces its current
              assignment.
            </p>
            <label for="destination">Site within this tenant</label
            ><select id="destination" v-model="destination">
              <option
                v-for="site in allowedSites"
                :key="site.id"
                :value="site.id"
              >
                {{ site.name }}
              </option></select
            ><UiButton
              type="submit"
              :disabled="destination === selected.siteId"
            >
              Move device
            </UiButton>
          </form>
        </details></template
      >
    </dialog>
  </div>
</template>

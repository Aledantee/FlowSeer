<script setup lang="ts">
import { computed, nextTick, onMounted, onUnmounted, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useMotionFeedback } from './motion/useMotionFeedback'
import ComponentsView from './ComponentsView.vue'
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
  sites,
  tenants,
  tenantIds,
  filterDevices,
  moveDevice,
} from './domain/fleet'
import type { Device } from './domain/fleet'
const { play, cancel } = useMotionFeedback()
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
const detail = ref<HTMLDialogElement>()
const selected = ref<Device>()
const destination = ref('')
const ascending = ref(true)
const view = computed(() => String(route.params.view || 'devices'))
const title = computed(
  () =>
    ({
      devices: 'Devices',
      sites: 'Sites',
      topology: 'Topology',
      components: 'Components',
    })[view.value] || 'Devices',
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
  message,
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
  ).sort((a, b) =>
    ascending.value
      ? a.name.localeCompare(b.name)
      : b.name.localeCompare(a.name),
  ),
)
const scopedSites = computed(() =>
  sites.filter((site) => tenantIds(query('tenant')).includes(site.tenantId)),
)
const visibleSites = computed(() =>
  scopedSites.value.filter(
    (site) => !query('site') || site.id === query('site'),
  ),
)
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
async function resetFilters() {
  try {
    await router.replace({ path: '/devices' })
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
async function openDevice(device: Device) {
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
function reassign() {
  if (!selected.value) return
  try {
    const updated = moveDevice(selected.value, destination.value)
    fleet.value = fleet.value.map((device) =>
      device.id === updated.id ? updated : device,
    )
    selected.value = updated
    message.value = `${updated.name} assigned to ${siteName(updated.siteId)}.`
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
onUnmounted(() => clearInterval(timer))
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
      <div class="nav-label">WORKSPACE</div>
      <nav ref="navigation" aria-label="Main navigation">
        <RouterLink
          v-for="item in ['devices', 'sites', 'topology', 'components']"
          :key="item"
          :aria-label="item"
          :title="item.charAt(0).toUpperCase() + item.slice(1)"
          :to="{ path: `/${item}`, query: route.query }"
          :class="{
            active: view === item,
            'desktop-navigation': item === 'topology',
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
              <span class="eyebrow">NETWORK OPERATIONS</span>
              <h1>{{ title }}</h1>
              <p>
                {{
                  view === 'devices'
                    ? 'Monitor health and keep your fleet connected.'
                    : view === 'sites'
                      ? 'A clear view of every location in your network.'
                      : 'Explore the devices connected at each site.'
                }}
              </p>
            </div>
          </div>
          <div v-if="message" ref="notice" role="status" class="notice">
            {{ message
            }}<button aria-label="Dismiss notification" @click="message = ''">
              <AppIcon name="close" />
            </button>
          </div>
          <section class="metrics" aria-label="Fleet summary">
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
            <div
              v-if="scope.some((device) => device.health !== 'Healthy')"
              class="attention"
            >
              <span class="attention-icon">!</span>
              <div>
                <strong
                  >{{ scope.length - healthy }}
                  {{
                    scope.length - healthy === 1
                      ? 'device needs'
                      : 'devices need'
                  }}
                  attention</strong
                ><span
                  >Review degraded or offline devices in the current
                  scope.</span
                >
              </div>
              <button
                @click="setQuery('health', query('health') ? '' : 'attention')"
              >
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
                      <th :aria-sort="ascending ? 'ascending' : 'descending'">
                        <button
                          class="sort-button"
                          @click="ascending = !ascending"
                        >
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
                                device.kind.includes('AP') ? 'pulse' : 'devices'
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
                        {{ device.throughput }} <span>Mbps</span>
                      </td>
                      <td>
                        <button
                          class="icon-button"
                          :aria-label="`Details for ${device.name}`"
                          @click="openDevice(device)"
                        >
                          <AppIcon name="arrow" />
                        </button>
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
              </div>
              <footer class="table-footer">
                <span
                  >Showing {{ filtered.length }} of
                  {{ scope.length }} devices</span
                >
              </footer>
            </section>
          </template>
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
                      (item) => item.siteId === site.id,
                    )"
                    :key="device.id"
                    :class="['node', device.health.toLowerCase()]"
                    @click="openDevice(device)"
                  >
                    <AppIcon
                      :name="device.kind.includes('AP') ? 'pulse' : 'devices'"
                    /><strong>{{ device.name }}</strong
                    ><small>{{ device.health }}</small>
                  </button>
                </div>
              </article>
            </div>
          </section>
        </template>
      </main>
    </div>
    <dialog ref="detail" class="device-dialog" aria-labelledby="detail-title">
      <template v-if="selected"
        ><div class="dialog-heading">
          <span class="eyebrow">DEVICE DETAILS</span
          ><button
            class="icon-button"
            aria-label="Close device details"
            @click="detail?.close()"
          >
            <AppIcon name="close" />
          </button>
        </div>
        <h2 id="detail-title">{{ selected.name }}</h2>
        <p>{{ selected.kind }} · {{ selected.address }}</p>
        <StatusBadge :status="selected.health" />
        <dl>
          <div>
            <dt>Tenant</dt>
            <dd>{{ tenantName(selected.siteId) }}</dd>
          </div>
          <div>
            <dt>Assigned site</dt>
            <dd>{{ siteName(selected.siteId) }}</dd>
          </div>
          <div>
            <dt>Clients</dt>
            <dd>{{ selected.clients }}</dd>
          </div>
        </dl>
        <form @submit.prevent="reassign">
          <h3>Site assignment</h3>
          <p>
            A device belongs to one site. Changing the site replaces its current
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
            variant="primary"
            :disabled="destination === selected.siteId"
          >
            Save assignment
          </UiButton>
        </form></template
      >
    </dialog>
  </div>
</template>

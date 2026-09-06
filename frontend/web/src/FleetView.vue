<script setup lang="ts">
import { computed, nextTick, onMounted, onUnmounted, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import AppIcon from './components/AppIcon.vue'
import {
  devices,
  sites,
  tenants,
  tenantIds,
  filterDevices,
  moveDevice,
} from './domain/fleet'
import type { Device } from './domain/fleet'
const route = useRoute()
const router = useRouter()
const fleet = ref(devices.map((device) => ({ ...device })))
const live = ref(true)
const tick = ref(0)
const message = ref('')
const detail = ref<HTMLDialogElement>()
const selected = ref<Device>()
const destination = ref('')
const ascending = ref(true)
const view = computed(() => String(route.params.view || 'devices'))
const title = computed(
  () =>
    ({ devices: 'Devices', sites: 'Sites', topology: 'Topology' })[
      view.value
    ] || 'Devices',
)
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
}
function reassign() {
  if (!selected.value) return
  try {
    const updated = moveDevice(selected.value, destination.value)
    fleet.value = fleet.value.map((device) =>
      device.id === updated.id ? updated : device,
    )
    selected.value = updated
    message.value = `${updated.name} assigned to ${siteName(updated.siteId)} in this demo.`
    detail.value?.close()
  } catch (error: unknown) {
    message.value =
      error instanceof Error ? error.message : 'Could not assign site.'
  }
}
let timer: ReturnType<typeof setInterval> | undefined
onMounted(() => {
  timer = setInterval(() => {
    if (!live.value) return
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
  <div class="shell">
    <a class="skip-link" href="#main">Skip to main content</a>
    <aside class="sidebar">
      <a class="brand" href="/devices"
        ><span class="brand-mark"><i></i><i></i><i></i></span>FlowSeer<span
          class="brand-period"
          >.</span
        ></a
      >
      <div class="workspace">
        <span class="workspace-avatar">m3</span>
        <div>
          <strong>Operations workspace</strong><small>Provider console</small>
        </div>
      </div>
      <div class="nav-label">WORKSPACE</div>
      <nav aria-label="Main navigation">
        <RouterLink
          v-for="item in ['devices', 'sites', 'topology']"
          :key="item"
          :aria-label="item"
          :to="{ path: `/${item}`, query: route.query }"
          :class="{ active: view === item }"
          :aria-current="view === item ? 'page' : undefined"
          ><AppIcon :name="item" /><span>{{
            item.charAt(0).toUpperCase() + item.slice(1)
          }}</span
          ><span v-if="item === 'devices'" class="nav-count">{{
            fleet.length
          }}</span></RouterLink
        >
      </nav>
      <div class="sidebar-note">
        <span class="little-dot"></span><strong>Room to grow</strong>
        <p>One view across your customers, sites, and network.</p>
        <div class="mini-lines"><i></i><i></i><i></i></div>
      </div>
      <div class="operator">
        <span class="avatar">OP</span>
        <div><strong>Operator</strong><small>All customer tenants</small></div>
        <span class="operator-dot"></span>
      </div>
    </aside>
    <div class="main-shell">
      <header class="topbar">
        <div class="breadcrumb">
          Workspace <span>/</span> <strong>{{ title }}</strong>
        </div>
        <div class="topbar-right">
          <span class="demo-badge">DEMO WORKSPACE</span
          ><span class="avatar small">OP</span>
        </div>
      </header>
      <div class="scopebar">
        <span class="scope-label">Viewing</span
        ><label
          ><span class="sr-only">Tenant scope</span
          ><select
            :value="query('tenant')"
            @change="setQuery('tenant', valueOf($event))"
          >
            <option value="">All customer tenants</option>
            <option
              v-for="tenant in tenants"
              :key="tenant.id"
              :value="tenant.id"
            >
              {{ tenant.parentId ? '↳ ' : '' }}{{ tenant.name }}
            </option>
          </select></label
        ><span class="scope-divider">/</span
        ><label
          ><span class="sr-only">Site scope</span
          ><select
            :value="query('site')"
            @change="setQuery('site', valueOf($event))"
          >
            <option value="">All sites</option>
            <option v-for="site in scopedSites" :key="site.id" :value="site.id">
              {{ site.name }}
            </option>
          </select></label
        ><span class="scope-hint">{{
          query('tenant') ? 'Includes sub-tenants' : 'Across your organization'
        }}</span>
      </div>
      <main id="main" tabindex="-1">
        <div class="page-heading">
          <div>
            <div class="eyebrow">NETWORK OPERATIONS</div>
            <h1>{{ title }}</h1>
            <p>
              {{
                view === 'devices'
                  ? 'Monitor fleet health and find the devices that need attention.'
                  : view === 'sites'
                    ? 'A shared view of every location, with clear tenant ownership.'
                    : 'Explore the connections within each site.'
              }}
            </p>
          </div>
          <button
            class="live-toggle"
            :aria-pressed="live"
            @click="live = !live"
          >
            <span :class="['little-dot', { paused: !live }]"></span
            >{{ live ? 'Live demo' : 'Updates paused'
            }}<span class="toggle-action">{{ live ? 'Pause' : 'Resume' }}</span>
          </button>
        </div>
        <div v-if="message" role="status" class="notice">
          {{ message
          }}<button aria-label="Dismiss notification" @click="message = ''">
            <AppIcon name="close" />
          </button>
        </div>
        <section class="metrics" aria-label="Fleet summary">
          <article>
            <span class="metric-label"
              >Devices in scope <AppIcon name="devices"
            /></span>
            <div class="metric-number">
              {{ scope.length }}<span class="metric-unit">devices</span>
            </div>
            <span class="metric-note"
              >Across {{ visibleSites.length }}
              {{ visibleSites.length === 1 ? 'site' : 'sites' }}</span
            >
          </article>
          <article>
            <span class="metric-label"
              >Fleet health <span class="healthy-dot"></span
            ></span>
            <div class="metric-number">
              {{ scope.length ? Math.round((healthy / scope.length) * 100) : 0
              }}<span class="metric-unit">%</span>
            </div>
            <span class="metric-note"
              ><b>{{ healthy }} healthy</b> · {{ scope.length - healthy }} need
              attention</span
            >
          </article>
          <article>
            <span class="metric-label"
              >Connected clients <AppIcon name="topology"
            /></span>
            <div class="metric-number">
              {{ clients }}
            </div>
            <span class="metric-note">Reported by access points</span>
          </article>
          <article>
            <span class="metric-label"
              >Device traffic <AppIcon name="pulse"
            /></span>
            <div class="metric-number">
              {{ throughput }}<span class="metric-unit">Mbps</span>
            </div>
            <span class="metric-note"
              >Simulated · {{ live ? 'refreshes every 2.5s' : 'paused' }}</span
            >
          </article>
        </section>
        <template v-if="view === 'devices'">
          <div
            v-if="scope.some((device) => device.health !== 'Healthy')"
            class="attention"
          >
            <span class="attention-icon">!</span>
            <div>
              <strong
                >{{ scope.length - healthy }} devices need attention</strong
              ><span
                >Review degraded or offline devices in the current scope.</span
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
                <p>All managed devices in your current scope.</p>
              </div>
              <span class="subtle">Mock data · local session</span>
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
                      <button class="device-button" @click="openDevice(device)">
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
                      <span :class="['status', device.health.toLowerCase()]"
                        ><i></i>{{ device.health }}</span
                      >
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
              ><span>Stable row order during live updates</span>
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
                      device.siteId === site.id && device.health !== 'Healthy',
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
              <p>Illustrative links. Select a device to inspect it.</p>
            </div>
            <span class="demo-badge">DEMO TOPOLOGY</span>
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
        <div class="page-footer">
          <span class="footer-mark">FlowSeer</span
          ><span>Operations workspace</span
          ><span class="footer-right"
            >Design preview · No live infrastructure connected</span
          >
        </div>
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
        <span :class="['status', selected.health.toLowerCase()]"
          ><i></i>{{ selected.health }}</span
        >
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
          ><button class="primary" :disabled="destination === selected.siteId">
            Save demo assignment</button
          ><small>Changes last until this page is reloaded.</small>
        </form></template
      >
    </dialog>
  </div>
</template>

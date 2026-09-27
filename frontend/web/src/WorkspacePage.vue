<script setup lang="ts">
import { computed, defineAsyncComponent, nextTick, ref, watch } from 'vue'
import DashboardView from './DashboardView.vue'
import DeviceView from './DeviceView.vue'
import ClientsView from './ClientsView.vue'
import {
  UiButton,
  UiEmptyState,
  UiInput,
  UiScrollArea,
  UiSegmentedMeter,
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
import { aiTarget, useAiSlot } from './ai'
import type { AiTarget, AiTargetSegment } from './ai'
import { scopeOf, usePage } from './navigation/page'
import { useWorkspace } from './navigation/workspace'
import { useMotionFeedback } from './motion/useMotionFeedback'
import { filterDevices, sites, tenantIds, tenants } from './domain/fleet'
import type { Device } from './domain/fleet'
import {
  formatAgo,
  healthLine,
  latestIssue,
  rankSites,
  siteRollups,
} from './domain/overview'
import type { SiteRollup } from './domain/overview'

// Vue Flow and the ELK layout engine are most of the bundle and only the
// topology page needs them, so they load when it first opens.
const TopologyGraph = defineAsyncComponent(
  () => import('./components/topology/TopologyGraph.vue'),
)
const page = usePage()
const workspace = useWorkspace()
const slot = useAiSlot()
const {
  fleet,
  message,
  move,
  undoMove,
  dismissNotice,
  reassign,
  siteName,
  tenantName,
  peek,
  sideDeviceId,
} = workspace

// Notices show in whichever pane the user is working in.
const noticeHere = computed(
  () => (workspace.activePane.value === 'main') === page.primary,
)
const { play } = useMotionFeedback()
const notice = ref<HTMLElement>()

type SortKey = 'status' | 'name' | 'site' | 'seen'
// Problems sort first by default so an operator never scrolls past healthy
// devices to find the one that needs attention.
const sortKey = ref<SortKey>('status')
const ascending = ref(true)
const severity = { Offline: 0, Degraded: 1, Healthy: 2 }

function sortBy(key: SortKey) {
  ascending.value = sortKey.value === key ? !ascending.value : true
  sortKey.value = key
}

const asOf = ref(new Date())
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

const scopedSites = computed(() =>
  sites.filter((site) => tenantIds(query('tenant')).includes(site.tenantId)),
)

// An unknown site in the query reports an error instead of a healthy
// empty scope: an empty scope would report every device as healthy.
const scopeError = computed(() => {
  if (
    query('site') &&
    !scopedSites.value.some((item) => item.id === query('site'))
  ) {
    return {
      text: `Scope not found: "${query('site')}". It may have been removed or belongs to another tenant.`,
      action: 'Clear site filter',
      key: 'site',
    }
  }
  return undefined
})

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
          : sortKey.value === 'seen'
            ? b.lastSeenMinutes - a.lastSeenMinutes
            : 0
    return (ascending.value ? 1 : -1) * (order || a.name.localeCompare(b.name))
  }),
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
  if (site) return `${site.name} · ${site.location} · ${tenantName(site.id)}`
  const tenant = tenants.find((item) => item.id === query('tenant'))
  const count = visibleSites.value.length
  return `${count} ${count === 1 ? 'site' : 'sites'} across ${tenant ? tenant.name : 'all tenants'}`
})

// Addressable targets. The mobile card and the desktop row are both mounted
// and hidden by CSS, so each carries its layout as a segment and the
// registry lists only the one the viewport shows.
function deviceTarget(device: Device, segment: AiTargetSegment): AiTarget {
  return aiTarget({
    slot,
    view: 'devices',
    kind: 'device',
    entityId: device.id,
    segment,
    label: device.name,
    context: {
      name: device.name,
      kind: device.kind,
      health: device.health,
      address: device.address,
      site: siteName(device.siteId),
      siteId: device.siteId,
      tenant: tenantName(device.siteId),
    },
  })
}
function siteRowTarget(rollup: SiteRollup): AiTarget {
  return aiTarget({
    slot,
    view: 'sites',
    kind: 'site',
    entityId: rollup.site.id,
    label: rollup.site.name,
    context: {
      name: rollup.site.name,
      location: rollup.site.location,
      tenant: tenantName(rollup.site.id),
      health: healthLine(rollup.health),
    },
  })
}
const inventoryTarget = computed(() =>
  aiTarget({
    slot,
    view: 'devices',
    kind: 'view',
    entityId: 'inventory',
    label: 'Device inventory',
    context: { scope: scopeSummary.value },
  }),
)
const sitesViewTarget = computed(() =>
  aiTarget({
    slot,
    view: 'sites',
    kind: 'view',
    entityId: 'sites',
    label: 'Sites',
    context: { scope: scopeSummary.value },
  }),
)

const healthy = computed(
  () => scope.value.filter((device) => device.health === 'Healthy').length,
)
const clients = computed(() =>
  scope.value.reduce(
    (sum, device) => sum + (device.health === 'Offline' ? 0 : device.clients),
    0,
  ),
)
const throughput = computed(() =>
  scope.value.reduce(
    (sum, device) =>
      sum + (device.health === 'Offline' ? 0 : device.throughput),
    0,
  ),
)

const siteRows = computed(() =>
  rankSites(siteRollups(scope.value, visibleSites.value)),
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

// Clearing filters keeps the tenant and site: widening scope is a separate,
// visible choice in the breadcrumb.
async function clearFilters() {
  try {
    await page.go(
      {
        query: {
          ...scopeOf(page.location.value),
          search: undefined,
          health: undefined,
        },
      },
      { replace: true },
    )
  } catch {
    message.value = 'Could not reset filters. Try again.'
  }
}

const statusOptions = computed(() => [
  { value: 'all', label: 'All statuses' },
  ...(scope.value.length > healthy.value
    ? [
        {
          value: 'attention',
          label: `Needs attention (${scope.value.length - healthy.value})`,
        },
      ]
    : []),
  { value: 'Healthy', label: 'Healthy' },
  { value: 'Degraded', label: 'Degraded' },
  { value: 'Offline', label: 'Offline' },
])

function deviceLink(device: Device) {
  return {
    path: `/devices/${device.id}`,
    query: scopeOf(page.location.value),
  }
}

function openMobileDevice(device: Device) {
  void workspace.follow(page, deviceLink(device))
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

async function handleUndo() {
  undoMove()
  await nextTick()
  notice.value?.focus()
}
</script>

<template>
  <TopologyGraph
    v-if="view === 'topology'"
    :focus="query('focus')"
    :fleet="fleet"
    :sites="visibleSites"
    :site-name="siteName"
    :tenant-name="tenantName"
  />
  <template v-else-if="view === 'device'">
    <div
      v-if="(message || move) && noticeHere"
      ref="notice"
      role="status"
      tabindex="-1"
      class="flex items-center justify-between gap-3 px-3.5 py-2.5 bg-card border border-border rounded-panel text-xs text-foreground mb-6 shadow-xs"
    >
      <span>
        <span v-if="message">{{ message }}</span>
        <span v-else-if="move && !move.observed"
          >Moving {{ move.name }} from {{ siteName(move.from) }} to
          {{ siteName(move.to) }}…</span
        >
        <span v-else-if="move?.reverted"
          >Move reverted. {{ move.name }} is back at
          {{ siteName(move.to) }}.</span
        >
        <span v-else-if="move"
          >{{ move.name }} is now at {{ siteName(move.to) }} (was
          {{ siteName(move.from) }}).</span
        >
      </span>
      <div class="flex items-center gap-2">
        <UiButton
          v-if="!message && move?.observed && !move.reverted"
          variant="secondary"
          size="sm"
          @click="handleUndo"
        >
          Undo
        </UiButton>
        <button
          aria-label="Dismiss notification"
          class="p-1 text-muted-foreground hover:text-foreground rounded cursor-pointer"
          @click="dismissNotice"
        >
          <AppIcon name="close" />
        </button>
      </div>
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
    <div
      class="page-heading flex justify-between items-center gap-5 mb-6 max-[800px]:items-start max-[560px]:block"
    >
      <div>
        <h1 class="text-2xl font-bold text-foreground">{{ title }}</h1>
        <p
          v-if="!scopeError"
          class="text-sm text-muted-foreground tabular-nums mt-1.5 max-[800px]:max-w-[330px]"
        >
          {{ scopeSummary }}
          <template v-if="view === 'dashboard'">
            · {{ scope.length }}
            {{ scope.length === 1 ? 'device' : 'devices' }} ·
            {{ clients }} clients · {{ throughput }} Mbps · as of
            {{
              asOf.toLocaleTimeString([], {
                hour: '2-digit',
                minute: '2-digit',
              })
            }}
          </template>
          <template v-if="view === 'devices' && scope.length > healthy">
            · {{ scope.length - healthy }} need attention
          </template>
        </p>
      </div>
    </div>

    <div
      v-if="scopeError"
      class="scope-error flex items-center justify-between gap-3 p-4 mb-6 rounded-panel bg-warning-surface border border-warning-border text-xs text-foreground"
      role="alert"
    >
      <p>{{ scopeError.text }}</p>
      <UiButton
        size="sm"
        variant="secondary"
        @click="setQuery(scopeError.key, '')"
      >
        {{ scopeError.action }}
      </UiButton>
    </div>

    <div
      v-if="(message || move) && noticeHere"
      ref="notice"
      role="status"
      tabindex="-1"
      class="flex items-center justify-between gap-3 px-3.5 py-2.5 bg-card border border-border rounded-panel text-xs text-foreground mb-6 shadow-xs"
    >
      <span>
        <span v-if="message">{{ message }}</span>
        <span v-else-if="move && !move.observed"
          >Moving {{ move.name }} from {{ siteName(move.from) }} to
          {{ siteName(move.to) }}…</span
        >
        <span v-else-if="move?.reverted"
          >Move reverted. {{ move.name }} is back at
          {{ siteName(move.to) }}.</span
        >
        <span v-else-if="move"
          >{{ move.name }} is now at {{ siteName(move.to) }} (was
          {{ siteName(move.from) }}).</span
        >
      </span>
      <div class="flex items-center gap-2">
        <UiButton
          v-if="!message && move?.observed && !move.reverted"
          variant="secondary"
          size="sm"
          @click="handleUndo"
        >
          Undo
        </UiButton>
        <button
          aria-label="Dismiss notification"
          class="p-1 text-muted-foreground hover:text-foreground rounded cursor-pointer"
          @click="dismissNotice"
        >
          <AppIcon name="close" />
        </button>
      </div>
    </div>

    <template v-if="!scopeError">
      <template v-if="view === 'devices'">
        <section
          v-ai-target="inventoryTarget"
          class="bg-card border border-border rounded-panel overflow-hidden shadow-xs"
          aria-labelledby="inventory-title"
        >
          <div
            class="px-6 py-5 pb-4 flex items-center justify-between gap-3 max-[560px]:p-[18px_14px]"
          >
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
          <div
            class="flex items-center gap-3 px-6 pb-5 max-[560px]:flex-wrap max-[560px]:p-[0_14px_15px] max-[560px]:gap-2.5"
          >
            <div
              class="relative flex items-center w-[340px] max-[560px]:w-full"
            >
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
            <div class="w-52 max-[560px]:w-full">
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
            class="mobile-devices max-[560px]:grid hidden max-[560px]:list-none max-[560px]:m-0 max-[560px]:p-0 max-[560px]:px-3.5 border-t border-border"
            aria-label="Device status"
          >
            <li
              v-for="device in filtered"
              :key="device.id"
              class="border-t border-border first:border-t-0"
            >
              <button
                v-ai-target="deviceTarget(device, 'mobile')"
                :data-device-id="device.id"
                class="grid grid-cols-[1fr_auto] gap-2.5 w-full py-4 text-left cursor-pointer border-0 bg-transparent p-0 text-inherit font-inherit"
                @click="openMobileDevice(device)"
              >
                <strong
                  class="font-semibold text-foreground self-center break-words text-sm"
                >
                  {{ device.name }}
                </strong>
                <UiStatusBadge :status="device.health" />
                <small class="text-xs text-muted-foreground">
                  {{ siteName(device.siteId) }} · {{ device.address }} ·
                  answered {{ formatAgo(device.lastSeenMinutes) }}
                </small>
                <span
                  class="inline-flex items-center gap-1 text-xs text-accent-foreground"
                >
                  View status <AppIcon name="arrow" />
                </span>
              </button>
            </li>
          </ul>

          <UiScrollArea
            axis="x"
            viewport-class="table-scroll max-[560px]:hidden"
          >
            <UiTable>
              <UiTableHeader>
                <UiTableRow>
                  <UiTableHead
                    sortable
                    :sort-direction="
                      sortKey === 'name'
                        ? ascending
                          ? 'ascending'
                          : 'descending'
                        : undefined
                    "
                    @sort="sortBy('name')"
                  >
                    <button
                      class="sort-button font-medium inline-flex items-center gap-1 cursor-pointer"
                      @click="sortBy('name')"
                    >
                      Device name<span
                        v-if="sortKey === 'name'"
                        aria-hidden="true"
                        >{{ ascending ? ' ↑' : ' ↓' }}</span
                      >
                    </button>
                  </UiTableHead>
                  <UiTableHead
                    sortable
                    :sort-direction="
                      sortKey === 'status'
                        ? ascending
                          ? 'ascending'
                          : 'descending'
                        : undefined
                    "
                    @sort="sortBy('status')"
                  >
                    <button
                      class="sort-button font-medium inline-flex items-center gap-1 cursor-pointer"
                      @click="sortBy('status')"
                    >
                      Status<span
                        v-if="sortKey === 'status'"
                        aria-hidden="true"
                        >{{ ascending ? ' ↑' : ' ↓' }}</span
                      >
                    </button>
                  </UiTableHead>
                  <UiTableHead
                    sortable
                    :sort-direction="
                      sortKey === 'seen'
                        ? ascending
                          ? 'ascending'
                          : 'descending'
                        : undefined
                    "
                    @sort="sortBy('seen')"
                  >
                    <button
                      class="sort-button font-medium inline-flex items-center gap-1 cursor-pointer"
                      @click="sortBy('seen')"
                    >
                      Last answered<span
                        v-if="sortKey === 'seen'"
                        aria-hidden="true"
                        >{{ ascending ? ' ↑' : ' ↓' }}</span
                      >
                    </button>
                  </UiTableHead>
                  <UiTableHead
                    sortable
                    :sort-direction="
                      sortKey === 'site'
                        ? ascending
                          ? 'ascending'
                          : 'descending'
                        : undefined
                    "
                    @sort="sortBy('site')"
                  >
                    <button
                      class="sort-button font-medium inline-flex items-center gap-1 cursor-pointer"
                      @click="sortBy('site')"
                    >
                      Site / tenant<span
                        v-if="sortKey === 'site'"
                        aria-hidden="true"
                        >{{ ascending ? ' ↑' : ' ↓' }}</span
                      >
                    </button>
                  </UiTableHead>
                  <UiTableHead>IP address</UiTableHead>
                  <UiTableHead align="numeric">Clients</UiTableHead>
                  <UiTableHead align="numeric">Traffic</UiTableHead>
                  <UiTableHead
                    ><span class="sr-only">Details</span></UiTableHead
                  >
                </UiTableRow>
              </UiTableHeader>
              <UiTableBody @click.capture="rememberList">
                <UiTableRow
                  v-for="device in filtered"
                  :key="device.id"
                  v-ai-target="deviceTarget(device, 'desktop')"
                  :data-device-id="device.id"
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
                  <UiTableCell class="seen text-xs text-muted-foreground">
                    {{ formatAgo(device.lastSeenMinutes) }}
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
                    {{
                      device.health === 'Offline' ? '—' : device.clients || '—'
                    }}
                  </UiTableCell>
                  <UiTableCell align="numeric" class="traffic">
                    <template v-if="device.health === 'Offline'">—</template>
                    <template v-else>
                      {{ device.throughput }}
                      <span class="text-2xs text-muted-foreground">Mbps</span>
                    </template>
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
                <UiButton
                  variant="secondary"
                  size="sm"
                  class="cursor-pointer"
                  @click="clearFilters"
                >
                  Clear search and status
                </UiButton>
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
        v-ai-target="sitesViewTarget"
        class="bg-card border border-border rounded-panel overflow-hidden shadow-xs"
        aria-labelledby="sites-title"
      >
        <div class="px-6 py-5 pb-4 flex items-center justify-between gap-3">
          <h2 id="sites-title" class="text-base font-semibold text-foreground">
            Sites
            <span
              class="text-xs bg-subtle px-1.5 py-0.5 rounded text-muted-foreground ml-1.5 font-medium"
            >
              {{ visibleSites.length }}
            </span>
          </h2>
        </div>
        <UiScrollArea axis="x" viewport-class="table-scroll">
          <UiTable>
            <UiTableHeader>
              <UiTableRow>
                <UiTableHead>Site</UiTableHead>
                <UiTableHead class="w-[28%]">Health</UiTableHead>
                <UiTableHead class="max-[560px]:hidden">Open issue</UiTableHead>
                <UiTableHead class="max-[560px]:hidden" align="numeric"
                  >Devices</UiTableHead
                >
                <UiTableHead
                  ><span class="sr-only"
                    >Devices at this site</span
                  ></UiTableHead
                >
              </UiTableRow>
            </UiTableHeader>
            <UiTableBody>
              <UiTableRow
                v-for="rollup in siteRows"
                :key="rollup.site.id"
                v-ai-target="siteRowTarget(rollup)"
              >
                <UiTableCell>
                  <AppLink
                    class="inline-block text-left group"
                    :to="{
                      path: '/dashboard',
                      query: {
                        tenant: rollup.site.tenantId,
                        site: rollup.site.id,
                      },
                    }"
                  >
                    <strong
                      class="font-semibold text-foreground group-hover:text-accent-foreground block text-xs"
                    >
                      {{ rollup.site.name }}
                    </strong>
                    <small class="text-2xs text-muted-foreground block">
                      {{ rollup.site.location }} ·
                      {{ tenantName(rollup.site.id) }}
                    </small>
                  </AppLink>
                </UiTableCell>
                <UiTableCell class="w-[28%]">
                  <UiSegmentedMeter :counts="rollup.health" />
                  <small class="text-2xs text-muted-foreground block mt-1">
                    {{ healthLine(rollup.health) }}
                  </small>
                </UiTableCell>
                <UiTableCell class="max-[560px]:hidden">
                  <template v-if="latestIssue(scope, rollup.site)">
                    <span class="block text-xs text-foreground font-medium">
                      {{ latestIssue(scope, rollup.site)?.summary }}
                    </span>
                    <small class="text-2xs text-muted-foreground block mt-0.5">
                      {{
                        formatAgo(
                          latestIssue(scope, rollup.site)?.minutesAgo ?? 0,
                        )
                      }}
                    </small>
                  </template>
                  <span v-else class="text-xs text-muted-foreground">None</span>
                </UiTableCell>
                <UiTableCell class="max-[560px]:hidden" align="numeric">
                  {{
                    rollup.health.Healthy +
                    rollup.health.Degraded +
                    rollup.health.Offline
                  }}
                </UiTableCell>
                <UiTableCell>
                  <AppLink
                    class="text-xs text-accent-foreground hover:underline"
                    :to="{
                      path: '/devices',
                      query: {
                        tenant: rollup.site.tenantId,
                        site: rollup.site.id,
                      },
                    }"
                  >
                    Devices
                  </AppLink>
                </UiTableCell>
              </UiTableRow>
            </UiTableBody>
          </UiTable>
        </UiScrollArea>
      </section>
    </template>
  </template>
</template>

<style scoped>
:deep(tbody tr.peeked) {
  background: color-mix(in srgb, var(--info-surface) 60%, transparent);
  box-shadow: inset 3px 0 0 var(--accent-foreground);
}
</style>

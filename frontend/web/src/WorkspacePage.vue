<script setup lang="ts">
import { computed, defineAsyncComponent, nextTick, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
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
  useMotionFeedback,
} from './ui'
import AppIcon from './components/AppIcon.vue'
import DeviceIcon from './components/DeviceIcon.vue'
import AppLink from './navigation/AppLink.vue'
import { aiTarget, useAiSlot } from './ai'
import type { AiTarget, AiTargetSegment } from './ai'
import { useFormat } from './i18n/format'
import { useLabels } from './i18n/labels'
import { scopeOf, usePage } from './navigation/page'
import { useWorkspace } from './navigation/workspace'
import { filterDevices, sites, tenantIds, tenants } from './domain/fleet'
import type { Device } from './domain/fleet'
import { latestIssue, rankSites, siteRollups } from './domain/overview'
import type { SiteRollup } from './domain/overview'

// Vue Flow and the ELK layout engine are most of the bundle and only the
// topology page needs them, so they load when it first opens.
const TopologyGraph = defineAsyncComponent(
  () => import('./components/topology/TopologyGraph.vue'),
)
const { t, n } = useI18n({ useScope: 'global' })
const format = useFormat()
const labels = useLabels()
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
const separator = computed(() => t('view.common.factSeparator'))

const selected = computed(() =>
  fleet.value.find((device) => device.id === page.deviceId.value),
)
const title = computed(() => labels.page(view.value))

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
      text: t('view.workspace.scopeNotFound', { site: query('site') }),
      action: t('view.workspace.clearSiteFilter'),
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

const scopeSite = computed(() =>
  sites.find((item) => item.id === query('site')),
)
const scopeTenant = computed(() =>
  tenants.find((item) => item.id === query('tenant')),
)
// The names a site scope lists, each marked as an identifier in the heading.
const scopeSiteParts = computed(() => {
  const site = scopeSite.value
  if (!site) return []
  return [
    { text: site.name, identifier: true },
    { text: site.location, identifier: false },
    { text: tenantName(site.id), identifier: true },
  ]
})
const scopeAcrossKey = computed(() =>
  scopeTenant.value
    ? 'view.workspace.scopeAcrossTenant'
    : 'view.workspace.scopeAcrossAll',
)
const scopeSummary = computed(() => {
  if (scopeSite.value) {
    return format.facts(scopeSiteParts.value.map((part) => part.text))
  }
  const count = visibleSites.value.length
  return t(
    scopeAcrossKey.value,
    { count: n(count, 'integer'), tenant: scopeTenant.value?.name ?? '' },
    count,
  )
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
      health: labels.healthLine(rollup.health),
    },
  })
}
const inventoryTarget = computed(() =>
  aiTarget({
    slot,
    view: 'devices',
    kind: 'view',
    entityId: 'inventory',
    label: t('view.devices.inventory'),
    context: {
      scope: scopeSummary.value,
      search: query('search') || t('view.devices.noSearch'),
      status: query('health') || 'all',
      total: n(scope.value.length, 'integer'),
      matching: n(filtered.value.length, 'integer'),
    },
  }),
)
const sitesViewTarget = computed(() =>
  aiTarget({
    slot,
    view: 'sites',
    kind: 'view',
    entityId: 'sites',
    label: labels.page('sites'),
    context: {
      scope: scopeSummary.value,
      sites: n(visibleSites.value.length, 'integer'),
    },
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

// The facts after the scope summary in the heading line.
const headingFacts = computed(() => {
  const dashboard = view.value === 'dashboard'
  const attention = scope.value.length - healthy.value
  return format.facts([
    dashboard && format.counted('view.common.devices', scope.value.length),
    dashboard && format.counted('view.common.clients', clients.value),
    dashboard && format.rate(throughput.value),
    dashboard && t('view.workspace.asOf', { time: format.clock(asOf.value) }),
    view.value === 'devices' &&
      attention > 0 &&
      t('view.devices.needAttention', { count: n(attention, 'integer') }),
  ])
})

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

// The notice for the move in flight, settled, or reverted.
const moveKey = computed(() =>
  !move.value?.observed
    ? 'view.workspace.moving'
    : move.value.reverted
      ? 'view.workspace.moveReverted'
      : 'view.workspace.moved',
)

watch(
  message,
  (value) => {
    if (value && page.primary)
      play(notice.value, {
        opacity: [0.6, 1],
        y: [-4, 0],
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
    message.value = t('view.workspace.updateFailed')
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
    message.value = t('view.workspace.resetFailed')
  }
}

const statusOptions = computed(() => [
  { value: 'all', label: t('view.devices.allStatuses') },
  ...(scope.value.length > healthy.value
    ? [
        {
          value: 'attention',
          label: t('view.devices.needsAttentionOption', {
            label: t('view.common.needsAttention'),
            count: n(scope.value.length - healthy.value, 'integer'),
          }),
        },
      ]
    : []),
  { value: 'Healthy', label: labels.health('Healthy') },
  { value: 'Degraded', label: labels.health('Degraded') },
  { value: 'Offline', label: labels.health('Offline') },
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
        <I18nT v-else-if="move" scope="global" tag="span" :keypath="moveKey">
          <template #name>
            <span translate="no">{{ move?.name }}</span>
          </template>
          <template #from>
            <span translate="no">{{ siteName(move?.from ?? '') }}</span>
          </template>
          <template #to>
            <span translate="no">{{ siteName(move?.to ?? '') }}</span>
          </template>
        </I18nT>
      </span>
      <div class="flex items-center gap-2">
        <UiButton
          v-if="!message && move?.observed && !move.reverted"
          variant="secondary"
          size="sm"
          @click="handleUndo"
        >
          {{ t('view.workspace.undo') }}
        </UiButton>
        <button
          :aria-label="t('view.workspace.dismissNotification')"
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
      :title="t('view.workspace.deviceMissingTitle')"
      :description="t('view.workspace.deviceMissingDescription')"
    >
      <template #icon>
        <AppIcon name="search" />
      </template>
      <template #actions>
        <AppLink
          class="text-xs text-accent-foreground hover:underline"
          :to="{ path: '/devices' }"
        >
          {{ t('view.workspace.backToDevices') }}
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
          <template v-if="scopeSite">
            <template v-for="(part, index) in scopeSiteParts" :key="index">
              <template v-if="index">{{ separator }}</template>
              <span :translate="part.identifier ? 'no' : undefined">{{
                part.text
              }}</span>
            </template>
          </template>
          <I18nT
            v-else
            scope="global"
            tag="span"
            :keypath="scopeAcrossKey"
            :plural="visibleSites.length"
          >
            <template #count>{{ n(visibleSites.length, 'integer') }}</template>
            <template #tenant>
              <span translate="no">{{ scopeTenant?.name }}</span>
            </template>
          </I18nT>
          <template v-if="headingFacts"
            >{{ separator }}{{ headingFacts }}</template
          >
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
        <I18nT v-else-if="move" scope="global" tag="span" :keypath="moveKey">
          <template #name>
            <span translate="no">{{ move?.name }}</span>
          </template>
          <template #from>
            <span translate="no">{{ siteName(move?.from ?? '') }}</span>
          </template>
          <template #to>
            <span translate="no">{{ siteName(move?.to ?? '') }}</span>
          </template>
        </I18nT>
      </span>
      <div class="flex items-center gap-2">
        <UiButton
          v-if="!message && move?.observed && !move.reverted"
          variant="secondary"
          size="sm"
          @click="handleUndo"
        >
          {{ t('view.workspace.undo') }}
        </UiButton>
        <button
          :aria-label="t('view.workspace.dismissNotification')"
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
                {{ t('view.devices.inventory') }}
                <span
                  class="text-xs bg-subtle px-1.5 py-0.5 rounded text-muted-foreground ml-1.5 font-medium"
                >
                  {{ n(scope.length, 'integer') }}
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
                :placeholder="t('view.devices.searchPlaceholder')"
                :aria-label="t('view.devices.search')"
                class="pl-8"
                @update:model-value="setQuery('search', $event)"
              />
            </div>
            <div class="w-52 max-[560px]:w-full">
              <UiSelect
                :model-value="query('health') || 'all'"
                :options="statusOptions"
                :aria-label="t('view.devices.filterStatus')"
                @update:model-value="
                  setQuery('health', $event === 'all' ? '' : $event)
                "
              />
            </div>
            <span class="ml-auto text-xs text-muted-foreground">
              {{ format.counted('view.common.results', filtered.length) }}
            </span>
          </div>

          <ul
            v-if="filtered.length"
            class="mobile-devices max-[560px]:grid hidden max-[560px]:list-none max-[560px]:m-0 max-[560px]:p-0 max-[560px]:px-3.5 border-t border-border"
            :aria-label="t('view.devices.deviceStatus')"
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
                  translate="no"
                  class="font-semibold text-foreground self-center break-words text-sm"
                >
                  {{ device.name }}
                </strong>
                <UiStatusBadge :status="device.health" />
                <small class="text-xs text-muted-foreground">
                  <span translate="no">{{ siteName(device.siteId) }}</span
                  >{{ separator
                  }}<span translate="no">{{ device.address }}</span
                  >{{ separator
                  }}{{
                    t('view.devices.answered', {
                      age: format.ago(device.lastSeenMinutes),
                    })
                  }}
                </small>
                <span
                  class="inline-flex items-center gap-1 text-xs text-accent-foreground"
                >
                  {{ t('view.devices.viewStatus') }} <AppIcon name="arrow" />
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
                      {{ t('view.devices.deviceName')
                      }}<span v-if="sortKey === 'name'" aria-hidden="true">{{
                        ascending ? ' ↑' : ' ↓'
                      }}</span>
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
                      {{ t('view.devices.status')
                      }}<span v-if="sortKey === 'status'" aria-hidden="true">{{
                        ascending ? ' ↑' : ' ↓'
                      }}</span>
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
                      {{ t('view.devices.lastAnswered')
                      }}<span v-if="sortKey === 'seen'" aria-hidden="true">{{
                        ascending ? ' ↑' : ' ↓'
                      }}</span>
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
                      {{ t('view.devices.siteTenant')
                      }}<span v-if="sortKey === 'site'" aria-hidden="true">{{
                        ascending ? ' ↑' : ' ↓'
                      }}</span>
                    </button>
                  </UiTableHead>
                  <UiTableHead>{{ t('view.devices.ipAddress') }}</UiTableHead>
                  <UiTableHead align="numeric">{{
                    t('view.common.columns.clients')
                  }}</UiTableHead>
                  <UiTableHead align="numeric">{{
                    t('view.common.columns.traffic')
                  }}</UiTableHead>
                  <UiTableHead
                    ><span class="sr-only">{{
                      t('view.devices.details')
                    }}</span></UiTableHead
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
                          translate="no"
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
                    {{ format.ago(device.lastSeenMinutes) }}
                  </UiTableCell>
                  <UiTableCell>
                    <span
                      translate="no"
                      class="font-medium text-foreground block text-xs"
                    >
                      {{ siteName(device.siteId) }}
                    </span>
                    <small
                      translate="no"
                      class="text-2xs text-muted-foreground block"
                    >
                      {{ tenantName(device.siteId) }}
                    </small>
                  </UiTableCell>
                  <UiTableCell mono translate="no">{{
                    device.address
                  }}</UiTableCell>
                  <UiTableCell align="numeric">
                    {{
                      device.health === 'Offline'
                        ? t('view.common.noReading')
                        : device.clients
                          ? n(device.clients, 'integer')
                          : t('view.common.noReading')
                    }}
                  </UiTableCell>
                  <UiTableCell align="numeric" class="traffic">
                    <template v-if="device.health === 'Offline'">{{
                      t('view.common.noReading')
                    }}</template>
                    <I18nT
                      v-else
                      scope="global"
                      keypath="view.common.valueWithUnit"
                    >
                      <template #value>{{
                        n(device.throughput, 'decimal')
                      }}</template>
                      <template #unit>
                        <span class="text-2xs text-muted-foreground">{{
                          t('view.common.units.mbps')
                        }}</span>
                      </template>
                    </I18nT>
                  </UiTableCell>
                  <UiTableCell>
                    <div class="flex items-center gap-1 justify-end">
                      <UiTooltip
                        :label="t('view.devices.peekBeside')"
                        :hint="t('view.devices.peekHint')"
                      >
                        <button
                          class="inline-flex items-center justify-center p-1.5 rounded hover:bg-hover text-muted-foreground hover:text-foreground cursor-pointer"
                          :aria-label="
                            t('view.devices.peekAt', { name: device.name })
                          "
                          @click="peekDevice(device)"
                        >
                          <AppIcon name="panel-right" />
                        </button>
                      </UiTooltip>
                      <AppLink
                        class="inline-flex items-center justify-center p-1.5 rounded hover:bg-hover text-muted-foreground hover:text-foreground"
                        :to="deviceLink(device)"
                        :aria-label="
                          t('view.devices.detailsFor', { name: device.name })
                        "
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
              :title="t('view.devices.emptyTitle')"
              :description="t('view.devices.emptyDescription')"
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
                  {{ t('view.devices.clearFilters') }}
                </UiButton>
              </template>
            </UiEmptyState>
          </UiScrollArea>
          <footer
            class="px-6 py-4 border-t border-border text-xs text-muted-foreground"
          >
            <span>{{
              t(
                'view.devices.showing',
                {
                  shown: n(filtered.length, 'integer'),
                  total: n(scope.length, 'integer'),
                },
                scope.length,
              )
            }}</span>
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
            {{ labels.page('sites') }}
            <span
              class="text-xs bg-subtle px-1.5 py-0.5 rounded text-muted-foreground ml-1.5 font-medium"
            >
              {{ n(visibleSites.length, 'integer') }}
            </span>
          </h2>
        </div>
        <UiScrollArea axis="x" viewport-class="table-scroll">
          <UiTable>
            <UiTableHeader>
              <UiTableRow>
                <UiTableHead>{{ t('view.common.columns.site') }}</UiTableHead>
                <UiTableHead class="w-[28%]">{{
                  t('view.common.columns.health')
                }}</UiTableHead>
                <UiTableHead class="max-[560px]:hidden">{{
                  t('view.sites.openIssue')
                }}</UiTableHead>
                <UiTableHead class="max-[560px]:hidden" align="numeric">{{
                  t('view.common.columns.devices')
                }}</UiTableHead>
                <UiTableHead
                  ><span class="sr-only">{{
                    t('view.sites.devicesAtSite')
                  }}</span></UiTableHead
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
                      translate="no"
                      class="font-semibold text-foreground group-hover:text-accent-foreground block text-xs"
                    >
                      {{ rollup.site.name }}
                    </strong>
                    <small class="text-2xs text-muted-foreground block">
                      {{ rollup.site.location }}{{ separator
                      }}<span translate="no">{{
                        tenantName(rollup.site.id)
                      }}</span>
                    </small>
                  </AppLink>
                </UiTableCell>
                <UiTableCell class="w-[28%]">
                  <UiSegmentedMeter :counts="rollup.health" />
                  <small class="text-2xs text-muted-foreground block mt-1">
                    {{ labels.healthLine(rollup.health) }}
                  </small>
                </UiTableCell>
                <UiTableCell class="max-[560px]:hidden">
                  <template v-if="latestIssue(scope, rollup.site)">
                    <span class="block text-xs text-foreground font-medium">
                      {{ latestIssue(scope, rollup.site)?.summary }}
                    </span>
                    <small class="text-2xs text-muted-foreground block mt-0.5">
                      {{
                        format.ago(
                          latestIssue(scope, rollup.site)?.minutesAgo ?? 0,
                        )
                      }}
                    </small>
                  </template>
                  <span v-else class="text-xs text-muted-foreground">{{
                    t('view.sites.none')
                  }}</span>
                </UiTableCell>
                <UiTableCell class="max-[560px]:hidden" align="numeric">
                  {{
                    n(
                      rollup.health.Healthy +
                        rollup.health.Degraded +
                        rollup.health.Offline,
                      'integer',
                    )
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
                    {{ labels.page('devices') }}
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

<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import AppLink from './navigation/AppLink.vue'
import { scopeOf, usePage } from './navigation/page'
import type { Device } from './domain/fleet'
import type { Band, Client } from './domain/clients'
import { clientsOf, signalQuality } from './domain/clients'
import { aiTarget, useAiSlot } from './ai'
import type { AiTarget } from './ai'
import AppIcon from './components/AppIcon.vue'
import { useFormat } from './i18n/format'
import { useLabels } from './i18n/labels'
import {
  UiEmptyState,
  UiInput,
  UiPagination,
  UiScrollArea,
  UiSelect,
  UiTable,
  UiTableBody,
  UiTableCell,
  UiTableHead,
  UiTableHeader,
  UiTableRow,
} from './ui'

const props = defineProps<{
  scope: Device[]
  siteName: (id: string) => string
}>()
const navPage = usePage()
const slot = useAiSlot()
const { t, n } = useI18n({ useScope: 'global' })
const format = useFormat()
const labels = useLabels()

// The reading and its quality word as one message, shown to an operator and
// carried in an AI target's context.
function signalReading(client: Client) {
  return t('view.common.signalReading', {
    reading: format.quantity(client.signal, 'dbm'),
    quality: labels.signal(signalQuality(client.signal)),
  })
}
const viewTarget = computed(() =>
  aiTarget({
    slot,
    view: 'clients',
    kind: 'view',
    entityId: accessPoint.value ? accessPoint.value.id : 'all',
    label: t('view.common.connectedClients'),
    context: {
      count: n(all.value.length, 'integer'),
      matching: n(filtered.value.length, 'integer'),
      accessPoint: accessPoint.value?.name ?? t('view.clients.allAccessPoints'),
      search: search.value || t('view.clients.noSearch'),
      band: band.value || 'all',
    },
  }),
)
function clientTarget(client: Client): AiTarget {
  return aiTarget({
    slot,
    view: 'clients',
    kind: 'client',
    entityId: client.id,
    label: client.hostname,
    context: {
      hostname: client.hostname,
      address: client.address,
      mac: client.mac,
      band: client.band,
      signal: signalReading(client),
      accessPoint:
        devicesById.value.get(client.deviceId)?.name ??
        t('view.common.unknownDevice'),
    },
  })
}
const pageSize = 50
const page = ref(1)
const search = ref(navPage.query('q'))
watch(
  () => navPage.query('q'),
  (q) => (search.value = q),
)
const band = ref<Band | ''>('')
const BANDS: Band[] = ['2.4 GHz', '5 GHz', '6 GHz']
const bandOptions = computed(() => [
  { value: 'all', label: t('view.clients.allBands') },
  ...BANDS.map((value) => ({ value, label: labels.band(value) })),
])
const accessPoint = computed(() =>
  props.scope.find((device) => device.id === navPage.query('ap')),
)
const all = computed(() =>
  clientsOf(accessPoint.value ? [accessPoint.value] : props.scope),
)
const devicesById = computed(
  () => new Map(props.scope.map((device) => [device.id, device])),
)
const filtered = computed(() => {
  const query = search.value.trim().toLowerCase()
  return all.value.filter(
    (client) =>
      (!band.value || client.band === band.value) &&
      `${client.hostname} ${client.address} ${client.mac}`
        .toLowerCase()
        .includes(query),
  )
})
const paginated = computed(() =>
  filtered.value.slice((page.value - 1) * pageSize, page.value * pageSize),
)
watch([search, band, accessPoint], () => (page.value = 1))
function isBand(value: string): value is Band {
  return BANDS.some((item) => item === value)
}
function setBand(value: string) {
  band.value = isBand(value) ? value : ''
}
function clearAccessPoint() {
  void navPage.go(
    { query: { ...navPage.location.value.query, ap: undefined } },
    { replace: true },
  )
}
</script>

<template>
  <section
    v-ai-target="viewTarget"
    class="bg-card border border-border rounded-panel overflow-hidden shadow-xs"
    aria-labelledby="clients-title"
  >
    <div
      class="px-6 py-5 pb-4 flex items-center justify-between gap-3 max-[560px]:p-[18px_14px]"
    >
      <h2 id="clients-title" class="text-base font-semibold text-foreground">
        {{ t('view.common.connectedClients') }}
        <span
          class="text-xs bg-subtle px-1.5 py-0.5 rounded text-muted-foreground ml-1.5 font-medium"
        >
          {{ n(all.length, 'integer') }}
        </span>
      </h2>
    </div>
    <div
      class="flex items-center gap-3 px-6 pb-5 max-[560px]:flex-wrap max-[560px]:p-[0_14px_15px] max-[560px]:gap-2.5"
    >
      <div class="relative flex items-center w-[340px] max-[560px]:w-full">
        <AppIcon
          name="search"
          class="absolute left-2.5 w-3.5 h-3.5 text-muted-foreground pointer-events-none z-10"
        />
        <UiInput
          v-model="search"
          :placeholder="t('view.clients.searchPlaceholder')"
          :aria-label="t('view.clients.search')"
          class="pl-8"
        />
      </div>
      <div class="w-36 max-[560px]:w-full">
        <UiSelect
          :model-value="band || 'all'"
          :options="bandOptions"
          :aria-label="t('view.clients.filterBand')"
          @update:model-value="setBand($event)"
        />
      </div>
      <button
        v-if="accessPoint"
        class="inline-flex items-center gap-1.5 px-2.5 py-1 text-xs rounded-control bg-subtle text-foreground hover:bg-hover border border-border cursor-pointer"
        :aria-label="
          t('view.clients.stopFiltering', { name: accessPoint.name })
        "
        @click="clearAccessPoint"
      >
        <span translate="no">{{ accessPoint.name }}</span
        ><AppIcon name="close" class="w-3 h-3 text-muted-foreground" />
      </button>
      <span class="ml-auto text-xs text-muted-foreground">
        {{ format.counted('view.common.results', filtered.length) }}
      </span>
    </div>
    <UiScrollArea axis="x" viewport-class="table-scroll">
      <UiTable>
        <UiTableHeader>
          <UiTableRow>
            <UiTableHead>{{ t('view.clients.client') }}</UiTableHead>
            <UiTableHead>{{ t('view.common.macAddress') }}</UiTableHead>
            <UiTableHead>{{ t('view.clients.accessPoint') }}</UiTableHead>
            <UiTableHead>{{ t('view.clients.band') }}</UiTableHead>
            <UiTableHead>{{ t('view.clients.signal') }}</UiTableHead>
            <UiTableHead align="numeric">{{
              t('view.common.columns.traffic')
            }}</UiTableHead>
          </UiTableRow>
        </UiTableHeader>
        <UiTableBody>
          <UiTableRow
            v-for="client in paginated"
            :key="client.id"
            v-ai-target="clientTarget(client)"
          >
            <UiTableCell>
              <strong
                translate="no"
                class="font-medium text-foreground block text-xs"
              >
                {{ client.hostname }}
              </strong>
              <small
                translate="no"
                class="font-mono text-2xs text-muted-foreground block"
              >
                {{ client.address }}
              </small>
            </UiTableCell>
            <UiTableCell mono translate="no">{{ client.mac }}</UiTableCell>
            <UiTableCell>
              <AppLink
                translate="no"
                class="text-accent-foreground hover:underline"
                :to="{
                  path: `/devices/${client.deviceId}`,
                  query: scopeOf(navPage.location.value),
                }"
              >
                {{ devicesById.get(client.deviceId)?.name }}
              </AppLink>
              <small
                translate="no"
                class="text-2xs text-muted-foreground block"
              >
                {{ siteName(devicesById.get(client.deviceId)?.siteId ?? '') }}
              </small>
            </UiTableCell>
            <UiTableCell>{{ labels.band(client.band) }}</UiTableCell>
            <UiTableCell>
              <span
                class="inline-flex items-center gap-1.5 text-xs"
                :class="{
                  'text-success-foreground':
                    signalQuality(client.signal) === 'Strong',
                  'text-warning-foreground':
                    signalQuality(client.signal) === 'Fair',
                  'text-danger-foreground':
                    signalQuality(client.signal) === 'Weak',
                }"
              >
                {{ labels.signal(signalQuality(client.signal)) }}
                <small class="text-2xs text-muted-foreground">
                  {{ format.quantity(client.signal, 'dbm') }}
                </small>
              </span>
            </UiTableCell>
            <UiTableCell align="numeric">
              <I18nT scope="global" keypath="view.common.valueWithUnit">
                <template #value>{{
                  n(client.throughput, 'decimal')
                }}</template>
                <template #unit>
                  <span class="text-2xs text-muted-foreground">{{
                    t('view.common.units.mbps')
                  }}</span>
                </template>
              </I18nT>
            </UiTableCell>
          </UiTableRow>
        </UiTableBody>
      </UiTable>
      <UiEmptyState
        v-if="!filtered.length"
        :title="t('view.clients.emptyTitle')"
        :description="t('view.clients.emptyDescription')"
      >
        <template #icon>
          <AppIcon name="search" />
        </template>
      </UiEmptyState>
    </UiScrollArea>
    <footer
      v-if="filtered.length"
      class="px-6 py-4 border-t border-border flex items-center justify-center"
    >
      <UiPagination
        v-model:page="page"
        :total="filtered.length"
        :items-per-page="pageSize"
        show-edges
      />
    </footer>
  </section>
</template>

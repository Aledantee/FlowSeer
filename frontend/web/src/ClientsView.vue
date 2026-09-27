<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import AppLink from './navigation/AppLink.vue'
import { scopeOf, usePage } from './navigation/page'
import type { Device } from './domain/fleet'
import type { Band } from './domain/clients'
import { clientsOf, signalQuality } from './domain/clients'
import AppIcon from './components/AppIcon.vue'
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
const pageSize = 50
const page = ref(1)
const search = ref(navPage.query('q'))
watch(
  () => navPage.query('q'),
  (q) => (search.value = q),
)
const band = ref<Band | ''>('')
const bandOptions = [
  { value: 'all', label: 'All bands' },
  { value: '2.4 GHz', label: '2.4 GHz' },
  { value: '5 GHz', label: '5 GHz' },
  { value: '6 GHz', label: '6 GHz' },
]
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
function clearAccessPoint() {
  void navPage.go(
    { query: { ...navPage.location.value.query, ap: undefined } },
    { replace: true },
  )
}
</script>

<template>
  <section
    class="bg-card border border-border rounded-panel overflow-hidden shadow-xs"
    aria-labelledby="clients-title"
  >
    <div class="px-6 py-5 pb-4 flex items-center justify-between gap-3">
      <h2 id="clients-title" class="text-base font-semibold text-foreground">
        Connected clients
        <span
          class="text-xs bg-subtle px-1.5 py-0.5 rounded text-muted-foreground ml-1.5 font-medium"
        >
          {{ all.length }}
        </span>
      </h2>
    </div>
    <div class="flex items-center gap-3 px-6 pb-5">
      <div class="relative flex items-center w-[340px]">
        <AppIcon
          name="search"
          class="absolute left-2.5 w-3.5 h-3.5 text-muted-foreground pointer-events-none z-10"
        />
        <UiInput
          v-model="search"
          placeholder="Search hostname, IP, or MAC address…"
          aria-label="Search clients"
          class="pl-8"
        />
      </div>
      <div class="w-36">
        <UiSelect
          :model-value="band || 'all'"
          :options="bandOptions"
          aria-label="Filter by band"
          @update:model-value="band = $event === 'all' ? '' : ($event as Band)"
        />
      </div>
      <button
        v-if="accessPoint"
        class="inline-flex items-center gap-1.5 px-2.5 py-1 text-xs rounded-control bg-subtle text-foreground hover:bg-hover border border-border cursor-pointer"
        :aria-label="`Stop filtering by ${accessPoint.name}`"
        @click="clearAccessPoint"
      >
        {{ accessPoint.name
        }}<AppIcon name="close" class="w-3 h-3 text-muted-foreground" />
      </button>
      <span class="ml-auto text-xs text-muted-foreground">
        {{ filtered.length }}
        {{ filtered.length === 1 ? 'result' : 'results' }}
      </span>
    </div>
    <UiScrollArea axis="x" viewport-class="table-scroll">
      <UiTable>
        <UiTableHeader>
          <UiTableRow>
            <UiTableHead>Client</UiTableHead>
            <UiTableHead>MAC address</UiTableHead>
            <UiTableHead>Access point</UiTableHead>
            <UiTableHead>Band</UiTableHead>
            <UiTableHead>Signal</UiTableHead>
            <UiTableHead align="numeric">Traffic</UiTableHead>
          </UiTableRow>
        </UiTableHeader>
        <UiTableBody>
          <UiTableRow v-for="client in paginated" :key="client.id">
            <UiTableCell>
              <strong class="font-medium text-foreground block text-xs">
                {{ client.hostname }}
              </strong>
              <small class="font-mono text-2xs text-muted-foreground block">
                {{ client.address }}
              </small>
            </UiTableCell>
            <UiTableCell mono>{{ client.mac }}</UiTableCell>
            <UiTableCell>
              <AppLink
                class="text-accent-foreground hover:underline"
                :to="{
                  path: `/devices/${client.deviceId}`,
                  query: scopeOf(navPage.location.value),
                }"
              >
                {{ devicesById.get(client.deviceId)?.name }}
              </AppLink>
              <small class="text-2xs text-muted-foreground block">
                {{ siteName(devicesById.get(client.deviceId)?.siteId ?? '') }}
              </small>
            </UiTableCell>
            <UiTableCell>{{ client.band }}</UiTableCell>
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
                {{ signalQuality(client.signal) }}
                <small class="text-2xs text-muted-foreground">
                  {{ client.signal }} dBm
                </small>
              </span>
            </UiTableCell>
            <UiTableCell align="numeric">
              {{ client.throughput }}
              <span class="text-2xs text-muted-foreground">Mbps</span>
            </UiTableCell>
          </UiTableRow>
        </UiTableBody>
      </UiTable>
      <UiEmptyState
        v-if="!filtered.length"
        title="No clients match this view"
        description="Try a different search or band."
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

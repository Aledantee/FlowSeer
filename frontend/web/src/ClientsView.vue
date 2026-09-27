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
  UiPagination,
  UiScrollArea,
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
  <section class="inventory" aria-labelledby="clients-title">
    <div class="section-heading">
      <h2 id="clients-title">
        Connected clients <span>{{ all.length }}</span>
      </h2>
    </div>
    <div class="toolbar">
      <label class="search"
        ><AppIcon name="search" /><input
          v-model="search"
          placeholder="Search hostname, IP, or MAC address…"
          aria-label="Search clients" /></label
      ><label class="status-filter"
        ><span>Band</span
        ><select v-model="band" aria-label="Filter by band">
          <option value="">All bands</option>
          <option>2.4 GHz</option>
          <option>5 GHz</option>
          <option>6 GHz</option>
        </select></label
      ><button
        v-if="accessPoint"
        class="filter-chip"
        :aria-label="`Stop filtering by ${accessPoint.name}`"
        @click="clearAccessPoint"
      >
        {{ accessPoint.name }}<AppIcon name="close" /></button
      ><span class="results"
        >{{ filtered.length }}
        {{ filtered.length === 1 ? 'result' : 'results' }}</span
      >
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
              <strong class="client-name">{{ client.hostname }}</strong>
              <small class="mono">{{ client.address }}</small>
            </UiTableCell>
            <UiTableCell mono>{{ client.mac }}</UiTableCell>
            <UiTableCell>
              <AppLink
                class="table-link"
                :to="{
                  path: `/devices/${client.deviceId}`,
                  query: scopeOf(navPage.location.value),
                }"
                >{{ devicesById.get(client.deviceId)?.name }}</AppLink
              ><small>{{
                siteName(devicesById.get(client.deviceId)?.siteId ?? '')
              }}</small>
            </UiTableCell>
            <UiTableCell>{{ client.band }}</UiTableCell>
            <UiTableCell>
              <span
                :class="['signal', signalQuality(client.signal).toLowerCase()]"
                >{{ signalQuality(client.signal) }}
                <small>{{ client.signal }} dBm</small></span
              >
            </UiTableCell>
            <UiTableCell align="numeric" class="traffic">
              {{ client.throughput }} <span>Mbps</span>
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
    <footer v-if="filtered.length" class="table-footer justify-center">
      <UiPagination
        v-model:page="page"
        :total="filtered.length"
        :items-per-page="pageSize"
        show-edges
      />
    </footer>
  </section>
</template>

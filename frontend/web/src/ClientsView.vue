<script setup lang="ts">
import ScrollArea from './components/ScrollArea.vue'
import { computed, ref, watch } from 'vue'
import AppLink from './navigation/AppLink.vue'
import { scopeOf, usePage } from './navigation/page'
import type { Device } from './domain/fleet'
import type { Band } from './domain/clients'
import { clientsOf, signalQuality } from './domain/clients'
import AppIcon from './components/AppIcon.vue'

const props = defineProps<{
  scope: Device[]
  siteName: (id: string) => string
}>()
const page = usePage()
const PAGE = 50
const search = ref(page.query('q'))
watch(
  () => page.query('q'),
  (q) => (search.value = q),
)
const band = ref<Band | ''>('')
const limit = ref(PAGE)
const accessPoint = computed(() =>
  props.scope.find((device) => device.id === page.query('ap')),
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
watch([search, band, accessPoint], () => (limit.value = PAGE))
function clearAccessPoint() {
  void page.go(
    { query: { ...page.location.value.query, ap: undefined } },
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
    <ScrollArea axis="x" viewport-class="table-scroll">
      <table>
        <thead>
          <tr>
            <th>Client</th>
            <th>MAC address</th>
            <th>Access point</th>
            <th>Band</th>
            <th>Signal</th>
            <th class="numeric">Traffic</th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="client in filtered.slice(0, limit)" :key="client.id">
            <td>
              <strong class="client-name">{{ client.hostname }}</strong
              ><small class="mono">{{ client.address }}</small>
            </td>
            <td class="mono">{{ client.mac }}</td>
            <td>
              <AppLink
                class="table-link"
                :to="{
                  path: `/devices/${client.deviceId}`,
                  query: scopeOf(page.location.value),
                }"
                >{{ devicesById.get(client.deviceId)?.name }}</AppLink
              ><small>{{
                siteName(devicesById.get(client.deviceId)?.siteId ?? '')
              }}</small>
            </td>
            <td>{{ client.band }}</td>
            <td>
              <span
                :class="['signal', signalQuality(client.signal).toLowerCase()]"
                >{{ signalQuality(client.signal) }}
                <small>{{ client.signal }} dBm</small></span
              >
            </td>
            <td class="numeric traffic">
              {{ client.throughput }} <span>Mbps</span>
            </td>
          </tr>
        </tbody>
      </table>
      <div v-if="!filtered.length" class="empty">
        <AppIcon name="search" />
        <h3>No clients match this view</h3>
        <p>Try a different search or band.</p>
      </div>
    </ScrollArea>
    <footer class="table-footer">
      <span
        >Showing {{ Math.min(limit, filtered.length) }} of
        {{ filtered.length }} clients</span
      ><button
        v-if="limit < filtered.length"
        class="link-button"
        @click="limit += PAGE"
      >
        Show more
      </button>
    </footer>
  </section>
</template>

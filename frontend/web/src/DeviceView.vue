<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import AppLink from './navigation/AppLink.vue'
import { scopeOf, usePage } from './navigation/page'
import type { Device, Site } from './domain/fleet'
import { downlinks } from './domain/fleet'
import { clientsOf, signalQuality } from './domain/clients'
import AppIcon from './components/AppIcon.vue'
import DeviceIcon from './components/DeviceIcon.vue'
import { UiButton, UiField, UiSelect, UiStatusBadge } from './ui'

const props = defineProps<{
  device: Device
  fleet: Device[]
  allowedSites: Site[]
  siteName: (id: string) => string
  tenantName: (siteId: string) => string
}>()
const emit = defineEmits<{ reassign: [siteId: string] }>()
const page = usePage()
const destination = ref(props.device.siteId)
watch(
  () => props.device.siteId,
  (siteId) => (destination.value = siteId),
)
const uplink = computed(() =>
  props.fleet.find((item) => item.id === props.device.uplinkId),
)
const links = computed(() => downlinks(props.fleet, props.device))
const clients = computed(() => clientsOf([props.device]).slice(0, 8))
const siteOptions = computed(() =>
  props.allowedSites.map((s) => ({ value: s.id, label: s.name })),
)
function to(path: string, extra: Record<string, string> = {}) {
  return { path, query: { ...scopeOf(page.location.value), ...extra } }
}
</script>

<template>
  <div class="device-page">
    <AppLink class="back-link" :to="to('/devices')"
      ><AppIcon name="back" />All devices</AppLink
    >
    <header class="device-header">
      <DeviceIcon :role="device.role" />
      <div>
        <span class="eyebrow">{{ device.kind.toUpperCase() }}</span>
        <h1>{{ device.name }}</h1>
        <p>{{ siteName(device.siteId) }} · {{ tenantName(device.siteId) }}</p>
      </div>
      <UiStatusBadge :status="device.health" />
    </header>

    <dl class="device-facts">
      <div>
        <dt>IP address</dt>
        <dd class="mono">{{ device.address }}</dd>
      </div>
      <div>
        <dt>Clients</dt>
        <dd>{{ device.clients }}</dd>
      </div>
      <div>
        <dt>Traffic</dt>
        <dd>{{ device.throughput }} <small>Mbps</small></dd>
      </div>
      <div>
        <dt>Uplink</dt>
        <dd>
          <AppLink v-if="uplink" :to="to(`/devices/${uplink.id}`)">{{
            uplink.name
          }}</AppLink
          ><template v-else>Site edge</template>
        </dd>
      </div>
    </dl>

    <div class="device-sections">
      <section class="device-panel" aria-labelledby="clients-title">
        <div class="section-heading">
          <h2 id="clients-title">
            Connected clients <span>{{ device.clients }}</span>
          </h2>
          <AppLink
            v-if="device.clients"
            class="panel-link"
            :to="to('/clients', { ap: device.id })"
            >View all <AppIcon name="arrow"
          /></AppLink>
        </div>
        <ul v-if="clients.length" class="device-list">
          <li v-for="client in clients" :key="client.id">
            <span
              ><strong>{{ client.hostname }}</strong
              ><small class="mono">{{ client.address }}</small></span
            ><span class="device-list-meta"
              >{{ client.band }} · {{ signalQuality(client.signal) }}</span
            >
          </li>
        </ul>
        <p v-else class="panel-empty">
          {{
            device.role === 'access-point'
              ? 'No clients are connected right now.'
              : 'Clients connect through access points, not this device.'
          }}
        </p>
      </section>

      <div class="device-side">
        <section class="device-panel" aria-labelledby="links-title">
          <div class="section-heading">
            <h2 id="links-title">
              Downlinks <span>{{ links.length }}</span>
            </h2>
          </div>
          <ul v-if="links.length" class="device-list">
            <li v-for="link in links" :key="link.id">
              <AppLink :to="to(`/devices/${link.id}`)"
                ><strong>{{ link.name }}</strong
                ><small>{{ link.kind }}</small></AppLink
              ><UiStatusBadge :status="link.health" />
            </li>
          </ul>
          <p v-else class="panel-empty">
            Nothing connects through this device.
          </p>
        </section>

        <section class="device-panel" aria-labelledby="assignment-title">
          <form
            class="assignment"
            @submit.prevent="emit('reassign', destination)"
          >
            <h2 id="assignment-title">Site assignment</h2>
            <p>
              A device belongs to one site. Changing the site replaces its
              current assignment.
            </p>
            <UiField id="destination" label="Site within this tenant">
              <UiSelect v-model="destination" :options="siteOptions" />
            </UiField>
            <UiButton
              type="submit"
              variant="primary"
              class="w-full mt-4"
              :disabled="destination === device.siteId"
            >
              Save assignment
            </UiButton>
          </form>
        </section>
      </div>
    </div>
  </div>
</template>

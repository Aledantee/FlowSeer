<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import AppLink from './navigation/AppLink.vue'
import { scopeOf, usePage } from './navigation/page'
import type { Device, Site } from './domain/fleet'
import { downlinks, uplinkOf } from './domain/fleet'
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
const uplink = computed(() => uplinkOf(props.fleet, props.device))
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
  <div class="grid gap-5">
    <AppLink
      class="inline-flex items-center gap-1.5 justify-self-start text-xs text-muted-foreground hover:text-accent-foreground"
      :to="to('/devices')"
    >
      <AppIcon name="back" class="w-3.5 h-3.5" />All devices
    </AppLink>
    <header class="flex items-center gap-4">
      <DeviceIcon
        :role="device.role"
        class="w-12 h-12 text-accent-foreground"
      />
      <div class="flex-1 min-w-0">
        <span
          class="block text-2xs font-semibold tracking-wider text-muted-foreground mb-1 uppercase"
        >
          {{ device.kind.toUpperCase() }}
        </span>
        <h1 class="text-2xl font-bold text-foreground my-1">
          {{ device.name }}
        </h1>
        <p class="text-xs text-muted-foreground">
          {{ siteName(device.siteId) }} · {{ tenantName(device.siteId) }}
        </p>
      </div>
      <UiStatusBadge :status="device.health" />
    </header>

    <dl
      class="grid grid-cols-4 max-[800px]:grid-cols-2 max-[500px]:grid-cols-1 m-0 border border-border rounded-panel bg-card-header overflow-hidden"
    >
      <div class="p-4 sm:px-5 sm:py-4 border-border">
        <dt class="text-xs text-muted-foreground">IP address</dt>
        <dd class="mt-1.5 text-base font-mono font-semibold text-foreground">
          {{ device.address }}
        </dd>
      </div>
      <div
        class="p-4 sm:px-5 sm:py-4 border-border border-l max-[500px]:border-l-0 max-[500px]:border-t"
      >
        <dt class="text-xs text-muted-foreground">Clients</dt>
        <dd class="mt-1.5 text-lg font-semibold text-foreground">
          {{ device.clients }}
        </dd>
      </div>
      <div
        class="p-4 sm:px-5 sm:py-4 border-border border-l max-[800px]:border-l-0 max-[800px]:border-t"
      >
        <dt class="text-xs text-muted-foreground">Traffic</dt>
        <dd class="mt-1.5 text-lg font-semibold text-foreground">
          {{ device.throughput }}
          <small class="inline text-xs font-normal text-muted-foreground"
            >Mbps</small
          >
        </dd>
      </div>
      <div
        class="p-4 sm:px-5 sm:py-4 border-border border-l max-[500px]:border-l-0 max-[800px]:border-t"
      >
        <dt class="text-xs text-muted-foreground">Uplink</dt>
        <dd class="mt-1.5 text-lg font-semibold text-foreground">
          <AppLink
            v-if="uplink"
            class="text-accent-foreground hover:underline"
            :to="to(`/devices/${uplink.id}`)"
          >
            {{ uplink.name }}
          </AppLink>
          <template v-else>Site edge</template>
        </dd>
      </div>
    </dl>

    <div
      class="grid grid-cols-[minmax(0,3fr)_minmax(0,2fr)] max-[1150px]:grid-cols-1 items-start gap-5"
    >
      <section
        class="overflow-hidden border border-border rounded-panel bg-card"
        aria-labelledby="clients-title"
      >
        <div class="px-5 pt-4 pb-3 flex items-center justify-between gap-3">
          <h2
            id="clients-title"
            class="text-base font-semibold text-foreground"
          >
            Connected clients
            <span
              class="text-xs bg-subtle px-1.5 py-0.5 rounded text-muted-foreground ml-1.5 font-medium"
            >
              {{ device.clients }}
            </span>
          </h2>
          <AppLink
            v-if="device.clients"
            class="inline-flex items-center gap-1 text-xs text-accent-foreground hover:underline"
            :to="to('/clients', { ap: device.id })"
          >
            View all <AppIcon name="arrow" class="w-3.5 h-3.5" />
          </AppLink>
        </div>
        <ul v-if="clients.length" class="m-0 p-0 list-none">
          <li
            v-for="client in clients"
            :key="client.id"
            class="flex items-center justify-between gap-3 px-5 py-2.5 border-t border-border text-xs"
          >
            <span>
              <strong class="block font-semibold text-foreground">
                {{ client.hostname }}
              </strong>
              <small
                class="block font-mono text-2xs text-muted-foreground mt-0.5"
              >
                {{ client.address }}
              </small>
            </span>
            <span class="text-2xs text-muted-foreground">
              {{ client.band }} · {{ signalQuality(client.signal) }}
            </span>
          </li>
        </ul>
        <p v-else class="px-5 pt-1 pb-5 text-xs text-muted-foreground">
          {{
            device.role === 'access-point'
              ? 'No clients are connected right now.'
              : 'Clients connect through access points, not this device.'
          }}
        </p>
      </section>

      <div class="grid gap-5">
        <section
          class="overflow-hidden border border-border rounded-panel bg-card"
          aria-labelledby="links-title"
        >
          <div class="px-5 pt-4 pb-3 flex items-center justify-between gap-3">
            <h2
              id="links-title"
              class="text-base font-semibold text-foreground"
            >
              Downlinks
              <span
                class="text-xs bg-subtle px-1.5 py-0.5 rounded text-muted-foreground ml-1.5 font-medium"
              >
                {{ links.length }}
              </span>
            </h2>
          </div>
          <ul v-if="links.length" class="m-0 p-0 list-none">
            <li
              v-for="link in links"
              :key="link.id"
              class="flex items-center justify-between gap-3 px-5 py-2.5 border-t border-border text-xs"
            >
              <AppLink class="group" :to="to(`/devices/${link.id}`)">
                <strong
                  class="block font-semibold text-foreground group-hover:text-accent-foreground"
                >
                  {{ link.name }}
                </strong>
                <small class="block text-2xs text-muted-foreground mt-0.5">
                  {{ link.kind }}
                </small>
              </AppLink>
              <UiStatusBadge :status="link.health" />
            </li>
          </ul>
          <p v-else class="px-5 pt-1 pb-5 text-xs text-muted-foreground">
            Nothing connects through this device.
          </p>
        </section>

        <section
          class="overflow-hidden border border-border rounded-panel bg-card"
          aria-labelledby="assignment-title"
        >
          <form class="p-5" @submit.prevent="emit('reassign', destination)">
            <h2
              id="assignment-title"
              class="text-base font-semibold text-foreground"
            >
              Site assignment
            </h2>
            <p class="my-2 mb-4 text-xs text-muted-foreground">
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

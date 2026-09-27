<script setup lang="ts">
import { computed, onUnmounted, ref, watch } from 'vue'
import AppLink from './navigation/AppLink.vue'
import { scopeOf, usePage } from './navigation/page'
import { useWorkspace } from './navigation/workspace'
import type { Device, Site } from './domain/fleet'
import {
  downlinks,
  integrations,
  pathSummary,
  pollDevice,
  siteNeighbours,
  uplinkOf,
} from './domain/fleet'
import { clientsOf, signalQuality } from './domain/clients'
import { formatAgo, openIssues } from './domain/overview'
import AppIcon from './components/AppIcon.vue'
import DeviceIcon from './components/DeviceIcon.vue'
import { UiButton, UiCard, UiField, UiSelect, UiStatusBadge } from './ui'

const props = defineProps<{
  device: Device
  fleet: Device[]
  allowedSites: Site[]
  siteName: (id: string) => string
  tenantName: (siteId: string) => string
}>()
const emit = defineEmits<{ reassign: [siteId: string] }>()
const page = usePage()
const workspace = useWorkspace()
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

const severityLabel = { critical: 'Critical', warning: 'Warning', info: 'Info' }

const selectedIssues = computed(() =>
  props.device
    ? openIssues(props.fleet).filter(
        (event) => event.deviceId === props.device.id,
      )
    : [],
)

function integration(id: string) {
  return integrations.find((item) => item.id === id)
}

const neighbours = computed(() => siteNeighbours(props.fleet, props.device))

// A plain-text summary an operator can paste into a ticket or a call to the
// site, built only from what FlowSeer observed.
const escalation = computed(() => {
  const device = props.device
  return [
    `${device.name} (${device.kind}, ${device.address}) is ${device.health.toLowerCase()}.`,
    `Site: ${props.siteName(device.siteId)}, ${props.tenantName(device.siteId)}.`,
    `Last answered ${formatAgo(device.lastSeenMinutes)}. ${pathSummary(device)}`,
    ...device.bindings.map(
      (binding) =>
        `- ${integration(binding.integrationId)?.name}: ${binding.reachability.toLowerCase()}, checked ${formatAgo(binding.observedMinutesAgo)}`,
    ),
    ...selectedIssues.value.map(
      (event) => `- ${event.summary} (${formatAgo(event.minutesAgo)})`,
    ),
    neighbours.value
      ? `${neighbours.value.answering} of ${neighbours.value.total} other devices at the site are answering.`
      : '',
  ]
    .filter(Boolean)
    .join('\n')
})

const copyState = ref<'' | 'copied' | 'failed'>('')
async function copyEscalation() {
  try {
    await navigator.clipboard.writeText(escalation.value)
    copyState.value = 'copied'
  } catch {
    copyState.value = 'failed'
  }
}

const polling = ref(false)
const pollFailed = ref(false)
const pollResult = ref('')
let pollTimer: ReturnType<typeof setTimeout> | undefined

function poll() {
  const current = props.device
  if (!current || polling.value) return
  polling.value = true
  pollResult.value = ''
  pollTimer = setTimeout(() => {
    const updated = pollDevice(current)
    workspace.fleet.value = workspace.fleet.value.map((item) =>
      item.id === updated.id ? updated : item,
    )
    polling.value = false
    pollFailed.value = updated.lastSeenMinutes !== 0
    pollResult.value =
      updated.lastSeenMinutes === 0
        ? `Answered just now. Still ${updated.health.toLowerCase()}.`
        : `Still not answering. ${pathSummary(updated)} Last answer ${formatAgo(updated.lastSeenMinutes)}.`
  }, 1200)
}

onUnmounted(() => {
  clearTimeout(pollTimer)
})

function revealMove(event: Event) {
  const details = event.target
  if (details instanceof HTMLDetailsElement && details.open)
    details.scrollIntoView({ block: 'nearest' })
}

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
        <h1 class="text-2xl font-bold text-foreground my-1">
          {{ device.name }}
        </h1>
        <p class="text-xs text-muted-foreground">
          {{ device.kind }} · {{ device.address }} ·
          {{ siteName(device.siteId) }} · {{ tenantName(device.siteId) }}
        </p>
      </div>
      <div class="flex flex-col items-end gap-1">
        <UiStatusBadge :status="device.health" />
        <span class="text-2xs text-muted-foreground">
          Last answered {{ formatAgo(device.lastSeenMinutes) }}
        </span>
      </div>
    </header>

    <UiCard as="section" aria-labelledby="issues-title">
      <template #header>
        <h2 id="issues-title" class="text-base font-semibold text-foreground">
          {{
            device.health === 'Healthy'
              ? 'No open issues'
              : 'Why it needs attention'
          }}
        </h2>
      </template>
      <div class="space-y-4">
        <ul
          v-if="selectedIssues.length"
          class="m-0 p-0 list-none border-t border-border"
        >
          <li
            v-for="event in selectedIssues"
            :key="event.id"
            class="flex gap-2.5 py-2.5 border-b border-border text-xs"
          >
            <i
              aria-hidden="true"
              class="shrink-0 w-2 h-2 mt-1 rounded-full"
              :class="{
                'bg-danger-border': event.severity === 'critical',
                'bg-warning-border': event.severity === 'warning',
                'bg-info-border': event.severity === 'info',
              }"
            />
            <div>
              <strong class="block font-semibold text-foreground">{{
                event.summary
              }}</strong>
              <small class="text-2xs text-muted-foreground"
                >{{ formatAgo(event.minutesAgo) }} ·
                <span class="font-medium text-foreground">{{
                  severityLabel[event.severity]
                }}</span></small
              >
            </div>
          </li>
        </ul>
        <p
          v-else-if="device.health === 'Healthy'"
          class="text-xs text-muted-foreground"
        >
          Every path to this device answered its last poll.
        </p>
        <p v-else class="text-xs text-muted-foreground">
          No event explains this status yet.
        </p>

        <div v-if="device.health !== 'Healthy'" class="flex items-center gap-3">
          <UiButton
            :variant="pollFailed ? 'secondary' : 'primary'"
            :disabled="polling"
            @click="poll"
          >
            {{ polling ? 'Polling…' : 'Poll now' }}
          </UiButton>
          <p
            v-if="pollResult"
            role="status"
            class="text-xs text-muted-foreground"
          >
            {{ pollResult }}
          </p>
        </div>

        <div
          v-if="device.health === 'Offline' && neighbours"
          class="mt-4 p-4 rounded-panel bg-subtle border border-border text-xs"
        >
          <p class="text-foreground">
            {{
              neighbours.answering === neighbours.total
                ? `The other ${neighbours.total} devices at ${siteName(device.siteId)} are answering, so the fault is likely this device or its link.`
                : neighbours.answering === 0
                  ? `No other device at ${siteName(device.siteId)} is answering; the whole site may be down.`
                  : `${neighbours.answering} of ${neighbours.total} other devices at ${siteName(device.siteId)} are answering.`
            }}
          </p>
          <div class="flex items-center gap-2.5 mt-3">
            <UiButton
              :variant="pollFailed ? 'primary' : 'secondary'"
              size="sm"
              @click="copyEscalation"
            >
              Copy escalation summary
            </UiButton>
            <AppLink
              class="text-xs text-accent-foreground hover:underline"
              :to="
                to('/dashboard', {
                  site: device.siteId,
                  tenant:
                    allowedSites.find((s) => s.id === device.siteId)
                      ?.tenantId ?? '',
                })
              "
            >
              Open {{ siteName(device.siteId) }} dashboard
            </AppLink>
          </div>
          <p
            v-if="copyState === 'copied'"
            role="status"
            class="text-2xs text-muted-foreground mt-2"
          >
            Summary copied. Paste it into the ticket or message.
          </p>
          <template v-else-if="copyState === 'failed'">
            <p role="status" class="text-2xs text-danger mt-2">
              Could not copy. Select the summary below.
            </p>
            <pre
              class="mt-2 p-2 bg-card rounded text-2xs font-mono whitespace-pre-wrap"
              >{{ escalation }}</pre>
          </template>
        </div>
      </div>
    </UiCard>

    <UiCard as="section" aria-labelledby="paths-title">
      <template #header>
        <h2 id="paths-title" class="text-base font-semibold text-foreground">
          How FlowSeer reaches it
        </h2>
      </template>
      <ul class="m-0 p-0 list-none border-t border-border">
        <li
          v-for="binding in device.bindings"
          :key="binding.integrationId"
          class="flex items-center justify-between gap-3 py-2.5 px-1 border-b border-border text-xs"
        >
          <span>
            <strong class="block font-semibold text-foreground">{{
              integration(binding.integrationId)?.name
            }}</strong>
            <small class="text-2xs text-muted-foreground">{{
              integration(binding.integrationId)?.kind
            }}</small>
          </span>
          <span class="text-right">
            <strong
              class="block font-semibold"
              :class="{
                'text-danger-foreground':
                  binding.reachability === 'Unreachable',
                'text-foreground': binding.reachability !== 'Unreachable',
              }"
              >{{ binding.reachability }}</strong
            >
            <small class="text-2xs text-muted-foreground"
              >checked {{ formatAgo(binding.observedMinutesAgo) }}</small
            >
          </span>
        </li>
      </ul>
    </UiCard>

    <dl
      class="grid grid-cols-4 max-[800px]:grid-cols-2 max-[500px]:grid-cols-1 m-0 border border-border rounded-panel bg-card overflow-hidden"
    >
      <div class="p-4 border-border">
        <dt class="text-xs text-muted-foreground">Lifecycle</dt>
        <dd class="mt-1.5 text-base font-semibold text-foreground">
          {{ device.lifecycle }}
        </dd>
      </div>
      <div
        class="p-4 border-border border-l max-[500px]:border-l-0 max-[500px]:border-t"
      >
        <dt class="text-xs text-muted-foreground">Clients</dt>
        <dd class="mt-1.5 text-base font-semibold text-foreground">
          {{ device.health === 'Offline' ? '—' : device.clients }}
        </dd>
      </div>
      <div
        class="p-4 border-border border-l max-[800px]:border-l-0 max-[800px]:border-t"
      >
        <dt class="text-xs text-muted-foreground">Traffic</dt>
        <dd class="mt-1.5 text-base font-semibold text-foreground">
          {{ device.health === 'Offline' ? '—' : `${device.throughput} Mbps` }}
        </dd>
      </div>
      <div
        class="p-4 border-border border-l max-[500px]:border-l-0 max-[800px]:border-t"
      >
        <dt class="text-xs text-muted-foreground">Uplink</dt>
        <dd class="mt-1.5 text-base font-semibold text-foreground">
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

        <details
          v-if="allowedSites.length > 1"
          class="border border-border rounded-panel bg-card p-5 group"
          @toggle="revealMove"
        >
          <summary
            class="font-semibold text-sm text-foreground cursor-pointer select-none"
          >
            Move to another site
          </summary>
          <form
            class="mt-4 space-y-4"
            @submit.prevent="emit('reassign', destination)"
          >
            <p class="text-xs text-muted-foreground">
              A device belongs to one site. Moving it replaces its current
              assignment.
            </p>
            <UiField id="destination" label="Site within this tenant">
              <UiSelect v-model="destination" :options="siteOptions" />
            </UiField>
            <UiButton
              type="submit"
              variant="primary"
              class="w-full"
              :disabled="destination === device.siteId"
            >
              Move device
            </UiButton>
          </form>
        </details>
        <p
          v-else
          class="text-xs text-muted-foreground p-4 rounded-panel bg-card border border-border"
        >
          {{ tenantName(device.siteId) }} has no other site to move this device
          to.
        </p>
      </div>
    </div>
  </div>
</template>

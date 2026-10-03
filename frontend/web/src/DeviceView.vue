<script setup lang="ts">
import { computed, onUnmounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import AppLink from './navigation/AppLink.vue'
import { scopeOf, usePage } from './navigation/page'
import { useWorkspace } from './navigation/workspace'
import type { Device, Health, Site } from './domain/fleet'
import {
  downlinks,
  integrations,
  pollDevice,
  siteNeighbours,
  uplinkOf,
} from './domain/fleet'
import { clientsOf, signalQuality } from './domain/clients'
import type { Client } from './domain/clients'
import { openIssues } from './domain/overview'
import AppIcon from './components/AppIcon.vue'
import DeviceIcon from './components/DeviceIcon.vue'
import { useFormat } from './i18n/format'
import { useLabels } from './i18n/labels'
import { aiTarget, useAiSlot } from './ai'
import type { AiTarget } from './ai'
import {
  UiAiSummary,
  UiButton,
  UiCard,
  UiField,
  UiSelect,
  UiStatusBadge,
} from './ui'

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
const slot = useAiSlot()
const { t, n } = useI18n({ useScope: 'global' })
const format = useFormat()
const labels = useLabels()
const separator = computed(() => t('view.common.factSeparator'))

// Sentences that embed a health word are one message per value.
const HEADLINE = {
  Healthy: 'view.device.escalation.headline.healthy',
  Degraded: 'view.device.escalation.headline.degraded',
  Offline: 'view.device.escalation.headline.offline',
} as const satisfies Record<Health, string>
const POLL_ANSWERED = {
  Healthy: 'view.device.pollAnswered.healthy',
  Degraded: 'view.device.pollAnswered.degraded',
  Offline: 'view.device.pollAnswered.offline',
} as const satisfies Record<Health, string>

// The reading and its quality word as one message, shown to an operator and
// carried in an AI target's context.
function signalReading(client: Client) {
  return t('view.common.signalReading', {
    reading: format.quantity(client.signal, 'dbm'),
    quality: labels.signal(signalQuality(client.signal)),
  })
}

// Says how many of a device's paths are down in words an operator can repeat,
// so "still not answering" comes with what FlowSeer actually observed.
function pathLine(device: Device): string {
  const down = device.bindings.filter(
    (binding) => binding.reachability === 'Unreachable',
  ).length
  const total = device.bindings.length
  const counts = { down: n(down, 'integer'), total: n(total, 'integer') }
  if (!down) {
    return t(
      total === 1
        ? 'view.device.paths.reachableOne'
        : 'view.device.paths.reachableAll',
    )
  }
  if (down < total) return t('view.device.paths.unreachableSome', counts)
  if (total === 1) return t('view.device.paths.unreachableOnly')
  if (total === 2) return t('view.device.paths.unreachableBoth')
  return t('view.device.paths.unreachableAll', counts)
}

const viewTarget = computed(() =>
  aiTarget({
    slot,
    view: 'device',
    kind: 'view',
    entityId: props.device.id,
    label: props.device.name,
    context: {
      name: props.device.name,
      kind: props.device.kind,
      health: props.device.health,
      address: props.device.address,
      site: props.siteName(props.device.siteId),
      tenant: props.tenantName(props.device.siteId),
    },
  }),
)
function clientTarget(client: Client): AiTarget {
  return aiTarget({
    slot,
    view: 'device',
    kind: 'client',
    entityId: client.id,
    label: client.hostname,
    context: {
      hostname: client.hostname,
      address: client.address,
      mac: client.mac,
      band: client.band,
      signal: signalReading(client),
      accessPoint: props.device.name,
    },
  })
}
function downlinkTarget(device: Device): AiTarget {
  return aiTarget({
    slot,
    view: 'device',
    kind: 'downlink',
    entityId: device.id,
    label: device.name,
    context: {
      name: device.name,
      kind: device.kind,
      health: device.health,
      uplink: props.device.name,
    },
  })
}
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
const neighbourKey = computed(() => {
  const { answering, total } = neighbours.value
  if (answering === total) return 'view.device.neighbours.all'
  return answering === 0
    ? 'view.device.neighbours.none'
    : 'view.device.neighbours.some'
})

// A plain-text summary an operator can paste into a ticket or a call to the
// site, built only from what FlowSeer observed.
const escalation = computed(() => {
  const device = props.device
  return [
    t(HEADLINE[device.health], {
      name: device.name,
      kind: device.kind,
      address: device.address,
    }),
    t('view.device.escalation.site', {
      site: props.siteName(device.siteId),
      tenant: props.tenantName(device.siteId),
    }),
    t('view.device.lastAnswered', {
      age: format.ago(device.lastSeenMinutes),
    }),
    pathLine(device),
    ...device.bindings.map((binding) =>
      t(
        binding.reachability === 'Reachable'
          ? 'view.device.escalation.bindingReachable'
          : 'view.device.escalation.bindingUnreachable',
        {
          name: integration(binding.integrationId)?.name ?? '',
          age: format.ago(binding.observedMinutesAgo),
        },
      ),
    ),
    ...selectedIssues.value.map((event) =>
      t('view.device.escalation.issue', {
        summary: event.summary,
        age: format.ago(event.minutesAgo),
      }),
    ),
    t(
      'view.device.escalation.neighbours',
      {
        answering: n(neighbours.value.answering, 'integer'),
        total: n(neighbours.value.total, 'integer'),
      },
      neighbours.value.total,
    ),
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
// The polled device, not its sentence, so the result follows a locale switch.
const pollResult = ref<Device>()
const pollText = computed(() => {
  const updated = pollResult.value
  if (!updated) return ''
  return updated.lastSeenMinutes === 0
    ? t(POLL_ANSWERED[updated.health])
    : t('view.device.pollUnanswered', {
        paths: pathLine(updated),
        age: format.ago(updated.lastSeenMinutes),
      })
})
let pollTimer: ReturnType<typeof setTimeout> | undefined

function poll() {
  const current = props.device
  if (!current || polling.value) return
  polling.value = true
  pollResult.value = undefined
  pollTimer = setTimeout(() => {
    const updated = pollDevice(current)
    workspace.fleet.value = workspace.fleet.value.map((item) =>
      item.id === updated.id ? updated : item,
    )
    polling.value = false
    pollFailed.value = updated.lastSeenMinutes !== 0
    pollResult.value = updated
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
  <div v-ai-target="viewTarget" class="grid gap-5">
    <AppLink
      class="inline-flex items-center gap-1.5 justify-self-start text-xs text-muted-foreground hover:text-accent-foreground"
      :to="to('/devices')"
    >
      <AppIcon name="back" class="w-3.5 h-3.5" />{{
        t('view.device.allDevices')
      }}
    </AppLink>
    <header class="flex items-center gap-4">
      <DeviceIcon
        :role="device.role"
        class="w-12 h-12 text-accent-foreground"
      />
      <div class="flex-1 min-w-0">
        <h1 translate="no" class="text-2xl font-bold text-foreground my-1">
          {{ device.name }}
        </h1>
        <p class="text-xs text-muted-foreground">
          {{ device.kind }}{{ separator
          }}<span translate="no">{{ device.address }}</span
          >{{ separator
          }}<span translate="no">{{ siteName(device.siteId) }}</span
          >{{ separator
          }}<span translate="no">{{ tenantName(device.siteId) }}</span>
        </p>
      </div>
      <div class="flex flex-col items-end gap-1">
        <UiStatusBadge :status="device.health" />
        <span class="text-2xs text-muted-foreground">
          {{
            t('view.device.lastAnswered', {
              age: format.ago(device.lastSeenMinutes),
            })
          }}
        </span>
      </div>
    </header>

    <UiCard as="section" aria-labelledby="issues-title">
      <template #header>
        <h2 id="issues-title" class="text-base font-semibold text-foreground">
          {{
            device.health === 'Healthy'
              ? t('view.device.issuesTitleHealthy')
              : t('view.device.issuesTitle')
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
                >{{ format.ago(event.minutesAgo) }}{{ separator
                }}<span class="text-sm font-medium text-foreground">{{
                  labels.severity(event.severity)
                }}</span></small
              >
            </div>
          </li>
        </ul>
        <p
          v-else-if="device.health === 'Healthy'"
          class="text-xs text-muted-foreground"
        >
          {{ t('view.device.issuesHealthy') }}
        </p>
        <p v-else class="text-xs text-muted-foreground">
          {{ t('view.device.issuesNoEvent') }}
        </p>

        <div v-if="device.health !== 'Healthy'" class="flex items-center gap-3">
          <UiButton
            :variant="pollFailed ? 'secondary' : 'primary'"
            :disabled="polling"
            @click="poll"
          >
            {{ polling ? t('view.device.polling') : t('view.device.poll') }}
          </UiButton>
          <p
            v-if="pollText"
            role="status"
            class="text-xs text-muted-foreground"
          >
            {{ pollText }}
          </p>
        </div>

        <div
          v-if="device.health === 'Offline' && neighbours"
          class="mt-4 p-4 rounded-panel bg-subtle border border-border text-xs"
        >
          <I18nT
            scope="global"
            tag="p"
            class="text-foreground"
            :keypath="neighbourKey"
            :plural="neighbours.total"
          >
            <template #count>{{ n(neighbours.total, 'integer') }}</template>
            <template #answering>{{
              n(neighbours.answering, 'integer')
            }}</template>
            <template #site>
              <span translate="no">{{ siteName(device.siteId) }}</span>
            </template>
          </I18nT>
          <div class="flex items-center gap-2.5 mt-3">
            <UiButton
              :variant="pollFailed ? 'primary' : 'secondary'"
              size="sm"
              @click="copyEscalation"
            >
              {{ t('view.device.copyEscalation') }}
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
              <I18nT scope="global" keypath="view.device.openDashboard">
                <template #site>
                  <span translate="no">{{ siteName(device.siteId) }}</span>
                </template>
              </I18nT>
            </AppLink>
          </div>
          <p
            v-if="copyState === 'copied'"
            role="status"
            class="text-2xs text-muted-foreground mt-2"
          >
            {{ t('view.device.copied') }}
          </p>
          <template v-else-if="copyState === 'failed'">
            <p role="status" class="text-2xs text-danger mt-2">
              {{ t('view.device.copyFailed') }}
            </p>
            <pre
              class="mt-2 p-2 bg-card rounded text-2xs font-mono whitespace-pre-wrap"
              >{{ escalation }}</pre>
          </template>
        </div>
        <UiAiSummary :target="viewTarget" />
      </div>
    </UiCard>

    <UiCard as="section" aria-labelledby="paths-title">
      <template #header>
        <h2 id="paths-title" class="text-base font-semibold text-foreground">
          {{ t('view.device.pathsTitle') }}
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
              >{{ labels.reachability(binding.reachability) }}</strong
            >
            <small class="text-2xs text-muted-foreground">{{
              t('view.device.checked', {
                age: format.ago(binding.observedMinutesAgo),
              })
            }}</small>
          </span>
        </li>
      </ul>
    </UiCard>

    <dl
      class="grid grid-cols-4 max-[800px]:grid-cols-2 max-[500px]:grid-cols-1 m-0 border border-border rounded-panel bg-card overflow-hidden"
    >
      <div class="p-4 border-border">
        <dt class="text-xs text-muted-foreground">
          {{ t('view.device.lifecycle') }}
        </dt>
        <dd class="mt-1.5 text-base font-semibold text-foreground">
          {{ labels.lifecycle(device.lifecycle) }}
        </dd>
      </div>
      <div
        class="p-4 border-border border-l max-[500px]:border-l-0 max-[500px]:border-t"
      >
        <dt class="text-xs text-muted-foreground">
          {{ t('view.common.columns.clients') }}
        </dt>
        <dd class="mt-1.5 text-base font-semibold text-foreground">
          {{
            device.health === 'Offline'
              ? t('view.common.noReading')
              : n(device.clients, 'integer')
          }}
        </dd>
      </div>
      <div
        class="p-4 border-border border-l max-[800px]:border-l-0 max-[800px]:border-t"
      >
        <dt class="text-xs text-muted-foreground">
          {{ t('view.common.columns.traffic') }}
        </dt>
        <dd class="mt-1.5 text-base font-semibold text-foreground">
          {{
            device.health === 'Offline'
              ? t('view.common.noReading')
              : format.rate(device.throughput)
          }}
        </dd>
      </div>
      <div
        class="p-4 border-border border-l max-[500px]:border-l-0 max-[800px]:border-t"
      >
        <dt class="text-xs text-muted-foreground">
          {{ t('view.device.uplink') }}
        </dt>
        <dd class="mt-1.5 text-base font-semibold text-foreground">
          <AppLink
            v-if="uplink"
            translate="no"
            class="text-accent-foreground hover:underline"
            :to="to(`/devices/${uplink.id}`)"
          >
            {{ uplink.name }}
          </AppLink>
          <template v-else>{{ t('view.device.siteEdge') }}</template>
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
            {{ t('view.common.connectedClients') }}
            <span
              class="text-xs bg-subtle px-1.5 py-0.5 rounded text-muted-foreground ml-1.5 font-medium"
            >
              {{ n(device.clients, 'integer') }}
            </span>
          </h2>
          <AppLink
            v-if="device.clients"
            class="inline-flex items-center gap-1 text-xs text-accent-foreground hover:underline"
            :to="to('/clients', { ap: device.id })"
          >
            {{ t('view.device.viewAll')
            }}<AppIcon name="arrow" class="w-3.5 h-3.5" />
          </AppLink>
        </div>
        <ul v-if="clients.length" class="m-0 p-0 list-none">
          <li
            v-for="client in clients"
            :key="client.id"
            v-ai-target="clientTarget(client)"
            class="flex items-center justify-between gap-3 px-5 py-2.5 border-t border-border text-xs"
          >
            <span>
              <strong
                translate="no"
                class="block font-semibold text-foreground"
              >
                {{ client.hostname }}
              </strong>
              <small
                translate="no"
                class="block font-mono text-2xs text-muted-foreground mt-0.5"
              >
                {{ client.address }}
              </small>
            </span>
            <span class="text-2xs text-muted-foreground">
              {{
                format.facts([
                  labels.band(client.band),
                  labels.signal(signalQuality(client.signal)),
                ])
              }}
            </span>
          </li>
        </ul>
        <p v-else class="px-5 pt-1 pb-5 text-xs text-muted-foreground">
          {{
            device.role === 'access-point'
              ? t('view.device.clientsNoneAccessPoint')
              : t('view.device.clientsNoneOther')
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
              {{ t('view.device.downlinks') }}
              <span
                class="text-xs bg-subtle px-1.5 py-0.5 rounded text-muted-foreground ml-1.5 font-medium"
              >
                {{ n(links.length, 'integer') }}
              </span>
            </h2>
          </div>
          <ul v-if="links.length" class="m-0 p-0 list-none">
            <li
              v-for="link in links"
              :key="link.id"
              v-ai-target="downlinkTarget(link)"
              class="flex items-center justify-between gap-3 px-5 py-2.5 border-t border-border text-xs"
            >
              <AppLink class="group" :to="to(`/devices/${link.id}`)">
                <strong
                  translate="no"
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
            {{ t('view.device.downlinksNone') }}
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
            {{ t('view.device.moveSummary') }}
          </summary>
          <form
            class="mt-4 space-y-4"
            @submit.prevent="emit('reassign', destination)"
          >
            <p class="text-xs text-muted-foreground">
              {{ t('view.device.moveIntro') }}
            </p>
            <UiField id="destination" :label="t('view.device.moveField')">
              <UiSelect v-model="destination" :options="siteOptions" />
            </UiField>
            <UiButton
              type="submit"
              variant="primary"
              class="w-full"
              :disabled="destination === device.siteId"
            >
              {{ t('view.device.moveButton') }}
            </UiButton>
          </form>
        </details>
        <I18nT
          v-else
          scope="global"
          tag="p"
          class="text-xs text-muted-foreground p-4 rounded-panel bg-card border border-border"
          keypath="view.device.moveNoSite"
        >
          <template #tenant>
            <span translate="no">{{ tenantName(device.siteId) }}</span>
          </template>
        </I18nT>
      </div>
    </div>
  </div>
</template>

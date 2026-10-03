<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import {
  type UiCommandItemSelectEvent,
  UiCommandDialog,
  UiCommandEmpty,
  UiCommandGroup,
  UiCommandInput,
  UiCommandItem,
  UiCommandList,
  UiKbd,
  UiTooltip,
} from '../ui'
import type { Device } from '../domain/fleet'
import { sites, tenantIds, tenants } from '../domain/fleet'
import { clientsOf } from '../domain/clients'
import { portsOf } from '../domain/telemetry'
import type { ResultKind, SearchResult } from '../domain/search'
import { searchAll } from '../domain/search'
import { useFormat } from '../i18n/format'
import { useLabels } from '../i18n/labels'
import AppIcon from './AppIcon.vue'
import type { RecentSearch } from './recentSearches'
import { clearRecent, loadRecent, rememberRecent } from './recentSearches'
import {
  SHORTCUTS,
  isMac,
  keysOf,
  matches,
  modalOpen,
  typingIn,
} from '../navigation/shortcuts'

export interface SearchPage {
  id: string
  title: string
  detail: string
  icon: string
}

const props = defineProps<{
  fleet: Device[]
  pages: SearchPage[]
  canSplit: boolean
}>()

const emit = defineEmits<{
  select: [result: SearchResult, beside: boolean]
  dock: [result: SearchResult]
}>()

// A result and the text shown under its title, built at render from the
// current data and locale.
interface Row {
  result: SearchResult
  detail: string
}

const { t } = useI18n({ useScope: 'global' })
const format = useFormat()
const labels = useLabels()

const open = ref(false)
const query = ref('')
const recent = ref<RecentSearch[]>([])
const selectedValue = ref('')

const clients = computed(() => clientsOf(props.fleet))

function deviceName(id: string) {
  return (
    props.fleet.find((device) => device.id === id)?.name ??
    t('view.common.unknownDevice')
  )
}

// Looks an entry up in the current data. Nothing is resolved for an object
// that no longer exists.
function resolve({ kind, id, port }: RecentSearch): Row | undefined {
  if (kind === 'page') {
    const page = props.pages.find((item) => item.id === id)
    return (
      page && {
        result: { kind, id, title: page.title },
        detail: page.detail,
      }
    )
  }
  if (kind === 'tenant') {
    const tenant = tenants.find((item) => item.id === id)
    if (!tenant) return undefined
    const scope = tenantIds(tenant.id)
    return {
      result: { kind, id, title: tenant.name },
      detail: format.counted(
        'view.common.sites',
        sites.filter((site) => scope.includes(site.tenantId)).length,
      ),
    }
  }
  if (kind === 'site') {
    const site = sites.find((item) => item.id === id)
    return (
      site && {
        result: { kind, id, title: site.name },
        detail: site.location,
      }
    )
  }
  if (kind === 'client') {
    const client = clients.value.find((item) => item.id === id)
    return (
      client && {
        result: { kind, id, title: client.hostname },
        detail: t('view.search.clientDetail', {
          address: client.address,
          mac: client.mac,
          device: deviceName(client.deviceId),
        }),
      }
    )
  }
  const device = props.fleet.find((item) => item.id === id)
  if (!device) return undefined
  if (kind === 'device') {
    const siteName =
      sites.find((site) => site.id === device.siteId)?.name ??
      t('view.common.unknownSite')
    return {
      result: { kind, id, title: device.name },
      detail: format.facts([device.kind, device.address, siteName]),
    }
  }
  const found = portsOf(props.fleet, device).find((item) => item.name === port)
  if (!found) return undefined
  return {
    result: {
      kind,
      id,
      port: found.name,
      title: `${device.name} ${found.name}`,
    },
    detail: format.facts([
      labels.portStatus(found.status),
      found.neighborId
        ? t('view.search.interfaceFarEnd', {
            device: deviceName(found.neighborId),
          })
        : found.endpoint,
    ]),
  }
}

const showingRecent = computed(() => !query.value.trim())

const pageMatches = computed(() => {
  const needle = query.value.trim().toLowerCase()
  return props.pages
    .filter((page) =>
      `${page.title} ${page.detail}`.toLowerCase().includes(needle),
    )
    .slice(0, 6)
    .map((page): RecentSearch => ({ kind: 'page', id: page.id }))
})

const results = computed(() =>
  (showingRecent.value
    ? recent.value
    : [...pageMatches.value, ...searchAll(query.value, props.fleet)]
  )
    .map(resolve)
    .filter((row) => row !== undefined),
)

const GROUPS: { kind: ResultKind; label: string; icon: string }[] = [
  { kind: 'page', label: 'view.search.groups.page', icon: 'dashboard' },
  { kind: 'tenant', label: 'view.search.groups.tenant', icon: 'tenants' },
  { kind: 'site', label: 'view.search.groups.site', icon: 'sites' },
  { kind: 'device', label: 'view.search.groups.device', icon: 'devices' },
  { kind: 'client', label: 'view.search.groups.client', icon: 'clients' },
  { kind: 'interface', label: 'view.search.groups.interface', icon: 'switch' },
]

// A computed, not a constant: a message read once at setup keeps the locale
// the app mounted with.
const groups = computed(() =>
  GROUPS.map((group) => ({ ...group, label: t(group.label) })),
)

const grouped = computed(() =>
  showingRecent.value
    ? results.value.length
      ? [
          {
            kind: 'recent',
            label: t('view.search.recent'),
            icon: 'search',
            items: results.value,
          },
        ]
      : []
    : groups.value
        .map((group) => ({
          ...group,
          items: results.value.filter((row) => row.result.kind === group.kind),
        }))
        .filter((group) => group.items.length),
)

const shortcut = computed(() =>
  keysOf(SHORTCUTS.search).join(isMac() ? '' : ' '),
)

function openSearch() {
  query.value = ''
  selectedValue.value = ''
  recent.value = loadRecent()
  open.value = true
}

function resultKey(result: SearchResult): string {
  return `${result.kind}:${result.id}:${result.port ?? ''}`
}

function choose(result: SearchResult | undefined, beside = false) {
  if (!result) return
  open.value = false
  recent.value = rememberRecent(result)
  emit('select', result, beside && props.canSplit)
}

function dock(result: SearchResult | undefined) {
  if (!result) return
  open.value = false
  recent.value = rememberRecent(result)
  emit('dock', result)
}

function iconFor(result: SearchResult, fallback: string) {
  if (result.kind === 'page')
    return props.pages.find((page) => page.id === result.id)?.icon ?? fallback
  return showingRecent.value
    ? (groups.value.find((group) => group.kind === result.kind)?.icon ??
        fallback)
    : fallback
}

function forget() {
  clearRecent()
  recent.value = []
}

function handleItemSelect(
  result: SearchResult,
  event: UiCommandItemSelectEvent,
) {
  const beside = event.shiftKey || event.metaKey || event.ctrlKey
  choose(result, beside)
}

function handleKeydown(event: KeyboardEvent) {
  if (event.key === 'Enter') {
    const isModified =
      event.shiftKey || event.metaKey || event.ctrlKey || event.altKey
    if (!isModified) return

    const target = (
      results.value.find((r) => resultKey(r.result) === selectedValue.value) ||
      results.value[0]
    )?.result
    if (target) {
      event.preventDefault()
      event.stopPropagation()
      if (event.altKey) {
        dock(target)
      } else if (event.shiftKey || event.metaKey || event.ctrlKey) {
        choose(target, true)
      }
    }
  }
}

function shortcutKey(event: KeyboardEvent) {
  if (!open.value && modalOpen()) return

  if (matches(event, SHORTCUTS.search)) {
    event.preventDefault()
    if (!open.value) openSearch()
    return
  }

  if (
    event.key === '/' &&
    !event.shiftKey &&
    !event.metaKey &&
    !event.ctrlKey &&
    !event.altKey &&
    !typingIn(event.target)
  ) {
    event.preventDefault()
    if (!open.value) openSearch()
  }
}

onMounted(() => window.addEventListener('keydown', shortcutKey))
onUnmounted(() => window.removeEventListener('keydown', shortcutKey))
</script>

<template>
  <button
    class="search-trigger flex items-center gap-2 h-[34px] mr-2 px-2.5 border border-chrome-border rounded-control bg-chrome-surface/60 text-chrome-muted-foreground text-xs hover:border-chrome-muted-foreground hover:text-chrome-foreground cursor-pointer max-[800px]:w-11 max-[800px]:h-11 max-[800px]:justify-center max-[800px]:mr-0 max-[800px]:p-0"
    aria-haspopup="dialog"
    @click="openSearch"
  >
    <AppIcon name="search" class="w-3.5 h-3.5" /><span
      class="min-w-[90px] text-left max-[800px]:hidden"
      >{{ t('view.search.trigger') }}</span
    ><UiKbd
      class="max-[800px]:hidden border-chrome-border bg-transparent text-chrome-muted-foreground"
      >{{ shortcut }}</UiKbd
    >
  </button>

  <UiCommandDialog
    v-model:open="open"
    v-model:highlighted-value="selectedValue"
    :ignore-filter="true"
    @keydown.capture="handleKeydown"
  >
    <UiCommandInput
      v-model="query"
      :placeholder="t('view.search.placeholder')"
      :label="t('view.search.label')"
    />
    <UiCommandList
      :label="t('view.search.resultsLabel')"
      class="search-scroll flex-1 min-h-0"
    >
      <UiCommandEmpty>
        <p
          v-if="query.trim() && !results.length"
          class="search-empty py-5 px-3 text-xs text-muted-foreground text-center"
        >
          {{ t('view.search.noMatch', { query: query.trim() }) }}
        </p>
        <p
          v-else
          class="search-empty py-5 px-3 text-xs text-muted-foreground text-center"
        >
          {{ t('view.search.emptyHint') }}
        </p>
      </UiCommandEmpty>

      <template v-for="group in grouped" :key="group.kind">
        <UiCommandGroup
          :heading="group.kind !== 'recent' ? group.label : undefined"
        >
          <div
            v-if="group.kind === 'recent'"
            class="flex items-center justify-between px-2 py-1.5 text-xs font-medium text-muted-foreground"
          >
            <span>{{ t('view.search.recent') }}</span>
            <button
              type="button"
              class="search-clear text-xs text-muted-foreground hover:text-foreground cursor-pointer"
              @mousedown.prevent
              @click="forget"
            >
              {{ t('view.search.clear') }}
            </button>
          </div>

          <UiCommandItem
            v-for="{ result, detail } in group.items"
            :key="resultKey(result)"
            :value="resultKey(result)"
            class="search-result"
            @select="handleItemSelect(result, $event)"
          >
            <AppIcon :name="iconFor(result, group.icon)" />
            <span>
              <strong :translate="result.kind === 'page' ? undefined : 'no'">{{
                result.title
              }}</strong>
              <small>{{ detail }}</small>
            </span>
            <span class="search-result-actions ml-auto flex items-center gap-1">
              <UiTooltip
                v-if="canSplit"
                :label="t('view.search.openBeside')"
                :shortcut="{ code: 'Enter', shift: true }"
                side="left"
                inline
              >
                <button
                  type="button"
                  tabindex="-1"
                  :aria-label="
                    t('view.search.openBesideLabel', { title: result.title })
                  "
                  @click.stop="choose(result, true)"
                >
                  <AppIcon name="panel-right" />
                </button>
              </UiTooltip>
              <UiTooltip
                v-if="!(result.kind === 'page' && result.id.startsWith('tab:'))"
                :label="t('view.search.sendToDock')"
                :shortcut="{ code: 'Enter', alt: true }"
                side="left"
                inline
              >
                <button
                  type="button"
                  tabindex="-1"
                  :aria-label="
                    t('view.search.sendToDockLabel', { title: result.title })
                  "
                  @click.stop="dock(result)"
                >
                  <AppIcon name="to-dock" />
                </button>
              </UiTooltip>
            </span>
          </UiCommandItem>
        </UiCommandGroup>
      </template>
    </UiCommandList>

    <footer
      class="search-hints flex items-center gap-4 border-t border-border px-3.5 py-2.5 text-xs text-muted-foreground"
      aria-hidden="true"
    >
      <span
        ><UiKbd class="mr-1">{{ t('view.search.keys.up') }}</UiKbd
        ><UiKbd>{{ t('view.search.keys.down') }}</UiKbd>
        {{ t('view.search.hints.move') }}</span
      >
      <span
        ><UiKbd>{{ t('view.search.keys.enter') }}</UiKbd>
        {{ t('view.search.hints.open') }}</span
      >
      <span v-if="canSplit"
        ><UiKbd class="mr-1">{{ t('view.search.keys.shift') }}</UiKbd
        ><UiKbd>{{ t('view.search.keys.enter') }}</UiKbd>
        {{ t('view.search.hints.sideBySide') }}</span
      >
      <span
        ><UiKbd class="mr-1">{{ t('view.search.keys.alt') }}</UiKbd
        ><UiKbd>{{ t('view.search.keys.enter') }}</UiKbd>
        {{ t('view.search.hints.toDock') }}</span
      >
      <span
        ><UiKbd>{{ t('view.search.keys.escape') }}</UiKbd>
        {{ t('view.search.hints.close') }}</span
      >
    </footer>
  </UiCommandDialog>
</template>

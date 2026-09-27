<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref } from 'vue'
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
import { sites, tenants } from '../domain/fleet'
import type { ResultKind, SearchResult } from '../domain/search'
import { searchAll } from '../domain/search'
import AppIcon from './AppIcon.vue'
import { clearRecent, loadRecent, rememberRecent } from './recentSearches'

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

const open = ref(false)
const query = ref('')
const recent = ref<SearchResult[]>([])
const selectedValue = ref('')

function exists(result: SearchResult) {
  if (result.kind === 'page')
    return props.pages.some((page) => page.id === result.id)
  if (result.kind === 'tenant')
    return tenants.some((tenant) => tenant.id === result.id)
  if (result.kind === 'site') return sites.some((site) => site.id === result.id)
  const deviceId =
    result.kind === 'client' ? result.id.replace(/-client-\d+$/, '') : result.id
  return props.fleet.some((device) => device.id === deviceId)
}

const showingRecent = computed(() => !query.value.trim())

const pageMatches = computed(() => {
  const needle = query.value.trim().toLowerCase()
  return props.pages
    .filter((page) =>
      `${page.title} ${page.detail}`.toLowerCase().includes(needle),
    )
    .slice(0, 6)
    .map((page): SearchResult => ({
      kind: 'page',
      id: page.id,
      title: page.title,
      detail: page.detail,
    }))
})

const results = computed(() =>
  showingRecent.value
    ? recent.value.filter(exists)
    : [...pageMatches.value, ...searchAll(query.value, props.fleet)],
)

const groups: { kind: ResultKind; label: string; icon: string }[] = [
  { kind: 'page', label: 'Pages', icon: 'dashboard' },
  { kind: 'tenant', label: 'Tenants', icon: 'tenants' },
  { kind: 'site', label: 'Sites', icon: 'sites' },
  { kind: 'device', label: 'Devices', icon: 'devices' },
  { kind: 'client', label: 'Clients', icon: 'clients' },
  { kind: 'interface', label: 'Interfaces', icon: 'switch' },
]

const grouped = computed(() =>
  showingRecent.value
    ? results.value.length
      ? [
          {
            kind: 'recent',
            label: 'Recent',
            icon: 'search',
            items: results.value,
          },
        ]
      : []
    : groups
        .map((group) => ({
          ...group,
          items: results.value.filter((result) => result.kind === group.kind),
        }))
        .filter((group) => group.items.length),
)

const shortcut = /Mac|iPhone|iPad/.test(navigator.platform) ? '⌘K' : 'Ctrl K'

function openSearch() {
  query.value = ''
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
    ? (groups.find((group) => group.kind === result.kind)?.icon ?? fallback)
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
  const modified =
    event.altKey || event.shiftKey || event.metaKey || event.ctrlKey
  if (event.key !== 'Enter' || !modified) return

  const root = event.currentTarget
  const highlightedKey =
    root instanceof HTMLElement
      ? root.querySelector<HTMLElement>(
          '[role="option"][data-highlighted][data-command-value]',
        )?.dataset.commandValue
      : undefined
  const target =
    results.value.find((result) => resultKey(result) === highlightedKey) ??
    results.value[0]
  if (!target) return

  event.preventDefault()
  event.stopPropagation()
  if (event.altKey) dock(target)
  else choose(target, true)
}

function shortcutKey(event: KeyboardEvent) {
  const typing =
    event.target instanceof HTMLInputElement ||
    event.target instanceof HTMLTextAreaElement ||
    event.target instanceof HTMLSelectElement
  if (
    (event.key.toLowerCase() === 'k' && (event.metaKey || event.ctrlKey)) ||
    (event.key === '/' && !typing)
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
      >Search</span
    ><UiKbd
      class="max-[800px]:hidden border-chrome-border bg-transparent text-chrome-muted-foreground"
      >{{ shortcut }}</UiKbd
    >
  </button>

  <UiCommandDialog
    v-model:open="open"
    v-model="selectedValue"
    :ignore-filter="true"
    @keydown.capture="handleKeydown"
  >
    <UiCommandInput
      v-model="query"
      placeholder="Search tenants, sites, devices, clients, interfaces…"
      label="Search tenants, sites, devices, clients, and interfaces"
    />
    <UiCommandList label="Search results" class="search-scroll flex-1 min-h-0">
      <UiCommandEmpty>
        <p
          v-if="query.trim() && !results.length"
          class="search-empty py-5 px-3 text-xs text-muted-foreground text-center"
        >
          Nothing matches “{{ query.trim() }}”.
        </p>
        <p
          v-else
          class="search-empty py-5 px-3 text-xs text-muted-foreground text-center"
        >
          Type a name, IP address, MAC address, or port.
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
            <span>Recent</span>
            <button
              type="button"
              class="search-clear text-xs text-muted-foreground hover:text-foreground cursor-pointer"
              @mousedown.prevent
              @click="forget"
            >
              Clear
            </button>
          </div>

          <UiCommandItem
            v-for="result in group.items"
            :key="resultKey(result)"
            :value="resultKey(result)"
            class="search-result"
            @select="handleItemSelect(result, $event)"
          >
            <AppIcon :name="iconFor(result, group.icon)" />
            <span>
              <strong>{{ result.title }}</strong>
              <small>{{ result.detail }}</small>
            </span>
            <span class="search-result-actions ml-auto flex items-center gap-1">
              <UiTooltip
                v-if="canSplit"
                label="Open side by side"
                :shortcut="{ code: 'Enter', shift: true }"
                side="left"
                inline
              >
                <button
                  type="button"
                  tabindex="-1"
                  :aria-label="`Open ${result.title} side by side`"
                  @click.stop="choose(result, true)"
                >
                  <AppIcon name="panel-right" />
                </button>
              </UiTooltip>
              <UiTooltip
                v-if="!(result.kind === 'page' && result.id.startsWith('tab:'))"
                label="Send to dock"
                :shortcut="{ code: 'Enter', alt: true }"
                side="left"
                inline
              >
                <button
                  type="button"
                  tabindex="-1"
                  :aria-label="`Send ${result.title} to the dock`"
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
      <span><UiKbd class="mr-1">↑</UiKbd><UiKbd>↓</UiKbd> move</span>
      <span><UiKbd>↵</UiKbd> open</span>
      <span v-if="canSplit"
        ><UiKbd class="mr-1">⇧</UiKbd><UiKbd>↵</UiKbd> side by side</span
      >
      <span><UiKbd class="mr-1">⌥</UiKbd><UiKbd>↵</UiKbd> to dock</span>
      <span><UiKbd>esc</UiKbd> close</span>
    </footer>
  </UiCommandDialog>
</template>

<script setup lang="ts">
import { UiTooltip } from '../ui'
import ScrollArea from './ScrollArea.vue'
import { computed, nextTick, onMounted, onUnmounted, ref, useId } from 'vue'
import type { Device } from '../domain/fleet'
import { searchAll } from '../domain/search'
import type { ResultKind, SearchResult } from '../domain/search'
import AppIcon from './AppIcon.vue'
import { clearRecent, loadRecent, rememberRecent } from './recentSearches'
import { sites, tenants } from '../domain/fleet'

export interface SearchPage {
  id: string
  title: string
  detail: string
  icon: string
}
const props = defineProps<{
  fleet: Device[]
  // Workspace pages and docked tabs, offered alongside fleet objects.
  pages: SearchPage[]
  canSplit: boolean
}>()
const emit = defineEmits<{
  select: [result: SearchResult, beside: boolean]
  dock: [result: SearchResult]
}>()
const id = useId()
const dialog = ref<HTMLDialogElement>()
const input = ref<HTMLInputElement>()
const query = ref('')
const active = ref(0)
const recent = ref<SearchResult[]>([])
// A remembered result is offered only while what it points at still exists.
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
            items: results.value.map((result, index) => ({ result, index })),
          },
        ]
      : []
    : groups
        .map((group) => ({
          ...group,
          items: results.value
            .map((result, index) => ({ result, index }))
            .filter(({ result }) => result.kind === group.kind),
        }))
        .filter((group) => group.items.length),
)
const shortcut = /Mac|iPhone|iPad/.test(navigator.platform) ? '⌘K' : 'Ctrl K'

async function open() {
  query.value = ''
  active.value = 0
  recent.value = loadRecent()
  dialog.value?.showModal()
  await nextTick()
  input.value?.focus()
}
// Shift, Cmd, or Ctrl with Enter or a click opens the result beside the
// current page.
function choose(result: SearchResult | undefined, beside = false) {
  if (!result) return
  dialog.value?.close()
  recent.value = rememberRecent(result)
  emit('select', result, beside && props.canSplit)
}
// Alt with Enter, or the row's dock button, keeps the result for later
// without leaving the current page.
function dock(result: SearchResult | undefined) {
  if (!result) return
  dialog.value?.close()
  recent.value = rememberRecent(result)
  emit('dock', result)
}
function modified(event: KeyboardEvent | MouseEvent) {
  return event.shiftKey || event.metaKey || event.ctrlKey
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
  input.value?.focus()
}
function keydown(event: KeyboardEvent) {
  const count = results.value.length
  if (event.key === 'ArrowDown' && count)
    active.value = (active.value + 1) % count
  else if (event.key === 'ArrowUp' && count)
    active.value = (active.value - 1 + count) % count
  else if (event.key === 'Enter' && event.altKey)
    dock(results.value[active.value])
  else if (event.key === 'Enter')
    choose(results.value[active.value], modified(event))
  else return
  event.preventDefault()
  void nextTick(() =>
    dialog.value
      ?.querySelector(`#${CSS.escape(`${id}-${active.value}`)}`)
      ?.scrollIntoView({ block: 'nearest' }),
  )
}
// ⌘K / Ctrl K anywhere, and "/" when focus is not in a text field.
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
    if (!dialog.value?.open) void open()
  }
}
onMounted(() => window.addEventListener('keydown', shortcutKey))
onUnmounted(() => window.removeEventListener('keydown', shortcutKey))
</script>

<template>
  <button class="search-trigger" aria-haspopup="dialog" @click="open">
    <AppIcon name="search" /><span>Search</span><kbd>{{ shortcut }}</kbd>
  </button>
  <dialog
    ref="dialog"
    class="search-dialog"
    aria-label="Search everything"
    @click.self="dialog?.close()"
  >
    <div class="search-panel">
      <label class="search-field"
        ><AppIcon name="search" /><input
          ref="input"
          v-model="query"
          role="combobox"
          aria-autocomplete="list"
          aria-expanded="true"
          :aria-controls="`${id}-list`"
          :aria-activedescendant="
            results.length ? `${id}-${active}` : undefined
          "
          aria-label="Search tenants, sites, devices, clients, and interfaces"
          placeholder="Search tenants, sites, devices, clients, interfaces…"
          @input="active = 0"
          @keydown="keydown"
      /></label>
      <ScrollArea class="search-scroll" viewport-class="search-viewport">
        <div :id="`${id}-list`" role="listbox" class="search-results">
          <div
            v-for="group in grouped"
            :key="group.kind"
            role="group"
            :aria-label="group.label"
          >
            <h3>
              {{ group.label
              }}<button
                v-if="group.kind === 'recent'"
                class="search-clear"
                @mousedown.prevent
                @click="forget"
              >
                Clear
              </button>
            </h3>
            <div
              v-for="{ result, index } in group.items"
              :id="`${id}-${index}`"
              :key="`${result.kind}-${result.id}-${result.port ?? ''}`"
              role="option"
              :aria-selected="index === active"
              :class="['search-result', { active: index === active }]"
              @mousemove="active = index"
              @mousedown.prevent
              @click="choose(result, modified($event))"
            >
              <AppIcon :name="iconFor(result, group.icon)" />
              <span
                ><strong>{{ result.title }}</strong
                ><small>{{ result.detail }}</small></span
              >
              <span class="search-result-actions">
                <UiTooltip
                  v-if="canSplit"
                  label="Open side by side"
                  hint="Shift+Enter"
                  side="left"
                >
                  <button
                    tabindex="-1"
                    :aria-label="`Open ${result.title} side by side`"
                    @click.stop="choose(result, true)"
                  >
                    <AppIcon name="panel-right" />
                  </button>
                </UiTooltip>
                <UiTooltip
                  v-if="
                    !(result.kind === 'page' && result.id.startsWith('tab:'))
                  "
                  label="Send to dock"
                  hint="Alt+Enter"
                  side="left"
                >
                  <button
                    tabindex="-1"
                    :aria-label="`Send ${result.title} to the dock`"
                    @click.stop="dock(result)"
                  >
                    <AppIcon name="to-dock" />
                  </button>
                </UiTooltip>
              </span>
            </div>
          </div>
          <p v-if="query.trim() && !results.length" class="search-empty">
            Nothing matches “{{ query.trim() }}”.
          </p>
          <p v-else-if="!results.length" class="search-empty">
            Type a name, IP address, MAC address, or port.
          </p>
        </div>
      </ScrollArea>
      <footer class="search-hints" aria-hidden="true">
        <span><kbd>↑</kbd><kbd>↓</kbd> move</span><span><kbd>↵</kbd> open</span
        ><span v-if="canSplit"><kbd>⇧</kbd><kbd>↵</kbd> side by side</span
        ><span><kbd>⌥</kbd><kbd>↵</kbd> to dock</span
        ><span><kbd>esc</kbd> close</span>
      </footer>
    </div>
  </dialog>
</template>

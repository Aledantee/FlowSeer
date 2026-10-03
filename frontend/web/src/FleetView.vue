<script setup lang="ts">
import {
  computed,
  nextTick,
  onMounted,
  onUnmounted,
  provide,
  ref,
  watch,
  watchEffect,
} from 'vue'
import type { ComponentPublicInstance, Ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRoute, useRouter } from 'vue-router'
import AppIcon from './components/AppIcon.vue'
import AccountMenu from './components/AccountMenu.vue'
import ReportBugButton from './components/ReportBugButton.vue'
import HelpButton from './components/HelpButton.vue'
import ThemeSwitcher from './components/ThemeSwitcher.vue'
import LocaleSwitcher from './components/LocaleSwitcher.vue'
import GlobalSearch, {
  type SearchPage,
  type TextPart,
} from './components/GlobalSearch.vue'
import TenantSwitcher from './components/TenantSwitcher.vue'
import ScopeSwitcher from './components/ScopeSwitcher.vue'
import PageHost from './navigation/PageHost.vue'
import {
  UiBreadcrumb,
  UiBreadcrumbEllipsis,
  UiBreadcrumbItem,
  UiBreadcrumbLink,
  UiBreadcrumbList,
  UiBreadcrumbPage,
  UiBreadcrumbSeparator,
  UiAiActionLayer,
  UiDropdownMenuItem,
  UiMotion,
  UiTooltip,
  useMotionFeedback,
} from './ui'
import {
  SHORTCUTS,
  dockTabShortcut,
  matches,
  modalOpen,
  typingIn,
} from './navigation/shortcuts'
import { usePanes } from './navigation/panes'
import type { SlotId } from './navigation/panes'
import AppLink from './navigation/AppLink.vue'
import PageDock from './navigation/PageDock.vue'
import type { SearchResult } from './domain/search'
import {
  devices,
  sites,
  tenants,
  tenantIds,
  filterDevices,
  moveDevice,
} from './domain/fleet'
import {
  pageContext,
  resolveTarget,
  routePage,
  scopeOf,
  viewOf,
} from './navigation/page'
import type { PageContext, PageLocation, PageTarget } from './navigation/page'
import { workspaceContext } from './navigation/workspace'
import type { PaneId } from './navigation/workspace'
import {
  DOCK_KEY,
  isLocation,
  loadDock,
  minimize,
  openBeside,
  openTab,
  saveDock,
} from './navigation/dock'
import type { DockTab, Panes } from './navigation/dock'
import { useLabels } from './i18n/labels'

// A name has no translation, so the brand is data, not a message.
const BRAND = 'FlowSeer'
const NAV_ITEMS = [
  'dashboard',
  'devices',
  'topology',
  'clients',
  'sites',
] as const
const SEARCH_VIEWS = [
  'dashboard',
  'devices',
  'clients',
  'sites',
  'topology',
] as const

const { t, n } = useI18n({ useScope: 'global' })
const labels = useLabels()
const { play, reduced } = useMotionFeedback()
const sidebar = ref<ComponentPublicInstance | null>(null)
const navigation = ref<ComponentPublicInstance | null>(null)
const mainShell = ref<ComponentPublicInstance | null>(null)
const topbar = ref<HTMLElement>()
const topbarHeight = ref(54)
const layoutDependency = ref(0)
let topbarObserver: ResizeObserver | undefined
onMounted(() => {
  topbarObserver = new ResizeObserver(() => {
    if (!topbar.value) return
    topbarHeight.value = topbar.value.offsetHeight
    const shell = mainShell.value?.$el
    if (shell instanceof HTMLElement)
      shell.style.setProperty('--topbar-height', `${topbarHeight.value}px`)
  })
  if (topbar.value) topbarObserver.observe(topbar.value)
})
onUnmounted(() => topbarObserver?.disconnect())
const route = useRoute()
const router = useRouter()
const fleet = ref(devices.map((device) => ({ ...device })))
const sidebarCollapsed = ref(false)
const tick = ref(0)
const message = ref('')

function siteName(id: string) {
  return (
    sites.find((site) => site.id === id)?.name || t('view.common.unknownSite')
  )
}
function tenantName(siteId: string) {
  return (
    tenants.find(
      (tenant) =>
        tenant.id === sites.find((site) => site.id === siteId)?.tenantId,
    )?.name || t('view.common.unknownTenant')
  )
}
export interface Move {
  deviceId: string
  name: string
  from: string
  to: string
  observed: boolean
  reverted: boolean
}
const move = ref<Move>()
let moveTimer: ReturnType<typeof setTimeout> | undefined

function refocus(deviceId: string) {
  if (document.activeElement !== document.body) return
  const row = [
    ...document.querySelectorAll<HTMLElement>(`[data-device-id="${deviceId}"]`),
  ].find((item) => item.offsetParent !== null)
  row?.focus()
}

function reassign(deviceId: string, destination: string, reverted = false) {
  const device = fleet.value.find((item) => item.id === deviceId)
  if (!device) return
  try {
    const updated = moveDevice(device, destination)
    clearTimeout(moveTimer)
    message.value = ''
    move.value = {
      deviceId: updated.id,
      name: updated.name,
      from: device.siteId,
      to: destination,
      observed: false,
      reverted,
    }
    moveTimer = setTimeout(() => {
      fleet.value = fleet.value.map((item) =>
        item.id === updated.id ? updated : item,
      )
      if (move.value?.deviceId === updated.id) move.value.observed = true
      void nextTick(() => refocus(updated.id))
    }, 1200)
  } catch {
    message.value = 'view.fleet.assignFailed'
  }
}

function undoMove() {
  const last = move.value
  if (!last) return
  reassign(last.deviceId, last.from, true)
}

function dismissNotice() {
  message.value = ''
  move.value = undefined
}
// Per-window view state survives a reload but is not shared with other
// windows, unlike the dock.
function sessionRef<T>(
  key: string,
  fallback: T,
  valid: (value: unknown) => value is T,
): Ref<T> {
  let initial = fallback
  try {
    const parsed: unknown = JSON.parse(sessionStorage.getItem(key) ?? 'null')
    if (valid(parsed)) initial = parsed
  } catch {
    // Unreadable storage starts from the default.
  }
  const state = ref(initial) as Ref<T>
  watch(
    state,
    (value) => {
      try {
        sessionStorage.setItem(key, JSON.stringify(value ?? null))
      } catch {
        // Losing it only resets the view on reload.
      }
    },
    { deep: true },
  )
  return state
}
const isBoolean = (value: unknown): value is boolean =>
  typeof value === 'boolean'
const isSlot = (value: unknown): value is SlotId =>
  value === 'a' || value === 'b'

// Two pane slots: the main one mirrors the browser route, the other is the
// side page. The active pane is the one search acts on.
const mainPage = routePage(route, router)
const savedSide = sessionRef<PageLocation | null>(
  'flowseer.side',
  null,
  (value): value is PageLocation | null => value === null || isLocation(value),
)
const savedSlot = sessionRef<SlotId>('flowseer.main-slot', 'a', isSlot)
const panes = usePanes(route, router, {
  mainSlot: savedSlot.value,
  side: savedSide.value ?? undefined,
})
const side = panes.side
watch(side, (value) => (savedSide.value = value ?? null), { deep: true })
watch(panes.mainSlot, (value) => (savedSlot.value = value))
const sidePage = computed(() => panes.sidePage.value)
const slotIds: SlotId[] = ['a', 'b']
const wideQuery = window.matchMedia('(min-width: 1151px)')
const wide = ref(wideQuery.matches)
const trackWidth = (event: MediaQueryListEvent) => (wide.value = event.matches)
onMounted(() => wideQuery.addEventListener('change', trackWidth))
onUnmounted(() => wideQuery.removeEventListener('change', trackWidth))
const showSplit = computed(() => wide.value && side.value !== undefined)
const activePane = ref<PaneId>('main')
watch(showSplit, (shown) => {
  if (!shown) activePane.value = 'main'
})
const activePage = computed(() =>
  activePane.value === 'side' && showSplit.value ? sidePage.value : mainPage,
)
const linkClicks = sessionRef('flowseer.link-clicks', false, isBoolean)
const peek = ref<string[]>([])
const sideDeviceId = computed(() =>
  showSplit.value ? sidePage.value.deviceId.value : undefined,
)
// A peek ends once the side page shows something outside its list.
watch(sideDeviceId, (id) => {
  if (!id || !peek.value.includes(id)) peek.value = []
})

async function navigateMain(location: PageLocation) {
  try {
    await panes.navigateMain(location)
  } catch {
    message.value = 'view.fleet.openFailed'
  }
}
// Opening a page on the right keeps the page already there by docking it.
// Navigation that belongs to the side page itself replaces it instead:
// linked clicks and peeks, which would otherwise fill the dock with every
// row glanced at.
async function openIn(
  pane: PaneId,
  location: PageLocation,
  options: { replace?: boolean } = {},
) {
  if (pane !== 'side' || !wide.value) return navigateMain(location)
  const current = side.value
  const peeking =
    sideDeviceId.value !== undefined && peek.value.includes(sideDeviceId.value)
  if (
    current &&
    !options.replace &&
    !peeking &&
    JSON.stringify(current) !== JSON.stringify(location)
  )
    tabs.value = minimize(tabs.value, current)
  side.value = location
}
async function follow(
  page: PageContext,
  target: PageTarget,
  options: { beside?: boolean; dock?: boolean } = {},
) {
  const location = resolveTarget(page.location.value, target)
  if (options.dock) {
    tabs.value = minimize(tabs.value, location)
    return
  }
  const from: PaneId = page.primary ? 'main' : 'side'
  if (options.beside && wide.value)
    return openIn(from === 'main' ? 'side' : 'main', location)
  // Linked clicks send the main pane's links to other pages into the side
  // pane; links that stay on the same page, like filters, stay put.
  if (
    from === 'main' &&
    linkClicks.value &&
    showSplit.value &&
    viewOf(location.path).view !== page.view.value
  )
    return openIn('side', location, { replace: true })
  try {
    await page.go(target)
  } catch {
    message.value = 'view.fleet.openFailed'
  }
}
// Links outside the panes, such as the sidebar, belong to the main pane.
provide(pageContext, mainPage)
provide(workspaceContext, {
  fleet,
  message,
  move,
  undoMove,
  dismissNotice,
  reassign,
  siteName,
  tenantName,
  follow,
  activePane,
  peek,
  sideDeviceId,
})

// The sidebar and breadcrumb describe the main pane; the side pane carries
// its own scope switchers. Search acts on whichever pane is active.
const mainView = mainPage.view
const section = computed(() =>
  mainView.value === 'device' ? 'devices' : mainView.value,
)
const mainScope = computed(() => scopeOf(mainPage.location.value))
const view = mainView
const query = mainPage.query
const activeQuery = (key: string) => activePage.value.query(key)
const activeSection = computed(() => {
  const active = activePage.value.view.value
  return active === 'device' ? 'devices' : active
})
const selected = computed(() =>
  fleet.value.find((device) => device.id === mainPage.deviceId.value),
)
const title = computed(() =>
  view.value === 'device'
    ? (selected.value?.name ?? t('view.common.unknownDevice'))
    : labels.page(view.value),
)
const scope = computed(() =>
  filterDevices(fleet.value, query('tenant'), query('site'), '', ''),
)
const scopedSites = computed(() =>
  sites.filter((site) => tenantIds(query('tenant')).includes(site.tenantId)),
)
watch(
  () => [mainPage.query('tenant'), mainPage.query('site')],
  () => {
    const shell = mainShell.value?.$el
    const pane =
      shell instanceof HTMLElement
        ? shell.querySelector<HTMLElement>('.main-pane .pane-scroll')
        : undefined
    play(pane ?? undefined, { opacity: [0.85, 1] }, 0.12)
  },
  { flush: 'post' },
)
function toggleSidebar() {
  if (!(sidebar.value?.$el instanceof HTMLElement)) return
  sidebarCollapsed.value = !sidebarCollapsed.value
  if (window.matchMedia('(min-width: 801px)').matches) {
    if (!reduced.value) layoutDependency.value++
  } else if (!sidebarCollapsed.value) {
    const nav = navigation.value?.$el
    play(
      nav instanceof HTMLElement ? nav : undefined,
      { opacity: [0.6, 1] },
      0.1,
    )
  }
}
async function setQuery(key: string, value: string) {
  try {
    await mainPage.go(
      {
        query: {
          ...mainPage.location.value.query,
          [key]: value,
          ...(key === 'tenant' ? { site: undefined } : {}),
          ...(key === 'site' && !query('tenant')
            ? { tenant: sites.find((item) => item.id === value)?.tenantId }
            : {}),
        },
      },
      { replace: true },
    )
  } catch {
    message.value = 'view.common.updateFailed'
  }
}
// A site without its tenant in the URL would leave the breadcrumb reading
// "All tenants" while one customer's site is in view, so the tenant is filled
// in from the site.
watch(
  () => [query('site'), query('tenant')],
  ([site, tenant]) => {
    const owner = sites.find((item) => item.id === site)?.tenantId
    if (owner && !tenant) {
      router
        .replace({ query: { ...route.query, tenant: owner } })
        .catch(() => undefined)
    }
  },
  { immediate: true },
)
// The breadcrumb offers the devices in the current scope, plus the open one
// when the scope would otherwise hide it.
const deviceOptions = computed(() =>
  [
    ...scope.value,
    ...(selected.value && !scope.value.includes(selected.value)
      ? [selected.value]
      : []),
  ]
    .sort((a, b) => a.name.localeCompare(b.name))
    .map((device) => ({
      value: device.id,
      label: device.name,
      identifier: true,
    })),
)
async function openDevice(id: string) {
  await follow(mainPage, {
    path: `/devices/${id}`,
    query: scopeOf(mainPage.location.value),
  })
}
// The side page keeps the scope it was opened with; its header sets its own.
const sideScopedSites = computed(() =>
  sites.filter((site) =>
    tenantIds(sidePage.value.query('tenant')).includes(site.tenantId),
  ),
)
function setSideScope(key: 'tenant' | 'site', value: string) {
  if (!side.value) return
  const query: Record<string, string | undefined> = {
    ...side.value.query,
    [key]: value || undefined,
  }
  if (key === 'tenant') query.site = undefined
  void sidePage.value.go({ query }, { replace: true })
}
// Each kind of search result opens where that object is looked after: pages
// open as they are, scope changes keep the current page, clients open
// filtered to their access point, and interfaces open selected on the
// topology.
function locationFor(result: SearchResult): PageLocation | undefined {
  const scoped = (extra: Record<string, string | undefined>) => {
    const next: Record<string, string> = {}
    for (const [key, value] of Object.entries(extra))
      if (value) next[key] = value
    return next
  }
  const tenant = activeQuery('tenant') || undefined
  if (result.kind === 'page')
    return result.id.startsWith('view:')
      ? {
          path: `/${result.id.slice(5)}`,
          query: scoped({ tenant, site: activeQuery('site') || undefined }),
        }
      : undefined
  if (result.kind === 'tenant')
    return { path: `/${activeSection.value}`, query: { tenant: result.id } }
  if (result.kind === 'site') {
    const site = sites.find((item) => item.id === result.id)
    const tenantKeeps =
      site && tenantIds(activeQuery('tenant')).includes(site.tenantId)
    return {
      path: `/${activeSection.value}`,
      query: scoped({
        tenant: tenantKeeps ? tenant : undefined,
        site: result.id,
      }),
    }
  }
  if (result.kind === 'device')
    return { path: `/devices/${result.id}`, query: scoped({ tenant }) }
  if (result.kind === 'client')
    return {
      path: '/clients',
      query: {
        ap: result.id.replace(/-client-\d+$/, ''),
        q: result.title,
      },
    }
  const device = fleet.value.find((item) => item.id === result.id)
  return {
    path: '/topology',
    query: scoped({
      site: device?.siteId,
      focus: `${result.id}~${result.port ?? ''}`,
    }),
  }
}
// A result opens in the active pane, or with beside in the other one.
async function openResult(result: SearchResult, beside: boolean) {
  if (result.kind === 'page' && result.id.startsWith('tab:')) {
    const id = result.id.slice(4)
    if (beside) openDockTabBeside(id)
    else await openDockTab(id)
    return
  }
  const location = locationFor(result)
  if (!location) return
  const target: PaneId = beside
    ? activePane.value === 'side'
      ? 'main'
      : 'side'
    : activePane.value
  await openIn(target, location)
  if (showSplit.value) activePane.value = target
}

function dockResult(result: SearchResult) {
  const location = locationFor(result)
  if (location) tabs.value = minimize(tabs.value, location)
}
// Minimized pages, and pairs of them, wait in the dock.
const tabs = ref<DockTab[]>(loadDock())
watch(tabs, (value) => saveDock(value), { deep: true })
// Every open window shares one dock: another window's change replaces this
// one's copy instead of being overwritten by it.
function syncDock(event: StorageEvent) {
  if (event.key === DOCK_KEY) tabs.value = loadDock()
}
onMounted(() => window.addEventListener('storage', syncDock))
onUnmounted(() => window.removeEventListener('storage', syncDock))
function currentPanes(): Panes {
  return {
    main: mainPage.location.value,
    side: showSplit.value ? side.value : undefined,
  }
}
async function leaveMain() {
  if (window.history.state?.back) router.back()
  else await navigateMain({ path: '/dashboard', query: mainScope.value })
}
// Docking the main pane hands it to the side page when there is one.
async function minimizePane(pane: PaneId = activePane.value) {
  if (pane === 'side' && side.value) {
    tabs.value = minimize(tabs.value, side.value)
    side.value = undefined
    return
  }
  tabs.value = minimize(tabs.value, mainPage.location.value)
  if (showSplit.value && side.value) await panes.promoteSide()
  else await leaveMain()
}
async function dockPair() {
  if (!side.value) return
  tabs.value = minimize(tabs.value, mainPage.location.value, side.value)
  side.value = undefined
  await leaveMain()
}
async function openDockTab(id: string) {
  const result = openTab(tabs.value, id, currentPanes())
  if (!result.panes) return
  tabs.value = result.tabs
  side.value = result.panes.side
  await navigateMain(result.panes.main)
}
function openDockTabBeside(id: string) {
  if (!wide.value) return void openDockTab(id)
  const result = openBeside(
    tabs.value,
    id,
    showSplit.value ? side.value : undefined,
  )
  if (!result.side) return
  tabs.value = result.tabs
  side.value = result.side
  activePane.value = 'side'
}
function closeTab(id: string) {
  tabs.value = tabs.value.filter((tab) => tab.id !== id)
}
// The focused page stays focused as it changes sides.
async function swapPanes() {
  if (!side.value) return
  activePane.value = activePane.value === 'main' ? 'side' : 'main'
  await panes.swap()
}
function closeSide() {
  side.value = undefined
  peek.value = []
}
// Splitting with nothing beside duplicates the page, as editors do; with a
// side page open it docks that page.
function toggleSplit() {
  if (!wide.value) return
  if (side.value) void minimizePane('side')
  else {
    side.value = { ...mainPage.location.value }
    activePane.value = 'side'
  }
}

const VIEW_ICONS: Record<string, string> = {
  device: 'devices',
  dashboard: 'dashboard',
  devices: 'devices',
  clients: 'clients',
  sites: 'sites',
  topology: 'topology',
}
function describe(location: PageLocation) {
  const { view: pageView, deviceId } = viewOf(location.path)
  const device = fleet.value.find((item) => item.id === deviceId)
  const site = sites.find((item) => item.id === location.query.site)
  const tenant = tenants.find((item) => item.id === location.query.tenant)
  // A docked page keeps reporting: a device page its health, a list page
  // how many devices in its scope need attention.
  const attention = device
    ? undefined
    : filterDevices(
        fleet.value,
        location.query.tenant ?? '',
        location.query.site ?? '',
        '',
        'attention',
      ).length
  return {
    label:
      pageView === 'device'
        ? (device?.name ?? t('view.common.unknownDevice'))
        : labels.page(pageView),
    detail: device
      ? siteName(device.siteId)
      : (site?.name ?? tenant?.name ?? t('view.common.allSites')),
    icon: VIEW_ICONS[pageView] ?? 'dashboard',
    health: device?.health,
    attention: attention || undefined,
    labelName: pageView === 'device' && Boolean(device?.name),
    detailName: Boolean(
      device
        ? sites.some((s) => s.id === device.siteId)
        : (site?.name ?? tenant?.name),
    ),
  }
}
function tabTitle(tab: DockTab) {
  const first = describe(tab.location)
  if (!tab.beside) return { ...first, pair: undefined }
  const second = describe(tab.beside)
  return {
    label: t('view.common.pair', { first: first.label, second: second.label }),
    detail: first.detail,
    icon: 'split',
    health:
      [first.health, second.health].find((health) => health === 'Offline') ??
      [first.health, second.health].find((health) => health === 'Degraded') ??
      first.health ??
      second.health,
    attention: (first.attention ?? 0) + (second.attention ?? 0) || undefined,
    labelName: false,
    detailName: first.detailName,
    pair: {
      first: { label: first.label, name: first.labelName },
      second: { label: second.label, name: second.labelName },
    },
  }
}
const sideTitle = computed(() =>
  side.value ? describe(side.value) : undefined,
)

const searchPages = computed<SearchPage[]>(() => [
  ...SEARCH_VIEWS.map((name) => ({
    id: `view:${name}`,
    title: labels.page(name),
    icon: VIEW_ICONS[name] ?? 'dashboard',
    parts: [{ text: t('view.fleet.searchPage') }],
  })),
  ...tabs.value.map((tab) => {
    const described = tabTitle(tab)
    const parts: TextPart[] = [
      { text: t(tab.beside ? 'view.fleet.dockedPair' : 'view.fleet.docked') },
      { text: described.detail ?? '', identifier: described.detailName },
    ]
    return {
      id: `tab:${tab.id}`,
      title: described.label,
      icon: described.icon,
      identifier: described.labelName,
      pair: described.pair,
      parts,
    }
  }),
])

const SPLIT_KEY = 'flowseer.split-ratio'
function storedRatio() {
  try {
    const value = Number(localStorage.getItem(SPLIT_KEY))
    return value >= 0.3 && value <= 0.7 ? value : 0.5
  } catch {
    return 0.5
  }
}
const splitRatio = ref(storedRatio())
watch(splitRatio, (value) => {
  try {
    localStorage.setItem(SPLIT_KEY, String(value))
  } catch {
    // The ratio falls back to half on the next load.
  }
})
function nudgeSplit(delta: number) {
  splitRatio.value = Math.min(0.7, Math.max(0.3, splitRatio.value + delta))
}
let stopResize: (() => void) | undefined
function startResize(event: PointerEvent) {
  stopResize?.()
  const panes =
    event.currentTarget instanceof HTMLElement
      ? event.currentTarget.closest('.panes')
      : null
  if (!panes) return
  const box = panes.getBoundingClientRect()
  const move = (next: PointerEvent) => {
    splitRatio.value = Math.min(
      0.7,
      Math.max(0.3, (next.clientX - box.left) / box.width),
    )
  }
  const stop = () => {
    if (stopResize !== stop) return
    stopResize = undefined
    window.removeEventListener('pointermove', move)
    window.removeEventListener('pointerup', stop)
    window.removeEventListener('pointercancel', stop)
    window.removeEventListener('blur', stop)
    document.body.classList.remove('resizing-panes')
  }
  stopResize = stop
  document.body.classList.add('resizing-panes')
  window.addEventListener('pointermove', move)
  window.addEventListener('pointerup', stop)
  window.addEventListener('pointercancel', stop)
  window.addEventListener('blur', stop)
}
onUnmounted(() => stopResize?.())

// Workspace shortcuts, from the registry the tooltips read. While a peek is
// open the arrow keys step through the list that opened it and Escape closes
// it.
function workspaceKey(event: KeyboardEvent) {
  if (modalOpen()) return
  const run = (action: () => unknown) => {
    event.preventDefault()
    if (event.repeat) return
    void action()
  }
  if (matches(event, SHORTCUTS.toggleSplit)) return run(toggleSplit)
  // Alt shortcuts would swallow characters typed into fields.
  if (typingIn(event.target)) return
  if (matches(event, SHORTCUTS.minimize)) return run(() => minimizePane())
  if (matches(event, SHORTCUTS.dockPair)) return run(dockPair)
  if (showSplit.value) {
    if (matches(event, SHORTCUTS.swap)) return run(swapPanes)
    if (matches(event, SHORTCUTS.closeSide)) return run(closeSide)
    if (matches(event, SHORTCUTS.linkClicks))
      return run(() => (linkClicks.value = !linkClicks.value))
    if (matches(event, SHORTCUTS.focusMain))
      return run(() => (activePane.value = 'main'))
    if (matches(event, SHORTCUTS.focusSide))
      return run(() => (activePane.value = 'side'))
  }
  for (const [index, tab] of tabs.value.slice(0, 9).entries()) {
    if (matches(event, dockTabShortcut(index + 1)))
      return run(() => openDockTab(tab.id))
    if (matches(event, dockTabShortcut(index + 1, true)))
      return run(() => openDockTabBeside(tab.id))
  }
  if (event.metaKey || event.ctrlKey || event.altKey) return
  const current = sideDeviceId.value
  const index = current ? peek.value.indexOf(current) : -1
  if (index < 0) return
  if (event.key === 'ArrowDown' || event.key === 'ArrowUp') {
    const next =
      peek.value[
        (index + (event.key === 'ArrowDown' ? 1 : -1) + peek.value.length) %
          peek.value.length
      ]
    if (!next || !side.value) return
    event.preventDefault()
    side.value = { path: `/devices/${next}`, query: scopeOf(side.value) }
  } else if (event.key === 'Escape') run(closeSide)
}
// The browser tab names the pages on screen.
watchEffect(() => {
  const first = describe(mainPage.location.value).label
  const pages =
    showSplit.value && side.value
      ? t('view.common.pair', {
          first,
          second: describe(side.value).label,
        })
      : first
  document.title = t('view.fleet.documentTitle', { pages, brand: BRAND })
})
onMounted(() => window.addEventListener('keydown', workspaceKey))
onUnmounted(() => window.removeEventListener('keydown', workspaceKey))

let timer: ReturnType<typeof setInterval> | undefined
onMounted(() => {
  timer = setInterval(() => {
    tick.value++
    for (const [index, device] of fleet.value.entries()) {
      if (device.health !== 'Offline')
        device.throughput = Math.max(
          1,
          device.throughput + ((tick.value + index) % 5) - 2,
        )
    }
  }, 2500)
})
onUnmounted(() => clearInterval(timer))
</script>

<template>
  <UiAiActionLayer />
  <div
    class="shell max-[800px]:flex-col"
    :class="{ 'sidebar-collapsed': sidebarCollapsed }"
  >
    <a class="skip-link" href="#main">{{ t('view.fleet.skipToMain') }}</a>
    <UiMotion
      id="workspace-sidebar"
      ref="sidebar"
      as="aside"
      layout
      :layout-dependency="layoutDependency"
      class="sidebar brand-glow max-[800px]:p-[16px_20px_8px] max-[560px]:p-[14px_14px_6px]"
    >
      <UiMotion
        as="div"
        layout="position"
        :layout-dependency="layoutDependency"
        class="product-brand"
        role="img"
        translate="no"
        :aria-label="BRAND"
        :title="BRAND"
      >
        <svg
          class="flowseer-mark"
          viewBox="0 0 32 32"
          fill="none"
          aria-hidden="true"
        >
          <g transform="translate(16 16) skewX(-13) translate(-16 -16)">
            <rect
              x="5.5"
              y="4.5"
              width="5"
              height="23"
              rx="1.5"
              fill="var(--accent)"
            />
            <rect
              x="13.5"
              y="0"
              width="5"
              height="32"
              rx="1.5"
              fill="var(--accent)"
            />
            <rect
              x="21.5"
              y="7.5"
              width="5"
              height="17"
              rx="1.5"
              fill="var(--primary)"
            />
          </g>
        </svg>
        <span>{{ BRAND }}</span>
      </UiMotion>
      <UiMotion
        as="div"
        layout="position"
        :layout-dependency="layoutDependency"
        class="nav-label mt-7 text-2xs tracking-[1.5px] text-chrome-muted-foreground px-3 pb-3 max-[800px]:hidden"
      >
        {{ t('view.fleet.workspace') }}
      </UiMotion>
      <UiMotion
        ref="navigation"
        as="nav"
        layout="position"
        :layout-dependency="layoutDependency"
        :aria-label="t('view.fleet.mainNavigation')"
        class="max-[800px]:flex max-[800px]:flex-row max-[800px]:gap-2 max-[800px]:mt-6 max-[560px]:gap-1"
        :class="{ 'max-[800px]:!hidden': sidebarCollapsed }"
      >
        <AppLink
          v-for="item in NAV_ITEMS"
          :key="item"
          :aria-label="
            item === 'devices'
              ? t('view.fleet.devicesLink', {
                  page: labels.page(item),
                  count: n(scope.length, 'integer'),
                })
              : labels.page(item)
          "
          :to="{ path: `/${item}`, query: mainScope }"
          class="relative isolate flex items-center gap-2.5 px-3 py-2.5 mb-1 rounded text-chrome-muted-foreground font-medium hover:bg-chrome-hover/35 aria-[current=page]:text-chrome-ring max-[800px]:mb-0 max-[800px]:p-2.5 max-[560px]:gap-1.5 max-[560px]:text-xs max-[560px]:p-[9px]"
          :class="{
            active: section === item,
            'max-[560px]:hidden': item === 'topology',
          }"
          :aria-current="mainView === item ? 'page' : undefined"
        >
          <UiMotion
            v-if="section === item"
            as="span"
            layout-id="nav-highlight"
            :layout-dependency="section"
            class="nav-highlight absolute inset-0 -z-10 rounded-[inherit] bg-chrome-surface/48 backdrop-blur-md pointer-events-none after:content-[''] after:absolute after:top-1.5 after:bottom-1.5 after:right-0 after:w-0.5 after:rounded-l after:bg-chrome-ring max-[800px]:after:top-auto max-[800px]:after:bottom-0 max-[800px]:after:left-2.5 max-[800px]:after:right-2.5 max-[800px]:after:w-auto max-[800px]:after:h-0.5 max-[800px]:after:rounded-xs"
            aria-hidden="true"
          />
          <AppIcon :name="item" />
          <span class="nav-text">{{ labels.page(item) }}</span>
          <span
            v-if="item === 'devices'"
            class="nav-count ml-auto bg-chrome-hover rounded px-1.5 py-px text-2xs"
          >
            {{ n(scope.length, 'integer') }}
          </span>
        </AppLink>
      </UiMotion>
      <UiTooltip
        :label="
          sidebarCollapsed
            ? t('view.fleet.expandSidebar')
            : t('view.fleet.collapseSidebar')
        "
        side="right"
      >
        <UiMotion
          as="button"
          layout="position"
          :layout-dependency="layoutDependency"
          class="sidebar-toggle max-[560px]:min-h-[44px] max-[560px]:min-w-[44px]"
          type="button"
          aria-controls="workspace-sidebar"
          :aria-expanded="!sidebarCollapsed"
          :aria-label="
            sidebarCollapsed
              ? t('view.fleet.expandSidebar')
              : t('view.fleet.collapseSidebar')
          "
          @click="toggleSidebar"
        >
          <svg
            viewBox="0 0 16 16"
            aria-hidden="true"
            fill="none"
            stroke="currentColor"
            stroke-width="1.5"
          >
            <path d="m10 4-4 4 4 4" />
          </svg>
        </UiMotion>
      </UiTooltip>
    </UiMotion>
    <UiMotion
      ref="mainShell"
      as="div"
      layout="position"
      :layout-dependency="layoutDependency"
      class="main-shell"
    >
      <span class="main-notch brand-glow" aria-hidden="true"></span>
      <header
        ref="topbar"
        class="topbar sticky top-0 z-(--z-sticky) flex items-center justify-between min-h-[54px] px-6 py-2.5 max-[1150px]:px-6 max-[800px]:px-5 max-[650px]:flex-wrap max-[650px]:pt-1.5 max-[650px]:pb-2.5 max-[650px]:gap-1 max-[560px]:min-h-[50px] max-[560px]:px-3.5"
      >
        <span class="topbar-glass brand-glow" aria-hidden="true"></span>
        <div
          class="topbar-start flex items-center gap-3 min-w-0 flex-1 max-[650px]:basis-full max-[560px]:gap-[9px]"
        >
          <UiBreadcrumb
            v-slot="{ collapsed }"
            class="breadcrumb min-w-0 max-[560px]:text-xs"
          >
            <UiBreadcrumbList class="min-w-0">
              <template v-if="tenants.length > 1">
                <UiBreadcrumbItem class="min-w-0 max-[560px]:max-w-[140px]">
                  <TenantSwitcher
                    :tenants="tenants"
                    :selected="query('tenant')"
                    @change="setQuery('tenant', $event)"
                  />
                </UiBreadcrumbItem>
                <UiBreadcrumbSeparator />
              </template>
              <UiBreadcrumbItem
                class="breadcrumb-scope min-w-0 max-[560px]:max-w-[155px]"
              >
                <ScopeSwitcher
                  :label="t('view.fleet.siteScope')"
                  :placeholder="t('view.fleet.searchSites')"
                  :selected="query('site')"
                  :options="[
                    { value: '', label: t('view.common.allSites') },
                    ...scopedSites.map((site) => ({
                      value: site.id,
                      label: site.name,
                      identifier: true,
                    })),
                  ]"
                  @change="setQuery('site', $event)"
                />
              </UiBreadcrumbItem>
              <UiBreadcrumbSeparator />
              <template v-if="view === 'device'">
                <template v-if="collapsed">
                  <UiBreadcrumbItem>
                    <UiBreadcrumbEllipsis>
                      <template #menu>
                        <UiDropdownMenuItem as-child>
                          <AppLink
                            class="w-full"
                            :to="{ path: '/devices', query: mainScope }"
                          >
                            {{ labels.page('devices') }}
                          </AppLink>
                        </UiDropdownMenuItem>
                      </template>
                    </UiBreadcrumbEllipsis>
                  </UiBreadcrumbItem>
                  <UiBreadcrumbSeparator />
                </template>
                <template v-else>
                  <UiBreadcrumbItem>
                    <UiBreadcrumbLink as-child>
                      <AppLink
                        class="breadcrumb-link"
                        :to="{ path: '/devices', query: mainScope }"
                        >{{ labels.page('devices') }}</AppLink
                      >
                    </UiBreadcrumbLink>
                  </UiBreadcrumbItem>
                  <UiBreadcrumbSeparator />
                </template>
              </template>
              <UiBreadcrumbItem
                v-if="view === 'device' && selected"
                class="breadcrumb-scope min-w-0 max-[560px]:max-w-[155px]"
              >
                <ScopeSwitcher
                  :label="t('view.fleet.deviceSwitcher')"
                  :placeholder="t('view.fleet.searchDevices')"
                  :selected="selected.id"
                  :options="deviceOptions"
                  @change="openDevice($event)"
                />
              </UiBreadcrumbItem>
              <UiBreadcrumbItem v-else>
                <UiBreadcrumbPage>{{ title }}</UiBreadcrumbPage>
              </UiBreadcrumbItem>
            </UiBreadcrumbList>
          </UiBreadcrumb>
          <UiTooltip
            :label="t('view.fleet.minimizeToDock')"
            :hint="t('view.fleet.minimizeHint')"
            :shortcut="SHORTCUTS.minimize"
          >
            <button
              class="minimize-page grid place-items-center shrink-0 w-6.5 h-6.5 ml-2 p-0 border-0 rounded bg-transparent text-chrome-muted-foreground hover:bg-chrome-hover/45 hover:text-chrome-foreground cursor-pointer [&>svg]:w-3.5 max-[560px]:min-h-[44px] max-[560px]:min-w-[44px]"
              :aria-label="t('view.fleet.minimizeMain')"
              @click="minimizePane('main')"
            >
              <AppIcon name="to-dock" />
            </button>
          </UiTooltip>
        </div>
        <div
          class="topbar-tools flex items-center gap-1.5 shrink-0 ml-4 max-[650px]:order-first max-[650px]:w-full max-[650px]:ml-0 max-[650px]:justify-end"
        >
          <GlobalSearch
            :fleet="fleet"
            :pages="searchPages"
            :can-split="wide"
            @select="openResult"
            @dock="dockResult"
          />
          <ThemeSwitcher />
          <LocaleSwitcher />
          <HelpButton />
          <ReportBugButton />
          <AccountMenu />
        </div>
      </header>
      <main
        id="main"
        tabindex="-1"
        :class="['panes', { split: showSplit, docked: tabs.length }]"
        :style="showSplit ? { '--split': `${splitRatio * 100}%` } : undefined"
      >
        <UiTooltip
          v-if="showSplit"
          :label="t('view.fleet.dragToResize')"
          :hint="t('view.fleet.resizeHint')"
          side="right"
        >
          <div
            class="pane-divider relative z-[3] flex-[0_0_9px] -mx-1 cursor-col-resize touch-none select-none after:content-[''] after:absolute after:inset-x-1 after:bottom-0 after:top-[var(--topbar-height)] after:bg-border after:transition-colors hover:after:bg-accent-foreground focus-visible:after:bg-accent-foreground"
            role="separator"
            aria-orientation="vertical"
            :aria-label="t('view.fleet.resizeSplit')"
            :aria-valuenow="Math.round(splitRatio * 100)"
            aria-valuemin="30"
            aria-valuemax="70"
            tabindex="0"
            @pointerdown.self="startResize"
            @dblclick.self="splitRatio = 0.5"
            @keydown.left.prevent="nudgeSplit(-0.05)"
            @keydown.right.prevent="nudgeSplit(0.05)"
          ></div>
        </UiTooltip>
        <!-- Both slots stay mounted while they hold a page, so swapping only
             changes roles and order; each page keeps its state. -->
        <template v-for="slot in slotIds" :key="slot">
          <Transition name="split">
            <PageHost
              v-if="
                panes.slots[slot].value &&
                (slot === panes.mainSlot.value || showSplit)
              "
              :context="panes.pages[slot]"
              :pane-slot="slot"
              :class="
                slot === panes.mainSlot.value
                  ? [
                      'main-pane',
                      { 'active-pane': showSplit && activePane === 'main' },
                    ]
                  : ['split-pane', { 'active-pane': activePane === 'side' }]
              "
              :style="
                slot === panes.mainSlot.value
                  ? undefined
                  : { '--topbar-height': `${topbarHeight + 36}px` }
              "
              @pointerdown="
                activePane = slot === panes.mainSlot.value ? 'main' : 'side'
              "
              @focusin="
                activePane = slot === panes.mainSlot.value ? 'main' : 'side'
              "
            >
              <header
                v-if="slot !== panes.mainSlot.value && sideTitle"
                class="pane-header absolute z-[6] top-[calc(var(--topbar-height)-36px)] inset-x-0 flex items-center gap-2 h-9 px-2 pl-4 border-b border-border bg-background/82 backdrop-blur-md text-muted-foreground text-xs transition-colors"
              >
                <AppIcon :name="sideTitle.icon" class="shrink-0 w-3.5" />
                <span
                  class="pane-title shrink-0 max-w-[30%] truncate text-foreground font-semibold"
                  ><strong
                    :translate="sideTitle.labelName ? 'no' : undefined"
                    >{{ sideTitle.label }}</strong
                  ></span
                >
                <div
                  class="pane-scope flex items-center gap-0.5 min-w-0 mr-auto"
                >
                  <UiBreadcrumb>
                    <UiBreadcrumbList>
                      <template v-if="tenants.length > 1">
                        <UiBreadcrumbItem>
                          <TenantSwitcher
                            :tenants="tenants"
                            :selected="sidePage.query('tenant')"
                            @change="setSideScope('tenant', $event)"
                          />
                        </UiBreadcrumbItem>
                        <UiBreadcrumbSeparator />
                      </template>
                      <UiBreadcrumbItem>
                        <ScopeSwitcher
                          :label="t('view.fleet.sideSiteScope')"
                          :placeholder="t('view.fleet.searchSites')"
                          :selected="sidePage.query('site')"
                          :options="[
                            {
                              value: '',
                              label: t('view.common.allSites'),
                            },
                            ...sideScopedSites.map((site) => ({
                              value: site.id,
                              label: site.name,
                              identifier: true,
                            })),
                          ]"
                          @change="setSideScope('site', $event)"
                        />
                      </UiBreadcrumbItem>
                    </UiBreadcrumbList>
                  </UiBreadcrumb>
                </div>
                <div
                  class="pane-tools flex items-center gap-1 max-[560px]:gap-0"
                  role="toolbar"
                  :aria-label="t('view.fleet.splitView')"
                >
                  <UiTooltip
                    :label="
                      linkClicks
                        ? t('view.fleet.linkClicksOn')
                        : t('view.fleet.linkClicksOff')
                    "
                    :hint="
                      linkClicks ? t('view.fleet.linkClicksHint') : undefined
                    "
                    :shortcut="SHORTCUTS.linkClicks"
                  >
                    <button
                      :aria-pressed="linkClicks"
                      :aria-label="t('view.fleet.linkClicksLabel')"
                      class="grid place-items-center w-6.5 h-6.5 p-0 border-0 rounded bg-transparent text-muted-foreground hover:bg-hover hover:text-foreground cursor-pointer [&>svg]:w-3.5"
                      @click="linkClicks = !linkClicks"
                    >
                      <AppIcon name="link-clicks" />
                    </button>
                  </UiTooltip>
                  <span
                    class="pane-tools-gap w-px h-3.5 mx-1 bg-border max-[560px]:hidden"
                    aria-hidden="true"
                  ></span>
                  <UiTooltip
                    :label="t('view.fleet.swapSides')"
                    :shortcut="SHORTCUTS.swap"
                  >
                    <button
                      :aria-label="t('view.fleet.swapPages')"
                      class="grid place-items-center w-6.5 h-6.5 p-0 border-0 rounded bg-transparent text-muted-foreground hover:bg-hover hover:text-foreground cursor-pointer [&>svg]:w-3.5"
                      @click="swapPanes"
                    >
                      <AppIcon name="swap" />
                    </button>
                  </UiTooltip>
                  <UiTooltip
                    :label="t('view.fleet.dockPair')"
                    :shortcut="SHORTCUTS.dockPair"
                  >
                    <button
                      :aria-label="t('view.fleet.dockPairLabel')"
                      class="grid place-items-center w-6.5 h-6.5 p-0 border-0 rounded bg-transparent text-muted-foreground hover:bg-hover hover:text-foreground cursor-pointer [&>svg]:w-3.5"
                      @click="dockPair"
                    >
                      <AppIcon name="dock-pair" />
                    </button>
                  </UiTooltip>
                  <UiTooltip
                    :label="t('view.fleet.minimizeToDock')"
                    :shortcut="SHORTCUTS.toggleSplit"
                  >
                    <button
                      :aria-label="t('view.fleet.minimizeSide')"
                      class="grid place-items-center w-6.5 h-6.5 p-0 border-0 rounded bg-transparent text-muted-foreground hover:bg-hover hover:text-foreground cursor-pointer [&>svg]:w-3.5"
                      @click="minimizePane('side')"
                    >
                      <AppIcon name="to-dock" />
                    </button>
                  </UiTooltip>
                  <UiTooltip
                    :label="t('view.fleet.closeSide')"
                    :shortcut="SHORTCUTS.closeSide"
                  >
                    <button
                      :aria-label="t('view.fleet.closeSideLabel')"
                      class="grid place-items-center w-6.5 h-6.5 p-0 border-0 rounded bg-transparent text-muted-foreground hover:bg-hover hover:text-foreground cursor-pointer [&>svg]:w-3.5"
                      @click="closeSide"
                    >
                      <AppIcon name="close" />
                    </button>
                  </UiTooltip>
                </div>
              </header>
            </PageHost>
          </Transition>
        </template>
      </main>
      <PageDock
        :tabs="tabs"
        :title="tabTitle"
        :can-split="wide"
        @open="openDockTab"
        @split="openDockTabBeside"
        @close="closeTab"
      />
    </UiMotion>
  </div>
</template>

<style scoped>
:deep(.pane.active-pane)::before {
  content: '';
  position: absolute;
  z-index: 7;
  top: var(--topbar-height);
  right: 0;
  left: 0;
  height: 2px;
  background: color-mix(in srgb, var(--accent-foreground) 70%, transparent);
  pointer-events: none;
}

:deep(.split-pane.active-pane)::before {
  display: none;
}

:deep(.split-pane.active-pane .pane-header) {
  border-bottom-color: var(--accent-foreground);
}

.panes.docked :deep(.canvas-view .vue-flow__panel.bottom) {
  bottom: 70px;
}
</style>

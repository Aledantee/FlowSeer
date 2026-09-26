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
import type { Ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useMotionFeedback } from './motion/useMotionFeedback'
import AppIcon from './components/AppIcon.vue'
import AccountMenu from './components/AccountMenu.vue'
import ReportBugButton from './components/ReportBugButton.vue'
import HelpButton from './components/HelpButton.vue'
import ThemeSwitcher from './components/ThemeSwitcher.vue'
import GlobalSearch from './components/GlobalSearch.vue'
import TenantSwitcher from './components/TenantSwitcher.vue'
import ScopeSwitcher from './components/ScopeSwitcher.vue'
import PageHost from './navigation/PageHost.vue'
import AppTooltip from './components/AppTooltip.vue'
import { TooltipProvider } from 'reka-ui'
import { SHORTCUTS, dockTabShortcut, matches } from './navigation/shortcuts'
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

const { play, cancel } = useMotionFeedback()
const sidebar = ref<HTMLElement>()
const navigation = ref<HTMLElement>()
const mainShell = ref<HTMLElement>()
const topbar = ref<HTMLElement>()
const topbarHeight = ref(54)
let topbarObserver: ResizeObserver | undefined
onMounted(() => {
  topbarObserver = new ResizeObserver(() => {
    if (!topbar.value) return
    topbarHeight.value = topbar.value.offsetHeight
    mainShell.value?.style.setProperty(
      '--topbar-height',
      `${topbarHeight.value}px`,
    )
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
  return sites.find((site) => site.id === id)?.name || 'Unknown site'
}
function tenantName(siteId: string) {
  return (
    tenants.find(
      (tenant) =>
        tenant.id === sites.find((site) => site.id === siteId)?.tenantId,
    )?.name || 'Unknown tenant'
  )
}
function reassign(deviceId: string, destination: string) {
  const device = fleet.value.find((item) => item.id === deviceId)
  if (!device) return
  try {
    const updated = moveDevice(device, destination)
    fleet.value = fleet.value.map((item) =>
      item.id === updated.id ? updated : item,
    )
    message.value = `${updated.name} assigned to ${siteName(updated.siteId)}.`
  } catch (error: unknown) {
    message.value =
      error instanceof Error ? error.message : 'Could not assign site.'
  }
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
    message.value = 'Could not open that page. Try again.'
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
    message.value = 'Could not open that page. Try again.'
  }
}
// Links outside the panes, such as the sidebar, belong to the main pane.
provide(pageContext, mainPage)
provide(workspaceContext, {
  fleet,
  message,
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
const VIEW_TITLES: Record<string, string> = {
  dashboard: 'Dashboard',
  devices: 'Devices',
  clients: 'Clients',
  sites: 'Sites',
  topology: 'Topology',
  components: 'Components',
}
const title = computed(() =>
  view.value === 'device'
    ? (selected.value?.name ?? 'Unknown device')
    : (VIEW_TITLES[view.value] ?? 'Dashboard'),
)
const scope = computed(() =>
  filterDevices(fleet.value, query('tenant'), query('site'), '', ''),
)
const scopedSites = computed(() =>
  sites.filter((site) => tenantIds(query('tenant')).includes(site.tenantId)),
)
watch(section, async (page) => {
  const previous = navigation.value
    ?.querySelector('.active')
    ?.getBoundingClientRect()
  await nextTick()
  if (section.value !== page || !previous) return
  const highlight =
    navigation.value?.querySelector<HTMLElement>('.nav-highlight')
  if (!highlight || !highlight.getClientRects().length) return
  const current = highlight.getBoundingClientRect()
  play(
    highlight,
    {
      transform: [
        `translate(${previous.left - current.left}px, ${previous.top - current.top}px)`,
        'none',
      ],
      width: [`${previous.width}px`, `${current.width}px`],
    },
    0.14,
  )
})
watch(
  () => [mainPage.query('tenant'), mainPage.query('site')],
  () =>
    play(
      document.querySelector<HTMLElement>('.main-pane .pane-scroll') ??
        undefined,
      { opacity: [0.85, 1] },
      0.12,
    ),
  { flush: 'post' },
)
async function toggleSidebar() {
  if (!sidebar.value || !mainShell.value) return
  cancel(sidebar.value)
  cancel(mainShell.value)
  const width = getComputedStyle(sidebar.value).width
  const margin = getComputedStyle(mainShell.value).marginLeft
  sidebarCollapsed.value = !sidebarCollapsed.value
  await nextTick()
  if (window.matchMedia('(min-width: 801px)').matches) {
    play(sidebar.value, {
      width: [width, getComputedStyle(sidebar.value).width],
    })
    play(mainShell.value, {
      marginLeft: [margin, getComputedStyle(mainShell.value).marginLeft],
    })
  } else if (!sidebarCollapsed.value) {
    play(
      sidebar.value.querySelector('nav') ?? undefined,
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
        },
      },
      { replace: true },
    )
  } catch {
    message.value = 'Could not update this view. Try again.'
  }
}
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
    .map((device) => ({ value: device.id, label: device.name })),
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
  components: 'components',
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
        ? (device?.name ?? 'Unknown device')
        : (VIEW_TITLES[pageView] ?? 'Dashboard'),
    detail: device
      ? siteName(device.siteId)
      : (site?.name ?? tenant?.name ?? 'All sites'),
    icon: VIEW_ICONS[pageView] ?? 'dashboard',
    health: device?.health,
    attention: attention || undefined,
  }
}
function tabTitle(tab: DockTab) {
  const first = describe(tab.location)
  if (!tab.beside) return first
  const second = describe(tab.beside)
  return {
    label: `${first.label} + ${second.label}`,
    detail: first.detail,
    icon: 'split',
    health:
      [first.health, second.health].find((health) => health === 'Offline') ??
      [first.health, second.health].find((health) => health === 'Degraded') ??
      first.health ??
      second.health,
    attention: (first.attention ?? 0) + (second.attention ?? 0) || undefined,
  }
}
const sideTitle = computed(() =>
  side.value ? describe(side.value) : undefined,
)

const searchPages = computed(() => [
  ...Object.entries(VIEW_TITLES).map(([name, label]) => ({
    id: `view:${name}`,
    title: label,
    detail: 'Page',
    icon: VIEW_ICONS[name] ?? 'dashboard',
  })),
  ...tabs.value.map((tab) => {
    const described = tabTitle(tab)
    return {
      id: `tab:${tab.id}`,
      title: described.label,
      detail: `${tab.beside ? 'Docked pair' : 'Docked'} · ${described.detail}`,
      icon: described.icon,
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
function startResize(event: PointerEvent) {
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
    window.removeEventListener('pointermove', move)
    window.removeEventListener('pointerup', stop)
    document.body.classList.remove('resizing-panes')
  }
  document.body.classList.add('resizing-panes')
  window.addEventListener('pointermove', move)
  window.addEventListener('pointerup', stop)
}

// Workspace shortcuts, from the registry the tooltips read. While a peek is
// open the arrow keys step through the list that opened it and Escape closes
// it.
function typingIn(target: EventTarget | null) {
  return (
    target instanceof HTMLInputElement ||
    target instanceof HTMLTextAreaElement ||
    target instanceof HTMLSelectElement ||
    (target instanceof HTMLElement && target.isContentEditable)
  )
}
function workspaceKey(event: KeyboardEvent) {
  if (document.querySelector('dialog[open]')) return
  const run = (action: () => unknown) => {
    event.preventDefault()
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
  const names = [describe(mainPage.location.value).label]
  if (showSplit.value && side.value) names.push(describe(side.value).label)
  document.title = `${names.join(' + ')} · FlowSeer`
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
  <TooltipProvider :delay-duration="350" :skip-delay-duration="250">
    <div class="shell" :class="{ 'sidebar-collapsed': sidebarCollapsed }">
      <a class="skip-link" href="#main">Skip to main content</a>
      <aside id="workspace-sidebar" ref="sidebar" class="sidebar brand-glow">
        <div
          class="product-brand"
          role="img"
          aria-label="FlowSeer"
          title="FlowSeer"
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
                fill="var(--cyan)"
              />
              <rect
                x="13.5"
                y="0"
                width="5"
                height="32"
                rx="1.5"
                fill="var(--cyan)"
              />
              <rect
                x="21.5"
                y="7.5"
                width="5"
                height="17"
                rx="1.5"
                fill="var(--coral)"
              />
            </g>
          </svg>
          <span>FlowSeer</span>
        </div>
        <div class="nav-label">WORKSPACE</div>
        <nav ref="navigation" aria-label="Main navigation">
          <AppLink
            v-for="item in [
              'dashboard',
              'devices',
              'topology',
              'clients',
              'sites',
              'components',
            ]"
            :key="item"
            :aria-label="item"
            :to="{ path: `/${item}`, query: mainScope }"
            :class="{
              active: section === item,
              'desktop-navigation': item === 'topology',
            }"
            :aria-current="mainView === item ? 'page' : undefined"
            ><span
              v-if="section === item"
              class="nav-highlight"
              aria-hidden="true"
            ></span
            ><AppIcon :name="item" /><span class="nav-text">{{
              item.charAt(0).toUpperCase() + item.slice(1)
            }}</span
            ><span v-if="item === 'devices'" class="nav-count">{{
              fleet.length
            }}</span></AppLink
          >
        </nav>
        <AppTooltip
          :label="sidebarCollapsed ? 'Expand sidebar' : 'Collapse sidebar'"
          side="right"
        >
          <button
            class="sidebar-toggle"
            type="button"
            aria-controls="workspace-sidebar"
            :aria-expanded="!sidebarCollapsed"
            :aria-label="
              sidebarCollapsed ? 'Expand sidebar' : 'Collapse sidebar'
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
          </button>
        </AppTooltip>
      </aside>
      <div ref="mainShell" class="main-shell">
        <span class="main-notch brand-glow" aria-hidden="true"></span>
        <header ref="topbar" class="topbar">
          <span class="topbar-glass brand-glow" aria-hidden="true"></span>
          <div class="topbar-start">
            <nav class="breadcrumb" aria-label="Breadcrumb">
              <template v-if="tenants.length > 1">
                <TenantSwitcher
                  :tenants="tenants"
                  :selected="query('tenant')"
                  @change="setQuery('tenant', $event)"
                />
                <span class="breadcrumb-separator" aria-hidden="true">/</span>
              </template>
              <template v-if="view !== 'components'">
                <div class="breadcrumb-scope">
                  <ScopeSwitcher
                    label="Site scope"
                    placeholder="Search sites…"
                    :selected="query('site')"
                    :options="[
                      { value: '', label: 'All sites' },
                      ...scopedSites.map((site) => ({
                        value: site.id,
                        label: site.name,
                      })),
                    ]"
                    @change="setQuery('site', $event)"
                  />
                </div>
                <span class="breadcrumb-separator" aria-hidden="true">/</span>
              </template>
              <template v-if="view === 'device'">
                <AppLink
                  class="breadcrumb-link"
                  :to="{ path: '/devices', query: mainScope }"
                  >Devices</AppLink
                ><span class="breadcrumb-separator" aria-hidden="true">/</span>
              </template>
              <div
                v-if="view === 'device' && selected"
                class="breadcrumb-scope"
              >
                <ScopeSwitcher
                  label="Device"
                  placeholder="Search devices…"
                  :selected="selected.id"
                  :options="deviceOptions"
                  @change="openDevice($event)"
                />
              </div>
              <strong v-else>{{ title }}</strong>
            </nav>
            <AppTooltip
              label="Minimize to dock"
              hint="The shortcut minimizes whichever pane is focused."
              :shortcut="SHORTCUTS.minimize"
            >
              <button
                class="icon-button minimize-page"
                aria-label="Minimize this page to the dock"
                @click="minimizePane('main')"
              >
                <AppIcon name="to-dock" />
              </button>
            </AppTooltip>
          </div>
          <div class="topbar-tools">
            <GlobalSearch
              :fleet="fleet"
              :pages="searchPages"
              :can-split="wide"
              @select="openResult"
              @dock="dockResult"
            />
            <ThemeSwitcher />
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
          <AppTooltip
            v-if="showSplit"
            label="Drag to resize"
            hint="Double-click to reset to half and half."
            side="right"
          >
            <div
              class="pane-divider"
              role="separator"
              aria-orientation="vertical"
              aria-label="Resize split view"
              :aria-valuenow="Math.round(splitRatio * 100)"
              aria-valuemin="30"
              aria-valuemax="70"
              tabindex="0"
              @pointerdown.self="startResize"
              @dblclick.self="splitRatio = 0.5"
              @keydown.left.prevent="nudgeSplit(-0.05)"
              @keydown.right.prevent="nudgeSplit(0.05)"
            ></div>
          </AppTooltip>
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
                  class="pane-header"
                >
                  <AppIcon :name="sideTitle.icon" />
                  <span class="pane-title"
                    ><strong>{{ sideTitle.label }}</strong></span
                  >
                  <div class="pane-scope">
                    <TenantSwitcher
                      v-if="tenants.length > 1"
                      :tenants="tenants"
                      :selected="sidePage.query('tenant')"
                      @change="setSideScope('tenant', $event)"
                    />
                    <span
                      v-if="tenants.length > 1"
                      class="breadcrumb-separator"
                      aria-hidden="true"
                      >/</span
                    >
                    <ScopeSwitcher
                      label="Side page site"
                      placeholder="Search sites…"
                      :selected="sidePage.query('site')"
                      :options="[
                        { value: '', label: 'All sites' },
                        ...sideScopedSites.map((site) => ({
                          value: site.id,
                          label: site.name,
                        })),
                      ]"
                      @change="setSideScope('site', $event)"
                    />
                  </div>
                  <div
                    class="pane-tools"
                    role="toolbar"
                    aria-label="Split view"
                  >
                    <AppTooltip
                      :label="
                        linkClicks
                          ? 'Links on the left open here'
                          : 'Open links from the left here'
                      "
                      :hint="linkClicks ? 'On' : undefined"
                      :shortcut="SHORTCUTS.linkClicks"
                    >
                      <button
                        :aria-pressed="linkClicks"
                        aria-label="Open links from the main page in the side page"
                        @click="linkClicks = !linkClicks"
                      >
                        <AppIcon name="link-clicks" />
                      </button>
                    </AppTooltip>
                    <span class="pane-tools-gap" aria-hidden="true"></span>
                    <AppTooltip label="Swap sides" :shortcut="SHORTCUTS.swap">
                      <button
                        aria-label="Swap the two pages"
                        @click="swapPanes"
                      >
                        <AppIcon name="swap" />
                      </button>
                    </AppTooltip>
                    <AppTooltip
                      label="Dock both as a pair"
                      :shortcut="SHORTCUTS.dockPair"
                    >
                      <button
                        aria-label="Dock both pages as a pair"
                        @click="dockPair"
                      >
                        <AppIcon name="dock-pair" />
                      </button>
                    </AppTooltip>
                    <AppTooltip
                      label="Minimize to dock"
                      :shortcut="SHORTCUTS.toggleSplit"
                    >
                      <button
                        aria-label="Minimize the side page to the dock"
                        @click="minimizePane('side')"
                      >
                        <AppIcon name="to-dock" />
                      </button>
                    </AppTooltip>
                    <AppTooltip
                      label="Close side page"
                      :shortcut="SHORTCUTS.closeSide"
                    >
                      <button
                        aria-label="Close the side page"
                        @click="closeSide"
                      >
                        <AppIcon name="close" />
                      </button>
                    </AppTooltip>
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
      </div>
    </div>
  </TooltipProvider>
</template>

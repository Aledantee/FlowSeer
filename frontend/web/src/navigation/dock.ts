import type { PageLocation } from './page'

// Minimized pages, each alone or as the pair that was side by side. Kept per
// browser and shared by its windows, like the rest of the workspace chrome.
export interface DockTab {
  id: string
  location: PageLocation
  beside?: PageLocation
}
// What the panes show: the main page and, when split, the side page.
export interface Panes {
  main: PageLocation
  side?: PageLocation
}
export const DOCK_KEY = 'flowseer.dock'
const LIMIT = 12

function isTab(value: unknown): value is DockTab {
  if (typeof value !== 'object' || value === null) return false
  const tab = value as Record<string, unknown>
  return (
    typeof tab.id === 'string' &&
    isLocation(tab.location) &&
    (tab.beside === undefined || isLocation(tab.beside))
  )
}
export function isLocation(value: unknown): value is PageLocation {
  if (typeof value !== 'object' || value === null) return false
  const location = value as Record<string, unknown>
  return (
    typeof location.path === 'string' &&
    typeof location.query === 'object' &&
    location.query !== null &&
    Object.values(location.query).every((item) => typeof item === 'string')
  )
}
export function loadDock(
  storage: Storage | undefined = globalThis.localStorage,
): DockTab[] {
  try {
    const parsed: unknown = JSON.parse(storage?.getItem(DOCK_KEY) ?? '[]')
    return Array.isArray(parsed) ? parsed.filter(isTab).slice(0, LIMIT) : []
  } catch {
    return []
  }
}
export function saveDock(
  tabs: DockTab[],
  storage: Storage | undefined = globalThis.localStorage,
) {
  try {
    storage?.setItem(DOCK_KEY, JSON.stringify(tabs.slice(0, LIMIT)))
  } catch {
    // A blocked store only loses the dock on reload.
  }
}
let counter = 0
function newId(): string {
  counter += 1
  return `tab-${Date.now().toString(36)}-${counter}`
}
const keyOf = (tab: Omit<DockTab, 'id'>) =>
  JSON.stringify([tab.location, tab.beside ?? null])
// Docking a page, or a pair, that is already docked moves that tab to the
// end instead of adding a twin.
export function minimize(
  tabs: DockTab[],
  location: PageLocation,
  beside?: PageLocation,
): DockTab[] {
  const entry = beside ? { location, beside } : { location }
  const kept = tabs.filter((tab) => keyOf(tab) !== keyOf(entry))
  return [...kept, { id: newId(), ...entry }].slice(-LIMIT)
}
// Opening a tab puts what it replaces into that tab's slot, so two views
// can be flipped between with one click each way. A pair replaces both
// panes; a single page replaces only the main pane.
export function openTab(
  tabs: DockTab[],
  id: string,
  current: Panes,
): { tabs: DockTab[]; panes?: Panes } {
  const tab = tabs.find((item) => item.id === id)
  if (!tab) return { tabs }
  const slot: DockTab = tab.beside
    ? current.side
      ? { id, location: current.main, beside: current.side }
      : { id, location: current.main }
    : { id, location: current.main }
  return {
    tabs: tabs.map((item) => (item.id === id ? slot : item)),
    panes: tab.beside
      ? { main: tab.location, side: tab.beside }
      : { main: tab.location, side: current.side },
  }
}
// Moving a single tab beside the main page; the page it displaces from the
// side pane takes its slot in the dock.
export function openBeside(
  tabs: DockTab[],
  id: string,
  side: PageLocation | undefined,
): { tabs: DockTab[]; side?: PageLocation } {
  const tab = tabs.find((item) => item.id === id)
  if (!tab || tab.beside) return { tabs }
  return {
    tabs: side
      ? tabs.map((item) => (item.id === id ? { id, location: side } : item))
      : tabs.filter((item) => item.id !== id),
    side: tab.location,
  }
}

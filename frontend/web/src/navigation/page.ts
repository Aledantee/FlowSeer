import { computed, inject } from 'vue'
import type { ComputedRef, InjectionKey } from 'vue'
import type { RouteLocationNormalizedLoaded, Router } from 'vue-router'

// Where a page is: its path and its string query. The main pane's location
// is the browser route; a split pane keeps its own, so every page component
// reads and changes its location through a PageContext, never the router.
export interface PageLocation {
  path: string
  query: Record<string, string>
}
export interface PageTarget {
  path?: string
  query?: Record<string, string | undefined>
}
export type PageView =
  'dashboard' | 'devices' | 'clients' | 'sites' | 'topology' | 'device'
export interface PageContext {
  location: ComputedRef<PageLocation>
  view: ComputedRef<PageView>
  deviceId: ComputedRef<string | undefined>
  // Whether this page is the one the browser route shows; a pane's page
  // changes role when the panes swap.
  readonly primary: boolean
  query: (key: string) => string
  go: (target: PageTarget, options?: { replace?: boolean }) => Promise<void>
  href: (target: PageTarget) => string
}

const VIEWS: PageView[] = [
  'dashboard',
  'devices',
  'clients',
  'sites',
  'topology',
]
export function viewOf(path: string): { view: PageView; deviceId?: string } {
  const device = /^\/devices\/([^/]+)$/.exec(path)
  if (device?.[1]) {
    try {
      return { view: 'device', deviceId: decodeURIComponent(device[1]) }
    } catch (error: unknown) {
      if (!(error instanceof URIError)) throw error
      return { view: 'device', deviceId: device[1] }
    }
  }
  const name = path.replace(/^\//, '')
  return { view: VIEWS.find((view) => view === name) ?? 'dashboard' }
}

// A target keeps the current path when it names none and drops query keys
// set to undefined or empty.
export function resolveTarget(
  from: PageLocation,
  target: PageTarget,
): PageLocation {
  const query: Record<string, string> = {}
  for (const [key, value] of Object.entries(target.query ?? from.query))
    if (value) query[key] = value
  return { path: target.path ?? from.path, query }
}
export function hrefOf(location: PageLocation): string {
  const search = new URLSearchParams(location.query).toString()
  return search ? `${location.path}?${search}` : location.path
}
export function sameLocation(a: PageLocation, b: PageLocation): boolean {
  return hrefOf(a) === hrefOf(b)
}

export function pageFor(
  location: ComputedRef<PageLocation>,
  primary: () => boolean,
  navigate: (location: PageLocation, replace: boolean) => Promise<void>,
): PageContext {
  const parsed = computed(() => viewOf(location.value.path))
  return {
    location,
    view: computed(() => parsed.value.view),
    deviceId: computed(() => parsed.value.deviceId),
    get primary() {
      return primary()
    },
    query: (key) => location.value.query[key] ?? '',
    go: (target, options) =>
      navigate(
        resolveTarget(location.value, target),
        options?.replace ?? false,
      ),
    href: (target) => hrefOf(resolveTarget(location.value, target)),
  }
}

export function routeLocation(
  route: RouteLocationNormalizedLoaded,
): PageLocation {
  const query: Record<string, string> = {}
  for (const [key, value] of Object.entries(route.query))
    if (typeof value === 'string' && value) query[key] = value
  return { path: route.path, query }
}
// The page the browser route shows, whichever pane displays it.
export function routePage(
  route: RouteLocationNormalizedLoaded,
  router: Router,
): PageContext {
  return pageFor(
    computed(() => routeLocation(route)),
    () => true,
    async (next, replace) => {
      await (replace ? router.replace(next) : router.push(next))
    },
  )
}

export const pageContext: InjectionKey<PageContext> = Symbol('pageContext')
export function usePage(): PageContext {
  const page = inject(pageContext)
  if (!page) throw new Error('Pages must render inside a PageHost.')
  return page
}

// The part of a location that follows the user between pages: tenant and
// site. Filters, searches, and focus belong to the page they were set on.
export function scopeOf(location: PageLocation): Record<string, string> {
  const scope: Record<string, string> = {}
  for (const key of ['tenant', 'site'])
    if (location.query[key]) scope[key] = location.query[key]
  return scope
}

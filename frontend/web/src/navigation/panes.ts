import { computed, ref, watch } from 'vue'
import type { ComputedRef, Ref, WritableComputedRef } from 'vue'
import type { RouteLocationNormalizedLoaded, Router } from 'vue-router'
import { pageFor, routeLocation } from './page'
import type { PageContext, PageLocation } from './page'

// The two panes are fixed slots, each with its own page and component
// state. One slot is main and mirrors the browser route; the other, when
// open, is the side page. Swapping only changes which slot is main, so a
// page keeps its scroll, selection, and zoom as it moves, and swapping twice
// restores both sides exactly.
export type SlotId = 'a' | 'b'
export const otherSlot = (slot: SlotId): SlotId => (slot === 'a' ? 'b' : 'a')

export interface Panes {
  mainSlot: Ref<SlotId>
  slots: Record<SlotId, Ref<PageLocation | undefined>>
  pages: Record<SlotId, PageContext>
  sidePage: ComputedRef<PageContext>
  // The side slot's location; undefined closes the side pane.
  side: WritableComputedRef<PageLocation | undefined>
  navigateMain: (location: PageLocation, replace?: boolean) => Promise<void>
  swap: () => Promise<void>
  // The side page becomes the main page and the side pane closes.
  promoteSide: () => Promise<void>
}

export function usePanes(
  route: RouteLocationNormalizedLoaded,
  router: Router,
  saved: { mainSlot?: SlotId; side?: PageLocation } = {},
): Panes {
  const mainSlot = ref<SlotId>(saved.mainSlot ?? 'a')
  const slots: Record<SlotId, Ref<PageLocation | undefined>> = {
    a: ref<PageLocation>(),
    b: ref<PageLocation>(),
  }
  slots[mainSlot.value].value = routeLocation(route)
  slots[otherSlot(mainSlot.value)].value = saved.side
  // Back, forward, and the sidebar move the route; the main slot follows.
  watch(
    () => route.fullPath,
    () => (slots[mainSlot.value].value = routeLocation(route)),
  )
  async function navigateMain(location: PageLocation, replace = false) {
    await (replace ? router.replace(location) : router.push(location))
  }
  function pageOf(slot: SlotId): PageContext {
    return pageFor(
      computed(() => slots[slot].value ?? { path: '/dashboard', query: {} }),
      () => mainSlot.value === slot,
      async (next, replace) => {
        if (mainSlot.value === slot) await navigateMain(next, replace)
        else slots[slot].value = next
      },
    )
  }
  const pages = { a: pageOf('a'), b: pageOf('b') }
  const side = computed<PageLocation | undefined>({
    get: () => slots[otherSlot(mainSlot.value)].value,
    set: (location) => (slots[otherSlot(mainSlot.value)].value = location),
  })
  async function swap() {
    const next = side.value
    if (!next) return
    mainSlot.value = otherSlot(mainSlot.value)
    await navigateMain(next)
  }
  async function promoteSide() {
    const next = side.value
    if (!next) return
    const previous = mainSlot.value
    mainSlot.value = otherSlot(previous)
    slots[previous].value = undefined
    await navigateMain(next)
  }
  return {
    mainSlot,
    slots,
    pages,
    sidePage: computed(() => pages[otherSlot(mainSlot.value)]),
    side,
    navigateMain,
    swap,
    promoteSide,
  }
}

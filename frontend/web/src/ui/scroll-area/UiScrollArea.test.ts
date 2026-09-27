// @vitest-environment happy-dom
import { afterEach, describe, expect, it } from 'vitest'
import { createApp, h, nextTick, ref } from 'vue'
import UiScrollArea from './UiScrollArea.vue'

let dispose = () => {}
afterEach(() => {
  dispose()
  document.body.replaceChildren()
})

function mountApp(renderFn: () => unknown) {
  const host = document.createElement('div')
  document.body.append(host)
  const app = createApp({
    render: renderFn,
  })
  app.mount(host)
  dispose = () => app.unmount()
  return host
}

describe('UiScrollArea', () => {
  it('renders content inside scroll viewport', () => {
    const host = mountApp(() =>
      h(
        UiScrollArea,
        {},
        {
          default: () => h('div', { id: 'scroll-content' }, 'Scrollable Text'),
        },
      ),
    )

    const content = host.querySelector('#scroll-content')
    expect(content).not.toBeNull()
    expect(content?.textContent).toBe('Scrollable Text')
  })

  it('preserves the stylesheet hooks on each scroll-area primitive', async () => {
    const host = mountApp(() =>
      h(
        UiScrollArea,
        {
          axis: 'both',
          type: 'always',
        },
        {
          default: () => h('div', 'Content'),
        },
      ),
    )

    await nextTick()

    expect(host.querySelector('.scroll-area')).not.toBeNull()
    expect(host.querySelector('.scroll-viewport')).not.toBeNull()
    expect(host.querySelectorAll('.scroll-bar')).toHaveLength(2)
    expect(host.querySelectorAll('.scroll-thumb')).toHaveLength(2)
  })

  it('exposes element reference pointing to the scrollable HTML element', async () => {
    const scrollAreaRef = ref<InstanceType<typeof UiScrollArea> | null>(null)
    mountApp(() =>
      h(
        UiScrollArea,
        {
          ref: scrollAreaRef,
        },
        {
          default: () => h('div', 'Content'),
        },
      ),
    )

    await nextTick()
    expect(scrollAreaRef.value).not.toBeNull()
    expect(scrollAreaRef.value?.element).toBeDefined()
    expect(scrollAreaRef.value?.element instanceof HTMLElement).toBe(true)
  })

  it('vertical scrollbar renders for axis="y" or axis="both"', async () => {
    const host = mountApp(() =>
      h(
        UiScrollArea,
        {
          axis: 'y',
          type: 'always',
        },
        {
          default: () => h('div', 'Tall content for scrollbar check'),
        },
      ),
    )

    await nextTick()
    await new Promise((r) => setTimeout(r, 20))

    const scrollbars = host.querySelectorAll('[data-orientation="vertical"]')
    expect(scrollbars.length).toBeGreaterThan(0)
  })

  it('retains root scroll-area and viewport scroll-viewport classes for legacy layout rules', () => {
    const host = mountApp(() =>
      h(
        UiScrollArea,
        {},
        {
          default: () => h('div', 'Content'),
        },
      ),
    )

    const root = host.querySelector('.scroll-area') as HTMLElement
    expect(root).not.toBeNull()
    expect(root.classList.contains('scroll-area')).toBe(true)
    expect(root.classList.contains('relative')).toBe(true)
    expect(root.classList.contains('overflow-hidden')).toBe(true)

    const viewport = host.querySelector('.scroll-viewport') as HTMLElement
    expect(viewport).not.toBeNull()
    expect(viewport.classList.contains('scroll-viewport')).toBe(true)
  })

  it('matches .pane > .scroll-area and .topology-inspector > .scroll-area selectors', () => {
    const host = mountApp(() =>
      h('div', [
        h('div', { class: 'pane' }, [
          h(
            UiScrollArea,
            { class: 'h-full', viewportClass: 'pane-scroll' },
            {
              default: () => h('div', 'Page Content'),
            },
          ),
        ]),
        h('aside', { class: 'topology-inspector' }, [
          h(
            UiScrollArea,
            { class: 'flex-1 min-h-0', viewportClass: 'inspector-viewport' },
            {
              default: () => h('div', 'Inspector Content'),
            },
          ),
        ]),
      ]),
    )

    const paneScroll = host.querySelector('.pane > .scroll-area') as HTMLElement
    expect(paneScroll).not.toBeNull()
    expect(paneScroll.classList.contains('h-full')).toBe(true)

    const inspectorScroll = host.querySelector(
      '.topology-inspector > .scroll-area',
    ) as HTMLElement
    expect(inspectorScroll).not.toBeNull()
    expect(inspectorScroll.classList.contains('flex-1')).toBe(true)
    expect(inspectorScroll.classList.contains('min-h-0')).toBe(true)
  })

  it('does not force full height on standalone, table, or dock callers', () => {
    const host = mountApp(() =>
      h('div', [
        h(UiScrollArea, {}, { default: () => h('div', 'Default') }),
        h('section', { class: 'dash-panel dash-sites' }, [
          h(
            UiScrollArea,
            { axis: 'x', viewportClass: 'table-scroll' },
            { default: () => h('table') },
          ),
        ]),
        h('nav', { class: 'page-dock' }, [
          h(
            UiScrollArea,
            { axis: 'x', class: 'dock-scroll' },
            { default: () => h('ul') },
          ),
        ]),
      ]),
    )

    const standalone = host.querySelector('.scroll-area') as HTMLElement
    expect(standalone.classList.contains('h-full')).toBe(false)

    const tableRoot = host.querySelector(
      '.dash-panel .scroll-area',
    ) as HTMLElement
    expect(tableRoot.classList.contains('h-full')).toBe(false)
    expect(tableRoot.matches('.pane > .scroll-area')).toBe(false)

    const dockRoot = host.querySelector(
      'nav.page-dock .scroll-area',
    ) as HTMLElement
    expect(dockRoot.classList.contains('h-full')).toBe(false)
    expect(dockRoot.classList.contains('dock-scroll')).toBe(true)
    const dockViewport = dockRoot.querySelector(
      '.scroll-viewport',
    ) as HTMLElement
    expect(dockViewport.matches('.dock-scroll .scroll-viewport')).toBe(true)
  })
})

// @vitest-environment happy-dom
import { afterEach, describe, expect, it, vi } from 'vitest'
import { createApp, h, computed } from 'vue'
import UiAiEntityChip, { type UiAiEntityChipProps } from './UiAiEntityChip.vue'
import {
  pageContext,
  type PageContext,
  type PageView,
} from '../../navigation/page'
import { createWebI18n } from '../../i18n'

let disposers: (() => void)[] = []
afterEach(() => {
  for (const dispose of disposers) dispose()
  disposers = []
  document.body.replaceChildren()
})

function mountChip(
  props: UiAiEntityChipProps,
  options?: {
    page?: Partial<PageContext>
    onRemove?: () => void
  },
) {
  const host = document.createElement('div')
  document.body.append(host)
  const i18n = createWebI18n('en')

  const app = createApp({
    render: () =>
      h(UiAiEntityChip, {
        ...props,
        onRemove: options?.onRemove,
      }),
  })

  app.use(i18n)

  if (options?.page) {
    const mockPage: PageContext = {
      location: computed(() => ({ path: '/dashboard', query: {} })),
      view: computed(() => 'dashboard' as PageView),
      deviceId: computed(() => undefined),
      primary: true,
      query: () => '',
      go: options.page.go ?? vi.fn(),
      href: options.page.href ?? vi.fn((t) => t.path ?? ''),
      ...options.page,
    }
    app.provide(pageContext, mockPage)
  }

  app.mount(host)
  disposers.push(() => app.unmount())
  return { host }
}

describe('UiAiEntityChip', () => {
  it('navigates to /devices/<id> for a device entity when PageContext is provided', () => {
    const go = vi.fn().mockResolvedValue(undefined)
    const { host } = mountChip(
      {
        entity: { kind: 'device', id: 'd1', label: 'core-sw-1' },
      },
      { page: { go } },
    )

    const btn = host.querySelector('button')
    expect(btn).not.toBeNull()
    expect(btn?.textContent).toContain('core-sw-1')

    btn?.click()
    expect(go).toHaveBeenCalledWith({ path: '/devices/d1' })
  })

  it('navigates to /dashboard?site=<id> for a site entity when PageContext is provided', () => {
    const go = vi.fn().mockResolvedValue(undefined)
    const { host } = mountChip(
      {
        entity: { kind: 'site', id: 'berlin-1', label: 'Berlin HQ' },
      },
      { page: { go } },
    )

    const btn = host.querySelector('button')
    expect(btn).not.toBeNull()
    expect(btn?.textContent).toContain('Berlin HQ')

    btn?.click()
    expect(go).toHaveBeenCalledWith({
      path: '/dashboard',
      query: { site: 'berlin-1' },
    })
  })

  it('renders as plain text when entity kind is neither device nor site', () => {
    const go = vi.fn().mockResolvedValue(undefined)
    const { host } = mountChip(
      {
        entity: { kind: 'chart', id: 'c1', label: 'Traffic overview' },
      },
      { page: { go } },
    )

    const btn = host.querySelector('button')
    expect(btn).toBeNull()

    const span = host.querySelector('span[data-ai-entity-chip]')
    expect(span).not.toBeNull()
    expect(span?.textContent).toContain('Traffic overview')
  })

  it('renders as plain text when PageContext is absent', () => {
    const { host } = mountChip({
      entity: { kind: 'device', id: 'd1', label: 'core-sw-1' },
    })

    const btn = host.querySelector('button')
    expect(btn).toBeNull()

    const span = host.querySelector('span[data-ai-entity-chip]')
    expect(span).not.toBeNull()
    expect(span?.textContent).toContain('core-sw-1')
  })

  it('falls back to entity id when label is omitted', () => {
    const { host } = mountChip({
      entity: { kind: 'device', id: 'switch-core-99' },
    })

    expect(host.textContent).toContain('switch-core-99')
  })

  it('emits remove when remove button is clicked', () => {
    const onRemove = vi.fn()
    const { host } = mountChip(
      {
        entity: { kind: 'device', id: 'd1', label: 'core-sw-1' },
        removable: true,
      },
      {
        page: { go: vi.fn() },
        onRemove,
      },
    )

    const removeBtn = host.querySelector(
      '[aria-label="Remove"]',
    ) as HTMLElement | null
    expect(removeBtn).not.toBeNull()
    removeBtn?.click()
    expect(onRemove).toHaveBeenCalledTimes(1)
  })
})

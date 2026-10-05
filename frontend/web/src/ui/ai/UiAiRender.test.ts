// @vitest-environment happy-dom
import { afterEach, describe, expect, it, vi } from 'vitest'
import { computed, createApp, h, isRef, nextTick, ref } from 'vue'
import type { Ref } from 'vue'
import { createWebI18n, type WebLocale } from '../../i18n'
import { AI_UI_CATALOG, type AiUiNode, type AiUiNavigateIntent } from '../../ai'
import {
  pageContext,
  type PageContext,
  type PageLocation,
  type PageView,
} from '../../navigation/page'
import UiAiRender, { AI_UI_COMPONENTS } from './UiAiRender.vue'

let disposers: (() => void)[] = []

async function settle() {
  await nextTick()
  await new Promise((resolve) => setTimeout(resolve, 0))
}

afterEach(() => {
  for (const dispose of disposers) dispose()
  disposers = []
  document.body.replaceChildren()
})

function mountRender(
  tree: unknown | Ref<unknown>,
  options: {
    locale?: WebLocale
    errorLabel?: string
    page?: PageContext
  } = {},
) {
  const host = document.createElement('div')
  document.body.append(host)
  const app = createApp({
    render: () =>
      h(UiAiRender, {
        tree: isRef(tree) ? tree.value : tree,
        errorLabel: options.errorLabel,
      }),
  })
  app.use(createWebI18n(options.locale ?? 'en'))
  if (options.page) app.provide(pageContext, options.page)
  app.mount(host)
  disposers.push(() => app.unmount())
  return { host, app }
}

function pageAt(
  query: Record<string, string>,
  go = vi.fn().mockResolvedValue(undefined),
) {
  const location = computed<PageLocation>(() => ({
    path: '/devices',
    query,
  }))
  const page: PageContext = {
    location,
    view: computed(() => 'devices' as PageView),
    deviceId: computed(() => undefined),
    primary: true,
    query: (key) => location.value.query[key] ?? '',
    go,
    href: vi.fn((target) => target.path ?? location.value.path),
  }
  return { page, go }
}

const nestedTree: AiUiNode[] = [
  {
    component: 'UiCard',
    props: {},
    children: [
      {
        component: 'UiStatusBadge',
        props: { status: 'Offline' },
      },
    ],
  },
]

const navigateIntent: AiUiNavigateIntent = {
  type: 'navigate',
  target: { path: '/devices/core-sw-1' },
}

function catalogRoots(host: HTMLElement): Element[] {
  return [
    ...host.querySelectorAll(
      '.bg-card.border.rounded-panel, [role="meter"], [role="progressbar"], h3, button, [data-ai-entity-chip], [data-orientation]',
    ),
  ]
}

describe('UiAiRender', () => {
  it('renders a validated component tree and nested card children', async () => {
    const { host } = mountRender(nestedTree)
    await settle()

    expect(host.querySelector('[data-ai-render]')).not.toBeNull()
    expect(host.querySelector('.bg-card')).not.toBeNull()
    expect(host.querySelector('[data-ai-render]')?.className).toContain(
      'flex-col',
    )
    expect(host.textContent).toContain('Offline')
  })

  it('renders text as text content rather than markup', () => {
    const { host } = mountRender([
      { component: 'UiBadge', props: { text: '<img src=x>' } },
    ])

    expect(host.textContent).toContain('<img src=x>')
    expect(host.querySelector('img')).toBeNull()
  })

  it.each([
    ['an unknown component', [{ component: 'div', props: {} }]],
    [
      'an unknown prop',
      [{ component: 'UiButton', props: { text: 'Open', onClick: 'bad' } }],
    ],
    [
      'an invalid prop value',
      [{ component: 'UiStatusBadge', props: { status: 'Broken' } }],
    ],
    [
      'a function prop',
      [
        {
          component: 'UiMeter',
          props: { label: 'CPU', value: 1, valueText: () => '' },
        },
      ],
    ],
    [
      'a non-navigate intent',
      [
        {
          component: 'UiButton',
          props: { text: 'Open', intent: { type: 'propose' } },
        },
      ],
    ],
    [
      'an invalid page path',
      [
        {
          component: 'UiButton',
          props: {
            text: 'Open',
            intent: { type: 'navigate', target: { path: '/settings' } },
          },
        },
      ],
    ],
    [
      'a tree over the node bound',
      Array.from({ length: 65 }, () => ({
        component: 'UiSeparator',
        props: {},
      })),
    ],
  ] as const)('%s renders one alert and no catalog root', (_reason, tree) => {
    const { host } = mountRender(tree)

    expect(host.querySelector('[role="alert"]')).not.toBeNull()
    expect(catalogRoots(host)).toHaveLength(0)
  })

  it('rejects a tree nested deeper than four levels', () => {
    let tree: AiUiNode = { component: 'UiBadge', props: { text: 'Leaf' } }
    for (let index = 0; index < 5; index++) {
      tree = {
        component: 'UiCard',
        props: {},
        children: [tree],
      }
    }

    const { host } = mountRender([tree])

    expect(host.querySelector('[role="alert"]')).not.toBeNull()
    expect(catalogRoots(host)).toHaveLength(0)
  })

  it('rejects a string longer than 500 characters', () => {
    const { host } = mountRender([
      { component: 'UiBadge', props: { text: 'x'.repeat(501) } },
    ])

    expect(host.querySelector('[role="alert"]')).not.toBeNull()
    expect(catalogRoots(host)).toHaveLength(0)
  })

  it('uses the caller error label', () => {
    const { host } = mountRender([{ component: 'div', props: {} }], {
      errorLabel: 'This answer section is unavailable.',
    })

    expect(host.querySelector('[role="alert"]')?.textContent).toContain(
      'This answer section is unavailable.',
    )
  })

  it('renders an empty array with no node and no alert', () => {
    const { host } = mountRender([])

    expect(host.querySelector('[data-ai-render]')).not.toBeNull()
    expect(host.querySelector('[role="alert"]')).toBeNull()
    expect(catalogRoots(host)).toHaveLength(0)
  })

  it('renders reactive trees and follows in-place changes', async () => {
    const tree = ref<unknown>([
      { component: 'UiBadge', props: { text: 'First' } },
    ])
    const { host } = mountRender(tree)
    const root = host.querySelector('[data-ai-render]')

    expect(host.textContent).toContain('First')
    ;(tree.value as Array<{ props: { text: string } }>)[0]!.props.text =
      'Changed'
    await settle()

    expect(root?.textContent).toContain('Changed')
    ;(tree.value as unknown[]).push({ component: 'div', props: {} })
    await settle()

    expect(root?.textContent).not.toContain('Changed')
    expect(host.querySelector('[role="alert"]')).not.toBeNull()
    expect(catalogRoots(host)).toHaveLength(0)
  })

  it('validates the provided tree instead of its __v_raw property', () => {
    const tree: unknown[] = [{ component: 'div', props: {} }]
    Object.defineProperty(tree, '__v_raw', {
      value: [{ component: 'UiBadge', props: { text: 'Hidden' } }],
    })

    const { host } = mountRender(tree)

    expect(host.querySelector('[role="alert"]')).not.toBeNull()
    expect(host.textContent).not.toContain('Hidden')
  })

  it('navigates with the page scope and keeps page-local query keys out', async () => {
    const { page, go } = pageAt({ tenant: 'acme', search: 'cologne' })
    const { host } = mountRender(
      [
        {
          component: 'UiButton',
          props: { text: 'Open', intent: navigateIntent },
        },
        {
          component: 'UiButton',
          props: {
            text: 'Filter',
            intent: { type: 'navigate', target: { query: { site: 'berlin' } } },
          },
        },
      ],
      { page },
    )

    await settle()
    const buttons = host.querySelectorAll('button')
    buttons[0]?.dispatchEvent(
      new MouseEvent('click', { bubbles: true, cancelable: true }),
    )

    expect(go).toHaveBeenCalledOnce()
    expect(go).toHaveBeenCalledWith({
      path: '/devices/core-sw-1',
      query: { tenant: 'acme' },
    })

    buttons[1]?.dispatchEvent(
      new MouseEvent('click', { bubbles: true, cancelable: true }),
    )
    expect(go).toHaveBeenLastCalledWith({
      query: { tenant: 'acme', site: 'berlin' },
    })
  })

  it('disables a navigate button when no page is provided', () => {
    const { host } = mountRender([
      {
        component: 'UiButton',
        props: { text: 'Open', intent: navigateIntent },
      },
    ])

    expect(host.querySelector('button')?.disabled).toBe(true)
  })

  it('renders the localized error in German', () => {
    const { host } = mountRender([{ component: 'div', props: {} }], {
      locale: 'de',
    })

    expect(host.querySelector('[role="alert"]')?.textContent).toContain(
      'FlowSeer kann diesen Teil der Antwort nicht anzeigen.',
    )
  })

  it('maps exactly the catalog component names', () => {
    expect(Object.keys(AI_UI_COMPONENTS).sort()).toEqual(
      Object.keys(AI_UI_CATALOG).sort(),
    )
  })
})

import { describe, expect, it } from 'vitest'
import {
  AI_UI_CATALOG,
  AI_UI_ERROR_MESSAGE,
  AI_UI_MAX_DEPTH,
  AI_UI_MAX_NODES,
  AI_UI_MAX_STRING_LENGTH,
  validateAiUiTree,
} from './catalog'
import type { AiUiNavigateIntent, AiUiNode } from './types'

function node(component: string, props: Record<string, unknown>): AiUiNode {
  return { component, props }
}

function rejects(value: unknown) {
  expect(() => validateAiUiTree(value)).toThrow(AI_UI_ERROR_MESSAGE)
}

const validCatalogCases = [
  ['UiCard', { as: 'section' }],
  ['UiBadge', { text: 'Ready', variant: 'success', size: 'sm' }],
  ['UiStatusBadge', { status: 'Offline', label: 'Unavailable', size: 'md' }],
  ['UiMetricCard', { label: 'Clients', value: 12, unit: 'clients' }],
  [
    'UiMeter',
    {
      label: 'Health',
      value: 72,
      min: 0,
      max: 100,
      unit: '%',
      detail: 'Good',
      tone: 'warning',
    },
  ],
  [
    'UiProgress',
    {
      modelValue: 40,
      max: 80,
      size: 'lg',
      variant: 'accent',
      ariaLabel: 'Completion',
      valueText: '50%',
    },
  ],
  ['UiSeparator', { orientation: 'vertical', decorative: true }],
  [
    'UiEmptyState',
    { title: 'No devices', description: 'No devices match this scope.' },
  ],
  [
    'UiAiEntityChip',
    {
      entity: { kind: 'device', id: 'core-sw-1', label: 'Core switch' },
      size: 'sm',
    },
  ],
  [
    'UiButton',
    {
      text: 'Open device',
      intent: {
        type: 'navigate',
        target: { path: '/devices/core-sw-1' },
      },
      variant: 'primary',
      size: 'sm',
    },
  ],
] satisfies readonly [string, Record<string, unknown>][]

describe('validateAiUiTree', () => {
  it.each(validCatalogCases)(
    'accepts %s with its listed props',
    (component, props) => {
      const tree = [node(component, props)]
      expect(validateAiUiTree(tree)).toEqual(tree)
    },
  )

  it('lists the ten catalog components', () => {
    expect(Object.keys(AI_UI_CATALOG)).toEqual([
      'UiCard',
      'UiBadge',
      'UiStatusBadge',
      'UiMetricCard',
      'UiMeter',
      'UiProgress',
      'UiSeparator',
      'UiEmptyState',
      'UiAiEntityChip',
      'UiButton',
    ])
  })

  it.each(['div', 'constructor', '__proto__'])(
    'rejects unknown component %s in the whole tree',
    (component) => {
      rejects([node('UiBadge', { text: 'Ready' }), node(component, {})])
    },
  )

  it.each([
    ['unknown prop', node('UiButton', { text: 'Open', onClick: () => {} })],
    ['invalid status', node('UiStatusBadge', { status: 'Broken' })],
    ['non-finite metric', node('UiMetricCard', { label: 'Value', value: NaN })],
    [
      'function prop',
      node('UiMeter', { label: 'Value', value: 1, valueText: () => '' }),
    ],
    ['AI prop', node('UiBadge', { text: 'Ready', ai: {} })],
  ])('rejects %s', (_name, value) => rejects([value]))

  it('rejects a non-array tree', () => rejects({ component: 'UiBadge' }))

  it('rejects a node that is not a plain object', () => rejects([new Date()]))

  it('rejects a node with an extra key', () =>
    rejects([{ component: 'UiBadge', props: {}, extra: true }]))

  it('rejects a node without props', () => rejects([{ component: 'UiBadge' }]))

  it.each([[], null])('rejects props that are %s', (props) =>
    rejects([{ component: 'UiBadge', props }]),
  )

  it('rejects a missing required prop', () =>
    rejects([node('UiStatusBadge', {})]))

  it('rejects children on a component that does not accept them', () =>
    rejects([{ component: 'UiBadge', props: { text: 'Ready' }, children: [] }]))

  it('rejects non-array children on a card', () =>
    rejects([{ component: 'UiCard', props: {}, children: {} }]))

  it('accepts a query-only navigate target', () => {
    expect(
      validateAiUiTree([
        node('UiButton', {
          text: 'Open site',
          intent: { type: 'navigate', target: { query: { site: 'berlin' } } },
        }),
      ]),
    ).toEqual([
      node('UiButton', {
        text: 'Open site',
        intent: { type: 'navigate', target: { query: { site: 'berlin' } } },
      }),
    ])
  })

  it('copies a query with an own __proto__ key without changing its prototype', () => {
    const query: Record<string, string> = { site: 'berlin' }
    Object.defineProperty(query, '__proto__', {
      value: 'safe',
      enumerable: true,
    })
    const input = [
      node('UiButton', {
        text: 'Open site',
        intent: { type: 'navigate', target: { query } },
      }),
    ]

    const copy = validateAiUiTree(input)
    const intent = copy[0]?.props.intent as {
      target: { query: Record<string, string> }
    }

    expect(intent.target.query).not.toBe(query)
    expect(Object.getPrototypeOf(intent.target.query)).toBe(
      Object.getPrototypeOf(query),
    )
    expect(Object.hasOwn(intent.target.query, '__proto__')).toBe(true)
    expect(intent.target.query.__proto__).toBe('safe')
  })

  it.each([
    [
      'a proposal intent',
      {
        text: 'Review',
        intent: { type: 'propose' },
      },
    ],
    [
      'a proposal intent with a target',
      {
        text: 'Review',
        intent: { type: 'propose', target: { path: '/devices' } },
      },
    ],
    [
      'an external path',
      {
        text: 'Open',
        intent: { type: 'navigate', target: { path: '//example.org' } },
      },
    ],
    [
      'an unknown page',
      {
        text: 'Open',
        intent: { type: 'navigate', target: { path: '/settings' } },
      },
    ],
    [
      'an empty target',
      {
        text: 'Open',
        intent: { type: 'navigate', target: {} },
      },
    ],
    [
      'a non-string query value',
      {
        text: 'Open',
        intent: { type: 'navigate', target: { query: { site: 1 } } },
      },
    ],
    [
      'an intent with an extra key',
      {
        text: 'Open',
        intent: {
          type: 'navigate',
          target: { path: '/devices' },
          extra: true,
        },
      },
    ],
    [
      'a target with an extra key',
      {
        text: 'Open',
        intent: {
          type: 'navigate',
          target: { path: '/devices', extra: true },
        },
      },
    ],
    ['a button without an intent', { text: 'Open' }],
  ])('rejects %s', (_name, props) => rejects([node('UiButton', props)]))

  it('rejects a device chip whose id cannot make a page path', () =>
    rejects([
      node('UiAiEntityChip', {
        entity: { kind: 'device', id: 'a/b', label: 'Device' },
      }),
    ]))

  it('accepts a tree at every bound exactly', () => {
    const nested = (depth: number): AiUiNode =>
      depth === AI_UI_MAX_DEPTH
        ? node('UiBadge', { text: 'x'.repeat(AI_UI_MAX_STRING_LENGTH) })
        : {
            component: 'UiCard',
            props: {},
            children: [nested(depth + 1)],
          }
    const tree = Array.from({ length: AI_UI_MAX_NODES }, () =>
      node('UiBadge', { text: 'x' }),
    )

    expect(validateAiUiTree(tree)).toHaveLength(AI_UI_MAX_NODES)
    expect(validateAiUiTree([nested(1)])).toHaveLength(1)
  })

  it.each([
    [
      'too many nodes',
      Array.from({ length: AI_UI_MAX_NODES + 1 }, () =>
        node('UiBadge', { text: 'x' }),
      ),
    ],
    [
      'too deep',
      (() => {
        const make = (depth: number): AiUiNode =>
          depth > AI_UI_MAX_DEPTH
            ? node('UiBadge', { text: 'x' })
            : {
                component: 'UiCard',
                props: {},
                children: [make(depth + 1)],
              }
        return [make(1)]
      })(),
    ],
    [
      'a string over the bound',
      [node('UiBadge', { text: 'x'.repeat(AI_UI_MAX_STRING_LENGTH + 1) })],
    ],
  ])('rejects %s', (_name, value) => rejects(value))

  it('returns nodes and props that are separate objects', () => {
    const input = [node('UiBadge', { text: 'Ready' })]
    const copy = validateAiUiTree(input)

    expect(copy).not.toBe(input)
    expect(copy[0]).not.toBe(input[0])
    expect(copy[0]?.props).not.toBe(input[0]?.props)
  })

  it('copies an accessor prop before validating it', () => {
    let reads = 0
    const props: Record<string, unknown> = {}
    Object.defineProperty(props, 'text', {
      enumerable: true,
      get: () => (reads++ === 0 ? 'ok' : () => ''),
    })

    const copy = validateAiUiTree([{ component: 'UiBadge', props }])

    expect(copy[0]?.props.text).toBe('ok')
  })

  it('copies an accessor component before validating it', () => {
    let reads = 0
    const input: Record<string, unknown> = { props: { text: 'ok' } }
    Object.defineProperty(input, 'component', {
      enumerable: true,
      get: () => (reads++ === 0 ? 'UiBadge' : 'div'),
    })

    const copy = validateAiUiTree([input])

    expect(copy[0]?.component).toBe('UiBadge')
  })

  it('validates each node of a tree whose array carries its own map', () => {
    const unchecked = [{ component: 'div', props: {} }]
    const tree: unknown[] = [node('UiBadge', { text: 'Ready' })]
    Object.defineProperty(tree, 'map', { value: () => unchecked })

    expect(validateAiUiTree(tree)).toEqual([
      { component: 'UiBadge', props: { text: 'Ready' } },
    ])
  })

  it('validates each child of a card whose children carry their own map', () => {
    const unchecked = [{ component: 'div', props: {} }]
    const children: unknown[] = [node('UiBadge', { text: 'Ready' })]
    Object.defineProperty(children, 'map', { value: () => unchecked })

    const copy = validateAiUiTree([
      { component: 'UiCard', props: {}, children },
    ])

    expect(copy[0]?.children).toEqual([
      { component: 'UiBadge', props: { text: 'Ready' } },
    ])
  })

  it('rejects a tree with an empty slot', () => {
    const tree: unknown[] = []
    tree[1] = node('UiBadge', { text: 'Ready' })

    rejects(tree)
  })

  // Each prop below is read after the value it repairs, so the repaired
  // object would pass validation if the copy step had kept it.
  it('rejects an intent that a later prop read repairs', () => {
    const intent: Record<string, unknown> = {
      type: 'navigate',
      target: { path: '/devices' },
      extra: true,
    }
    const props: Record<string, unknown> = { text: 'Open', intent }
    Object.defineProperty(props, 'size', {
      enumerable: true,
      get: () => {
        delete intent.extra
        return 'sm'
      },
    })

    rejects([{ component: 'UiButton', props }])
  })

  it('rejects a target that a later prop read repairs', () => {
    const target: Record<string, unknown> = { path: '/devices', extra: true }
    const props: Record<string, unknown> = {
      text: 'Open',
      intent: { type: 'navigate', target },
    }
    Object.defineProperty(props, 'size', {
      enumerable: true,
      get: () => {
        delete target.extra
        return 'sm'
      },
    })

    rejects([{ component: 'UiButton', props }])
  })

  it('copies only the own keys of a query that has its own prototype', () => {
    const query = Object.create({ inherited: 'value' }) as Record<
      string,
      unknown
    >
    query.site = 'berlin'

    const copy = validateAiUiTree([
      node('UiButton', {
        text: 'Open',
        intent: { type: 'navigate', target: { query } },
      }),
    ])
    const copied = (copy[0]?.props.intent as AiUiNavigateIntent).target.query

    expect(copied).toEqual({ site: 'berlin' })
    expect(Object.getPrototypeOf(copied)).toBe(Object.prototype)
  })

  it('rejects an entity that a later prop read repairs', () => {
    const entity: Record<string, unknown> = {
      kind: 'site',
      id: 'berlin',
      label: 'Berlin',
      extra: true,
    }
    const props: Record<string, unknown> = { entity }
    Object.defineProperty(props, 'size', {
      enumerable: true,
      get: () => {
        delete entity.extra
        return 'sm'
      },
    })

    rejects([{ component: 'UiAiEntityChip', props }])
  })

  it('rejects a Proxy tree whose length changes between reads', () => {
    let reads = 0
    const length = { valueOf: () => (reads++ === 0 ? 2 : 0) }
    const tree = new Proxy(
      [node('UiBadge', { text: 'Ready' }), node('div', {})],
      {
        get: (target, key, receiver) =>
          key === 'length' ? length : Reflect.get(target, key, receiver),
      },
    )

    rejects(tree)
  })

  it('rejects an empty slot that the array prototype would fill', () => {
    const tree = new Array<unknown>(1)
    Object.setPrototypeOf(tree, { 0: node('UiBadge', { text: 'Ready' }) })

    rejects(tree)
  })

  it('reports an input accessor that throws as a rejected tree', () => {
    const input: Record<string, unknown> = { props: {} }
    Object.defineProperty(input, 'component', {
      enumerable: true,
      get: () => {
        throw new Error('handler failure')
      },
    })

    rejects([input])
  })
})

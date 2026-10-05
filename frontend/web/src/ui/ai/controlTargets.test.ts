// @vitest-environment happy-dom
import { afterEach, describe, expect, it } from 'vitest'
import {
  computed,
  createApp,
  h,
  nextTick,
  reactive,
  ref,
  type Component,
  type VNodeChild,
} from 'vue'
import { ToastProvider, ToastViewport } from 'reka-ui'
import { createAiRegistry } from '../../ai'
import type { AiRegistry, AiTarget } from '../../ai'
import { createWebI18n } from '../../i18n'
import { pageContext } from '../../navigation/page'
import type { PageContext } from '../../navigation/page'
import UiAlertDialog from '../alert-dialog/UiAlertDialog.vue'
import UiBreadcrumbEllipsis from '../breadcrumb/UiBreadcrumbEllipsis.vue'
import UiBreadcrumbLink from '../breadcrumb/UiBreadcrumbLink.vue'
import UiButton from '../button/UiButton.vue'
import UiCombobox from '../combobox/UiCombobox.vue'
import UiCommand from '../command/UiCommand.vue'
import UiCommandDialog from '../command/UiCommandDialog.vue'
import UiCommandEmpty from '../command/UiCommandEmpty.vue'
import UiCommandGroup from '../command/UiCommandGroup.vue'
import UiCommandInput from '../command/UiCommandInput.vue'
import UiCommandItem from '../command/UiCommandItem.vue'
import UiCommandList from '../command/UiCommandList.vue'
import UiCommandShortcut from '../command/UiCommandShortcut.vue'
import UiContextMenu from '../context-menu/UiContextMenu.vue'
import UiContextMenuItem from '../context-menu/UiContextMenuItem.vue'
import UiDialog from '../dialog/UiDialog.vue'
import UiDropdownMenu from '../dropdown-menu/UiDropdownMenu.vue'
import UiDropdownMenuItem from '../dropdown-menu/UiDropdownMenuItem.vue'
import UiCheckbox from '../form/UiCheckbox.vue'
import UiField from '../form/UiField.vue'
import UiInput from '../form/UiInput.vue'
import UiRadioGroup from '../form/UiRadioGroup.vue'
import UiSelect from '../form/UiSelect.vue'
import UiSwitch from '../form/UiSwitch.vue'
import UiTextarea from '../form/UiTextarea.vue'
import UiPagination from '../pagination/UiPagination.vue'
import UiPopover from '../popover/UiPopover.vue'
import UiTabs from '../tabs/UiTabs.vue'
import UiToast from '../toast/UiToast.vue'
import UiTooltip from '../tooltip/UiTooltip.vue'
import { TooltipProvider } from 'reka-ui'
import { aiRegistryKey } from './context'
import type { AiOriginRequest } from './context'
import UiAiAssistant from './UiAiAssistant.vue'
import UiAiEntityChip from './UiAiEntityChip.vue'
import UiAiLabel from './UiAiLabel.vue'
import UiAiResult from './UiAiResult.vue'
import UiAiResultActions from './UiAiResultActions.vue'
import UiAiSummary from './UiAiSummary.vue'

let disposers: (() => void)[] = []
afterEach(() => {
  for (const dispose of disposers) dispose()
  disposers = []
  document.body.replaceChildren()
})

function target(id: string): AiTarget {
  return { id, kind: 'device', label: id, context: {} }
}

function request(requestId: string): AiOriginRequest {
  return { requestId, action: 'summary', targets: [], history: [] }
}

async function settle() {
  await nextTick()
  await new Promise((resolve) => setTimeout(resolve, 30))
  await nextTick()
}

const page: PageContext = {
  location: computed(() => ({ path: '/devices', query: {} })),
  view: computed(() => 'devices' as const),
  deviceId: computed(() => undefined),
  primary: true,
  query: () => '',
  go: () => Promise.resolve(),
  href: (to) => `${to.path ?? '/'}`,
}

// What a caller hands every component under test.
interface Shared {
  ai?: AiTarget
  aiOrigin?: AiOriginRequest
  onAiOriginAcknowledged: (requestId: string) => void
}

interface State {
  ai?: AiTarget
  origin?: AiOriginRequest
  open: boolean
}

interface Mounted {
  host: HTMLElement
  registry: AiRegistry
  state: State
  acks: string[]
  unmount: () => void
}

function mount(
  render: (shared: Shared, open: boolean) => VNodeChild,
  initial: Partial<State> = {},
): Mounted {
  const registry = createAiRegistry()
  const host = document.createElement('div')
  document.body.append(host)
  const state = reactive<State>({ open: true, ...initial })
  const acks: string[] = []
  const app = createApp({
    render: () =>
      render(
        {
          ai: state.ai,
          aiOrigin: state.origin,
          onAiOriginAcknowledged: (requestId) => acks.push(requestId),
        },
        state.open,
      ),
  } satisfies Component)
  app.use(createWebI18n('en'))
  app.provide(aiRegistryKey, registry)
  app.provide(pageContext, page)
  app.mount(host)
  let mounted = true
  const unmount = () => {
    if (!mounted) return
    mounted = false
    app.unmount()
  }
  disposers.push(unmount)
  return { host, registry, state, acks, unmount }
}

function ids(registry: AiRegistry): string[] {
  return registry.list().map((entry) => entry.id)
}

function byId(selector: string) {
  return () => document.querySelector(selector)
}

function tooltipContent() {
  return document.querySelector('[role="tooltip"]')?.parentElement ?? null
}

const summaryTarget = target('summary-subject')

interface Case {
  name: string
  render: (shared: Shared, open: boolean) => VNodeChild
  // The element the contract names as this component's anchor.
  anchor: () => Element | null
  tag: string
  // The anchor exists only while the component's popup is open.
  popup?: boolean
}

const options = [
  { value: 'a', label: 'Alpha' },
  { value: 'b', label: 'Beta' },
]

const commandItems = () => [
  h(UiCommandInput, { placeholder: 'Search' }),
  h(UiCommandList, null, () => [
    h(UiCommandItem, { value: 'apple' }, () => 'Apple'),
  ]),
]

const cases: Case[] = [
  {
    name: 'UiButton',
    render: (s) => h(UiButton, s, () => 'Save'),
    anchor: byId('button'),
    tag: 'BUTTON',
  },
  {
    name: 'UiBreadcrumbLink',
    render: (s) => h(UiBreadcrumbLink, { ...s, href: '/home' }, () => 'Home'),
    anchor: byId('a'),
    tag: 'A',
  },
  {
    name: 'UiBreadcrumbLink as child',
    render: (s) =>
      h(UiBreadcrumbLink, { ...s, asChild: true }, () =>
        h('a', { id: 'forwarded', href: '/y' }, 'Child'),
      ),
    anchor: byId('#forwarded'),
    tag: 'A',
  },
  {
    name: 'UiBreadcrumbEllipsis',
    render: (s) => h(UiBreadcrumbEllipsis, { ...s, items: [] }),
    anchor: byId('button[aria-label]'),
    tag: 'BUTTON',
  },
  {
    name: 'UiField',
    render: (s) => h(UiField, { ...s, label: 'Name', 'data-anchor': '' }),
    anchor: byId('[data-anchor]'),
    tag: 'DIV',
  },
  {
    name: 'UiInput',
    render: (s) => h(UiInput, s),
    anchor: byId('input'),
    tag: 'INPUT',
  },
  {
    name: 'UiTextarea',
    render: (s) => h(UiTextarea, s),
    anchor: byId('textarea'),
    tag: 'TEXTAREA',
  },
  {
    name: 'UiCheckbox',
    render: (s) => h(UiCheckbox, s),
    anchor: byId('[role="checkbox"]'),
    tag: 'BUTTON',
  },
  {
    name: 'UiSwitch',
    render: (s) => h(UiSwitch, s),
    anchor: byId('[role="switch"]'),
    tag: 'BUTTON',
  },
  {
    name: 'UiRadioGroup',
    render: (s) => h(UiRadioGroup, { ...s, options }),
    anchor: byId('[role="radiogroup"]'),
    tag: 'DIV',
  },
  {
    name: 'UiSelect',
    render: (s) => h(UiSelect, { ...s, options }),
    anchor: byId('button[role="combobox"]'),
    tag: 'BUTTON',
  },
  {
    name: 'UiCombobox input',
    render: (s) => h(UiCombobox, { ...s, options }),
    anchor: byId('input[role="combobox"]'),
    tag: 'INPUT',
  },
  {
    name: 'UiCombobox trigger',
    render: (s) =>
      h(
        UiCombobox,
        { ...s, options },
        { trigger: () => h('button', { id: 'cb-trigger' }, 'Pick') },
      ),
    anchor: byId('#cb-trigger'),
    tag: 'BUTTON',
  },
  {
    name: 'UiTabs',
    render: (s) =>
      h(UiTabs, {
        ...s,
        'data-anchor': '',
        tabs: [{ value: 'a', label: 'Alpha', content: 'A body' }],
        defaultValue: 'a',
      }),
    anchor: byId('[data-anchor]'),
    tag: 'DIV',
  },
  {
    name: 'UiPagination',
    render: (s) => h(UiPagination, { ...s, total: 30 }),
    anchor: byId('nav'),
    tag: 'NAV',
  },
  {
    name: 'UiCommand',
    render: (s) => h(UiCommand, { ...s, 'data-anchor': '' }, commandItems),
    anchor: byId('[data-anchor]'),
    tag: 'DIV',
  },
  {
    name: 'UiCommandInput',
    render: (s) => h(UiCommand, null, () => h(UiCommandInput, s)),
    anchor: byId('input'),
    tag: 'INPUT',
  },
  {
    name: 'UiCommandItem',
    render: (s) =>
      h(UiCommand, null, () =>
        h(UiCommandList, null, () =>
          h(UiCommandItem, { ...s, value: 'apple' }, () => 'Apple'),
        ),
      ),
    anchor: byId('[role="option"]'),
    tag: 'DIV',
  },
  {
    name: 'UiCommandEmpty',
    render: (s) =>
      h(UiCommand, null, () =>
        h(UiCommandList, null, () =>
          h(UiCommandEmpty, { ...s, 'data-anchor': '' }),
        ),
      ),
    anchor: byId('[data-anchor]'),
    tag: 'DIV',
  },
  {
    name: 'UiCommandGroup',
    render: (s) =>
      h(UiCommand, null, () =>
        h(UiCommandList, null, () =>
          h(UiCommandGroup, { ...s, heading: 'Fruit' }, () =>
            h(UiCommandItem, { value: 'apple' }, () => 'Apple'),
          ),
        ),
      ),
    anchor: byId('[role="group"]'),
    tag: 'DIV',
  },
  {
    name: 'UiCommandShortcut',
    render: (s) => h(UiCommandShortcut, { ...s, id: 'shortcut' }, () => 'K'),
    anchor: byId('#shortcut'),
    tag: 'SPAN',
  },
  {
    name: 'UiDropdownMenuItem',
    render: (s) =>
      h(
        UiDropdownMenu,
        { defaultOpen: true },
        {
          trigger: () => h('button', 'Menu'),
          default: () => h(UiDropdownMenuItem, s, () => 'Rename'),
        },
      ),
    anchor: byId('[role="menuitem"]'),
    tag: 'DIV',
  },
  {
    name: 'UiAlertDialog',
    render: (s, open) =>
      h(UiAlertDialog, { ...s, open, title: 'Delete', description: 'Sure?' }),
    anchor: byId('[role="alertdialog"]'),
    tag: 'DIV',
    popup: true,
  },
  {
    name: 'UiDialog',
    render: (s, open) => h(UiDialog, { ...s, open, title: 'Edit' }),
    anchor: byId('[role="dialog"]'),
    tag: 'DIV',
    popup: true,
  },
  {
    name: 'UiCommandDialog',
    render: (s, open) => h(UiCommandDialog, { ...s, open }, commandItems),
    anchor: byId('[role="dialog"]'),
    tag: 'DIV',
    popup: true,
  },
  {
    name: 'UiDropdownMenu',
    render: (s, open) =>
      h(
        UiDropdownMenu,
        { ...s, open },
        {
          trigger: () => h('button', 'Menu'),
          default: () => h(UiDropdownMenuItem, null, () => 'Rename'),
        },
      ),
    anchor: byId('[role="menu"]'),
    tag: 'DIV',
    popup: true,
  },
  {
    name: 'UiPopover',
    render: (s, open) =>
      h(
        UiPopover,
        { ...s, open },
        {
          trigger: () => h('button', 'Info'),
          default: () => 'Details',
        },
      ),
    anchor: byId('[role="dialog"]'),
    tag: 'DIV',
    popup: true,
  },
  {
    name: 'UiTooltip',
    render: (s, open) =>
      h(TooltipProvider, null, () =>
        h(UiTooltip, { ...s, open, label: 'Hint' }, () =>
          h('button', 'Target'),
        ),
      ),
    anchor: tooltipContent,
    tag: 'DIV',
    popup: true,
  },
  {
    name: 'UiToast',
    render: (s, open) =>
      h(ToastProvider, null, () => [
        h(UiToast, { ...s, open, title: 'Saved' }),
        h(ToastViewport),
      ]),
    anchor: byId('li'),
    tag: 'LI',
    popup: true,
  },
  {
    name: 'UiAiSummary',
    render: (s) => h(UiAiSummary, { ...s, target: summaryTarget }),
    anchor: byId('section[data-ai-summary]'),
    tag: 'SECTION',
  },
  {
    name: 'UiAiLabel',
    render: (s) => h(UiAiLabel, s),
    anchor: byId('[data-ai-label-trigger]'),
    tag: 'BUTTON',
  },
  {
    name: 'UiAiEntityChip button',
    render: (s) =>
      h(UiAiEntityChip, {
        ...s,
        entity: { kind: 'device', id: 'd9', label: 'edge' },
      }),
    anchor: byId('button[data-ai-entity-chip]'),
    tag: 'BUTTON',
  },
  {
    name: 'UiAiEntityChip text',
    render: (s) =>
      h(UiAiEntityChip, {
        ...s,
        entity: { kind: 'chart', id: 'c1', label: 'traffic' },
      }),
    anchor: byId('span[data-ai-entity-chip]'),
    tag: 'SPAN',
  },
  {
    name: 'UiAiAssistant',
    render: (s) => h(UiAiAssistant, { ...s }),
    anchor: byId('section[data-ai-assistant]'),
    tag: 'SECTION',
  },
  {
    name: 'UiAiResult',
    render: (s) => h(UiAiResult, { ...s, state: 'idle' }),
    anchor: byId('[data-ai-result]'),
    tag: 'DIV',
  },
  {
    name: 'UiAiResultActions',
    render: (s) => h(UiAiResultActions, s),
    anchor: byId('[data-ai-result-actions]'),
    tag: 'DIV',
  },
]

describe('anchors of the controls, overlays, and AI surfaces', () => {
  describe.each(cases)('$name', (kase) => {
    it('registers its anchor, follows the prop, and cleans up', async () => {
      const mounted = mount(kase.render, { ai: target('d1') })
      await settle()

      const anchor = kase.anchor()
      expect(anchor?.tagName).toBe(kase.tag)
      expect(ids(mounted.registry)).toEqual(['d1'])
      expect(mounted.registry.view('d1')?.element).toBe(anchor)

      mounted.state.ai = target('d2')
      await settle()
      expect(ids(mounted.registry)).toEqual(['d2'])
      expect(mounted.registry.view('d2')?.element).toBe(kase.anchor())

      mounted.state.ai = undefined
      await settle()
      expect(ids(mounted.registry)).toEqual([])

      mounted.state.ai = target('d3')
      await settle()
      expect(ids(mounted.registry)).toEqual(['d3'])

      mounted.unmount()
      expect(ids(mounted.registry)).toEqual([])
    })

    it('registers nothing without a target', async () => {
      const mounted = mount(kase.render)
      await settle()

      expect(kase.anchor()).not.toBeNull()
      expect(ids(mounted.registry)).toEqual([])
    })

    it('marks its anchor with the origin and acknowledges once per interaction', async () => {
      const mounted = mount(kase.render, { origin: request('r1') })
      await settle()

      const anchor = kase.anchor()
      expect(anchor?.getAttribute('data-ai-origin')).toBe('agent')
      expect(ids(mounted.registry)).toEqual([])

      anchor?.dispatchEvent(new Event('pointerdown', { bubbles: true }))
      anchor?.dispatchEvent(new Event('pointerdown', { bubbles: true }))
      await settle()

      expect(mounted.acks).toEqual(['r1'])
      expect(kase.anchor()?.hasAttribute('data-ai-origin')).toBe(false)
    })
  })

  const popups = cases.filter((kase) => kase.popup)
  describe.each(popups)('$name popup', (kase) => {
    it('registers only while its content is mounted', async () => {
      const mounted = mount(kase.render, {
        ai: target('p1'),
        open: false,
      })
      await settle()
      expect(kase.anchor()).toBeNull()
      expect(ids(mounted.registry)).toEqual([])

      mounted.state.open = true
      await settle()
      expect(ids(mounted.registry)).toEqual(['p1'])
      expect(mounted.registry.view('p1')?.element).toBe(kase.anchor())

      mounted.state.open = false
      await settle()
      expect(kase.anchor()).toBeNull()
      expect(ids(mounted.registry)).toEqual([])
    })
  })
})

function type(input: Element | null, value: string) {
  if (!(
    input instanceof HTMLInputElement || input instanceof HTMLTextAreaElement
  ))
    throw new Error('Missing text control')
  input.value = value
  input.dispatchEvent(new Event('input', { bubbles: true }))
}

function option(label: string): HTMLElement {
  const found = [
    ...document.querySelectorAll<HTMLElement>('[role="option"]'),
  ].find((item) => item.textContent?.includes(label))
  if (!found) throw new Error(`Missing option ${label}`)
  return found
}

describe('registration through the popup of a control', () => {
  it('registers a context menu and its item while the menu is open', async () => {
    const mounted = mount(
      (s) =>
        h(UiContextMenu, s, {
          trigger: () => h('div', { id: 'area' }, 'Area'),
          default: () =>
            h(UiContextMenuItem, { label: 'Copy', ai: target('item') }),
        }),
      { ai: target('menu') },
    )
    await settle()
    expect(ids(mounted.registry)).toEqual([])

    document.querySelector('#area')?.dispatchEvent(
      new MouseEvent('contextmenu', {
        bubbles: true,
        cancelable: true,
        clientX: 5,
        clientY: 5,
      }),
    )
    await settle()

    expect(ids(mounted.registry).sort()).toEqual(['item', 'menu'])
    const menu = document.querySelector('[role="menu"]')
    const item = document.querySelector('[role="menuitem"]')
    expect(mounted.registry.view('menu')?.element).toBe(menu)
    expect(mounted.registry.view('item')?.element).toBe(item)
    expect(item?.tagName).toBe('DIV')

    menu?.dispatchEvent(
      new KeyboardEvent('keydown', {
        key: 'Escape',
        bubbles: true,
        cancelable: true,
      }),
    )
    await settle()
    expect(ids(mounted.registry)).toEqual([])
  })

  it('drops a command item from the registry while the filter hides it', async () => {
    const mounted = mount(() =>
      h(UiCommand, null, () => [
        h(UiCommandInput, { placeholder: 'Search' }),
        h(UiCommandList, null, () => [
          h(
            UiCommandItem,
            { value: 'apple', ai: target('apple') },
            () => 'Apple',
          ),
          h(
            UiCommandItem,
            { value: 'banana', ai: target('banana') },
            () => 'Banana',
          ),
          h(UiCommandEmpty, { ai: target('empty') }, () => 'Nothing'),
        ]),
      ]),
    )
    await settle()
    expect(ids(mounted.registry).sort()).toEqual(['apple', 'banana'])

    type(document.querySelector('input'), 'ban')
    await settle()
    expect(ids(mounted.registry)).toEqual(['banana'])

    type(document.querySelector('input'), 'zzz')
    await settle()
    expect(ids(mounted.registry)).toEqual(['empty'])
    expect(mounted.registry.view('empty')?.element.textContent).toBe('Nothing')

    type(document.querySelector('input'), '')
    await settle()
    expect(ids(mounted.registry).sort()).toEqual(['apple', 'banana'])
  })

  it('keeps a popup registered through its exit and drops it when the exit ends', async () => {
    const style = document.createElement('style')
    style.textContent =
      '[data-state="closed"] { animation-name: leave; animation-duration: 1s; }'
    document.head.append(style)
    disposers.push(() => style.remove())
    const mounted = mount(
      (s, open) => h(UiDialog, { ...s, open, title: 'Edit' }),
      { ai: target('dialog') },
    )
    await settle()
    const dialog = document.querySelector('[role="dialog"]')
    expect(ids(mounted.registry)).toEqual(['dialog'])

    mounted.state.open = false
    await settle()
    expect(document.querySelector('[role="dialog"]')).toBe(dialog)
    expect(ids(mounted.registry)).toEqual(['dialog'])

    const end = new Event('animationend', { bubbles: true })
    Object.defineProperty(end, 'animationName', { value: 'leave' })
    dialog?.dispatchEvent(end)
    await settle()
    expect(document.querySelector('[role="dialog"]')).toBeNull()
    expect(ids(mounted.registry)).toEqual([])
  })

  it('keeps a toast registered until its exit ends, then drops it', async () => {
    const style = document.createElement('style')
    style.textContent =
      '[data-state="open"] { animation-name: fade-in; animation-duration: 1s; } [data-state="closed"] { animation-name: fade-out; animation-duration: 1s; }'
    document.head.append(style)
    disposers.push(() => style.remove())
    const mounted = mount(
      (s, open) =>
        h(ToastProvider, null, () => [
          h(UiToast, { ...s, open, title: 'Saved' }),
          h(ToastViewport),
        ]),
      { ai: target('toast') },
    )
    await settle()
    const toast = document.querySelector('li')
    expect(ids(mounted.registry)).toEqual(['toast'])

    mounted.state.open = false
    await settle()
    expect(document.querySelector('li')).toBe(toast)
    expect(ids(mounted.registry)).toEqual(['toast'])

    const end = new Event('animationend', { bubbles: true })
    Object.defineProperty(end, 'animationName', { value: 'fade-out' })
    toast?.dispatchEvent(end)
    await settle()
    expect(document.querySelector('li')).toBeNull()
    expect(ids(mounted.registry)).toEqual([])
  })
})

describe('origin acknowledgement of a control', () => {
  it('survives selection, hover, and a programmatic value update, then clears on input', async () => {
    const value = ref('core')
    const origin = ref<AiOriginRequest | undefined>(request('r1'))
    const acks: string[] = []
    const mounted = mount(() =>
      h(UiInput, {
        modelValue: value.value,
        ai: target('name'),
        aiOrigin: origin.value,
        onAiOriginAcknowledged: (id: string) => acks.push(id),
      }),
    )
    await settle()
    const input = document.querySelector('input')
    expect(input?.getAttribute('data-ai-origin')).toBe('agent')

    mounted.registry.highlight('name')
    input?.dispatchEvent(new Event('pointerenter'))
    input?.dispatchEvent(new Event('mouseover', { bubbles: true }))
    value.value = 'edge'
    await settle()

    expect(input?.hasAttribute('data-ai-selected')).toBe(true)
    expect(input?.getAttribute('data-ai-origin')).toBe('agent')
    expect(acks).toEqual([])

    type(input, 'edge-2')
    await settle()
    expect(acks).toEqual(['r1'])
    expect(input?.hasAttribute('data-ai-origin')).toBe(false)
    expect(input?.hasAttribute('data-ai-selected')).toBe(true)

    origin.value = request('r1')
    await settle()
    expect(input?.hasAttribute('data-ai-origin')).toBe(false)

    origin.value = request('r2')
    await settle()
    expect(input?.getAttribute('data-ai-origin')).toBe('agent')

    origin.value = undefined
    await settle()
    expect(input?.hasAttribute('data-ai-origin')).toBe(false)
  })

  it('clears the marker and the label of a field value together and keeps them away after a remount', async () => {
    const origin = ref<AiOriginRequest | undefined>(request('r1'))
    const name = ref('core')
    const render = () =>
      h(UiField, { label: 'Name' }, () => [
        h(UiInput, {
          modelValue: name.value,
          'onUpdate:modelValue': (next: string) => (name.value = next),
          ai: target('name'),
          aiOrigin: origin.value,
          onAiOriginAcknowledged: () => (origin.value = undefined),
        }),
        origin.value ? h(UiAiLabel, { request: origin.value }) : null,
      ])
    const first = mount(render)
    await settle()
    first.registry.highlight('name')
    expect(
      document.querySelector('input')?.getAttribute('data-ai-origin'),
    ).toBe('agent')
    expect(document.querySelector('[data-ai-label-trigger]')).not.toBeNull()

    type(document.querySelector('input'), 'edge')
    await settle()

    expect(name.value).toBe('edge')
    expect(
      document.querySelector('input')?.hasAttribute('data-ai-origin'),
    ).toBe(false)
    expect(document.querySelector('[data-ai-label-trigger]')).toBeNull()
    expect(
      document.querySelector('input')?.hasAttribute('data-ai-selected'),
    ).toBe(true)

    first.unmount()
    mount(render)
    await settle()
    expect(
      document.querySelector('input')?.hasAttribute('data-ai-origin'),
    ).toBe(false)
    expect(document.querySelector('[data-ai-label-trigger]')).toBeNull()
  })

  it.each([
    {
      name: 'UiSelect',
      render: (item: Origins) =>
        h(UiSelect, {
          id: item.id,
          options,
          defaultOpen: item.open,
          ai: target(`${item.id}-target`),
          aiOrigin: item.origin,
          onAiOriginAcknowledged: item.acknowledge,
        }),
      pick: async () => {
        const choice = option('Beta')
        choice.dispatchEvent(new Event('pointerdown', { bubbles: true }))
        choice.focus()
        choice.dispatchEvent(
          new KeyboardEvent('keydown', {
            key: 'Enter',
            bubbles: true,
            cancelable: true,
          }),
        )
      },
    },
    {
      name: 'UiCombobox',
      render: (item: Origins) =>
        h(UiCombobox, {
          options,
          defaultOpen: item.open,
          ai: target(`${item.id}-target`),
          aiOrigin: item.origin,
          onAiOriginAcknowledged: item.acknowledge,
        }),
      pick: async () => {
        const choice = option('Beta')
        choice.dispatchEvent(new Event('pointerdown', { bubbles: true }))
        choice.click()
      },
    },
  ])(
    '$name acknowledges its own origin from its portal and leaves a sibling alone',
    async ({ render, pick }) => {
      const states = reactive({
        a: request('ra') as AiOriginRequest | undefined,
        b: request('rb') as AiOriginRequest | undefined,
      })
      const acks: string[] = []
      const mounted = mount(() =>
        h('div', [
          render({
            id: 'a',
            open: true,
            origin: states.a,
            acknowledge: (id) => {
              acks.push(id)
              states.a = undefined
            },
          }),
          render({
            id: 'b',
            open: false,
            origin: states.b,
            acknowledge: (id) => {
              acks.push(id)
              states.b = undefined
            },
          }),
        ]),
      )
      await settle()
      const markers = () =>
        [...document.querySelectorAll('[data-ai-origin]')].length
      expect(markers()).toBe(2)
      expect(ids(mounted.registry).sort()).toEqual(['a-target', 'b-target'])

      await pick()
      await settle()

      expect(acks).toEqual(['ra'])
      expect(states.b?.requestId).toBe('rb')
      expect(markers()).toBe(1)
    },
  )

  it('keeps a combobox trigger value away from a portal interaction of another value once it acknowledged', async () => {
    const acks: string[] = []
    mount(() =>
      h(
        UiCombobox,
        {
          options,
          defaultOpen: true,
          aiOrigin: request('rt'),
          onAiOriginAcknowledged: (id: string) => acks.push(id),
        },
        { trigger: () => h('button', { id: 'cb-trigger' }, 'Pick') },
      ),
    )
    await settle()
    expect(
      document.querySelector('#cb-trigger')?.getAttribute('data-ai-origin'),
    ).toBe('agent')

    type(document.querySelector('input'), 'be')
    await settle()

    expect(acks).toEqual(['rt'])
    expect(
      document.querySelector('#cb-trigger')?.hasAttribute('data-ai-origin'),
    ).toBe(false)
  })
})

interface Origins {
  id: string
  open: boolean
  origin?: AiOriginRequest
  acknowledge: (requestId: string) => void
}

describe('native behavior with a target and an origin', () => {
  it('lets a button click, focus, and block while disabled', async () => {
    const clicks: number[] = []
    const mounted = mount(
      (s) => h(UiButton, { ...s, onClick: () => clicks.push(1) }, () => 'Go'),
      { ai: target('b'), origin: request('r1') },
    )
    await settle()
    const button = document.querySelector('button')
    button?.focus()
    expect(document.activeElement).toBe(button)
    expect(mounted.acks).toEqual([])

    button?.click()
    expect(clicks).toHaveLength(1)

    mounted.state.ai = undefined
    const disabled = mount(
      (s) =>
        h(
          UiButton,
          { ...s, disabled: true, onClick: () => clicks.push(1) },
          () => 'Go',
        ),
      { ai: target('c') },
    )
    await settle()
    document.querySelectorAll('button')[1]?.click()
    expect(clicks).toHaveLength(1)
    expect(ids(disabled.registry)).toEqual(['c'])
  })

  it('lets a text control take focus without acknowledging and emit its value', async () => {
    const values: string[] = []
    const mounted = mount(
      (s) =>
        h(UiTextarea, {
          ...s,
          'onUpdate:modelValue': (next: string) => values.push(next),
        }),
      { ai: target('t'), origin: request('r1') },
    )
    await settle()
    const area = document.querySelector('textarea')
    area?.focus()
    expect(document.activeElement).toBe(area)
    expect(mounted.acks).toEqual([])

    type(area, 'notes')
    expect(values).toEqual(['notes'])
    expect(mounted.acks).toEqual(['r1'])
  })

  it.each([
    {
      name: 'UiCheckbox',
      component: UiCheckbox as Component,
      role: 'checkbox',
    },
    { name: 'UiSwitch', component: UiSwitch as Component, role: 'switch' },
  ])(
    'toggles a $name on click and acknowledges once',
    async ({ component, role }) => {
      const values: unknown[] = []
      const mounted = mount(
        (s) =>
          h(component, {
            ...s,
            modelValue: false,
            'onUpdate:modelValue': (next: unknown) => values.push(next),
          }),
        { ai: target('c'), origin: request('r1') },
      )
      await settle()
      const control = document.querySelector<HTMLElement>(`[role="${role}"]`)
      control?.dispatchEvent(new Event('pointerdown', { bubbles: true }))
      control?.click()
      await settle()

      expect(values).toEqual([true])
      expect(mounted.acks).toEqual(['r1'])
    },
  )

  it('lets a radio group pick and a pagination page', async () => {
    const picks: string[] = []
    const pages: number[] = []
    const radios = mount(
      (s) =>
        h(UiRadioGroup, {
          ...s,
          options,
          'onUpdate:modelValue': (next: string) => picks.push(next),
        }),
      { ai: target('r'), origin: request('r1') },
    )
    await settle()
    const radio = document.querySelectorAll<HTMLElement>('[role="radio"]')[1]
    radio?.dispatchEvent(new Event('pointerdown', { bubbles: true }))
    radio?.click()
    await settle()
    expect(picks).toEqual(['b'])
    expect(radios.acks).toEqual(['r1'])

    mount(
      (s) =>
        h(UiPagination, {
          ...s,
          total: 30,
          'onUpdate:page': (next: number) => pages.push(next),
        }),
      { ai: target('p') },
    )
    await settle()
    document.querySelector<HTMLElement>('[aria-label="Page 2"]')?.click()
    await settle()
    expect(pages).toEqual([2])
  })

  it('lets a tab trigger switch panels', async () => {
    const values: string[] = []
    mount(
      (s) =>
        h(UiTabs, {
          ...s,
          defaultValue: 'a',
          tabs: [
            { value: 'a', label: 'Alpha', content: 'A body' },
            { value: 'b', label: 'Beta', content: 'B body' },
          ],
          'onUpdate:modelValue': (next: string) => values.push(next),
        }),
      { ai: target('tabs') },
    )
    await settle()
    document.querySelectorAll<HTMLElement>('[role="tab"]')[1]?.dispatchEvent(
      new MouseEvent('mousedown', {
        bubbles: true,
        cancelable: true,
        button: 0,
      }),
    )
    await settle()
    expect(values).toEqual(['b'])
  })

  it('lets the chip remove control remove', async () => {
    const removed: number[] = []
    mount(
      (s) =>
        h(UiAiEntityChip, {
          ...s,
          entity: { kind: 'chart', id: 'c1', label: 'traffic' },
          removable: true,
          onRemove: () => removed.push(1),
        }),
      { ai: target('chip') },
    )
    await settle()
    document
      .querySelector<HTMLElement>('span[data-ai-entity-chip] button')
      ?.click()
    expect(removed).toEqual([1])
  })

  it('opens the menu from the ellipsis toggle and selects an item', async () => {
    const selected: number[] = []
    mount(
      (s) =>
        h(
          UiBreadcrumbEllipsis,
          { ...s },
          {
            menu: () =>
              h(
                UiDropdownMenuItem,
                { onSelect: () => selected.push(1) },
                () => 'Parent',
              ),
          },
        ),
      { ai: target('e'), origin: request('r1') },
    )
    await settle()
    const toggle = document.querySelector<HTMLElement>('button[aria-label]')
    toggle?.click()
    await settle()
    const item = document.querySelector<HTMLElement>('[role="menuitem"]')
    expect(item).not.toBeNull()
    item?.click()
    expect(selected).toEqual([1])
  })
})

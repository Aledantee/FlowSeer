// @vitest-environment happy-dom
import { afterEach, describe, expect, it, vi } from 'vitest'
import { createApp, h, nextTick, type App, type Component } from 'vue'
import { composeStories, setProjectAnnotations } from '@storybook/vue3-vite'
import preview from './preview'
import type { AiRequest } from '../src/ai'
import { aiRegistry } from '../src/ai'
import { createWebI18n } from '../src/i18n'

setProjectAnnotations(preview)

// Storybook Docs mounts several canvases into one document. The decorator
// must keep all of them inspectable through one window contract and render a
// single action layer, releasing both only after the last canvas unmounts.
const storyModule = {
  default: { title: 'Lifecycle/DocumentScope' },
  Alpha: {
    parameters: {
      ai: { handler: (request: AiRequest) => `alpha:${request.targetId}` },
    },
    render: () => h('div', 'Alpha body'),
  },
  Beta: {
    parameters: {
      ai: { handler: (request: AiRequest) => `beta:${request.targetId}` },
    },
    render: () => h('div', 'Beta body'),
  },
  WithOverrides: {
    parameters: {
      ai: {
        handler: (request: AiRequest) => `overrides:${request.targetId}`,
        labels: {
          ai: 'Bot',
          askAbout: 'Custom inquire',
          heading: 'Custom heading',
        },
      },
    },
    render: () => h('div', 'Overrides body'),
  },
}

const stories = composeStories(storyModule)

interface Box {
  top: number
  left: number
  width: number
  height: number
}

const box: Box = { top: 100, left: 40, width: 320, height: 80 }

function setBox(element: HTMLElement) {
  element.getBoundingClientRect = () =>
    ({
      x: box.left,
      y: box.top,
      width: box.width,
      height: box.height,
      top: box.top,
      left: box.left,
      right: box.left + box.width,
      bottom: box.top + box.height,
      toJSON: () => ({}),
    }) as DOMRect
}

let mounted: { app: App; container: HTMLElement }[] = []
let manualTargets: HTMLElement[] = []

function mountStory(component: Component) {
  const container = document.createElement('div')
  document.body.append(container)
  const app = createApp(component)
  app.use(createWebI18n())
  app.mount(container)
  const root = container.querySelector<HTMLElement>('[data-ai-story-root]')
  if (root) {
    root.tabIndex = 0
    setBox(root)
  }
  mounted.push({ app, container })
  return { app, container, root }
}

async function settle() {
  await nextTick()
  await new Promise((resolve) => setTimeout(resolve, 0))
  await nextTick()
}

function askButton(): HTMLButtonElement | undefined {
  return document.querySelector<HTMLButtonElement>('.ai-ask') ?? undefined
}

async function ask(prompt: string) {
  askButton()?.click()
  await settle()
  const textarea = document.querySelector('textarea')
  if (!textarea) throw new Error('Missing prompt field')
  textarea.value = prompt
  textarea.dispatchEvent(new Event('input', { bubbles: true }))
  await settle()
  const submit = document.querySelector<HTMLButtonElement>(
    'form button[type="submit"]',
  )
  if (!submit) throw new Error('Missing ask submit')
  submit.click()
  await settle()
}

async function select(id: string) {
  expect(window.flowseerAi?.highlight(id)).toBe(true)
  window.dispatchEvent(new Event('resize'))
  await settle()
}

afterEach(() => {
  for (const target of manualTargets) aiRegistry.unregister(target)
  manualTargets = []
  for (const { app, container } of mounted) {
    app.unmount()
    container.remove()
  }
  mounted = []
  document.body.replaceChildren()
  vi.restoreAllMocks()
})

describe('AI decorator document scope', () => {
  it('routes a nested component target to its canvas handler', async () => {
    const alpha = mountStory(stories.Alpha)
    const root = alpha.container.querySelector<HTMLElement>(
      '[data-ai-story-root]',
    )
    expect(root).not.toBeNull()
    const nested = document.createElement('button')
    root?.append(nested)
    setBox(nested)
    manualTargets.push(nested)
    const id = 'standalone:story:alpha:nested'
    aiRegistry.register(nested, {
      id,
      kind: 'row',
      label: 'Nested target',
      context: { story: 'alpha' },
    })

    await select(id)
    await ask('Why this row?')
    expect(document.body.textContent).toContain(`alpha:${id}`)
  })

  it('keeps both canvases inspectable and releases them after the last unmount', async () => {
    const alpha = mountStory(stories.Alpha)
    const beta = mountStory(stories.Beta)
    await settle()

    const api = window.flowseerAi
    expect(api).toBeDefined()

    const targets = api?.listTargets() ?? []
    expect(targets).toHaveLength(2)
    const alphaId = targets.find((item) => item.id.includes('alpha'))?.id
    const betaId = targets.find((item) => item.id.includes('beta'))?.id
    expect(alphaId).toBeDefined()
    expect(betaId).toBeDefined()

    await select(alphaId ?? '')
    expect(document.querySelectorAll('.ai-ask')).toHaveLength(1)
    await ask('Why this alpha?')
    expect(document.body.textContent).toContain(`alpha:${alphaId}`)

    alpha.app.unmount()
    await settle()

    expect(window.flowseerAi).toBeDefined()
    expect(window.flowseerAi?.listTargets().map((item) => item.id)).toEqual([
      betaId,
    ])

    await select(betaId ?? '')
    expect(document.querySelector('.ai-ask')).not.toBeNull()
    await ask('Why this beta?')
    expect(document.body.textContent).toContain(`beta:${betaId}`)

    beta.app.unmount()
    await settle()

    expect(window.flowseerAi).toBeUndefined()
    expect(document.querySelector('.ai-ask')).toBeNull()
    expect(document.querySelector('.ai-layer')).toBeNull()
  })

  it('keeps a duplicate story id invalid without stacking an action layer', async () => {
    const warn = vi.spyOn(console, 'warn').mockImplementation(() => {})
    mountStory(stories.Alpha)
    mountStory(stories.Alpha)
    await settle()

    expect(window.flowseerAi?.listTargets()).toHaveLength(1)
    await select(window.flowseerAi?.listTargets()[0]?.id ?? '')
    expect(document.querySelectorAll('.ai-ask')).toHaveLength(1)
    expect(warn).toHaveBeenCalled()
  })

  it('renders one layer with story label overrides across host canvas boundary', async () => {
    mountStory(stories.Alpha)
    mountStory(stories.WithOverrides)
    await settle()

    const targets = window.flowseerAi?.listTargets() ?? []
    const overridesId = targets.find((item) =>
      item.id.includes('with-overrides'),
    )?.id
    expect(overridesId).toBeDefined()

    await select(overridesId ?? '')
    const asks = document.querySelectorAll<HTMLButtonElement>('.ai-ask')
    expect(asks).toHaveLength(1)
    expect(asks[0]?.textContent?.trim()).toBe('Bot')
    expect(asks[0]?.getAttribute('aria-label')).toBe('Custom inquire')

    const alphaId = targets.find((item) => item.id.includes('alpha'))?.id
    expect(alphaId).toBeDefined()
    await select(alphaId ?? '')
    expect(document.querySelectorAll('.ai-ask')).toHaveLength(1)
    expect(askButton()?.textContent?.trim()).toBe('AI')
    expect(askButton()?.getAttribute('aria-label')).toBe(
      'Ask about Lifecycle/DocumentScope',
    )
  })

  it('holds the canvas-label invariant across all host, selection, focus, and panel states', async () => {
    const hosts = ['default', 'overrides'] as const
    const selections = ['none', 'default', 'overrides'] as const
    const focuses = ['none', 'default', 'overrides'] as const
    const panels = ['closed', 'open'] as const

    for (const host of hosts) {
      for (const selection of selections) {
        for (const focus of focuses) {
          for (const panel of panels) {
            const stateName = `host=${host}, selection=${selection}, focus=${focus}, panel=${panel}`
            const owner = focus !== 'none' ? focus : selection
            if (owner === 'none') continue

            const firstStory =
              host === 'default' ? stories.Alpha : stories.WithOverrides
            const secondStory =
              host === 'default' ? stories.WithOverrides : stories.Alpha

            const firstMount = mountStory(firstStory)
            const secondMount = mountStory(secondStory)
            const outside = document.createElement('button')
            document.body.append(outside)
            await settle()

            const defaultRoot =
              host === 'default' ? firstMount.root : secondMount.root
            const overridesRoot =
              host === 'default' ? secondMount.root : firstMount.root
            if (!defaultRoot || !overridesRoot) {
              throw new Error('Missing story root')
            }

            const defaultTargetId = aiRegistry.idForElement(defaultRoot)
            const overridesTargetId = aiRegistry.idForElement(overridesRoot)
            if (!defaultTargetId || !overridesTargetId) {
              throw new Error('Missing target id')
            }

            if (selection === 'default') {
              await select(defaultTargetId)
            } else if (selection === 'overrides') {
              await select(overridesTargetId)
            } else {
              window.flowseerAi?.clearHighlight()
              await settle()
            }

            if (focus === 'default') {
              defaultRoot.focus()
              await settle()
            } else if (focus === 'overrides') {
              overridesRoot.focus()
              await settle()
            } else {
              outside.focus()
              await settle()
            }

            const expectedTrigger = owner === 'overrides' ? 'Bot' : 'AI'
            const expectedHeading =
              owner === 'overrides' ? 'Custom heading' : 'Ask about'

            if (panel === 'closed') {
              expect(
                askButton()?.textContent?.trim(),
                `${stateName}: trigger`,
              ).toBe(expectedTrigger)
            } else {
              askButton()?.click()
              await settle()

              const otherTargetId =
                owner === 'default' ? overridesTargetId : defaultTargetId
              await select(otherTargetId)

              expect(
                askButton()?.textContent?.trim(),
                `${stateName}: trigger`,
              ).toBe(expectedTrigger)
              expect(
                document.querySelector('form p')?.textContent,
                `${stateName}: heading`,
              ).toContain(expectedHeading)
            }

            firstMount.app.unmount()
            secondMount.app.unmount()
            mounted = []
            outside.remove()
            document.body.replaceChildren()
            await settle()
          }
        }
      }
    }
  })
})

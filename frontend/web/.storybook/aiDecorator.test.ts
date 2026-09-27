// @vitest-environment happy-dom
import { afterEach, describe, expect, it, vi } from 'vitest'
import { createApp, h, nextTick, type App, type Component } from 'vue'
import { composeStories, setProjectAnnotations } from '@storybook/vue3-vite'
import preview from './preview'
import type { AiRequest } from '../src/ai'
import { aiRegistry } from '../src/ai'

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
  app.mount(container)
  const root = container.querySelector<HTMLElement>('[data-ai-story-root]')
  if (root) setBox(root)
  mounted.push({ app, container })
  return { app, container }
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
})

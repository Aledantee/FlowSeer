// @vitest-environment happy-dom
import { afterEach, describe, expect, it, vi } from 'vitest'
import { createApp, h, nextTick, ref, type App, type Component } from 'vue'
import { composeStories, setProjectAnnotations } from '@storybook/vue3-vite'
import preview from './preview'
import type { AiRequest } from '../src/ai'
import { aiRegistry } from '../src/ai'
import { createWebI18n } from '../src/i18n'

setProjectAnnotations(preview)

// Storybook Docs mounts several canvases into one document. The decorator
// must keep all of them inspectable through one window contract and render
// per-canvas context layers, releasing the document-scoped contracts only
// after the last canvas unmounts.
const storyModule = {
  default: { title: 'Lifecycle/DocumentScope' },
  Alpha: {
    parameters: {
      ai: {
        handler: (request: AiRequest) => ({
          type: 'answer' as const,
          text: `alpha:${request.targets[0]?.id}`,
          refs: [],
        }),
      },
    },
    render: () => h('div', 'Alpha body'),
  },
  Beta: {
    parameters: {
      ai: {
        handler: (request: AiRequest) => ({
          type: 'answer' as const,
          text: `beta:${request.targets[0]?.id}`,
          refs: [],
        }),
      },
    },
    render: () => h('div', 'Beta body'),
  },
  WithNested: {
    parameters: {
      ai: {
        handler: (request: AiRequest) => ({
          type: 'answer' as const,
          text: `nested-handler:${request.targets[0]?.id}`,
          refs: [],
        }),
      },
    },
    render: () => ({
      setup() {
        const nestedRef = ref<HTMLElement>()
        const target = {
          id: 'standalone:story:lifecycle-documentscope--with-nested:nested',
          kind: 'device',
          label: 'Nested device',
          context: { health: 'Offline' },
        }
        return { nestedRef, target }
      },
      template: `
        <div>
          <button
            ref="nestedRef"
            v-ai-target="target"
            tabindex="0"
            data-nested-target
          >
            Nested device
          </button>
        </div>
      `,
    }),
  },
}

const stories = composeStories(storyModule)

let mounted: { app: App; container: HTMLElement }[] = []
let manualTargets: HTMLElement[] = []

function mountStory(component: Component) {
  const container = document.createElement('div')
  document.body.append(container)
  const app = createApp(component)
  app.use(createWebI18n())
  app.mount(container)
  const root = container.querySelector<HTMLElement>('[data-ai-story-root]')
  mounted.push({ app, container })
  return { app, container, root }
}

async function settle() {
  await nextTick()
  await new Promise((resolve) => setTimeout(resolve, 0))
  await nextTick()
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
    const withNested = mountStory(stories.WithNested)
    await settle()

    const nested = withNested.container.querySelector<HTMLElement>(
      '[data-nested-target]',
    )
    expect(nested).not.toBeNull()

    // Trigger contextmenu on nested element
    nested?.dispatchEvent(
      new MouseEvent('contextmenu', {
        bubbles: true,
        cancelable: true,
        clientX: 50,
        clientY: 50,
      }),
    )
    await settle()

    const menu = document.body.querySelector('[role="menu"]')
    expect(menu).not.toBeNull()

    const verb = [...document.body.querySelectorAll('[role="menuitem"]')].find(
      (el) => el.textContent?.includes('Why is this offline?'),
    ) as HTMLElement
    expect(verb).toBeDefined()

    verb.click()
    await settle()
    await new Promise((r) => setTimeout(r, 30))

    const popover = document.body.querySelector('[data-ai-context-popover]')
    expect(popover).not.toBeNull()
    expect(popover?.textContent).toContain(
      'nested-handler:standalone:story:lifecycle-documentscope--with-nested:nested',
    )
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

    alpha.app.unmount()
    await settle()

    expect(window.flowseerAi).toBeDefined()
    expect(window.flowseerAi?.listTargets().map((item) => item.id)).toEqual([
      betaId,
    ])

    beta.app.unmount()
    await settle()

    expect(window.flowseerAi).toBeUndefined()
  })

  it('keeps a duplicate story id invalid', async () => {
    const warn = vi.spyOn(console, 'warn').mockImplementation(() => {})
    mountStory(stories.Alpha)
    mountStory(stories.Alpha)
    await settle()

    expect(window.flowseerAi?.listTargets()).toHaveLength(1)
    expect(warn).toHaveBeenCalled()
  })
})

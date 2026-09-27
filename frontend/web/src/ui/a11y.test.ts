// @vitest-environment happy-dom
import { afterEach, describe, expect, it } from 'vitest'
import { createApp, h, nextTick, type Component } from 'vue'
import axe from 'axe-core'
import { composeStories, setProjectAnnotations } from '@storybook/vue3-vite'
import preview from '../../.storybook/preview'
import UiInput from './form/UiInput.vue'

setProjectAnnotations(preview)

const storyModules = import.meta.glob<Record<string, unknown>>(
  './**/*.stories.ts',
  { eager: true },
)

const componentModules = import.meta.glob('./**/Ui*.vue')

const AXE_OPTIONS: axe.RunOptions = {
  runOnly: {
    type: 'tag',
    values: ['wcag2a', 'wcag2aa', 'wcag21aa'],
  },
  rules: {
    'color-contrast': { enabled: false },
  },
}

async function runAudit(element: Element = document.body) {
  await nextTick()
  await new Promise((resolve) => setTimeout(resolve, 0))
  return axe.run(element, AXE_OPTIONS)
}

describe('accessibility (axe-core)', () => {
  let cleanups: (() => void)[] = []

  afterEach(() => {
    for (const cleanup of cleanups) {
      cleanup()
    }
    cleanups = []
    document.body.replaceChildren()
  })

  it('discovers story files and pairs every component with a stories file', () => {
    const storyPaths = Object.keys(storyModules)
    expect(storyPaths.length).toBeGreaterThan(0)

    const componentPaths = Object.keys(componentModules)
    expect(componentPaths.length).toBeGreaterThan(0)

    for (const compPath of componentPaths) {
      const expectedStoryPath = compPath.replace(/\.vue$/, '.stories.ts')
      expect(storyPaths).toContain(expectedStoryPath)
    }
  })

  it('detects missing label violations on unlabelled input', async () => {
    const container = document.createElement('div')
    document.body.append(container)
    const app = createApp({
      render() {
        return h(UiInput)
      },
    })
    app.mount(container)
    cleanups.push(() => {
      app.unmount()
      container.remove()
    })

    const results = await runAudit(document.body)

    expect(results.violations.length).toBeGreaterThan(0)
    const labelViolation = results.violations.find(
      (v) => v.id === 'label' || v.id === 'label-title-only',
    )
    expect(labelViolation).toBeDefined()
  })

  for (const [path, storyModule] of Object.entries(storyModules)) {
    if (path.includes('/foundations/')) {
      continue
    }

    const stories = composeStories(
      storyModule as Parameters<typeof composeStories>[0],
    )
    describe(`Stories in ${path}`, () => {
      for (const [storyName, StoryComponent] of Object.entries(stories)) {
        it(`${storyName} passes axe accessibility checks`, async () => {
          const container = document.createElement('div')
          document.body.append(container)

          const app = createApp(StoryComponent as Component)
          app.mount(container)
          cleanups.push(() => {
            app.unmount()
            container.remove()
          })

          const results = await runAudit(document.body)

          expect(
            results.violations,
            `Expected no axe violations in ${path} -> ${storyName}, found: ${JSON.stringify(
              results.violations.map((v) => ({
                id: v.id,
                impact: v.impact,
                description: v.description,
                nodes: v.nodes.map((n) => n.html),
              })),
              null,
              2,
            )}`,
          ).toHaveLength(0)
        })
      }
    })
  }
})

// @vitest-environment happy-dom
import { afterEach, describe, expect, it } from 'vitest'
import { createApp, h, type Component } from 'vue'
import axe from 'axe-core'
import { composeStories, setProjectAnnotations } from '@storybook/vue3-vite'
import preview from '../../.storybook/preview'
import UiInput from './form/UiInput.vue'

setProjectAnnotations(preview)

const storyModules = import.meta.glob<Record<string, unknown>>(
  './**/*.stories.ts',
  {
    eager: true,
  },
)

describe('accessibility (axe-core)', () => {
  let cleanups: (() => void)[] = []

  afterEach(() => {
    for (const cleanup of cleanups) {
      cleanup()
    }
    cleanups = []
    document.body.replaceChildren()
  })

  // Negative fixture: unlabelled UiInput must trigger an axe violation
  it('detects missing label violations on unlabelled input', async () => {
    const container = document.createElement('div')
    document.body.append(container)
    const app = createApp({
      render() {
        return h(UiInput)
      },
    })
    app.mount(container)
    cleanups.push(() => app.unmount())

    const results = await axe.run(container, {
      runOnly: {
        type: 'tag',
        values: ['wcag2a', 'wcag2aa', 'wcag21aa'],
      },
      rules: {
        'color-contrast': { enabled: false },
      },
    })

    expect(results.violations.length).toBeGreaterThan(0)
    const labelViolation = results.violations.find(
      (v) => v.id === 'label' || v.id === 'label-title-only',
    )
    expect(labelViolation).toBeDefined()
  })

  // Test all component stories across src/ui
  for (const [path, storyModule] of Object.entries(storyModules)) {
    // Component stories under src/ui (exclude foundations docs if any)
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
          cleanups.push(() => app.unmount())

          const results = await axe.run(container, {
            runOnly: {
              type: 'tag',
              values: ['wcag2a', 'wcag2aa', 'wcag21aa'],
            },
            rules: {
              'color-contrast': { enabled: false },
            },
          })

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

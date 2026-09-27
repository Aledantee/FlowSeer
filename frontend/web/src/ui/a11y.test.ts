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

const storySources = import.meta.glob<string>('./**/*.stories.ts', {
  eager: true,
  query: '?raw',
  import: 'default',
})

const componentModules = import.meta.glob('./**/Ui*.vue')

interface OverlayAuditExpectation {
  role: string
  triggerEvent?: 'click' | 'input'
  triggerSelector?: string
}

const OVERLAY_AUDITS: Readonly<Record<string, OverlayAuditExpectation>> = {
  './alert-dialog/UiAlertDialog.stories.ts:AccessibilityAudit': {
    role: 'alertdialog',
  },
  './combobox/UiCombobox.stories.ts:AccessibilityAudit': {
    role: 'listbox',
    triggerEvent: 'input',
    triggerSelector: '[role="combobox"]',
  },
  './command/UiCommand.stories.ts:AccessibilityAudit': { role: 'dialog' },
  './dialog/UiDialog.stories.ts:AccessibilityAudit': { role: 'dialog' },
  './dropdown-menu/UiDropdownMenu.stories.ts:AccessibilityAudit': {
    role: 'menu',
  },
  './popover/UiPopover.stories.ts:AccessibilityAudit': { role: 'dialog' },
}

const AXE_OPTIONS: axe.RunOptions = {
  runOnly: {
    type: 'tag',
    values: ['wcag2a', 'wcag2aa', 'wcag21aa'],
  },
  rules: {
    'color-contrast': { enabled: false },
    region: { enabled: false },
  },
}

async function runAudit(
  element: Element = document.body,
  overlayAudit?: OverlayAuditExpectation & { container: Element },
) {
  await nextTick()
  await new Promise((resolve) => setTimeout(resolve, 0))

  if (overlayAudit?.triggerSelector) {
    const trigger = overlayAudit.container.querySelector<HTMLElement>(
      overlayAudit.triggerSelector,
    )
    expect(
      trigger,
      `Expected the accessibility audit story to render ${overlayAudit.triggerSelector}`,
    ).not.toBeNull()
    if (overlayAudit.triggerEvent === 'input') {
      trigger?.dispatchEvent(new Event('input', { bubbles: true }))
    } else {
      trigger?.click()
    }
    await nextTick()
    await new Promise((resolve) => setTimeout(resolve, 0))
  }

  if (overlayAudit) {
    const selector = `[role="${overlayAudit.role}"]`
    const portalledElement = document.body.querySelector(selector)

    expect(
      portalledElement,
      `Expected the accessibility audit story to render ${selector} in document.body`,
    ).not.toBeNull()
    expect(
      overlayAudit.container.querySelector(selector),
      `Expected ${selector} to render outside the story mount container`,
    ).toBeNull()
  }

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

  it('discovers story files and covers every component in a story', () => {
    const storyPaths = Object.keys(storyModules)
    expect(storyPaths.length).toBeGreaterThan(0)

    const componentPaths = Object.keys(componentModules)
    expect(componentPaths.length).toBeGreaterThan(0)

    for (const compPath of componentPaths) {
      const directoryEnd = compPath.lastIndexOf('/') + 1
      const componentDirectory = compPath.slice(0, directoryEnd)
      const componentFilename = compPath.slice(directoryEnd)
      const isCovered = Object.entries(storySources).some(
        ([storyPath, source]) =>
          storyPath.startsWith(componentDirectory) &&
          source.includes(`./${componentFilename}`),
      )

      expect(
        isCovered,
        `${compPath} is not imported by a colocated story`,
      ).toBe(true)
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

          const overlayAudit = OVERLAY_AUDITS[`${path}:${storyName}`]
          const results = await runAudit(
            document.body,
            overlayAudit ? { container, ...overlayAudit } : undefined,
          )

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

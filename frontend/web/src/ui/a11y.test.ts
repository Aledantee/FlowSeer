// @vitest-environment happy-dom
import { afterEach, describe, expect, it, vi } from 'vitest'
import { createApp, h, nextTick, type Component } from 'vue'
import axe from 'axe-core'
import { composeStories, setProjectAnnotations } from '@storybook/vue3-vite'
import preview from '../../.storybook/preview'
import UiInput from './form/UiInput.vue'

setProjectAnnotations(preview)

// The audit covers the component families under this directory and the
// component stories alongside it; a story outside the glob is unaudited.
const storyModules = import.meta.glob<Record<string, unknown>>(
  ['./**/*.stories.ts', '../components/**/*.stories.ts'],
  { eager: true },
)

const storySources = import.meta.glob<string>(
  ['./**/*.stories.ts', '../components/**/*.stories.ts'],
  {
    eager: true,
    query: '?raw',
    import: 'default',
  },
)

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
    triggerSelector: 'button',
  },
  './form/UiSelect.stories.ts:AccessibilityAudit': {
    role: 'listbox',
  },
  './popover/UiPopover.stories.ts:AccessibilityAudit': { role: 'dialog' },
  './tooltip/UiTooltip.stories.ts:AccessibilityAudit': {
    role: 'tooltip',
  },
}

const COMPONENT_TARGETS: Readonly<
  Record<string, { kind: string; count: number }>
> = {
  '../components/TrafficChart.stories.ts:Default': { kind: 'chart', count: 1 },
  '../components/TrafficChart.stories.ts:DarkMode': { kind: 'chart', count: 1 },
  '../components/TrafficSparkline.stories.ts:AllColorVariants': {
    kind: 'chart',
    count: 6,
  },
  './card/UiCard.stories.ts:Default': { kind: 'card', count: 1 },
  './table/UiTable.stories.ts:Default': { kind: 'device', count: 4 },
  './ai/UiAiActionLayer.stories.ts:Selected': { kind: 'row', count: 1 },
  './ai/UiAiSummary.stories.ts:Idle': { kind: 'device', count: 1 },
  './ai/UiAiSummary.stories.ts:Loading': { kind: 'device', count: 1 },
  './ai/UiAiSummary.stories.ts:Result': { kind: 'device', count: 1 },
  './ai/UiAiSummary.stories.ts:ErrorState': { kind: 'device', count: 1 },
  './ai/UiAiSummary.stories.ts:Unavailable': { kind: 'device', count: 1 },
  './ai/UiAiSummary.stories.ts:DarkMode': { kind: 'device', count: 1 },
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

async function settle() {
  await nextTick()
  await new Promise((resolve) => setTimeout(resolve, 0))
}

async function runAudit(element: Element = document.body) {
  await settle()
  element.querySelectorAll('[data-aria-hidden]').forEach((el) => {
    el.removeAttribute('aria-hidden')
    el.removeAttribute('data-aria-hidden')
  })
  return axe.run(element, AXE_OPTIONS)
}

async function openOverlay(
  overlayAudit: OverlayAuditExpectation & { container: Element },
) {
  await settle()
  if (overlayAudit.triggerSelector) {
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
    await settle()
  }

  const selector = `[role="${overlayAudit.role}"]`
  expect(
    document.body.querySelector(selector),
    `Expected the accessibility audit story to render ${selector} in document.body`,
  ).not.toBeNull()
  expect(
    overlayAudit.container.querySelector(selector),
    `Expected ${selector} to render outside the story mount container`,
  ).toBeNull()
  await settle()
}

function box(): DOMRect {
  return {
    x: 0,
    y: 0,
    width: 320,
    height: 120,
    top: 0,
    left: 0,
    right: 320,
    bottom: 120,
    toJSON: () => ({}),
  } as DOMRect
}

// Select a meaningful target and give it real geometry so the action layer
// draws Ask over it. The decorator registers the story wrapper as a target of
// its own, so prefer a target the component registered and only fall back to
// the wrapper when a story has none.
async function selectTarget(label: string) {
  const api = window.flowseerAi
  expect(
    api,
    `Expected the AI decorator to install the window contract in ${label}`,
  ).toBeDefined()

  const targets = api?.listTargets() ?? []
  expect(
    targets.length,
    `Expected ${label} to register an AI target`,
  ).toBeGreaterThan(0)

  const expected = COMPONENT_TARGETS[label.replace(' -> ', ':')]
  if (expected) {
    expect(
      targets.filter((target) => target.kind === expected.kind),
      `Expected ${label} to register ${expected.count} ${expected.kind} target(s)`,
    ).toHaveLength(expected.count)
  }

  const componentTarget = targets.find(
    (target) =>
      target.kind === expected?.kind || (!expected && target.kind !== 'story'),
  )
  const selected = componentTarget ?? targets[0]
  expect(
    selected,
    `Expected ${label} to expose a selectable target`,
  ).toBeDefined()

  const highlighted: Element[] = []
  const scrollIntoView = vi
    .spyOn(Element.prototype, 'scrollIntoView')
    .mockImplementation(function (this: Element) {
      highlighted.push(this)
    })
  let highlightedTarget: boolean | undefined
  try {
    highlightedTarget = api?.highlight(selected?.id ?? '')
  } finally {
    scrollIntoView.mockRestore()
  }
  expect(
    highlightedTarget,
    `Expected ${label} to highlight its registered target`,
  ).toBe(true)

  const element = highlighted[0]
  expect(
    element,
    `Expected ${label} to highlight a mounted target element`,
  ).toBeInstanceOf(HTMLElement)
  if (element instanceof HTMLElement) element.getBoundingClientRect = box

  window.dispatchEvent(new Event('resize'))
  await settle()
  return { selected, element, componentTarget }
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

    const storyKeys = Object.entries(storyModules).flatMap(([path, module]) =>
      Object.keys(
        composeStories(module as Parameters<typeof composeStories>[0]),
      ).map((name) => `${path}:${name}`),
    )
    for (const key of Object.keys(COMPONENT_TARGETS)) {
      expect(storyKeys, `${key} is missing from the story audit`).toContain(key)
    }

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

          const label = `${path} -> ${storyName}`
          const overlayAudit = OVERLAY_AUDITS[`${path}:${storyName}`]
          if (overlayAudit) {
            await openOverlay({ container, ...overlayAudit })
          }

          const { selected, element, componentTarget } =
            await selectTarget(label)

          const ask = document.querySelector<HTMLElement>('.ai-ask')
          expect(
            ask,
            `Expected ${label} to mount the Ask action for its selected target`,
          ).not.toBeNull()
          ask?.click()
          await settle()
          expect(
            document.querySelector('form textarea'),
            `Expected ${label} to open the Ask panel for its selected target`,
          ).not.toBeNull()

          const root = container.querySelector<HTMLElement>(
            '[data-ai-story-root]',
          )
          expect(
            root,
            `Expected ${label} to render the decorator story root`,
          ).not.toBeNull()
          if (componentTarget) {
            expect(
              element === root,
              `Expected ${label} to register its target on a component element, not the decorator wrapper`,
            ).toBe(false)
            expect(
              selected?.kind,
              `Expected ${label} to select a component-level target`,
            ).not.toBe('story')
          }

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

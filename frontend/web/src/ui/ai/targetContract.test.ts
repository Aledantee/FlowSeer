import { describe, expect, it } from 'vitest'
import type { Component } from 'vue'
import TrafficChart from '../../components/TrafficChart.vue'
import TrafficSparkline from '../../components/TrafficSparkline.vue'
import displayCoverage from './displayTargets.test.ts?raw'
import controlCoverage from './controlTargets.test.ts?raw'
import anchorCoverage from './UiAiTarget.test.ts?raw'

// Every kit component is either semantic, so it takes the `ai` and `aiOrigin`
// props, emits `aiOriginAcknowledged`, and has a mount test that asserts its
// anchor, or exempt, so it carries no AI props. A new `Ui*.vue` fails here
// until someone classifies it.
const semantic = {
  UiAiTarget: 'the original native tag, or the single forwarded child',
  UiBadge: 'its visible root',
  UiStatusBadge: 'its visible root',
  UiCard: 'its visible root',
  UiMetricCard: 'its visible root',
  UiEmptyState: 'its visible root',
  UiKbd: 'its visible root',
  UiMeter: 'its visible root',
  UiSegmentedMeter: 'its visible root',
  UiProgress: 'its visible root',
  UiTable: 'the table',
  UiTableRow: 'the tr',
  UiTableCell: 'the td',
  UiTableHead: 'the th',
  UiTableEmpty: 'the empty row',
  UiBreadcrumbPage: 'its visible text element',
  UiButton: 'the actual button',
  UiBreadcrumbLink: 'the forwarded link',
  UiBreadcrumbEllipsis: 'the actual button',
  UiField: 'the field root',
  UiInput: 'the native control',
  UiTextarea: 'the native control',
  UiCheckbox: 'the Reka control root',
  UiSwitch: 'the Reka control root',
  UiRadioGroup: 'the group',
  UiSelect: 'the select trigger',
  UiCombobox: 'the combobox input',
  UiTabs: 'its visible root',
  UiPagination: 'its visible root',
  UiCommand: 'its visible root',
  UiCommandInput: 'its visible control',
  UiCommandItem: 'its visible item',
  UiCommandEmpty: 'its visible root',
  UiCommandShortcut: 'its visible root',
  UiCommandGroup: 'its visible root',
  UiDropdownMenuItem: 'the mounted menu item',
  UiContextMenuItem: 'the mounted menu item',
  UiAlertDialog: 'the mounted popup content',
  UiDialog: 'the mounted popup content',
  UiCommandDialog: 'the mounted popup content',
  UiDropdownMenu: 'the mounted popup content',
  UiContextMenu: 'the mounted popup content',
  UiPopover: 'the mounted popup content',
  UiTooltip: 'the mounted popup content',
  UiToast: 'the mounted toast content',
  UiAiSummary: 'the summary section',
  UiAiLabel: 'the label trigger',
  UiAiEntityChip: 'the entity button, or the fallback text',
  UiAiAssistant: 'its visible panel',
  UiAiResult: 'its visible result root',
  UiAiResultActions: 'its visible result root',
} as const satisfies Record<string, string>

const exempt = {
  UiAppRoot: 'provider',
  UiToastProvider: 'provider',
  UiAiContextLayer: 'provider',
  UiAiRender: 'structural',
  UiScrollArea: 'structural',
  UiSkeleton: 'decorative',
  UiSpinner: 'decorative',
  UiSeparator: 'decorative',
  UiDropdownMenuSeparator: 'decorative',
  UiContextMenuSeparator: 'decorative',
  UiCommandSeparator: 'decorative',
  UiCommandList: 'structural',
  UiBreadcrumb: 'structural',
  UiBreadcrumbList: 'structural',
  UiBreadcrumbItem: 'structural',
  UiBreadcrumbSeparator: 'decorative',
  UiTableHeader: 'structural',
  UiTableBody: 'structural',
} as const satisfies Record<string, string>

// The two chart components live outside `src/ui/` and follow the same contract.
const charts: Record<string, Component> = {
  TrafficChart,
  TrafficSparkline,
}

const modules = import.meta.glob<{ default: Component }>('../**/Ui*.vue', {
  eager: true,
})

function nameOf(path: string): string {
  return path.slice(path.lastIndexOf('/') + 1).replace(/\.vue$/, '')
}

const components = new Map<string, Component>(
  Object.entries(modules).map(([path, module]) => [
    nameOf(path),
    module.default,
  ]),
)

function propNames(component: Component): string[] {
  const props = (component as { props?: unknown }).props
  if (Array.isArray(props)) return props.map(String)
  return Object.keys((props as object | undefined) ?? {})
}

function emitNames(component: Component): string[] {
  const emits = (component as { emits?: unknown }).emits
  if (Array.isArray(emits)) return emits.map(String)
  return Object.keys((emits as object | undefined) ?? {})
}

// A mount case names its component in `name: 'UiX'` or `component: UiX`.
// Passing it as a child of another case does not count.
function covers(source: string, name: string): boolean {
  return (
    new RegExp(`name: '${name}[ ']`).test(source) ||
    new RegExp(`component: ${name}\\b`).test(source)
  )
}

// UiAiTarget is the anchor the other cases build on, so its own test file is
// its mount coverage.
function mounts(name: string): boolean {
  if (name === 'UiAiTarget') return /\bh\(\s*UiAiTarget,/.test(anchorCoverage)
  // The context menu opens from a trigger event, so controlTargets.test.ts
  // mounts it in a dedicated test rather than in a case table.
  if (name === 'UiContextMenu' || name === 'UiContextMenuItem') {
    return new RegExp(`\\bh\\(\\s*${name},[^]*?ai: target\\(`).test(
      controlCoverage,
    )
  }
  return [displayCoverage, controlCoverage].some((source) =>
    covers(source, name),
  )
}

describe('target contract inventory', () => {
  it('classifies every Ui Vue file as semantic or exempt', () => {
    const classified = new Set<string>([
      ...Object.keys(semantic),
      ...Object.keys(exempt),
    ])
    const unclassified = [...components.keys()].filter(
      (name) => !classified.has(name),
    )
    expect(
      unclassified,
      'Classify each new component in the semantic or exempt table',
    ).toEqual([])

    const missing = [...classified].filter((name) => !components.has(name))
    expect(missing, 'Drop entries whose component no longer exists').toEqual([])
  })

  it('lists no component as both semantic and exempt', () => {
    const both = Object.keys(semantic).filter((name) => name in exempt)
    expect(both).toEqual([])
  })

  it.each([
    ...Object.keys(semantic).map(
      (name) => [name, components.get(name)] as const,
    ),
    ...Object.entries(charts),
  ])(
    '%s takes the ai props, emits the acknowledgement, and has mount coverage',
    (name, component) => {
      expect(component, `Expected ${name} to exist`).toBeDefined()
      if (!component) return

      const props = propNames(component)
      expect(props, `${name} props`).toContain('ai')
      expect(props, `${name} props`).toContain('aiOrigin')
      expect(emitNames(component), `${name} emits`).toContain(
        'aiOriginAcknowledged',
      )

      const mounted = mounts(name)
      expect(
        mounted,
        `Expected ${name} to have a mount case in displayTargets.test.ts or controlTargets.test.ts`,
      ).toBe(true)
    },
  )

  it.each(Object.keys(exempt))('%s carries no ai props', (name) => {
    const props = propNames(components.get(name) as Component)
    expect(props).not.toContain('ai')
    expect(props).not.toContain('aiOrigin')
  })
})

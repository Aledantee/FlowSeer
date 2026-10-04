import type { Meta, StoryObj } from '@storybook/vue3-vite'
import UiAiSummary from './UiAiSummary.vue'
import { AiUnavailableError } from '../../ai'
import type { AiTarget } from '../../ai'

const meta: Meta<typeof UiAiSummary> = {
  title: 'Ui/AiSummary',
  component: UiAiSummary,
}

export default meta
type Story = StoryObj<typeof UiAiSummary>

// A summary requests a target, so each story registers its placement as that
// target in the registry the shared decorator installs. The decorator routes
// the request to the canvas that contains the target, so this story's handler
// answers it and no story depends on a private registry. Every story keeps a
// distinct target id because Storybook Docs mounts several canvases at once,
// and the application has no backend.
function summaryTarget(story: string): AiTarget {
  return {
    id: `standalone:story:ai-summary-${story}:berlin-ap-01`,
    kind: 'device',
    label: 'berlin-ap-01',
    context: { device: 'berlin-ap-01', site: 'Berlin Mitte' },
  }
}

function placement(story: string) {
  return () => ({
    components: { UiAiSummary },
    setup() {
      return { target: summaryTarget(story) }
    },
    template: `
      <div
        v-ai-target="target"
        class="max-w-sm p-4 bg-card border border-border rounded-panel text-foreground"
      >
        <p class="mb-2 text-xs text-muted-foreground">Site summary placement</p>
        <UiAiSummary :target="target" />
      </div>
    `,
  })
}

export const Idle: Story = {
  render: placement('idle'),
}

export const Loading: Story = {
  parameters: {
    ai: { handler: () => new Promise<never>(() => {}) },
  },
  render: placement('loading'),
}

export const Result: Story = {
  parameters: {
    ai: {
      handler: async () => ({
        type: 'summary',
        headline:
          'Berlin Mitte is degraded: brl-sw-01 uplinks are up, but two access points stopped answering within the last hour. No site-wide outage is indicated.',
        tone: 'warning',
        findings: [],
        metrics: [],
        next: [],
        sources: [],
      }),
    },
  },
  render: placement('result'),
}

export const ErrorState: Story = {
  parameters: {
    ai: {
      handler: async () => {
        throw new Error('The summary service did not answer.')
      },
    },
  },
  render: placement('error'),
}

export const Unavailable: Story = {
  parameters: {
    // The decorator always installs a handler, so this story rejects as
    // unavailable to keep the no-handler state reachable in Storybook.
    ai: { handler: () => Promise.reject(new AiUnavailableError()) },
  },
  render: placement('unavailable'),
}

export const DarkMode: Story = {
  parameters: {
    themes: { themeOverride: 'dark' },
    ai: {
      handler: async () => ({
        type: 'summary',
        headline: 'All paths answered the last poll.',
        tone: 'ok',
        findings: [],
        metrics: [],
        next: [],
        sources: [],
      }),
    },
  },
  render: () => ({
    components: { UiAiSummary },
    setup() {
      return { target: summaryTarget('dark') }
    },
    template: `
      <div
        data-theme="dark"
        v-ai-target="target"
        class="max-w-sm p-4 bg-card border border-border rounded-panel text-foreground"
      >
        <p class="mb-2 text-xs text-muted-foreground">Site summary placement</p>
        <UiAiSummary :target="target" />
      </div>
    `,
  }),
}

export const LongText: Story = {
  parameters: {
    ai: {
      handler: async () => ({
        type: 'summary',
        headline:
          'Frankfurt am Main Rechenzentrum Campus Nord Halle B zeigt eine erhöhte Fehlerrate auf den Uplink-Schnittstellen. Drei Access Points im östlichen Flügel antworten seit mehr als 45 Minuten nicht auf SNMP-Polls. Die redundante Stromversorgung meldet stabile Spannungswerte ohne Unterbrechungen.',
        tone: 'warning',
        findings: [],
        metrics: [],
        next: [],
        sources: [],
      }),
    },
  },
  render: () => ({
    components: { UiAiSummary },
    setup() {
      const target: AiTarget = {
        id: 'standalone:story:ai-summary-longtext:datacenter-core-aggregation-switch-fra-01',
        kind: 'device',
        label:
          'Datacenter Core Aggregation Switch Cluster FRA-DC-01 with redundant supervisor engines',
        context: {
          device: 'fra-dc-core-01',
          site: 'Frankfurt Datacenter Campus North Building 3 Hall B Rack 42',
        },
      }
      return { target }
    },
    template: `
      <div
        v-ai-target="target"
        class="max-w-sm p-4 bg-card border border-border rounded-panel text-foreground"
      >
        <p class="mb-2 text-xs text-muted-foreground">LongText summary placement</p>
        <UiAiSummary :target="target" />
      </div>
    `,
  }),
}

export const Overrides: Story = {
  render: () => ({
    components: { UiAiSummary },
    setup() {
      const target = summaryTarget('overrides')
      const labels = {
        summaryLabel: 'Custom summary for target',
        generate: 'Generate brief',
        generating: 'Generating brief…',
        unavailable: 'Service unavailable',
        retry: 'Try again',
        error: 'Failed to generate brief',
      }
      return { target, labels }
    },
    template: `
      <div
        v-ai-target="target"
        class="max-w-sm p-4 bg-card border border-border rounded-panel text-foreground"
      >
        <p class="mb-2 text-xs text-muted-foreground">Custom labels placement</p>
        <UiAiSummary :target="target" :labels="labels" />
      </div>
    `,
  }),
}

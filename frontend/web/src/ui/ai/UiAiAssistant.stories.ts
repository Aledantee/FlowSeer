import type { Meta, StoryObj } from '@storybook/vue3-vite'
import UiAiAssistant from './UiAiAssistant.vue'
import type { AiSeed } from '../../ai'

const meta: Meta<typeof UiAiAssistant> = {
  title: 'Ui/Ai/UiAiAssistant',
  component: UiAiAssistant,
}

export default meta
type Story = StoryObj<typeof UiAiAssistant>

export const Default: Story = {
  render: () => ({
    components: { UiAiAssistant },
    template: `
      <div class="h-[600px] w-96 border border-border rounded-panel overflow-hidden">
        <UiAiAssistant />
      </div>
    `,
  }),
}

const seedExample: AiSeed = {
  targets: [
    {
      id: 'standalone:story:assistant:device-1',
      kind: 'device',
      view: 'devices',
      label: 'core-sw-1',
      context: { health: 'Offline', site: 'Berlin Mitte' },
    },
  ],
  turns: [
    {
      role: 'user',
      prompt: 'Why is this offline?',
    },
    {
      role: 'assistant',
      result: {
        type: 'summary',
        tone: 'critical',
        headline: 'core-sw-1 is offline',
        findings: [
          {
            title: 'Power supply failure',
            severity: 'critical',
            refs: [],
          },
        ],
        metrics: [{ label: 'Uptime', value: '0m' }],
        next: [{ label: 'Check power cable' }],
        sources: [],
      },
    },
  ],
}

export const Seeded: Story = {
  render: () => ({
    components: { UiAiAssistant },
    setup() {
      return { seedExample }
    },
    template: `
      <div class="h-[600px] w-96 border border-border rounded-panel overflow-hidden">
        <UiAiAssistant :seed="seedExample" />
      </div>
    `,
  }),
}

const multipleChipsSeed: AiSeed = {
  targets: [
    {
      id: 'standalone:story:assistant:device-1',
      kind: 'device',
      view: 'devices',
      label: 'core-sw-1',
      context: { health: 'Degraded', site: 'Berlin Mitte' },
    },
    {
      id: 'standalone:story:assistant:site-1',
      kind: 'site',
      view: 'dashboard',
      label: 'Berlin Mitte',
      context: {},
    },
  ],
  turns: [],
}

export const SuggestionsWithChips: Story = {
  render: () => ({
    components: { UiAiAssistant },
    setup() {
      return { multipleChipsSeed }
    },
    template: `
      <div class="h-[600px] w-96 border border-border rounded-panel overflow-hidden">
        <UiAiAssistant :seed="multipleChipsSeed" />
      </div>
    `,
  }),
}

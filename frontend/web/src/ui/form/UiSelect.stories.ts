import { ref } from 'vue'
import type { Meta, StoryObj } from '@storybook/vue3-vite'
import { aiRegistry } from '../../ai'
import type { AiTarget } from '../../ai'
import type { AiOriginRequest } from '../ai/context'
import UiSelect from './UiSelect.vue'

const sampleOptions = [
  { value: 'ham', label: 'Hamburg Site (North)' },
  { value: 'ber', label: 'Berlin Core Data Center' },
  { value: 'mun', label: 'Munich Gateway' },
  { value: 'fra', label: 'Frankfurt Hub (Offline)', disabled: true },
]

const meta: Meta<typeof UiSelect> = {
  title: 'Ui/Select',
  component: UiSelect,
  argTypes: {
    placeholder: { control: 'text' },
    disabled: { control: 'boolean' },
    invalid: { control: 'boolean' },
  },
}

export default meta
type Story = StoryObj<typeof UiSelect>

export const Default: Story = {
  args: {
    options: sampleOptions,
    placeholder: 'Choose site location...',
    ariaLabel: 'Site location select',
  },
}

export const Selected: Story = {
  args: {
    modelValue: 'ber',
    options: sampleOptions.map((opt) => ({ ...opt, identifier: true })),
    placeholder: 'Choose site location...',
    ariaLabel: 'Site location select',
  },
}

export const Invalid: Story = {
  args: {
    options: sampleOptions,
    placeholder: 'Choose site location...',
    invalid: true,
    ariaLabel: 'Site location select',
  },
}

export const Disabled: Story = {
  args: {
    modelValue: 'ham',
    options: sampleOptions,
    disabled: true,
    ariaLabel: 'Site location select',
  },
}

export const Focus: Story = {
  args: {
    options: sampleOptions,
    placeholder: 'Choose site location...',
    ariaLabel: 'Site location select',
  },
  render: (args) => ({
    components: { UiSelect },
    setup() {
      return { args }
    },
    template: '<UiSelect v-bind="args" autofocus />',
  }),
}

export const AccessibilityAudit: Story = {
  args: {
    options: sampleOptions,
    placeholder: 'Choose site location...',
    ariaLabel: 'Site location select',
    defaultOpen: true,
  },
}

export const DefaultPlaceholder: Story = {
  name: 'Default Placeholder',
  args: {
    options: sampleOptions,
    ariaLabel: 'Site location select',
  },
}

export const LongText: Story = {
  args: {
    options: [
      {
        value: 'ham-dc01-cluster-alpha',
        label:
          'High-Density Redundant Metropolitan Gateway Node Hamburg Core Facility Cluster Alpha (Western Europe)',
      },
      {
        value: 'ber-dc02-cluster-beta',
        label:
          'Primary Interconnect Aggregation Transit Facility Berlin Tier-IV Infrastructure Node Beta (Central Europe)',
      },
    ],
    placeholder:
      'Select an autonomous border gateway router, aggregation switch, or telemetry edge collector...',
    ariaLabel:
      'Autonomous network infrastructure telemetry ingestion endpoint selector',
  },
}

const origin: AiOriginRequest = {
  requestId: 'req-story-select',
  action: 'summary',
  targets: [],
  history: [],
}

// Each story keeps its own target id because Storybook Docs mounts several
// canvases into one document.
function storyTarget(state: string): AiTarget {
  return {
    id: `standalone:story:ui-select:${state}`,
    kind: 'control',
    label: 'Site location',
    context: { state },
  }
}

// The registry draws the selection outline on the element the component
// registered.
export const AiSelected: Story = {
  args: {
    modelValue: 'ber',
    options: sampleOptions,
    ariaLabel: 'Site location select',
    ai: storyTarget('selected'),
  },
  play: () => {
    aiRegistry.highlight(storyTarget('selected').id)
  },
}

// The outline marks a value an agent changed until the user touches it. The
// caller clears its own state when the component reports the interaction.
export const AgentChanged: Story = {
  args: {
    modelValue: 'ber',
    options: sampleOptions,
    ariaLabel: 'Site location select',
    ai: storyTarget('agent-changed'),
  },
  render: (args) => ({
    components: { UiSelect },
    setup() {
      const changed = ref<AiOriginRequest | undefined>(origin)
      return { args, changed }
    },
    template: `
      <UiSelect
        v-bind="args"
        :ai-origin="changed"
        @ai-origin-acknowledged="changed = undefined"
      />
    `,
  }),
}

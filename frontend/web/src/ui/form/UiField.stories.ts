import { ref } from 'vue'
import type { Meta, StoryObj } from '@storybook/vue3-vite'
import type { AiOriginRequest } from '../ai/context'
import UiAiLabel from '../ai/UiAiLabel.vue'
import UiField from './UiField.vue'
import UiInput from './UiInput.vue'

const meta: Meta<typeof UiField> = {
  title: 'Ui/Field',
  component: UiField,
  argTypes: {
    label: { control: 'text' },
    description: { control: 'text' },
    error: { control: 'text' },
    required: { control: 'boolean' },
    requiredMark: { control: 'text' },
  },
}

export default meta
type Story = StoryObj<typeof UiField>

export const Default: Story = {
  args: {
    label: 'Device Name',
    description: 'A unique identifier for this hardware unit.',
  },
  render: (args) => ({
    components: { UiField, UiInput },
    setup() {
      return { args }
    },
    template: `
      <UiField v-bind="args">
        <UiInput placeholder="e.g. edge-router-01" />
      </UiField>
    `,
  }),
}

export const WithError: Story = {
  args: {
    label: 'Device Name',
    description: 'A unique identifier for this hardware unit.',
    error: 'Device name is already in use on this site.',
  },
  render: (args) => ({
    components: { UiField, UiInput },
    setup() {
      return { args }
    },
    template: `
      <UiField v-bind="args">
        <UiInput placeholder="e.g. edge-router-01" />
      </UiField>
    `,
  }),
}

export const Required: Story = {
  args: {
    label: 'Site Location',
    required: true,
  },
  render: (args) => ({
    components: { UiField, UiInput },
    setup() {
      return { args }
    },
    template: `
      <UiField v-bind="args">
        <UiInput required placeholder="Select or enter site..." />
      </UiField>
    `,
  }),
}

export const LongText: Story = {
  args: {
    label:
      'Fully qualified organizational domain name for edge telemetry endpoint integration',
    description:
      'Enter the canonical host name where edge gateways will transmit collected flow samples and device status reports.',
    required: true,
  },
  render: (args) => ({
    components: { UiField, UiInput },
    setup() {
      return { args }
    },
    template: `
      <UiField v-bind="args">
        <UiInput placeholder="edge-gateway-telemetry.corp.internal.example" />
      </UiField>
    `,
  }),
}

export const CustomRequiredMark: Story = {
  args: {
    label: 'Primary Contact',
    required: true,
    requiredMark: ' (mandatory)',
  },
  render: (args) => ({
    components: { UiField, UiInput },
    setup() {
      return { args }
    },
    template: `
      <UiField v-bind="args">
        <UiInput placeholder="admin@example.com" />
      </UiField>
    `,
  }),
}

const origin: AiOriginRequest = {
  requestId: 'req-story-field',
  action: 'summary',
  targets: [
    {
      id: 'standalone:story:ui-field:device-name',
      kind: 'device',
      view: 'devices',
      label: 'Device Name',
      context: { site: 'Berlin Mitte' },
    },
  ],
  history: [],
}

// An agent changed the value. The input carries the marker and the label
// explains the request, and the caller clears both when the user edits. The
// text control keeps its own context menu, so the story registers no target.
export const AgentChanged: Story = {
  args: {
    label: 'Device Name',
  },
  render: (args) => ({
    components: { UiField, UiInput, UiAiLabel },
    setup() {
      const name = ref('edge-router-01')
      const changed = ref<AiOriginRequest | undefined>(origin)
      return { args, name, changed }
    },
    template: `
      <UiField v-bind="args">
        <UiInput
          v-model="name"
          :ai-origin="changed"
          @ai-origin-acknowledged="changed = undefined"
        />
        <UiAiLabel v-if="changed" :request="changed" />
      </UiField>
    `,
  }),
}

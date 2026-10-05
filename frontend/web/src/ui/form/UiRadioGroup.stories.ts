import { ref } from 'vue'
import type { Meta, StoryObj } from '@storybook/vue3-vite'
import type { AiTarget } from '../../ai'
import type { AiOriginRequest } from '../ai/context'
import UiRadioGroup from './UiRadioGroup.vue'

const sampleOptions = [
  { value: 'all', label: 'All traffic' },
  { value: 'critical', label: 'Critical only' },
  { value: 'none', label: 'Mute alerts', disabled: true },
]

const meta: Meta<typeof UiRadioGroup> = {
  title: 'Ui/RadioGroup',
  component: UiRadioGroup,
  argTypes: {
    orientation: { control: 'select', options: ['vertical', 'horizontal'] },
    disabled: { control: 'boolean' },
  },
}

export default meta
type Story = StoryObj<typeof UiRadioGroup>

export const Vertical: Story = {
  args: {
    modelValue: 'all',
    options: sampleOptions,
    orientation: 'vertical',
  },
}

export const Horizontal: Story = {
  args: {
    modelValue: 'critical',
    options: sampleOptions,
    orientation: 'horizontal',
  },
}

export const Disabled: Story = {
  args: {
    modelValue: 'all',
    options: sampleOptions,
    disabled: true,
  },
}

export const Focus: Story = {
  args: {
    modelValue: 'all',
    options: sampleOptions,
    orientation: 'vertical',
  },
  render: (args) => ({
    components: { UiRadioGroup },
    setup() {
      return { args }
    },
    template: '<UiRadioGroup v-bind="args" autofocus />',
  }),
}

const origin: AiOriginRequest = {
  requestId: 'req-story-radio-group',
  action: 'summary',
  targets: [],
  history: [],
}

// Each story keeps its own target id because Storybook Docs mounts several
// canvases into one document.
function storyTarget(state: string): AiTarget {
  return {
    id: `standalone:story:ui-radio-group:${state}`,
    kind: 'control',
    label: 'Alert filter',
    context: { state },
  }
}

// The outline marks a value an agent changed until the user touches it. The
// caller clears its own state when the component reports the interaction.
export const AgentChanged: Story = {
  args: {
    modelValue: 'critical',
    options: sampleOptions,
    ai: storyTarget('agent-changed'),
  },
  render: (args) => ({
    components: { UiRadioGroup },
    setup() {
      const changed = ref<AiOriginRequest | undefined>(origin)
      return { args, changed }
    },
    template: `
      <UiRadioGroup
        v-bind="args"
        :ai-origin="changed"
        @ai-origin-acknowledged="changed = undefined"
      />
    `,
  }),
}

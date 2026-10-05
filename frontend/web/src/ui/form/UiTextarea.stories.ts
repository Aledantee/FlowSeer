import { ref } from 'vue'
import type { Meta, StoryObj } from '@storybook/vue3-vite'
import type { AiOriginRequest } from '../ai/context'
import UiTextarea from './UiTextarea.vue'

const meta: Meta<typeof UiTextarea> = {
  title: 'Ui/Textarea',
  component: UiTextarea,
  argTypes: {
    placeholder: { control: 'text' },
    disabled: { control: 'boolean' },
    readonly: { control: 'boolean' },
    invalid: { control: 'boolean' },
    rows: { control: 'number' },
  },
}

export default meta
type Story = StoryObj<typeof UiTextarea>

export const Default: Story = {
  args: {
    placeholder: 'Write notes or description...',
    ariaLabel: 'Default notes textarea',
  },
}

export const Invalid: Story = {
  args: {
    modelValue: 'Invalid contents...',
    invalid: true,
    ariaLabel: 'Invalid notes textarea',
  },
}

export const Disabled: Story = {
  args: {
    modelValue: 'Read-only archived telemetry notes.',
    disabled: true,
    ariaLabel: 'Disabled notes textarea',
  },
}

export const Focus: Story = {
  args: {
    placeholder: 'Focused notes textarea...',
    ariaLabel: 'Focused notes textarea',
  },
  render: (args) => ({
    components: { UiTextarea },
    setup() {
      return { args }
    },
    template: '<UiTextarea v-bind="args" autofocus />',
  }),
}

const origin: AiOriginRequest = {
  requestId: 'req-story-textarea',
  action: 'summary',
  targets: [],
  history: [],
}

// The outline marks a value an agent changed until the user touches it. The
// caller clears its own state when the component reports the interaction. A
// native text control keeps its own context menu, so the story registers no
// target and shows the origin on its own.
export const AgentChanged: Story = {
  args: {
    ariaLabel: 'Device notes',
    modelValue: 'Rack 4, upper slot.',
  },
  render: (args) => ({
    components: { UiTextarea },
    setup() {
      const changed = ref<AiOriginRequest | undefined>(origin)
      return { args, changed }
    },
    template: `
      <UiTextarea
        v-bind="args"
        :ai-origin="changed"
        @ai-origin-acknowledged="changed = undefined"
      />
    `,
  }),
}

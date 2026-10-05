import { ref } from 'vue'
import type { Meta, StoryObj } from '@storybook/vue3-vite'
import type { AiOriginRequest } from '../ai/context'
import UiInput from './UiInput.vue'

const meta: Meta<typeof UiInput> = {
  title: 'Ui/Input',
  component: UiInput,
  argTypes: {
    placeholder: { control: 'text' },
    disabled: { control: 'boolean' },
    readonly: { control: 'boolean' },
    invalid: { control: 'boolean' },
  },
}

export default meta
type Story = StoryObj<typeof UiInput>

export const Default: Story = {
  args: {
    placeholder: 'Enter text...',
    ariaLabel: 'Default text input',
  },
}

export const Invalid: Story = {
  args: {
    modelValue: 'invalid@value',
    invalid: true,
    ariaLabel: 'Invalid text input',
  },
}

export const Disabled: Story = {
  args: {
    modelValue: 'Disabled value',
    disabled: true,
    ariaLabel: 'Disabled text input',
  },
}

export const Focus: Story = {
  args: {
    placeholder: 'Focused input...',
    ariaLabel: 'Focused text input',
  },
  render: (args) => ({
    components: { UiInput },
    setup() {
      return { args }
    },
    template: '<UiInput v-bind="args" autofocus />',
  }),
}

const origin: AiOriginRequest = {
  requestId: 'req-story-input',
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
    ariaLabel: 'Device name',
    modelValue: 'core-sw-1',
  },
  render: (args) => ({
    components: { UiInput },
    setup() {
      const changed = ref<AiOriginRequest | undefined>(origin)
      return { args, changed }
    },
    template: `
      <UiInput
        v-bind="args"
        :ai-origin="changed"
        @ai-origin-acknowledged="changed = undefined"
      />
    `,
  }),
}

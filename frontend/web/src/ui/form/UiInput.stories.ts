import type { Meta, StoryObj } from '@storybook/vue3-vite'
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

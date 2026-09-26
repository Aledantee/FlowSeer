import type { Meta, StoryObj } from '@storybook/vue3-vite'
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

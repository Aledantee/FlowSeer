import type { Meta, StoryObj } from '@storybook/vue3-vite'
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

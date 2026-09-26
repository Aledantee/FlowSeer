import type { Meta, StoryObj } from '@storybook/vue3-vite'
import UiSpinner from './UiSpinner.vue'

const meta: Meta<typeof UiSpinner> = {
  title: 'Ui/Spinner',
  component: UiSpinner,
  argTypes: {
    size: {
      control: 'select',
      options: ['sm', 'md', 'lg'],
    },
  },
}

export default meta
type Story = StoryObj<typeof UiSpinner>

export const Default: Story = {
  args: {
    size: 'md',
  },
}

export const Small: Story = {
  args: {
    size: 'sm',
  },
}

export const Large: Story = {
  args: {
    size: 'lg',
  },
}

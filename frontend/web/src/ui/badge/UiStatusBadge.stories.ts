import type { Meta, StoryObj } from '@storybook/vue3-vite'
import UiStatusBadge from './UiStatusBadge.vue'

const meta: Meta<typeof UiStatusBadge> = {
  title: 'Ui/StatusBadge',
  component: UiStatusBadge,
  argTypes: {
    status: {
      control: 'select',
      options: ['Healthy', 'Degraded', 'Offline'],
    },
    size: {
      control: 'select',
      options: ['sm', 'md'],
    },
  },
}

export default meta
type Story = StoryObj<typeof UiStatusBadge>

export const Healthy: Story = {
  args: {
    status: 'Healthy',
  },
}

export const Degraded: Story = {
  args: {
    status: 'Degraded',
  },
}

export const Offline: Story = {
  args: {
    status: 'Offline',
  },
}

export const Small: Story = {
  args: {
    status: 'Healthy',
    size: 'sm',
  },
}

import type { Meta, StoryObj } from '@storybook/vue3-vite'
import UiTabs from './UiTabs.vue'

const meta: Meta<typeof UiTabs> = {
  title: 'Ui/Tabs',
  component: UiTabs,
  argTypes: {
    orientation: {
      control: 'select',
      options: ['horizontal', 'vertical'],
    },
  },
}

export default meta
type Story = StoryObj<typeof UiTabs>

export const Default: Story = {
  args: {
    defaultValue: 'overview',
    tabs: [
      {
        value: 'overview',
        label: 'Overview',
        content: 'Overview panel displaying system telemetry summaries.',
      },
      {
        value: 'telemetry',
        label: 'Telemetry',
        content: 'Detailed real-time device telemetry streams.',
      },
      {
        value: 'alerts',
        label: 'Alerts',
        content: 'Active warnings and diagnostic events.',
      },
      {
        value: 'disabled',
        label: 'Disabled',
        disabled: true,
        content: 'Disabled tab content.',
      },
    ],
  },
}

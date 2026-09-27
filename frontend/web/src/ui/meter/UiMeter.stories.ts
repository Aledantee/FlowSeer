import type { Meta, StoryObj } from '@storybook/vue3-vite'
import UiMeter from './UiMeter.vue'
import UiSegmentedMeter from './UiSegmentedMeter.vue'

const meta: Meta<typeof UiMeter> = {
  title: 'Ui/Meter',
  component: UiMeter,
  argTypes: {
    label: { control: 'text' },
    value: { control: 'number' },
    tone: {
      control: 'select',
      options: ['auto', 'normal', 'warning', 'critical'],
    },
  },
}

export default meta
type Story = StoryObj<typeof UiMeter>

export const SingleNormal: Story = {
  args: {
    label: 'CPU Utilization',
    value: 42,
    detail: '4 cores active',
  },
  render: (args) => ({
    components: { UiMeter },
    setup() {
      return { args }
    },
    template: '<div class="max-w-xs"><UiMeter v-bind="args" /></div>',
  }),
}

export const SingleWarning: Story = {
  args: {
    label: 'Memory Allocation',
    value: 82,
    detail: '13.1 / 16 GB',
  },
  render: (args) => ({
    components: { UiMeter },
    setup() {
      return { args }
    },
    template: '<div class="max-w-xs"><UiMeter v-bind="args" /></div>',
  }),
}

export const SingleCritical: Story = {
  args: {
    label: 'Bandwidth Saturation',
    value: 94,
    detail: '940 / 1000 Mbps',
  },
  render: (args) => ({
    components: { UiMeter },
    setup() {
      return { args }
    },
    template: '<div class="max-w-xs"><UiMeter v-bind="args" /></div>',
  }),
}

export const SegmentedDistribution: StoryObj<typeof UiSegmentedMeter> = {
  render: () => ({
    components: { UiSegmentedMeter },
    setup() {
      const counts = { Healthy: 18, Degraded: 3, Offline: 1 }
      return { counts }
    },
    template: `
      <div class="max-w-md space-y-4">
        <UiSegmentedMeter :counts="counts" legend />
      </div>
    `,
  }),
}

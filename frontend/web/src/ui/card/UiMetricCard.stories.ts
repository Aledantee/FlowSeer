import type { Meta, StoryObj } from '@storybook/vue3-vite'
import UiMetricCard from './UiMetricCard.vue'

const meta: Meta<typeof UiMetricCard> = {
  title: 'Ui/MetricCard',
  component: UiMetricCard,
  argTypes: {
    label: { control: 'text' },
    value: { control: 'number' },
    unit: { control: 'text' },
  },
}

export default meta
type Story = StoryObj<typeof UiMetricCard>

export const Default: Story = {
  args: {
    label: 'Online Devices',
    value: 142,
    unit: 'nodes',
  },
  render: (args) => ({
    components: { UiMetricCard },
    setup() {
      return { args }
    },
    template: `
      <div class="max-w-xs">
        <UiMetricCard v-bind="args">
          98.6% availability over past 24h
        </UiMetricCard>
      </div>
    `,
  }),
}

export const WithIcon: Story = {
  args: {
    label: 'Aggregate Throughput',
    value: 840,
    unit: 'Gbps',
  },
  render: (args) => ({
    components: { UiMetricCard },
    setup() {
      return { args }
    },
    template: `
      <div class="max-w-xs">
        <UiMetricCard v-bind="args">
          <template #icon>
            <svg class="h-4 w-4 text-muted-foreground" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true">
              <path stroke-linecap="round" stroke-linejoin="round" d="M13 10V3L4 14h7v7l9-11h-7z" />
            </svg>
          </template>
          +12% vs last week peak
        </UiMetricCard>
      </div>
    `,
  }),
}

export const FormattedValue: Story = {
  args: {
    label: 'Total Packets Ingested',
    value: 1234567.8,
    unit: 'pkts/s',
  },
  render: (args) => ({
    components: { UiMetricCard },
    setup() {
      return { args }
    },
    template: `
      <div class="max-w-xs">
        <UiMetricCard v-bind="args" />
      </div>
    `,
  }),
}

export const LongText: Story = {
  args: {
    label:
      'Cumulative cross-region synchronized replication throughput across all active cluster nodes',
    value: 9876543.2,
    unit: 'megabits per second',
  },
  render: (args) => ({
    components: { UiMetricCard },
    setup() {
      return { args }
    },
    template: `
      <div class="max-w-sm">
        <UiMetricCard v-bind="args">
          Aggregated across 4 geographic regions and 12 availability zones
        </UiMetricCard>
      </div>
    `,
  }),
}

export const CustomValueText: Story = {
  args: {
    label: 'Storage Capacity',
    value: 84.5,
    unit: '%',
    valueText: (value, unit) => `${value} ${unit} utilized of 100 TB`,
  },
  render: (args) => ({
    components: { UiMetricCard },
    setup() {
      return { args }
    },
    template: `
      <div class="max-w-xs">
        <UiMetricCard v-bind="args" />
      </div>
    `,
  }),
}

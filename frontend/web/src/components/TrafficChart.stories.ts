import type { Meta, StoryObj } from '@storybook/vue3-vite'
import TrafficChart from './TrafficChart.vue'
import type { TrafficPoint } from '../domain/overview'

const samplePoints: TrafficPoint[] = Array.from({ length: 24 }, (_, i) => ({
  hour: i,
  mbps: Math.round(
    150 + 100 * Math.sin((i / 24) * Math.PI * 2) + Math.random() * 20,
  ),
}))

const meta: Meta<typeof TrafficChart> = {
  title: 'Components/TrafficChart',
  component: TrafficChart,
  argTypes: {
    label: { control: 'text' },
  },
}

export default meta
type Story = StoryObj<typeof TrafficChart>

export const Default: Story = {
  args: {
    points: samplePoints,
    label: 'Aggregate Fleet Throughput (24h)',
  },
  render: (args) => ({
    components: { TrafficChart },
    setup() {
      const chartTarget = {
        id: 'standalone:story:traffic-chart-default:chart',
        kind: 'chart',
        label: 'Aggregate Fleet Throughput (24h)',
        context: { points: String(samplePoints.length) },
      }
      return { args, chartTarget }
    },
    template: `
      <div class="p-6 bg-card border border-border rounded-panel max-w-3xl">
        <TrafficChart v-bind="args" v-ai-target="chartTarget" />
      </div>
    `,
  }),
}

export const DarkMode: Story = {
  args: {
    points: samplePoints,
    label: 'Aggregate Fleet Throughput (24h)',
  },
  parameters: {
    themes: {
      themeOverride: 'dark',
    },
  },
  render: (args) => ({
    components: { TrafficChart },
    setup() {
      return { args }
    },
    template: `
      <div data-theme="dark" class="p-6 bg-card border border-border rounded-panel max-w-3xl text-foreground">
        <TrafficChart v-bind="args" />
      </div>
    `,
  }),
}

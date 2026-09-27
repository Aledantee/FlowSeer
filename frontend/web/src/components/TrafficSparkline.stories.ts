import type { Meta, StoryObj } from '@storybook/vue3-vite'
import TrafficSparkline from './TrafficSparkline.vue'

const sampleValues = [12, 19, 34, 45, 30, 22, 65, 80, 72, 60, 48, 55, 90]

const meta: Meta<typeof TrafficSparkline> = {
  title: 'Components/TrafficSparkline',
  component: TrafficSparkline,
  argTypes: {
    color: {
      control: 'select',
      options: [
        'chart-1',
        'chart-2',
        'chart-3',
        'chart-4',
        'chart-5',
        'chart-6',
      ],
    },
  },
}

export default meta
type Story = StoryObj<typeof TrafficSparkline>

export const Chart1: Story = {
  args: {
    values: sampleValues,
    label: 'Primary Series (Chart 1 - Cyan)',
    color: 'chart-1',
  },
  render: (args) => ({
    components: { TrafficSparkline },
    setup() {
      return { args }
    },
    template:
      '<div class="w-60 h-12 bg-card p-2 border border-border rounded-control"><TrafficSparkline v-bind="args" /></div>',
  }),
}

export const AllColorVariants: Story = {
  render: () => ({
    components: { TrafficSparkline },
    setup() {
      const variants = [
        { color: 'chart-1' as const, label: 'chart-1 (Cyan)' },
        { color: 'chart-2' as const, label: 'chart-2 (Coral)' },
        { color: 'chart-3' as const, label: 'chart-3 (Violet)' },
        { color: 'chart-4' as const, label: 'chart-4 (Green)' },
        { color: 'chart-5' as const, label: 'chart-5 (Amber)' },
        { color: 'chart-6' as const, label: 'chart-6 (Blue)' },
      ]
      // Each series is a distinct target, so a composite story addresses one
      // sparkline rather than the group.
      const seriesTarget = (v: (typeof variants)[number]) => ({
        id: `standalone:story:traffic-sparkline-variants:${v.color}`,
        kind: 'chart',
        label: v.label,
        context: { color: v.color, samples: String(sampleValues.length) },
      })
      return { variants, values: sampleValues, seriesTarget }
    },
    template: `
      <div class="space-y-4 max-w-sm">
        <div
          v-for="v in variants"
          :key="v.color"
          class="space-y-1"
          v-ai-target="seriesTarget(v)"
        >
          <div class="text-xs text-muted-foreground">{{ v.label }}</div>
          <div class="w-60 h-12 bg-card p-2 border border-border rounded-control">
            <TrafficSparkline :values="values" :label="v.label" :color="v.color" />
          </div>
        </div>
      </div>
    `,
  }),
}

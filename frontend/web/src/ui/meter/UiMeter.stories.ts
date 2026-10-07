import type { Meta, StoryObj } from '@storybook/vue3-vite'
import { ref } from 'vue'
import type { AiOriginRequest } from '../ai/context'
import UiAiLabel from '../ai/UiAiLabel.vue'
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

export const FormattedValues: Story = {
  args: {
    label: 'Heap Memory Allocation',
    value: 1234.5,
    unit: 'MB',
  },
  render: (args) => ({
    components: { UiMeter },
    setup() {
      return { args }
    },
    template: '<div class="max-w-xs"><UiMeter v-bind="args" /></div>',
  }),
}

export const LongText: Story = {
  args: {
    label:
      'Long-term aggregate processor utilization across all computing clusters and tenant environments',
    value: 78.4,
    detail: '32 active physical cores and 64 virtual threads assigned',
  },
  render: (args) => ({
    components: { UiMeter },
    setup() {
      return { args }
    },
    template: '<div class="max-w-md"><UiMeter v-bind="args" /></div>',
  }),
}

export const SegmentedOverrides: StoryObj<typeof UiSegmentedMeter> = {
  render: () => ({
    components: { UiSegmentedMeter },
    setup() {
      const counts = { Healthy: 120, Degraded: 14, Offline: 2 }
      const labels = { Healthy: 'Operational', Degraded: 'Warning' }
      return { counts, labels }
    },
    template: `
      <div class="max-w-md space-y-4">
        <UiSegmentedMeter :counts="counts" :labels="labels" legend />
      </div>
    `,
  }),
}

// The outline marks a value an agent changed until the user touches it. The
// explanation sits beside the meter, and the caller clears both through the
// typed event.
export const AgentChanged: Story = {
  render: () => ({
    components: { UiMeter, UiAiLabel },
    setup() {
      const target = {
        id: 'standalone:story:ui-meter:cpu',
        kind: 'metric',
        label: 'CPU Utilization',
        context: { value: '42' },
      }
      const origin = ref<AiOriginRequest | undefined>({
        requestId: 'req-story',
        action: 'summary',
        targets: [],
        history: [],
      })
      return { target, origin }
    },
    template: `
      <div class="max-w-xs flex items-end gap-2">
        <div class="flex-1">
          <UiMeter
            label="CPU Utilization"
            :value="42"
            :ai="target"
            :ai-origin="origin"
            @ai-origin-acknowledged="origin = undefined"
          />
        </div>
        <UiAiLabel v-if="origin" :request="origin" />
      </div>
    `,
  }),
}

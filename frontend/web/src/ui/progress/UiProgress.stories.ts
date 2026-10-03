import type { Meta, StoryObj } from '@storybook/vue3-vite'
import UiProgress from './UiProgress.vue'

const meta: Meta<typeof UiProgress> = {
  title: 'Ui/Progress',
  component: UiProgress,
  argTypes: {
    modelValue: { control: 'number' },
    size: {
      control: 'select',
      options: ['sm', 'md', 'lg'],
    },
    variant: {
      control: 'select',
      options: ['default', 'accent', 'success', 'warning', 'danger'],
    },
  },
}

export default meta
type Story = StoryObj<typeof UiProgress>

export const Determinate: Story = {
  args: {
    modelValue: 65,
    variant: 'default',
    size: 'md',
  },
  render: (args) => ({
    components: { UiProgress },
    setup() {
      return { args }
    },
    template: '<div class="max-w-md"><UiProgress v-bind="args" /></div>',
  }),
}

export const Indeterminate: Story = {
  args: {
    modelValue: null,
    variant: 'accent',
    size: 'md',
  },
  render: (args) => ({
    components: { UiProgress },
    setup() {
      return { args }
    },
    template: '<div class="max-w-md"><UiProgress v-bind="args" /></div>',
  }),
}

export const Variants: Story = {
  render: () => ({
    components: { UiProgress },
    template: `
      <div class="max-w-md space-y-4">
        <UiProgress :model-value="80" variant="default" />
        <UiProgress :model-value="60" variant="accent" />
        <UiProgress :model-value="100" variant="success" />
        <UiProgress :model-value="40" variant="warning" />
        <UiProgress :model-value="20" variant="danger" />
      </div>
    `,
  }),
}

export const CustomValueText: Story = {
  args: {
    modelValue: 35,
    max: 50,
    ariaLabel: 'Cluster Node Sync Progress',
    valueText: (val: number | null | undefined, max: number) =>
      `${val} of ${max} nodes converged`,
  },
  render: (args) => ({
    components: { UiProgress },
    setup() {
      return { args }
    },
    template: '<div class="max-w-md"><UiProgress v-bind="args" /></div>',
  }),
}

export const LongText: Story = {
  args: {
    modelValue: 72,
    max: 100,
    ariaLabel:
      'Global Telemetry Route Convergence and Buffer Synchronization Level for Metropolitan Core Transit Exchange Tier-IV Node Alpha',
    valueText: (val: number | null | undefined, max: number) =>
      `Telemetry buffer capacity at ${val}% of total allocated threshold (${max}% maximum capacity)`,
  },
  render: (args) => ({
    components: { UiProgress },
    setup() {
      return { args }
    },
    template: '<div class="max-w-md"><UiProgress v-bind="args" /></div>',
  }),
}

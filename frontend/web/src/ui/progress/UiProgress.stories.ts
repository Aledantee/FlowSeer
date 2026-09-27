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

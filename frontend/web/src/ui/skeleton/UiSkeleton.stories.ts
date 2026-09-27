import type { Meta, StoryObj } from '@storybook/vue3-vite'
import UiSkeleton from './UiSkeleton.vue'

const meta: Meta<typeof UiSkeleton> = {
  title: 'Ui/Skeleton',
  component: UiSkeleton,
  argTypes: {
    variant: {
      control: 'select',
      options: ['text', 'circular', 'rectangular'],
    },
  },
}

export default meta
type Story = StoryObj<typeof UiSkeleton>

export const Text: Story = {
  render: () => ({
    components: { UiSkeleton },
    template: `
      <div class="space-y-2 max-w-sm">
        <UiSkeleton variant="text" />
        <UiSkeleton variant="text" width="80%" />
        <UiSkeleton variant="text" width="60%" />
      </div>
    `,
  }),
}

export const Circular: Story = {
  args: {
    variant: 'circular',
    width: 48,
    height: 48,
  },
  render: (args) => ({
    components: { UiSkeleton },
    setup() {
      return { args }
    },
    template: '<UiSkeleton v-bind="args" />',
  }),
}

export const Rectangular: Story = {
  args: {
    variant: 'rectangular',
    width: '100%',
    height: 120,
  },
  render: (args) => ({
    components: { UiSkeleton },
    setup() {
      return { args }
    },
    template: '<UiSkeleton v-bind="args" />',
  }),
}

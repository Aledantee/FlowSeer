import type { Meta, StoryObj } from '@storybook/vue3-vite'
import UiSeparator from './UiSeparator.vue'

const meta: Meta<typeof UiSeparator> = {
  title: 'Ui/Separator',
  component: UiSeparator,
  argTypes: {
    orientation: {
      control: 'select',
      options: ['horizontal', 'vertical'],
    },
    decorative: { control: 'boolean' },
  },
}

export default meta
type Story = StoryObj<typeof UiSeparator>

export const Horizontal: Story = {
  args: {
    orientation: 'horizontal',
  },
  render: (args) => ({
    components: { UiSeparator },
    setup() {
      return { args }
    },
    template: `
      <div class="w-64 space-y-3 p-4 bg-card border border-border rounded-panel text-sm text-foreground">
        <div>Header content</div>
        <UiSeparator v-bind="args" />
        <div>Footer content</div>
      </div>
    `,
  }),
}

export const Vertical: Story = {
  args: {
    orientation: 'vertical',
  },
  render: (args) => ({
    components: { UiSeparator },
    setup() {
      return { args }
    },
    template: `
      <div class="flex items-center h-10 space-x-3 p-4 bg-card border border-border rounded-panel text-sm text-foreground">
        <span>Left</span>
        <UiSeparator v-bind="args" />
        <span>Right</span>
      </div>
    `,
  }),
}

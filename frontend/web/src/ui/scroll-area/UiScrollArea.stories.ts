import type { Meta, StoryObj } from '@storybook/vue3-vite'
import UiScrollArea from './UiScrollArea.vue'

const meta: Meta<typeof UiScrollArea> = {
  title: 'Ui/ScrollArea',
  component: UiScrollArea,
  argTypes: {
    axis: {
      control: 'select',
      options: ['y', 'x', 'both'],
    },
  },
}

export default meta
type Story = StoryObj<typeof UiScrollArea>

export const Vertical: Story = {
  args: {
    axis: 'y',
  },
  render: (args) => ({
    components: { UiScrollArea },
    setup() {
      return { args }
    },
    template: `
      <UiScrollArea v-bind="args" class="h-48 w-64 border border-border rounded-control p-4">
        <div class="space-y-4">
          <h4 class="text-sm font-semibold">Tags</h4>
          <div v-for="tag in 20" :key="tag" class="text-sm text-muted-foreground border-b border-border py-1">
            v1.2.0-beta.{{ tag }}
          </div>
        </div>
      </UiScrollArea>
    `,
  }),
}

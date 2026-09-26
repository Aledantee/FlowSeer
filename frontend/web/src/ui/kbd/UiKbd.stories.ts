import type { Meta, StoryObj } from '@storybook/vue3-vite'
import UiKbd from './UiKbd.vue'

const meta: Meta<typeof UiKbd> = {
  title: 'Ui/Kbd',
  component: UiKbd,
}

export default meta
type Story = StoryObj<typeof UiKbd>

export const Default: Story = {
  render: () => ({
    components: { UiKbd },
    template: `
      <div class="flex items-center gap-2">
        <UiKbd>⌘</UiKbd>
        <UiKbd>K</UiKbd>
      </div>
    `,
  }),
}

import type { Meta, StoryObj } from '@storybook/vue3-vite'
import UiPopover from './UiPopover.vue'
import UiButton from '../button/UiButton.vue'

const meta: Meta<typeof UiPopover> = {
  title: 'Ui/Popover',
  component: UiPopover,
  argTypes: {
    side: {
      control: 'select',
      options: ['top', 'right', 'bottom', 'left'],
    },
    align: {
      control: 'select',
      options: ['start', 'center', 'end'],
    },
  },
}

export default meta
type Story = StoryObj<typeof UiPopover>

export const Default: Story = {
  args: {
    side: 'bottom',
    align: 'center',
  },
  render: (args) => ({
    components: { UiPopover, UiButton },
    setup() {
      return { args }
    },
    template: `
      <UiPopover v-bind="args">
        <template #trigger>
          <UiButton variant="secondary">Open Popover</UiButton>
        </template>
        <div class="flex flex-col gap-2">
          <p class="text-sm font-semibold">Quick Info</p>
          <p class="text-xs text-muted-foreground">Detailed telemetry parameters for current gateway.</p>
        </div>
      </UiPopover>
    `,
  }),
}

export const Open: Story = {
  args: {
    side: 'bottom',
    align: 'center',
    defaultOpen: true,
  },
  render: (args) => ({
    components: { UiPopover, UiButton },
    setup() {
      return { args }
    },
    template: `
      <UiPopover v-bind="args">
        <template #trigger>
          <UiButton variant="secondary">Open Popover</UiButton>
        </template>
        <div class="flex flex-col gap-2">
          <p class="text-sm font-semibold">Quick Info</p>
          <p class="text-xs text-muted-foreground">Detailed telemetry parameters for current gateway.</p>
        </div>
      </UiPopover>
    `,
  }),
}

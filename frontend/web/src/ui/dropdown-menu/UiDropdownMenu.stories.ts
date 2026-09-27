import type { Meta, StoryObj } from '@storybook/vue3-vite'
import UiDropdownMenu from './UiDropdownMenu.vue'
import UiDropdownMenuItem from './UiDropdownMenuItem.vue'
import UiDropdownMenuSeparator from './UiDropdownMenuSeparator.vue'
import UiButton from '../button/UiButton.vue'

const meta: Meta<typeof UiDropdownMenu> = {
  title: 'Ui/DropdownMenu',
  component: UiDropdownMenu,
}

export default meta
type Story = StoryObj<typeof UiDropdownMenu>

export const Default: Story = {
  render: () => ({
    components: {
      UiDropdownMenu,
      UiDropdownMenuItem,
      UiDropdownMenuSeparator,
      UiButton,
    },
    template: `
      <UiDropdownMenu>
        <template #trigger>
          <UiButton variant="secondary">Open Options</UiButton>
        </template>
        <UiDropdownMenuItem>Account settings</UiDropdownMenuItem>
        <UiDropdownMenuItem>Billing & invoices</UiDropdownMenuItem>
        <UiDropdownMenuSeparator />
        <UiDropdownMenuItem disabled>Log out (disabled)</UiDropdownMenuItem>
      </UiDropdownMenu>
    `,
  }),
}

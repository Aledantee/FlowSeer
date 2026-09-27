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
  render: (args) => ({
    components: {
      UiDropdownMenu,
      UiDropdownMenuItem,
      UiDropdownMenuSeparator,
      UiButton,
    },
    setup() {
      return { args }
    },
    template: `
      <UiDropdownMenu v-bind="args">
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

export const AccessibilityAudit: Story = {
  name: 'Accessibility Audit (Open, Nonmodal)',
  render: () => ({
    components: {
      UiDropdownMenu,
      UiDropdownMenuItem,
      UiDropdownMenuSeparator,
    },
    template: `
      <UiDropdownMenu :open="true" :modal="false">
        <UiDropdownMenuItem>Account settings</UiDropdownMenuItem>
        <UiDropdownMenuItem>Billing & invoices</UiDropdownMenuItem>
        <UiDropdownMenuSeparator />
        <UiDropdownMenuItem disabled>Log out (disabled)</UiDropdownMenuItem>
      </UiDropdownMenu>
    `,
  }),
}

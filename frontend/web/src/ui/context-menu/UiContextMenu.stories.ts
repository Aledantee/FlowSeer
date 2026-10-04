import type { Meta, StoryObj } from '@storybook/vue3-vite'
import UiContextMenu from './UiContextMenu.vue'
import UiContextMenuItem from './UiContextMenuItem.vue'
import UiContextMenuSeparator from './UiContextMenuSeparator.vue'

const meta: Meta<typeof UiContextMenu> = {
  title: 'Ui/ContextMenu',
  component: UiContextMenu,
}

export default meta
type Story = StoryObj<typeof UiContextMenu>

export const Default: Story = {
  render: (args) => ({
    components: {
      UiContextMenu,
      UiContextMenuItem,
      UiContextMenuSeparator,
    },
    setup() {
      return { args }
    },
    template: `
      <UiContextMenu v-bind="args">
        <template #trigger>
          <div class="flex items-center justify-center p-12 border border-dashed border-border rounded-control text-sm text-muted-foreground select-none">
            Right click here
          </div>
        </template>
        <UiContextMenuItem label="Back" hint="⌘[" />
        <UiContextMenuItem label="Forward" hint="⌘]" disabled />
        <UiContextMenuItem label="Reload" hint="⌘R" />
        <UiContextMenuSeparator />
        <UiContextMenuItem label="Inspect Element" hint="⌥⌘I" />
      </UiContextMenu>
    `,
  }),
}

export const AccessibilityAudit: Story = {
  name: 'Accessibility Audit (Open, Nonmodal)',
  render: () => ({
    components: {
      UiContextMenu,
      UiContextMenuItem,
      UiContextMenuSeparator,
    },
    template: `
      <UiContextMenu :modal="false">
        <template #trigger>
          <button
            data-context-menu-trigger
            type="button"
            class="flex items-center justify-center p-12 border border-dashed border-border rounded-control text-sm text-muted-foreground"
          >
            Right click for context menu
          </button>
        </template>
        <UiContextMenuItem label="Back" hint="⌘[" />
        <UiContextMenuItem label="Forward" hint="⌘]" disabled />
        <UiContextMenuItem label="Reload" hint="⌘R" />
        <UiContextMenuSeparator />
        <UiContextMenuItem label="Inspect Element" hint="⌥⌘I" />
      </UiContextMenu>
    `,
  }),
}

import type { Meta, StoryObj } from '@storybook/vue3-vite'
import { ref } from 'vue'
import UiCommand from './UiCommand.vue'
import UiCommandDialog from './UiCommandDialog.vue'
import UiCommandInput from './UiCommandInput.vue'
import UiCommandList from './UiCommandList.vue'
import UiCommandEmpty from './UiCommandEmpty.vue'
import UiCommandGroup from './UiCommandGroup.vue'
import UiCommandItem from './UiCommandItem.vue'
import UiCommandSeparator from './UiCommandSeparator.vue'
import UiCommandShortcut from './UiCommandShortcut.vue'
import UiButton from '../button/UiButton.vue'

const meta: Meta<typeof UiCommand> = {
  title: 'Ui/Command',
  component: UiCommand,
}

export default meta
type Story = StoryObj<typeof UiCommand>

export const Default: Story = {
  render: () => ({
    components: {
      UiCommand,
      UiCommandInput,
      UiCommandList,
      UiCommandEmpty,
      UiCommandGroup,
      UiCommandItem,
      UiCommandSeparator,
      UiCommandShortcut,
    },
    template: `
      <div class="w-[450px] rounded-panel border border-border bg-popover shadow-md overflow-hidden">
        <UiCommand>
          <UiCommandInput placeholder="Type a command or search..." />
          <UiCommandList>
            <UiCommandEmpty>No results found.</UiCommandEmpty>
            <UiCommandGroup heading="Suggestions">
              <UiCommandItem value="calendar">
                <span>Calendar</span>
              </UiCommandItem>
              <UiCommandItem value="search-emoji">
                <span>Search Emoji</span>
              </UiCommandItem>
              <UiCommandItem value="calculator">
                <span>Calculator</span>
              </UiCommandItem>
            </UiCommandGroup>
            <UiCommandSeparator />
            <UiCommandGroup heading="Settings">
              <UiCommandItem value="profile">
                <span>Profile</span>
                <UiCommandShortcut>⌘P</UiCommandShortcut>
              </UiCommandItem>
              <UiCommandItem value="billing">
                <span>Billing</span>
                <UiCommandShortcut>⌘B</UiCommandShortcut>
              </UiCommandItem>
              <UiCommandItem value="settings">
                <span>Settings</span>
                <UiCommandShortcut>⌘S</UiCommandShortcut>
              </UiCommandItem>
            </UiCommandGroup>
          </UiCommandList>
        </UiCommand>
      </div>
    `,
  }),
}

export const DialogMode: Story = {
  render: () => ({
    components: {
      UiCommandDialog,
      UiCommandInput,
      UiCommandList,
      UiCommandEmpty,
      UiCommandGroup,
      UiCommandItem,
      UiCommandSeparator,
      UiCommandShortcut,
      UiButton,
    },
    setup() {
      const open = ref(false)
      return { open }
    },
    template: `
      <div>
        <UiButton @click="open = true">Open Command Palette</UiButton>
        <UiCommandDialog v-model:open="open">
          <UiCommandInput placeholder="Search everything..." />
          <UiCommandList>
            <UiCommandEmpty>No results found.</UiCommandEmpty>
            <UiCommandGroup heading="Quick Actions">
              <UiCommandItem value="new-device" @select="open = false">
                <span>Add new device</span>
                <UiCommandShortcut>⌘N</UiCommandShortcut>
              </UiCommandItem>
              <UiCommandItem value="toggle-theme" @select="open = false">
                <span>Toggle theme</span>
                <UiCommandShortcut>⌘T</UiCommandShortcut>
              </UiCommandItem>
            </UiCommandGroup>
          </UiCommandList>
        </UiCommandDialog>
      </div>
    `,
  }),
}

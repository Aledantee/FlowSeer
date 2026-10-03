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
      <div class="w-full max-w-[450px] rounded-panel border border-border bg-popover shadow-md overflow-hidden">
        <UiCommand>
          <UiCommandInput />
          <UiCommandList>
            <UiCommandEmpty />
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

function renderDialogMode(initiallyOpen: boolean, showTrigger: boolean) {
  return {
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
      const open = ref(initiallyOpen)
      return { open, showTrigger }
    },
    template: `
      <div>
        <UiButton v-if="showTrigger" @click="open = true">Open Command Palette</UiButton>
        <UiCommandDialog v-model:open="open">
          <UiCommandInput />
          <UiCommandList>
            <UiCommandEmpty />
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
  }
}

export const DialogMode: Story = {
  render: () => renderDialogMode(false, true),
}

export const AccessibilityAudit: Story = {
  name: 'Dialog Accessibility Audit (Open)',
  render: () => renderDialogMode(true, false),
}

export const DialogModeOpen: Story = {
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
    },
    setup() {
      const open = ref(true)
      return { open }
    },
    template: `
      <div>
        <UiCommandDialog v-model:open="open">
          <UiCommandInput />
          <UiCommandList>
            <UiCommandEmpty />
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

export const LongText: Story = {
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
    },
    setup() {
      const open = ref(true)
      return { open }
    },
    template: `
      <div>
        <UiCommandDialog
          v-model:open="open"
          title="Global Autonomous Core Fabric Orchestration and Operational Diagnostics Palette"
          description="Execute comprehensive telemetry actions, inspect live routing tables, adjust interface thresholds, and coordinate multi-chassis switch failovers across European facilities."
        >
          <UiCommandInput
            placeholder="Search across all global fabric telemetry operations, switch actions, and routing diagnostics..."
            label="Filter global operational commands"
          />
          <UiCommandList label="Available Autonomous Fabric Operations">
            <UiCommandEmpty text="No matching operational commands or diagnostics found for your search criteria." />
            <UiCommandGroup heading="Critical Fabric Reconfiguration Operations">
              <UiCommandItem value="evacuate-routes" @select="open = false">
                <span>Evacuate all dynamic peer routes from secondary backbone router</span>
                <UiCommandShortcut>Ctrl+Shift+E</UiCommandShortcut>
              </UiCommandItem>
              <UiCommandItem value="restart-collector" @select="open = false">
                <span>Gracefully restart telemetry collector microservices on cluster nodes</span>
                <UiCommandShortcut>Ctrl+Shift+R</UiCommandShortcut>
              </UiCommandItem>
            </UiCommandGroup>
          </UiCommandList>
        </UiCommandDialog>
      </div>
    `,
  }),
}

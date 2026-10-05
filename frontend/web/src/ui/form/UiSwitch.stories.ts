import { ref } from 'vue'
import type { Meta, StoryObj } from '@storybook/vue3-vite'
import type { AiTarget } from '../../ai'
import type { AiOriginRequest } from '../ai/context'
import UiSwitch from './UiSwitch.vue'

const meta: Meta<typeof UiSwitch> = {
  title: 'Ui/Switch',
  component: UiSwitch,
  argTypes: {
    modelValue: { control: 'boolean' },
    disabled: { control: 'boolean' },
    required: { control: 'boolean' },
  },
}

export default meta
type Story = StoryObj<typeof UiSwitch>

export const Default: Story = {
  args: {
    modelValue: false,
    id: 'switch-default',
  },
  render: (args) => ({
    components: { UiSwitch },
    setup() {
      return { args }
    },
    template: `
      <div class="flex items-center gap-3">
        <UiSwitch v-bind="args" />
        <label for="switch-default" class="text-sm font-medium text-foreground cursor-pointer select-none">
          Live stream topology
        </label>
      </div>
    `,
  }),
}

export const Checked: Story = {
  args: {
    modelValue: true,
    id: 'switch-checked',
  },
  render: (args) => ({
    components: { UiSwitch },
    setup() {
      return { args }
    },
    template: `
      <div class="flex items-center gap-3">
        <UiSwitch v-bind="args" />
        <label for="switch-checked" class="text-sm font-medium text-foreground cursor-pointer select-none">
          Dark theme override
        </label>
      </div>
    `,
  }),
}

export const Disabled: Story = {
  args: {
    modelValue: true,
    disabled: true,
    id: 'switch-disabled',
  },
  render: (args) => ({
    components: { UiSwitch },
    setup() {
      return { args }
    },
    template: `
      <div class="flex items-center gap-3">
        <UiSwitch v-bind="args" />
        <label for="switch-disabled" class="text-sm font-medium text-muted-foreground cursor-not-allowed select-none">
          Managed by policy
        </label>
      </div>
    `,
  }),
}

export const Focus: Story = {
  args: {
    id: 'switch-focus',
  },
  render: (args) => ({
    components: { UiSwitch },
    setup() {
      return { args }
    },
    template: `
      <div class="flex items-center gap-3">
        <UiSwitch v-bind="args" autofocus />
        <label for="switch-focus" class="text-sm font-medium text-foreground cursor-pointer select-none">
          Focused switch
        </label>
      </div>
    `,
  }),
}

const origin: AiOriginRequest = {
  requestId: 'req-story-switch',
  action: 'summary',
  targets: [],
  history: [],
}

// Each story keeps its own target id because Storybook Docs mounts several
// canvases into one document.
function storyTarget(state: string): AiTarget {
  return {
    id: `standalone:story:ui-switch:${state}`,
    kind: 'control',
    label: 'Live stream topology',
    context: { state },
  }
}

// The outline marks a value an agent changed until the user touches it. The
// caller clears its own state when the component reports the interaction.
export const AgentChanged: Story = {
  args: {
    modelValue: true,
    id: 'switch-agent',
    ai: storyTarget('agent-changed'),
  },
  render: (args) => ({
    components: { UiSwitch },
    setup() {
      const changed = ref<AiOriginRequest | undefined>(origin)
      return { args, changed }
    },
    template: `
      <div class="flex items-center gap-3">
        <UiSwitch
          v-bind="args"
          :ai-origin="changed"
          @ai-origin-acknowledged="changed = undefined"
        />
        <label for="switch-agent" class="text-sm font-medium text-foreground cursor-pointer select-none">
          Live stream topology
        </label>
      </div>
    `,
  }),
}

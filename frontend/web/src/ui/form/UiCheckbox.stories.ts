import { ref } from 'vue'
import type { Meta, StoryObj } from '@storybook/vue3-vite'
import type { AiTarget } from '../../ai'
import type { AiOriginRequest } from '../ai/context'
import UiCheckbox from './UiCheckbox.vue'

const meta: Meta<typeof UiCheckbox> = {
  title: 'Ui/Checkbox',
  component: UiCheckbox,
  argTypes: {
    modelValue: { control: 'select', options: [false, true, 'indeterminate'] },
    disabled: { control: 'boolean' },
    required: { control: 'boolean' },
  },
}

export default meta
type Story = StoryObj<typeof UiCheckbox>

export const Default: Story = {
  args: {
    modelValue: false,
    id: 'chk-default',
  },
  render: (args) => ({
    components: { UiCheckbox },
    setup() {
      return { args }
    },
    template: `
      <div class="flex items-center gap-2">
        <UiCheckbox v-bind="args" />
        <label for="chk-default" class="text-sm font-medium text-foreground cursor-pointer select-none">
          Enable alerts
        </label>
      </div>
    `,
  }),
}

export const Checked: Story = {
  args: {
    modelValue: true,
    id: 'chk-checked',
  },
  render: (args) => ({
    components: { UiCheckbox },
    setup() {
      return { args }
    },
    template: `
      <div class="flex items-center gap-2">
        <UiCheckbox v-bind="args" />
        <label for="chk-checked" class="text-sm font-medium text-foreground cursor-pointer select-none">
          Auto-refresh metrics
        </label>
      </div>
    `,
  }),
}

export const Indeterminate: Story = {
  args: {
    modelValue: 'indeterminate',
    id: 'chk-indeterminate',
  },
  render: (args) => ({
    components: { UiCheckbox },
    setup() {
      return { args }
    },
    template: `
      <div class="flex items-center gap-2">
        <UiCheckbox v-bind="args" />
        <label for="chk-indeterminate" class="text-sm font-medium text-foreground cursor-pointer select-none">
          Select all devices (partially selected)
        </label>
      </div>
    `,
  }),
}

export const Disabled: Story = {
  args: {
    modelValue: true,
    disabled: true,
    id: 'chk-disabled',
  },
  render: (args) => ({
    components: { UiCheckbox },
    setup() {
      return { args }
    },
    template: `
      <div class="flex items-center gap-2">
        <UiCheckbox v-bind="args" />
        <label for="chk-disabled" class="text-sm font-medium text-muted-foreground cursor-not-allowed select-none">
          Read-only setting
        </label>
      </div>
    `,
  }),
}

export const Focus: Story = {
  args: {
    id: 'chk-focus',
  },
  render: (args) => ({
    components: { UiCheckbox },
    setup() {
      return { args }
    },
    template: `
      <div class="flex items-center gap-2">
        <UiCheckbox v-bind="args" autofocus />
        <label for="chk-focus" class="text-sm font-medium text-foreground cursor-pointer select-none">
          Focused checkbox
        </label>
      </div>
    `,
  }),
}

const origin: AiOriginRequest = {
  requestId: 'req-story-checkbox',
  action: 'summary',
  targets: [],
  history: [],
}

// Each story keeps its own target id because Storybook Docs mounts several
// canvases into one document.
function storyTarget(state: string): AiTarget {
  return {
    id: `standalone:story:ui-checkbox:${state}`,
    kind: 'control',
    label: 'Enable alerts',
    context: { state },
  }
}

// The outline marks a value an agent changed until the user touches it. The
// caller clears its own state when the component reports the interaction.
export const AgentChanged: Story = {
  args: { modelValue: true, id: 'chk-agent', ai: storyTarget('agent-changed') },
  render: (args) => ({
    components: { UiCheckbox },
    setup() {
      const changed = ref<AiOriginRequest | undefined>(origin)
      return { args, changed }
    },
    template: `
      <div class="flex items-center gap-2">
        <UiCheckbox
          v-bind="args"
          :ai-origin="changed"
          @ai-origin-acknowledged="changed = undefined"
        />
        <label for="chk-agent" class="text-sm font-medium text-foreground cursor-pointer select-none">
          Enable alerts
        </label>
      </div>
    `,
  }),
}

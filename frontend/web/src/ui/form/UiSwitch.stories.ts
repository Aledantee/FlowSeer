import type { Meta, StoryObj } from '@storybook/vue3-vite'
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

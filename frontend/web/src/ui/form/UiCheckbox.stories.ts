import type { Meta, StoryObj } from '@storybook/vue3-vite'
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

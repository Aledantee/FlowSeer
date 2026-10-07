import type { Meta, StoryObj } from '@storybook/vue3-vite'
import UiTooltip from './UiTooltip.vue'
import UiButton from '../button/UiButton.vue'

const meta: Meta<typeof UiTooltip> = {
  title: 'Ui/Tooltip',
  component: UiTooltip,
  argTypes: {
    label: { control: 'text' },
    hint: { control: 'text' },
    side: {
      control: 'select',
      options: ['top', 'right', 'bottom', 'left'],
    },
  },
}

export default meta
type Story = StoryObj<typeof UiTooltip>

export const Default: Story = {
  args: {
    label: 'Search telemetry',
    hint: 'Quick lookup across all sites',
    shortcut: ['⌘', 'K'],
    side: 'bottom',
  },
  render: (args) => ({
    components: { UiTooltip, UiButton },
    setup() {
      return { args }
    },
    template: `
      <div class="p-12 flex justify-center">
        <UiTooltip v-bind="args">
          <UiButton variant="secondary">Hover me</UiButton>
        </UiTooltip>
      </div>
    `,
  }),
}

export const Open: Story = {
  args: {
    label: 'Search telemetry',
    hint: 'Quick lookup across all sites',
    shortcut: ['⌘', 'K'],
    side: 'bottom',
    defaultOpen: true,
  },
  render: (args) => ({
    components: { UiTooltip, UiButton },
    setup() {
      return { args }
    },
    template: `
      <div class="p-12 flex justify-center">
        <UiTooltip v-bind="args">
          <UiButton variant="secondary">Hover me</UiButton>
        </UiTooltip>
      </div>
    `,
  }),
}

export const ShortcutObject: Story = {
  args: {
    label: 'Search telemetry',
    hint: 'Quick lookup across all sites',
    shortcut: { code: 'KeyK', mod: true },
    side: 'bottom',
    defaultOpen: true,
  },
  render: (args) => ({
    components: { UiTooltip, UiButton },
    setup() {
      return { args }
    },
    template: `
      <div class="p-12 flex justify-center">
        <UiTooltip v-bind="args">
          <UiButton variant="secondary">Hover me</UiButton>
        </UiTooltip>
      </div>
    `,
  }),
}

export const LongText: Story = {
  args: {
    label:
      'Show detailed telemetry and performance metrics for the selected network nodes',
    hint: 'Opens the extended monitoring view with historical data',
    shortcut: { code: 'KeyK', mod: true, shift: true },
    side: 'bottom',
    defaultOpen: true,
  },
  render: (args) => ({
    components: { UiTooltip, UiButton },
    setup() {
      return { args }
    },
    template: `
      <div class="p-12 flex justify-center">
        <UiTooltip v-bind="args">
          <UiButton variant="secondary">Hover me</UiButton>
        </UiTooltip>
      </div>
    `,
  }),
}

export const AccessibilityAudit: Story = {
  args: {
    label: 'Search telemetry',
    hint: 'Quick lookup across all sites',
    shortcut: ['⌘', 'K'],
    side: 'bottom',
    defaultOpen: true,
  },
  render: (args) => ({
    components: { UiTooltip, UiButton },
    setup() {
      return { args }
    },
    template: `
      <div class="p-12 flex justify-center">
        <UiTooltip v-bind="args">
          <UiButton variant="secondary">Hover me</UiButton>
        </UiTooltip>
      </div>
    `,
  }),
}

export const Slots: Story = {
  args: {
    label: 'Search telemetry',
    hint: 'Quick lookup across all sites',
    side: 'bottom',
    defaultOpen: true,
  },
  render: (args) => ({
    components: { UiTooltip, UiButton },
    setup() {
      return { args }
    },
    template: `
      <div class="p-12 flex justify-center">
        <UiTooltip v-bind="args">
          <UiButton variant="secondary">Hover me</UiButton>
          <template #label>
            <span>Custom slot label</span>
          </template>
          <template #hint>
            <span>Custom slot hint</span>
          </template>
        </UiTooltip>
      </div>
    `,
  }),
}

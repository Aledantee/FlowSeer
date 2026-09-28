import type { Meta, StoryObj } from '@storybook/vue3-vite'
import { TooltipProvider } from 'reka-ui'
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
    components: { UiTooltip, UiButton, TooltipProvider },
    setup() {
      return { args }
    },
    template: `
      <TooltipProvider>
        <div class="p-12 flex justify-center">
          <UiTooltip v-bind="args">
            <UiButton variant="secondary">Hover me</UiButton>
          </UiTooltip>
        </div>
      </TooltipProvider>
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
    components: { UiTooltip, UiButton, TooltipProvider },
    setup() {
      return { args }
    },
    template: `
      <TooltipProvider>
        <div class="p-12 flex justify-center">
          <UiTooltip v-bind="args">
            <UiButton variant="secondary">Hover me</UiButton>
          </UiTooltip>
        </div>
      </TooltipProvider>
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
    components: { UiTooltip, UiButton, TooltipProvider },
    setup() {
      return { args }
    },
    template: `
      <TooltipProvider>
        <div class="p-12 flex justify-center">
          <UiTooltip v-bind="args">
            <UiButton variant="secondary">Hover me</UiButton>
          </UiTooltip>
        </div>
      </TooltipProvider>
    `,
  }),
}

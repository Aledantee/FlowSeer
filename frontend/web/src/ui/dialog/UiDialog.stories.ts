import type { Meta, StoryObj } from '@storybook/vue3-vite'
import UiDialog from './UiDialog.vue'
import UiButton from '../button/UiButton.vue'

const meta: Meta<typeof UiDialog> = {
  title: 'Ui/Dialog',
  component: UiDialog,
  argTypes: {
    size: {
      control: 'select',
      options: ['sm', 'md', 'lg'],
    },
    open: { control: 'boolean' },
    title: { control: 'text' },
    description: { control: 'text' },
  },
}

export default meta
type Story = StoryObj<typeof UiDialog>

export const Default: Story = {
  args: {
    title: 'Edit Workspace',
    description: 'Configure workspace settings and device access parameters.',
    size: 'md',
  },
  render: (args) => ({
    components: { UiDialog, UiButton },
    setup() {
      return { args }
    },
    template: `
      <UiDialog v-bind="args">
        <template #trigger>
          <UiButton variant="primary">Open Dialog</UiButton>
        </template>
        <p class="text-sm text-foreground">Workspace settings content goes here.</p>
        <template #footer>
          <UiButton variant="secondary">Cancel</UiButton>
          <UiButton variant="primary">Save changes</UiButton>
        </template>
      </UiDialog>
    `,
  }),
}

export const AccessibilityAudit: Story = {
  name: 'Accessibility Audit (Open)',
  args: {
    ...Default.args,
    open: true,
  },
  render: (args) => ({
    components: { UiDialog, UiButton },
    setup() {
      return { args }
    },
    template: `
      <UiDialog v-bind="args">
        <p class="text-sm text-foreground">Workspace settings content goes here.</p>
        <template #footer>
          <UiButton variant="secondary">Cancel</UiButton>
          <UiButton variant="primary">Save changes</UiButton>
        </template>
      </UiDialog>
    `,
  }),
}

export const Small: Story = {
  args: {
    title: 'Confirm Reset',
    description: 'Are you sure you want to reset preferences?',
    size: 'sm',
  },
  render: (args) => ({
    components: { UiDialog, UiButton },
    setup() {
      return { args }
    },
    template: `
      <UiDialog v-bind="args">
        <template #trigger>
          <UiButton variant="secondary">Reset</UiButton>
        </template>
        <p class="text-sm text-foreground">All temporary overrides will be cleared.</p>
      </UiDialog>
    `,
  }),
}

export const Large: Story = {
  args: {
    title: 'Topology Diagnostics',
    description: 'Extended network link topology and routing table entries.',
    size: 'lg',
  },
  render: (args) => ({
    components: { UiDialog, UiButton },
    setup() {
      return { args }
    },
    template: `
      <UiDialog v-bind="args">
        <template #trigger>
          <UiButton variant="secondary">View Details</UiButton>
        </template>
        <div class="h-40 flex items-center justify-center border border-dashed border-border rounded-control text-sm text-muted-foreground">
          Diagnostic Telemetry Chart
        </div>
      </UiDialog>
    `,
  }),
}

export const Open: Story = {
  args: {
    title: 'Edit Workspace',
    description: 'Configure workspace settings and device access parameters.',
    size: 'md',
    defaultOpen: true,
  },
  render: (args) => ({
    components: { UiDialog, UiButton },
    setup() {
      return { args }
    },
    template: `
      <UiDialog v-bind="args">
        <p class="text-sm text-foreground">Workspace settings content goes here.</p>
        <template #footer>
          <UiButton variant="secondary">Cancel</UiButton>
          <UiButton variant="primary">Save changes</UiButton>
        </template>
      </UiDialog>
    `,
  }),
}

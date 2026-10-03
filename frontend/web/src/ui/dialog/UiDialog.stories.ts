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

export const DefaultFallbacksOpen: Story = {
  name: 'Default Fallbacks (Open)',
  args: {
    defaultOpen: true,
    size: 'md',
  },
  render: (args) => ({
    components: { UiDialog, UiButton },
    setup() {
      return { args }
    },
    template: `
      <UiDialog v-bind="args">
        <p class="text-sm text-foreground">This dialog has no explicit title or description prop to exercise hidden fallback labels.</p>
        <template #footer>
          <UiButton variant="secondary">Dismiss</UiButton>
        </template>
      </UiDialog>
    `,
  }),
}

export const LongText: Story = {
  args: {
    title:
      'Global Telemetry Interconnect and Cross-Facility Routing Configuration Parameters for Autonomous Transit Exchange',
    description:
      'Modify advanced BGP routing parameters, autonomous system boundaries, transit path cost metrics, packet sampling multipliers, and persistent error-budget alerting thresholds across European and North American facilities.',
    size: 'lg',
    defaultOpen: true,
  },
  render: (args) => ({
    components: { UiDialog, UiButton },
    setup() {
      return { args }
    },
    template: `
      <UiDialog v-bind="args">
        <div class="space-y-3 text-sm text-muted-foreground">
          <p>
            Detailed routing configuration profiles will be committed synchronously to all participating border gateway controllers upon confirmation.
          </p>
          <p>
            Ensure that adjacent downstream peer transit fabrics have acknowledged maintenance windows before proceeding with live route convergence.
          </p>
        </div>
        <template #footer>
          <UiButton variant="secondary">Cancel Reconfiguration</UiButton>
          <UiButton variant="primary">Commit Autonomous Policy Changes</UiButton>
        </template>
      </UiDialog>
    `,
  }),
}

export const RightSheet: Story = {
  args: {
    title: 'Assistant Sheet',
    description:
      'Autonomous reasoning diagnostics and fleet overview assistance.',
    side: 'right',
    size: 'sm',
    defaultOpen: true,
  },
  render: (args) => ({
    components: { UiDialog, UiButton },
    setup() {
      return { args }
    },
    template: `
      <UiDialog v-bind="args">
        <p class="text-sm text-foreground">Sheet body content aligned to the right viewport edge.</p>
        <template #footer>
          <UiButton variant="primary">Dismiss</UiButton>
        </template>
      </UiDialog>
    `,
  }),
}

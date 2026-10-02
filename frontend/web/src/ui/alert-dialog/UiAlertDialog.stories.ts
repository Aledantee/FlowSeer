import type { Meta, StoryObj } from '@storybook/vue3-vite'
import UiAlertDialog from './UiAlertDialog.vue'
import UiButton from '../button/UiButton.vue'

const meta: Meta<typeof UiAlertDialog> = {
  title: 'Ui/AlertDialog',
  component: UiAlertDialog,
  argTypes: {
    destructive: { control: 'boolean' },
    title: { control: 'text' },
    description: { control: 'text' },
    confirmText: { control: 'text' },
    cancelText: { control: 'text' },
  },
}

export default meta
type Story = StoryObj<typeof UiAlertDialog>

export const Default: Story = {
  args: {
    title: 'Decommission Device',
    description:
      'This will remove the selected gateway from active monitoring and flush cached routes.',
    confirmText: 'Decommission',
    cancelText: 'Cancel',
    destructive: true,
  },
  render: (args) => ({
    components: { UiAlertDialog, UiButton },
    setup() {
      return { args }
    },
    template: `
      <UiAlertDialog v-bind="args">
        <template #trigger>
          <UiButton variant="danger">Delete Device</UiButton>
        </template>
      </UiAlertDialog>
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
    components: { UiAlertDialog },
    setup() {
      return { args }
    },
    template: '<UiAlertDialog v-bind="args" />',
  }),
}

export const NonDestructive: Story = {
  args: {
    title: 'Restart Collector Service',
    description:
      'Are you sure you want to restart telemetry ingestion? Live metrics may pause briefly.',
    confirmText: 'Restart',
    cancelText: 'Keep Running',
    destructive: false,
  },
  render: (args) => ({
    components: { UiAlertDialog, UiButton },
    setup() {
      return { args }
    },
    template: `
      <UiAlertDialog v-bind="args">
        <template #trigger>
          <UiButton variant="secondary">Restart Collector</UiButton>
        </template>
      </UiAlertDialog>
    `,
  }),
}

export const Open: Story = {
  args: {
    title: 'Decommission Device',
    description:
      'This will remove the selected gateway from active monitoring and flush cached routes.',
    confirmText: 'Decommission',
    cancelText: 'Cancel',
    destructive: true,
    defaultOpen: true,
  },
  render: (args) => ({
    components: { UiAlertDialog },
    setup() {
      return { args }
    },
    template: `
      <UiAlertDialog v-bind="args" />
    `,
  }),
}

export const DefaultLabelsOpen: Story = {
  name: 'Default Labels (Open)',
  args: {
    title: 'Discard Changes',
    description:
      'Are you sure you want to discard unsaved telemetry profile adjustments?',
    destructive: false,
    defaultOpen: true,
  },
  render: (args) => ({
    components: { UiAlertDialog },
    setup() {
      return { args }
    },
    template: `
      <UiAlertDialog v-bind="args" />
    `,
  }),
}

export const LongText: Story = {
  args: {
    title:
      'Permanent Network Core Infrastructure Decommissioning and Route Evacuation Notice for Gateway Node gw-dc01-cluster-alpha',
    description:
      'This critical operation will permanently detach the selected redundant gateway cluster node from live autonomous telemetry routing, tear down all active BGP peering sessions, flush ephemeral routing table caches across upstream fabrics, and invalidate active tenant interconnect tunnels immediately.',
    confirmText:
      'Permanently Decommission Core Gateway Node and Drain Peer Routes',
    cancelText: 'Retain Active Cluster Node and Cancel Evacuation',
    destructive: true,
    defaultOpen: true,
  },
  render: (args) => ({
    components: { UiAlertDialog },
    setup() {
      return { args }
    },
    template: `
      <UiAlertDialog v-bind="args" />
    `,
  }),
}

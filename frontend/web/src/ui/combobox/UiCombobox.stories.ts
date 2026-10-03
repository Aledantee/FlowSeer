import type { Meta, StoryObj } from '@storybook/vue3-vite'
import UiCombobox from './UiCombobox.vue'
import UiButton from '../button/UiButton.vue'

const sampleOptions = [
  { value: 'gw-01', label: 'Gateway 01', group: 'Gateways' },
  { value: 'gw-02', label: 'Gateway 02', group: 'Gateways' },
  { value: 'sw-01', label: 'Switch Core 01', group: 'Switches' },
  { value: 'sw-02', label: 'Switch Access 02', group: 'Switches' },
  { value: 'ap-01', label: 'Access Point North', group: 'Wireless' },
]

const meta: Meta<typeof UiCombobox> = {
  title: 'Ui/Combobox',
  component: UiCombobox,
}

export default meta
type Story = StoryObj<typeof UiCombobox>

export const Default: Story = {
  args: {
    options: sampleOptions,
    placeholder: 'Search device...',
  },
}

export const AccessibilityAudit: Story = {
  name: 'Accessibility Audit (Triggered)',
  args: {
    ...Default.args,
  },
  render: (args) => ({
    components: { UiCombobox },
    setup() {
      return { args }
    },
    template: `
      <label>
        <span>Devices</span>
        <UiCombobox v-bind="args" />
      </label>
    `,
  }),
}

export const WithCustomTrigger: Story = {
  render: () => ({
    components: { UiCombobox, UiButton },
    setup() {
      return { sampleOptions }
    },
    template: `
      <UiCombobox :options="sampleOptions">
        <template #trigger>
          <UiButton variant="secondary">Select Device Scope</UiButton>
        </template>
      </UiCombobox>
    `,
  }),
}

export const DefaultEmpty: Story = {
  name: 'Default Empty State',
  args: {
    options: [],
  },
}

export const LongText: Story = {
  args: {
    options: [
      {
        value: 'gw-cluster-redundant-alpha-01',
        label:
          'High-Throughput Autonomous Redundant Border Gateway Router Cluster Alpha Node 01 (Western Facility)',
        group:
          'Primary Telemetry Ingestion Gateways and Autonomous Edge Routers',
      },
      {
        value: 'sw-backbone-spine-aggregate-02',
        label:
          'Multi-Chassis High-Density Backbone Aggregation Spine Switch Node 02 (Eastern Facility)',
        group:
          'Core Layer 3 Fabric Spine Switches and Dynamic Fabric Interconnects',
      },
    ],
    placeholder:
      'Search across all managed global fabric telemetry devices, gateways, and edge collectors...',
    emptyText:
      'No managed networking devices or fabric endpoints matched your query criteria.',
  },
}

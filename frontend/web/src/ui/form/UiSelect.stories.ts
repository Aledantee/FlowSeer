import type { Meta, StoryObj } from '@storybook/vue3-vite'
import UiSelect from './UiSelect.vue'

const sampleOptions = [
  { value: 'ham', label: 'Hamburg Site (North)' },
  { value: 'ber', label: 'Berlin Core Data Center' },
  { value: 'mun', label: 'Munich Gateway' },
  { value: 'fra', label: 'Frankfurt Hub (Offline)', disabled: true },
]

const meta: Meta<typeof UiSelect> = {
  title: 'Ui/Select',
  component: UiSelect,
  argTypes: {
    placeholder: { control: 'text' },
    disabled: { control: 'boolean' },
    invalid: { control: 'boolean' },
  },
}

export default meta
type Story = StoryObj<typeof UiSelect>

export const Default: Story = {
  args: {
    options: sampleOptions,
    placeholder: 'Choose site location...',
    ariaLabel: 'Site location select',
  },
}

export const Selected: Story = {
  args: {
    modelValue: 'ber',
    options: sampleOptions.map((opt) => ({ ...opt, identifier: true })),
    placeholder: 'Choose site location...',
    ariaLabel: 'Site location select',
  },
}

export const Invalid: Story = {
  args: {
    options: sampleOptions,
    placeholder: 'Choose site location...',
    invalid: true,
    ariaLabel: 'Site location select',
  },
}

export const Disabled: Story = {
  args: {
    modelValue: 'ham',
    options: sampleOptions,
    disabled: true,
    ariaLabel: 'Site location select',
  },
}

export const Focus: Story = {
  args: {
    options: sampleOptions,
    placeholder: 'Choose site location...',
    ariaLabel: 'Site location select',
  },
  render: (args) => ({
    components: { UiSelect },
    setup() {
      return { args }
    },
    template: '<UiSelect v-bind="args" autofocus />',
  }),
}

export const AccessibilityAudit: Story = {
  args: {
    options: sampleOptions,
    placeholder: 'Choose site location...',
    ariaLabel: 'Site location select',
    defaultOpen: true,
  },
}

export const DefaultPlaceholder: Story = {
  name: 'Default Placeholder',
  args: {
    options: sampleOptions,
    ariaLabel: 'Site location select',
  },
}

export const LongText: Story = {
  args: {
    options: [
      {
        value: 'ham-dc01-cluster-alpha',
        label:
          'High-Density Redundant Metropolitan Gateway Node Hamburg Core Facility Cluster Alpha (Western Europe)',
      },
      {
        value: 'ber-dc02-cluster-beta',
        label:
          'Primary Interconnect Aggregation Transit Facility Berlin Tier-IV Infrastructure Node Beta (Central Europe)',
      },
    ],
    placeholder:
      'Select an autonomous border gateway router, aggregation switch, or telemetry edge collector...',
    ariaLabel:
      'Autonomous network infrastructure telemetry ingestion endpoint selector',
  },
}

import type { Meta, StoryObj } from '@storybook/vue3-vite'
import UiField from './UiField.vue'
import UiInput from './UiInput.vue'

const meta: Meta<typeof UiField> = {
  title: 'Ui/Field',
  component: UiField,
  argTypes: {
    label: { control: 'text' },
    description: { control: 'text' },
    error: { control: 'text' },
    required: { control: 'boolean' },
  },
}

export default meta
type Story = StoryObj<typeof UiField>

export const Default: Story = {
  args: {
    label: 'Device Name',
    description: 'A unique identifier for this hardware unit.',
  },
  render: (args) => ({
    components: { UiField, UiInput },
    setup() {
      return { args }
    },
    template: `
      <UiField v-bind="args">
        <UiInput placeholder="e.g. edge-router-01" />
      </UiField>
    `,
  }),
}

export const WithError: Story = {
  args: {
    label: 'Device Name',
    description: 'A unique identifier for this hardware unit.',
    error: 'Device name is already in use on this site.',
  },
  render: (args) => ({
    components: { UiField, UiInput },
    setup() {
      return { args }
    },
    template: `
      <UiField v-bind="args">
        <UiInput placeholder="e.g. edge-router-01" />
      </UiField>
    `,
  }),
}

export const Required: Story = {
  args: {
    label: 'Site Location',
    required: true,
  },
  render: (args) => ({
    components: { UiField, UiInput },
    setup() {
      return { args }
    },
    template: `
      <UiField v-bind="args">
        <UiInput placeholder="Select or enter site..." />
      </UiField>
    `,
  }),
}

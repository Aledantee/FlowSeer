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
    options: sampleOptions,
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

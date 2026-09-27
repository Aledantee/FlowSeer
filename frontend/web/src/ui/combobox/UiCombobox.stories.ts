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

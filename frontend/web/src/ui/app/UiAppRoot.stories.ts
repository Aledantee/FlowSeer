import type { Meta, StoryObj } from '@storybook/vue3-vite'
import UiAppRoot from './UiAppRoot.vue'
import UiTooltip from '../tooltip/UiTooltip.vue'
import UiButton from '../button/UiButton.vue'

const meta: Meta<typeof UiAppRoot> = {
  title: 'Ui/AppRoot',
  component: UiAppRoot,
}

export default meta
type Story = StoryObj<typeof UiAppRoot>

export const Default: Story = {
  render: (args) => ({
    components: { UiAppRoot, UiTooltip, UiButton },
    setup() {
      return { args }
    },
    template: `
      <UiAppRoot v-bind="args">
        <div class="p-12 flex justify-center">
          <UiTooltip label="App root tooltip">
            <UiButton variant="secondary">Hover me</UiButton>
          </UiTooltip>
        </div>
      </UiAppRoot>
    `,
  }),
}

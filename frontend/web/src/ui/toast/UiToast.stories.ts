import type { Meta, StoryObj } from '@storybook/vue3-vite'
import UiToast from './UiToast.vue'
import UiToastProvider from './UiToastProvider.vue'
import UiButton from '../button/UiButton.vue'
import { useToast } from './useToast'

const meta: Meta<typeof UiToast> = {
  title: 'Ui/Toast',
  component: UiToast,
  argTypes: {
    variant: {
      control: 'select',
      options: ['default', 'success', 'warning', 'danger'],
    },
  },
}

export default meta
type Story = StoryObj<typeof UiToast>

export const Interactive: Story = {
  render: () => ({
    components: { UiToastProvider, UiButton },
    setup() {
      const { toast } = useToast()
      function showSuccess() {
        toast({
          title: 'Route Saved',
          description: 'Gateway telemetry cache flushed successfully.',
          variant: 'success',
          action: {
            label: 'Undo',
            onClick: () => {
              console.log('Undo clicked')
            },
          },
        })
      }
      function showDanger() {
        toast({
          title: 'Connection Lost',
          description: 'Device unreachable after 3 consecutive heartbeats.',
          variant: 'danger',
        })
      }
      return { showSuccess, showDanger }
    },
    template: `
      <UiToastProvider>
        <div class="flex gap-3 p-4">
          <UiButton variant="primary" @click="showSuccess">Show Success Toast</UiButton>
          <UiButton variant="danger" @click="showDanger">Show Danger Toast</UiButton>
        </div>
      </UiToastProvider>
    `,
  }),
}

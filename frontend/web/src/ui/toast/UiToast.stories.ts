import type { Meta, StoryObj } from '@storybook/vue3-vite'
import { defineComponent } from 'vue'
import { injectToastProviderContext, ToastProvider } from 'reka-ui'
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

const ToastViewportSurface = defineComponent({
  setup() {
    const providerContext = injectToastProviderContext()
    return {
      setViewport: (el: unknown) => {
        if (el instanceof HTMLElement) {
          providerContext.onViewportChange(el)
        }
      },
    }
  },
  template: `
    <div
      role="region"
      aria-label="Notifications"
      class="fixed bottom-0 right-0 z-(--z-toast) p-4 pointer-events-none"
    >
      <ol
        :ref="setViewport"
        class="flex flex-col gap-2 w-full max-w-[420px]"
      >
        <slot />
      </ol>
    </div>
  `,
})

export const VisibleToast: Story = {
  render: () => ({
    components: { ToastProvider, ToastViewportSurface, UiToast },
    template: `
      <ToastProvider>
        <ToastViewportSurface />
        <UiToast
          title="Route Saved"
          description="Gateway telemetry cache flushed successfully."
          variant="success"
        />
      </ToastProvider>
    `,
  }),
}

import type { Meta, StoryObj } from '@storybook/vue3-vite'
import UiAiContextLayer from './UiAiContextLayer.vue'
import UiAiTarget from './UiAiTarget.vue'

const meta: Meta<typeof UiAiContextLayer> = {
  title: 'Ui/AiContextLayer',
  component: UiAiContextLayer,
}

export default meta
type Story = StoryObj<typeof UiAiContextLayer>

const targetId = 'standalone:story:ai-context-layer:device'

export const Default: Story = {
  render: () => ({
    components: {
      UiAiContextLayer,
      UiAiTarget,
    },
    setup() {
      const target = {
        id: targetId,
        kind: 'device',
        label: 'core-sw-1',
        context: { health: 'Offline', site: 'Berlin Mitte' },
      }
      return { target }
    },
    template: `
      <UiAiContextLayer>
        <div class="p-6 space-y-3 text-foreground">
          <p class="text-xs text-muted-foreground">
            Right-click the target row below to open context-specific AI actions.
          </p>
          <UiAiTarget
            as="div"
            :ai="target"
            tabindex="0"
            class="p-3 border border-border rounded-panel bg-card text-xs cursor-context-menu"
          >
            core-sw-1 (Offline)
          </UiAiTarget>
        </div>
      </UiAiContextLayer>
    `,
  }),
}

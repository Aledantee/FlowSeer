import type { Meta, StoryObj } from '@storybook/vue3-vite'
import { onMounted, onUnmounted, provide, ref } from 'vue'
import UiAiActionLayer from './UiAiActionLayer.vue'
import { aiRegistryKey } from './context'
import { createAiRegistry } from '../../ai'

const meta: Meta<typeof UiAiActionLayer> = {
  title: 'Ui/AiActionLayer',
  component: UiAiActionLayer,
}

export default meta
type Story = StoryObj<typeof UiAiActionLayer>

// The layer draws over a registered target, so the story registers a demo
// element and selects it. Ask opens on the target; the panel is the same
// component the console uses.
export const Selected: Story = {
  render: () => ({
    components: { UiAiActionLayer },
    setup() {
      const registry = createAiRegistry()
      const target = ref<HTMLElement>()
      provide(aiRegistryKey, registry)
      onMounted(() => {
        if (target.value) {
          registry.register(target.value, {
            id: 'standalone:story:action-layer:row',
            kind: 'row',
            label: 'Demo target row',
            context: { site: 'Berlin Mitte' },
          })
          registry.highlight('standalone:story:action-layer:row')
        }
      })
      onUnmounted(() => {
        if (target.value) registry.unregister(target.value)
      })
      return { target }
    },
    template: `
      <div class="p-6 space-y-3 text-foreground">
        <p class="text-xs text-muted-foreground">
          The outline and Ask follow the selected target.
        </p>
        <div
          ref="target"
          tabindex="0"
          class="p-3 border border-border rounded-panel bg-card text-xs"
        >
          Demo target row
        </div>
        <UiAiActionLayer />
      </div>
    `,
  }),
}

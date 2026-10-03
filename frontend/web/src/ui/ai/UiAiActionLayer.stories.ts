import type { Meta, StoryObj } from '@storybook/vue3-vite'
import { onMounted, onUnmounted } from 'vue'
import UiAiActionLayer from './UiAiActionLayer.vue'
import { aiRegistry } from '../../ai'

const meta: Meta<typeof UiAiActionLayer> = {
  title: 'Ui/AiActionLayer',
  component: UiAiActionLayer,
}

export default meta
type Story = StoryObj<typeof UiAiActionLayer>

const targetId = 'standalone:story:ai-action-layer:selected:row'

// The shared decorator owns the registry and the single document-wide action
// layer, so the story registers the row it wants the layer to follow and
// selects it. The selection then draws the outline and Ask over that row; the
// panel is the same component the console mounts.
export const Selected: Story = {
  render: () => ({
    setup() {
      onMounted(() => {
        aiRegistry.highlight(targetId)
      })
      onUnmounted(() => {
        aiRegistry.clearHighlight()
      })
      const target = {
        id: targetId,
        kind: 'row',
        label: 'Demo target row',
        context: { site: 'Berlin Mitte' },
      }
      return { target }
    },
    template: `
      <div class="p-6 space-y-3 text-foreground">
        <p class="text-xs text-muted-foreground">
          The outline and Ask follow the selected target.
        </p>
        <div
          v-ai-target="target"
          tabindex="0"
          class="p-3 border border-border rounded-panel bg-card text-xs"
        >
          Demo target row
        </div>
      </div>
    `,
  }),
}

const longTextTargetId = 'standalone:story:ai-action-layer:long-text:row'

export const LongText: Story = {
  render: () => ({
    setup() {
      onMounted(() => {
        aiRegistry.highlight(longTextTargetId)
      })
      onUnmounted(() => {
        aiRegistry.clearHighlight()
      })
      const target = {
        id: longTextTargetId,
        kind: 'row',
        label:
          'Primary Core Aggregation Node EMEA-BER-DC1-SW-AGG-01A with redundant fabric interconnects',
        context: {
          site: 'Frankfurt Datacenter Campus North Building 3 Hall B Rack 42',
        },
      }
      return { target }
    },
    template: `
      <div class="p-6 space-y-3 text-foreground">
        <p class="text-xs text-muted-foreground">
          The outline and Ask follow the selected target with an extensive label.
        </p>
        <div
          v-ai-target="target"
          tabindex="0"
          class="p-3 border border-border rounded-panel bg-card text-xs"
        >
          Primary Core Aggregation Node EMEA-BER-DC1-SW-AGG-01A with redundant fabric interconnects
        </div>
      </div>
    `,
  }),
}

const overridesTargetId = 'standalone:story:ai-action-layer:overrides:row'

export const Overrides: Story = {
  parameters: {
    ai: {
      labels: {
        askAbout: 'Inquire regarding target',
        heading: 'Diagnostic inquiry for target',
        ai: 'Bot',
        questionLabel: 'Specify query',
        questionPlaceholder:
          'Explain telemetry anomalies observed on this interface...',
        cancel: 'Dismiss',
        ask: 'Inquire',
        asking: 'Querying telemetry engine…',
        unavailable: 'Diagnostic assistant offline',
        error: 'Telemetry analysis request rejected',
      },
    },
  },
  render: () => ({
    setup() {
      onMounted(() => {
        aiRegistry.highlight(overridesTargetId)
      })
      onUnmounted(() => {
        aiRegistry.clearHighlight()
      })
      const target = {
        id: overridesTargetId,
        kind: 'row',
        label: 'Overridden target row',
        context: { site: 'Berlin Mitte' },
      }
      return { target }
    },
    template: `
      <div class="p-6 space-y-3 text-foreground">
        <p class="text-xs text-muted-foreground">
          Action layer rendering with caller-supplied label overrides.
        </p>
        <div
          v-ai-target="target"
          tabindex="0"
          class="p-3 border border-border rounded-panel bg-card text-xs"
        >
          Overridden target row
        </div>
      </div>
    `,
  }),
}

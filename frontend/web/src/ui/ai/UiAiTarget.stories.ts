import { ref } from 'vue'
import type { Meta, StoryObj } from '@storybook/vue3-vite'
import type { AiOriginRequest } from './context'
import UiAiTarget from './UiAiTarget.vue'

const meta: Meta<typeof UiAiTarget> = {
  title: 'Ui/Ai/UiAiTarget',
  component: UiAiTarget,
}

export default meta
type Story = StoryObj<typeof UiAiTarget>

const devices = [
  { id: 'core-sw-1', health: 'Offline' },
  { id: 'edge-ap-7', health: 'Online' },
]

// A native list keeps its semantics: each item is the registered element.
export const Default: Story = {
  render: () => ({
    components: { UiAiTarget },
    setup() {
      const targets = devices.map((device) => ({
        id: `standalone:story:ai-target:device:${device.id}`,
        kind: 'device',
        label: device.id,
        context: { health: device.health },
      }))
      return { devices, targets }
    },
    template: `
      <ul class="p-6 space-y-2 text-xs text-foreground">
        <UiAiTarget
          v-for="(device, index) in devices"
          :key="device.id"
          as="li"
          :ai="targets[index]"
          tabindex="0"
          class="p-3 border border-border rounded-panel bg-card cursor-context-menu"
        >
          {{ device.id }} ({{ device.health }})
        </UiAiTarget>
      </ul>
    `,
  }),
}

export const NativeSection: Story = {
  render: () => ({
    components: { UiAiTarget },
    setup() {
      const target = {
        id: 'standalone:story:ai-target:site:berlin',
        kind: 'site',
        label: 'Berlin Mitte',
        context: { site: 'Berlin Mitte' },
      }
      return { target }
    },
    template: `
      <UiAiTarget
        as="section"
        aria-label="Berlin Mitte"
        :ai="target"
        tabindex="0"
        class="m-6 p-4 border border-border rounded-panel bg-card text-xs text-foreground cursor-context-menu"
      >
        <p>Berlin Mitte</p>
      </UiAiTarget>
    `,
  }),
}

// The outline marks a value an agent changed until the user touches it. The
// caller clears its own state when the component reports the interaction.
export const AgentChanged: Story = {
  render: () => ({
    components: { UiAiTarget },
    setup() {
      const target = {
        id: 'standalone:story:ai-target:device:agent-changed',
        kind: 'device',
        label: 'core-sw-1',
        context: { health: 'Offline' },
      }
      const origin = ref<AiOriginRequest | undefined>({
        requestId: 'req-story',
        action: 'summary',
        targets: [],
        history: [],
      })
      return { target, origin }
    },
    template: `
      <ul class="p-6 text-xs text-foreground">
        <UiAiTarget
          as="li"
          :ai="target"
          :ai-origin="origin"
          tabindex="0"
          class="p-3 border border-border rounded-panel bg-card cursor-context-menu"
          @ai-origin-acknowledged="origin = undefined"
        >
          core-sw-1 (Offline)
        </UiAiTarget>
      </ul>
    `,
  }),
}

import type { Meta, StoryObj } from '@storybook/vue3-vite'
import { onMounted, onUnmounted, provide, ref } from 'vue'
import UiAiSummary from './UiAiSummary.vue'
import { aiRegistryKey } from './context'
import { createAiRegistry } from '../../ai'
import type { AiHandler, AiTarget } from '../../ai'

const meta: Meta<typeof UiAiSummary> = {
  title: 'Ui/AiSummary',
  component: UiAiSummary,
}

export default meta
type Story = StoryObj<typeof UiAiSummary>

const target: AiTarget = {
  id: 'standalone:story:ai-summary:berlin-ap-01',
  kind: 'device',
  label: 'berlin-ap-01',
  context: { device: 'berlin-ap-01', site: 'Berlin Mitte' },
}

// A summary requests a target, so the story registers its own wrapper as
// that target and installs a demo handler — the application has no backend.
function demo(handler?: AiHandler) {
  return () => ({
    components: { UiAiSummary },
    setup() {
      const registry = createAiRegistry()
      const root = ref<HTMLElement>()
      provide(aiRegistryKey, registry)
      if (handler) registry.onRequest(handler)
      onMounted(() => {
        if (root.value) registry.register(root.value, target)
      })
      onUnmounted(() => {
        if (root.value) registry.unregister(root.value)
      })
      return { root, target }
    },
    template: `
      <div ref="root" class="max-w-sm p-4 bg-card border border-border rounded-panel text-foreground">
        <p class="mb-2 text-xs text-muted-foreground">Site summary placement</p>
        <UiAiSummary :target="target" />
      </div>
    `,
  })
}

export const Idle: Story = {
  render: demo(),
}

export const Loading: Story = {
  render: demo(() => new Promise<string>(() => {})),
}

export const Result: Story = {
  render: demo(
    async () =>
      'Berlin Mitte is degraded: brl-sw-01 uplinks are up, but two access points stopped answering within the last hour. No site-wide outage is indicated.',
  ),
}

export const ErrorState: Story = {
  render: demo(async () => {
    throw new Error('The summary service did not answer.')
  }),
}

export const Unavailable: Story = {
  render: demo(),
}

export const DarkMode: Story = {
  parameters: { themes: { themeOverride: 'dark' } },
  render: () => ({
    components: { UiAiSummary },
    setup() {
      const registry = createAiRegistry()
      const root = ref<HTMLElement>()
      provide(aiRegistryKey, registry)
      registry.onRequest(async () => 'All paths answered the last poll.')
      onMounted(() => {
        if (root.value) registry.register(root.value, target)
      })
      onUnmounted(() => {
        if (root.value) registry.unregister(root.value)
      })
      return { root, target }
    },
    template: `
      <div data-theme="dark" class="max-w-sm p-4 bg-card border border-border rounded-panel text-foreground">
        <UiAiSummary :target="target" />
      </div>
    `,
  }),
}

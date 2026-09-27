import type { Decorator } from '@storybook/vue3-vite'
import { getCurrentInstance, onBeforeUnmount, onMounted, ref } from 'vue'
import { aiRegistry, installFlowSeerAi, vAiTarget } from '../src/ai'
import type { AiHandler } from '../src/ai'
import UiAiActionLayer from '../src/ui/ai/UiAiActionLayer.vue'

// One decorator gives every story an inspectable target and a local demo
// handler, independently of `main.ts`. It registers the story's wrapper as a
// target, installs the window contract, and mounts the action layer, so Ask
// is available for every component family without editing each story. The
// target, the handler, and the window contract are removed on unmount.

export const withAiTargets: Decorator = (story, context) => {
  const id = `standalone:story:${context.id}`
  const configured = context.parameters?.ai?.handler as AiHandler | undefined
  return {
    components: { story, UiAiActionLayer },
    setup() {
      // Storybook's app is shared, so the directive is registered once and
      // stays; `v-ai-target` then works in any story template.
      getCurrentInstance()?.appContext.app.directive('ai-target', vAiTarget)
      const removeWindow = installFlowSeerAi()
      const handler: AiHandler =
        configured ??
        (async (request) =>
          `Demo answer for “${request.label}” in ${context.title ?? 'this story'}. Install a handler with parameters.ai.handler.`)
      const unsubscribe = aiRegistry.onRequest(handler)
      const root = ref<HTMLElement>()
      onMounted(() => {
        if (root.value)
          aiRegistry.register(root.value, {
            id,
            kind: 'story',
            label: context.title ?? context.id,
            context: { story: context.id },
          })
      })
      onBeforeUnmount(() => {
        if (root.value) aiRegistry.unregister(root.value)
        unsubscribe()
        removeWindow()
        aiRegistry.clearHighlight()
      })
      return { root }
    },
    template: `
      <div ref="root" data-ai-story-root>
        <story />
        <UiAiActionLayer />
      </div>
    `,
  }
}

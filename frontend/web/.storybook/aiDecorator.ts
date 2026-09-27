import type { Decorator } from '@storybook/vue3-vite'
import { getCurrentInstance, onBeforeUnmount, onMounted, ref } from 'vue'
import type { Ref } from 'vue'
import {
  aiRegistry,
  AiUnavailableError,
  installFlowSeerAi,
  vAiTarget,
} from '../src/ai'
import type { AiHandler } from '../src/ai'
import UiAiActionLayer from '../src/ui/ai/UiAiActionLayer.vue'

// One decorator gives every story an inspectable target and a local demo
// handler, independently of `main.ts`. It registers the story's wrapper as a
// target, installs the window contract, and mounts the action layer, so Ask
// is available for every component family without editing each story.
//
// Storybook Docs mounts several canvases into one document, and each canvas
// runs its own decorator instance. The window contract, the request
// dispatcher, and the action layer belong to the document, not to a single
// canvas: one canvas unmounting must not take them from the canvases still on
// screen, and the canvases must not stack duplicate teleported Ask layers. A
// module-level scope owns the document-wide pieces and the active layer, and
// releases them only when the last canvas unmounts.

interface StoryCanvas {
  id: string
  element: HTMLElement
  handler: AiHandler
  label: string
  story: string
  showLayer: Ref<boolean>
}

interface MountedCanvas {
  id: string
  element: HTMLElement
  handler: AiHandler
  ownsId: boolean
  showLayer: Ref<boolean>
}

function createStoryScope() {
  const canvases = new Set<MountedCanvas>()
  const handlers = new Map<string, AiHandler>()
  let host: MountedCanvas | undefined
  let removeWindow: (() => void) | undefined
  let removeDispatcher: (() => void) | undefined

  function openDocument() {
    if (removeWindow) return
    removeWindow = installFlowSeerAi()
    // The registry holds one handler. With several canvases mounted it
    // dispatches each request to the canvas that registered that target, so a
    // story keeps its own configured demo handler instead of sharing the
    // handler of whichever canvas mounted last.
    removeDispatcher = aiRegistry.onRequest((request) => {
      const targetElement = aiRegistry.view(request.targetId)?.element
      const handler =
        handlers.get(request.targetId) ??
        [...canvases].find(
          (canvas) => targetElement && canvas.element.contains(targetElement),
        )?.handler
      if (!handler) return Promise.reject(new AiUnavailableError())
      return handler(request)
    })
  }

  function closeDocument() {
    aiRegistry.clearHighlight()
    removeDispatcher?.()
    removeDispatcher = undefined
    removeWindow?.()
    removeWindow = undefined
  }

  function electHost() {
    if (host) return
    const next = canvases.values().next().value
    if (!next) return
    host = next
    next.showLayer.value = true
  }

  function mount(canvas: StoryCanvas): () => void {
    openDocument()
    aiRegistry.register(canvas.element, {
      id: canvas.id,
      kind: 'story',
      label: canvas.label,
      context: { story: canvas.story },
    })
    // The registry keeps the first registration of a duplicate id, so a
    // second canvas reusing a story id must not take over that target's
    // handler or release it on unmount.
    const ownsId = !handlers.has(canvas.id)
    if (ownsId) handlers.set(canvas.id, canvas.handler)
    const mounted: MountedCanvas = {
      id: canvas.id,
      element: canvas.element,
      handler: canvas.handler,
      ownsId,
      showLayer: canvas.showLayer,
    }
    canvases.add(mounted)
    electHost()

    return () => {
      aiRegistry.unregister(canvas.element)
      canvases.delete(mounted)
      if (ownsId) handlers.delete(canvas.id)
      if (host !== mounted) return
      mounted.showLayer.value = false
      host = undefined
      if (canvases.size > 0) electHost()
      else closeDocument()
    }
  }

  return { mount }
}

const storyScope = createStoryScope()

export const withAiTargets: Decorator = (story, context) => {
  const id = `standalone:story:${context.id}`
  const configured = context.parameters?.ai?.handler as AiHandler | undefined
  const handler: AiHandler =
    configured ??
    (async (request) =>
      `Demo answer for “${request.label}” in ${context.title ?? 'this story'}. Install a handler with parameters.ai.handler.`)
  return {
    components: { story, UiAiActionLayer },
    setup() {
      // Storybook's app is shared, so the directive is registered once and
      // stays; `v-ai-target` then works in any story template.
      getCurrentInstance()?.appContext.app.directive('ai-target', vAiTarget)
      const showLayer = ref(false)
      const root = ref<HTMLElement>()
      let release: (() => void) | undefined
      onMounted(() => {
        if (!root.value) return
        release = storyScope.mount({
          id,
          element: root.value,
          handler,
          label: context.title ?? context.id,
          story: context.id,
          showLayer,
        })
      })
      onBeforeUnmount(() => release?.())
      return { root, showLayer }
    },
    template: `
      <div ref="root" data-ai-story-root>
        <story />
        <UiAiActionLayer v-if="showLayer" />
      </div>
    `,
  }
}

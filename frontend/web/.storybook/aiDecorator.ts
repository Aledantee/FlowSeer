import type { Decorator } from '@storybook/vue3-vite'
import { getCurrentInstance, onBeforeUnmount, onMounted, ref } from 'vue'
import {
  aiRegistry,
  AiUnavailableError,
  installFlowSeerAi,
  vAiTarget,
} from '../src/ai'
import type { AiHandler } from '../src/ai'
import UiAiContextLayer from '../src/ui/ai/UiAiContextLayer.vue'

// One decorator gives every story an inspectable target and a local demo
// handler, independently of `main.ts`. It registers the story's wrapper as a
// target, installs the window contract, and wraps the story in an AI context
// layer.
//
// Storybook Docs mounts several canvases into one document, and each canvas
// runs its own decorator instance. The window contract and the request
// dispatcher belong to the document, not to a single canvas: one canvas
// unmounting must not take them from the canvases still on screen. A
// module-level scope owns the document-wide pieces and releases them only when
// the last canvas unmounts. Because UiAiContextLayer scopes its listeners to its
// own layer root, each canvas safely renders its own layer without host election.

interface StoryCanvas {
  id: string
  element: HTMLElement
  handler: AiHandler
  label: string
  story: string
}

interface MountedCanvas {
  id: string
  element: HTMLElement
  handler: AiHandler
  ownsId: boolean
}

function createStoryScope() {
  const canvases = new Set<MountedCanvas>()
  const handlers = new Map<string, AiHandler>()
  let removeWindow: (() => void) | undefined
  let removeDispatcher: (() => void) | undefined

  function openDocument() {
    if (removeWindow) return
    removeWindow = installFlowSeerAi()
    removeDispatcher = aiRegistry.onRequest((request) => {
      const targetId = request.targets[0]?.id
      const targetElement = targetId
        ? aiRegistry.view(targetId)?.element
        : undefined
      const handler =
        (targetId ? handlers.get(targetId) : undefined) ??
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

  function mount(canvas: StoryCanvas): () => void {
    openDocument()
    aiRegistry.register(canvas.element, {
      id: canvas.id,
      kind: 'story',
      label: canvas.label,
      context: { story: canvas.story },
    })
    const ownsId = !handlers.has(canvas.id)
    if (ownsId) handlers.set(canvas.id, canvas.handler)
    const mounted: MountedCanvas = {
      id: canvas.id,
      element: canvas.element,
      handler: canvas.handler,
      ownsId,
    }
    canvases.add(mounted)

    return () => {
      aiRegistry.unregister(canvas.element)
      canvases.delete(mounted)
      if (ownsId) handlers.delete(canvas.id)
      if (canvases.size === 0) {
        closeDocument()
      }
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
    (async (request) => ({
      type: 'answer',
      text: `Demo answer for “${request.targets[0]?.label ?? ''}” in ${context.title ?? 'this story'}. Install a handler with parameters.ai.handler.`,
      refs: [],
    }))
  return {
    components: { story, UiAiContextLayer },
    setup() {
      getCurrentInstance()?.appContext.app.directive('ai-target', vAiTarget)
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
        })
      })
      onBeforeUnmount(() => release?.())
      return {
        root,
      }
    },
    template: `
      <UiAiContextLayer>
        <div ref="root" data-ai-story-root>
          <story />
        </div>
      </UiAiContextLayer>
    `,
  }
}

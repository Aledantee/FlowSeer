import { defineComponent, getCurrentInstance, onMounted, shallowRef } from 'vue'
import type { PropType } from 'vue'
import type { AiTarget } from '../../ai'
import type { AiOriginRequest } from '../ai/context'
import { useAiOrigin } from '../ai/useAiOrigin'
import { useAiTarget } from '../ai/useAiTarget'

// Registers the element a popup renders its content in. Reka keeps some popup
// roots mounted and swaps their content in and out through its own presence,
// and a `$el` read off such a root is a placeholder or a stale node, so no ref
// on the popup component follows the content. This component renders nothing
// and sits first in the content, so it mounts and unmounts exactly when the
// content does, exit presence included, and its parent element is the content
// element itself (`Presence/Presence.js`, `Popper/PopperContent.js`, Reka UI
// 2.10.5).
export const PopupAnchor = defineComponent({
  name: 'PopupAnchor',
  props: {
    ai: { type: Object as PropType<AiTarget>, default: undefined },
    aiOrigin: { type: Object as PropType<AiOriginRequest>, default: undefined },
  },
  emits: ['aiOriginAcknowledged'],
  setup(props, { emit }) {
    const instance = getCurrentInstance()
    const mounted = shallowRef(false)
    onMounted(() => {
      mounted.value = true
    })
    const content = () => {
      const marker = mounted.value ? instance?.vnode.el : undefined
      return marker instanceof Node ? marker.parentElement : undefined
    }
    useAiTarget(content, () => props.ai)
    useAiOrigin(
      content,
      () => props.aiOrigin,
      (requestId) => emit('aiOriginAcknowledged', requestId),
    )
    return () => null
  },
})

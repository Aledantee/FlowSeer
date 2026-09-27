import type { AiRegistry } from './registry'
import type { AiHandler, AiTarget } from './types'

// The inspectable contract an agent uses in the console or in Storybook.
// `onRequest` installs the asynchronous handler; the returned function
// removes it. An absent handler is a normal state, not an error.
export interface FlowSeerAi {
  listTargets(): AiTarget[]
  highlight(id: string): boolean
  clearHighlight(): void
  onRequest(handler: AiHandler): () => void
}

declare global {
  interface Window {
    flowseerAi?: FlowSeerAi
  }
}

export function installAiWindow(
  registry: AiRegistry,
  target: Window = window,
): () => void {
  const api: FlowSeerAi = {
    listTargets: () => registry.list(),
    highlight: (id) => registry.highlight(id),
    clearHighlight: () => registry.clearHighlight(),
    onRequest: (handler) => registry.onRequest(handler),
  }
  target.flowseerAi = api
  return () => {
    if (target.flowseerAi === api) delete target.flowseerAi
  }
}

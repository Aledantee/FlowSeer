import { createAiTargetDirective } from './directive'
import { createAiRegistry } from './registry'
import { installAiWindow } from './window'

// The console-wide registry. `main.ts` owns installing the window API and
// the directive; tests and Storybook build their own registry so a run does
// not inherit targets from another document.
export const aiRegistry = createAiRegistry()
export const vAiTarget = createAiTargetDirective(aiRegistry)

// Install the document contract for the console-wide registry.
export function installFlowSeerAi(target: Window = window): () => void {
  return installAiWindow(aiRegistry, target)
}

export { createAiRegistry, AiStaleError, AiUnavailableError } from './registry'
export type { AiRegistry, AiRegistryOptions, AiViewport } from './registry'
export { createAiTargetDirective } from './directive'
export { installAiWindow } from './window'
export type { FlowSeerAi } from './window'
export { aiTarget, aiTargetId, useAiSlot, aiSlot } from './target'
export type { AiSlot, AiTargetInput } from './target'
export type {
  AiHandler,
  AiRequest,
  AiRequestKind,
  AiTarget,
  AiTargetSegment,
  AiTargetView,
} from './types'

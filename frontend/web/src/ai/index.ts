import { createAiRegistry } from './registry'
import { installAiWindow } from './window'

// The console-wide registry. `main.ts` owns installing the window API;
// tests and Storybook build their own registry so a run does
// not inherit targets from another document.
export const aiRegistry = createAiRegistry()

// Install the document contract for the console-wide registry.
export function installFlowSeerAi(target: Window = window): () => void {
  return installAiWindow(aiRegistry, target)
}

export {
  createAiRegistry,
  cloneAiTarget,
  isAiTargetElement,
  AiStaleError,
  AiUnavailableError,
} from './registry'
export type {
  AiRegistry,
  AiRegistryOptions,
  AiRequestOptions,
  AiViewport,
  FeedbackPayload,
} from './registry'
export { installAiWindow } from './window'
export { createMockAiHandler } from './mock'
export { aiActions } from './actions'
export type { AiActionTarget } from './actions'
export {
  validateAiResult,
  validateAiSummary,
  validateAiAnswer,
  validateEntityRef,
  AI_VALIDATION_ERROR_MESSAGE,
} from './validate'
export {
  AI_UI_CATALOG,
  AI_UI_ERROR_MESSAGE,
  AI_UI_MAX_DEPTH,
  AI_UI_MAX_NODES,
  AI_UI_MAX_STRING_LENGTH,
  validateAiUiTree,
} from './catalog'
export { isPagePath } from '../navigation/page'
export type { FlowSeerAi } from './window'
export {
  aiTarget,
  aiTargetId,
  resolveTargetEntity,
  useAiSlot,
  aiSlot,
} from './target'
export type { AiSlot, AiTargetInput } from './target'
export type {
  AiAnswer,
  AiCause,
  AiConfidence,
  AiEntityKind,
  AiEntityRef,
  AiFinding,
  AiHandler,
  AiImpact,
  AiMetric,
  AiNextStep,
  AiRef,
  AiRequest,
  AiRequestKind,
  AiResult,
  AiRun,
  AiSeed,
  AiSeverity,
  AiSummary,
  AiTarget,
  AiTargetElement,
  AiTargetSegment,
  AiTargetSnapshot,
  AiTargetView,
  AiTone,
  AiTurn,
  AiUiIntent,
  AiUiNavigateIntent,
  AiUiNode,
} from './types'

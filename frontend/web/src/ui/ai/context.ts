import { inject } from 'vue'
import type { InjectionKey } from 'vue'
import { aiRegistry } from '../../ai'
import type { AiRegistry, AiRequest, AiTarget } from '../../ai'

// Components read the registry through injection so a test or a Storybook
// canvas can install its own instead of the console-wide one.
export const aiRegistryKey: InjectionKey<AiRegistry> = Symbol('aiRegistry')

export function useAiRegistry(): AiRegistry {
  return inject(aiRegistryKey, aiRegistry)
}

// The request that changed a value, as `UiAiLabel` already displays it. The
// signal stays with the run that owns it.
export type AiOriginRequest = Omit<AiRequest, 'signal'>

// The optional props every addressable component takes. `ai` is the resolved
// target from `aiTarget()`, and `aiOrigin` names the request behind a value an
// agent changed. Neither depends on the other.
export interface UiAiProps {
  ai?: AiTarget
  aiOrigin?: AiOriginRequest
}

export interface UiAiEmits {
  (e: 'aiOriginAcknowledged', requestId: string): void
}

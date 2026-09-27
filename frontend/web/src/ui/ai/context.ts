import { inject } from 'vue'
import type { InjectionKey } from 'vue'
import { aiRegistry } from '../../ai'
import type { AiRegistry } from '../../ai'

// Components read the registry through injection so a test or a Storybook
// canvas can install its own instead of the console-wide one.
export const aiRegistryKey: InjectionKey<AiRegistry> = Symbol('aiRegistry')

export function useAiRegistry(): AiRegistry {
  return inject(aiRegistryKey, aiRegistry)
}

// @vitest-environment happy-dom
import { afterEach, describe, expect, it } from 'vitest'
import { createApp, h, nextTick, ref, withDirectives } from 'vue'
import { createAiTargetDirective } from './directive'
import { createAiRegistry } from './registry'
import type { AiTarget } from './types'

let dispose = () => {}
afterEach(() => {
  dispose()
  dispose = () => {}
  document.body.replaceChildren()
})

function mount(target: () => AiTarget | undefined) {
  const registry = createAiRegistry()
  const directive = createAiTargetDirective(registry)
  const app = createApp({
    setup() {
      return () =>
        withDirectives(h('div', { id: 'row' }), [[directive, target()]])
    },
  })
  const host = document.createElement('div')
  document.body.append(host)
  app.mount(host)
  dispose = () => app.unmount()
  return { registry, host }
}

describe('ai-target directive', () => {
  it('registers on mount, replaces on update, and removes on unmount', async () => {
    const current = ref<AiTarget | undefined>({
      id: 'a:devices:device:d1',
      kind: 'device',
      label: 'd1',
      context: {},
    })
    const { registry, host } = mount(() => current.value)

    expect(registry.list().map((item) => item.id)).toEqual([
      'a:devices:device:d1',
    ])

    current.value = {
      id: 'a:devices:device:d2',
      kind: 'device',
      label: 'd2',
      context: {},
    }
    await nextTick()
    expect(registry.list().map((item) => item.id)).toEqual([
      'a:devices:device:d2',
    ])

    current.value = undefined
    await nextTick()
    expect(host.querySelector('#row')).not.toBeNull()
    expect(registry.list()).toHaveLength(0)

    dispose()
    dispose = () => {}
    expect(registry.list()).toHaveLength(0)
  })

  it('sets data-ai-selected on highlight and removes on clearHighlight', async () => {
    const current = ref<AiTarget | undefined>({
      id: 'a:devices:device:d1',
      kind: 'device',
      label: 'd1',
      context: {},
    })
    const { registry, host } = mount(() => current.value)
    const row = host.querySelector('#row')
    expect(row).not.toBeNull()
    expect(row?.hasAttribute('data-ai-selected')).toBe(false)

    registry.highlight('a:devices:device:d1')
    expect(row?.hasAttribute('data-ai-selected')).toBe(true)

    registry.clearHighlight()
    expect(row?.hasAttribute('data-ai-selected')).toBe(false)
  })
})

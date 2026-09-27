// @vitest-environment happy-dom
import { afterEach, describe, expect, it, vi } from 'vitest'
import { createApp, h } from 'vue'
import UiProgress from './UiProgress.vue'

let dispose = () => {}
afterEach(() => {
  dispose()
  document.body.replaceChildren()
  vi.restoreAllMocks()
})

function mountProgress(props: Record<string, unknown> = {}) {
  const host = document.createElement('div')
  document.body.append(host)
  const app = createApp({
    render() {
      return h(UiProgress, props)
    },
  })
  app.mount(host)
  dispose = () => app.unmount()
  const el = host.firstElementChild as HTMLElement
  return { host, el }
}

describe('UiProgress', () => {
  it('determinate progress sets aria-valuenow on ProgressRoot and applies transform style', () => {
    const { el } = mountProgress({ modelValue: 45, max: 100 })
    expect(el.getAttribute('aria-valuenow')).toBe('45')
    expect(el.getAttribute('aria-valuemax')).toBe('100')
    const indicator = el.querySelector('[data-max]') as HTMLElement
    expect(indicator).not.toBeNull()
    expect(indicator.style.transform).toContain('translateX(-55%)')
  })

  it('indeterminate progress sets data-state="indeterminate"', () => {
    const { el } = mountProgress({ modelValue: null })
    expect(el.getAttribute('data-state')).toBe('indeterminate')
    expect(el.getAttribute('aria-valuenow')).toBeNull()
  })
})

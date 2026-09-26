// @vitest-environment happy-dom
import { afterEach, describe, expect, it } from 'vitest'
import { createApp, h } from 'vue'
import UiSeparator from './UiSeparator.vue'

let dispose = () => {}
afterEach(() => {
  dispose()
  document.body.replaceChildren()
})

function mountSeparator(props: Record<string, unknown> = {}) {
  const host = document.createElement('div')
  document.body.append(host)
  const app = createApp({
    render() {
      return h(UiSeparator, props)
    },
  })
  app.mount(host)
  dispose = () => app.unmount()
  const separator = host.querySelector('[role="separator"]')
  return { host, separator }
}

describe('UiSeparator', () => {
  it('renders with role="separator" and orientation styling', () => {
    const { separator: horizontal } = mountSeparator({
      orientation: 'horizontal',
    })
    expect(horizontal).not.toBeNull()
    expect(horizontal?.getAttribute('role')).toBe('separator')
    expect(horizontal?.className).toContain('w-full')
    expect(horizontal?.className).toContain('h-[1px]')
    dispose()

    const { separator: vertical } = mountSeparator({
      orientation: 'vertical',
    })
    expect(vertical).not.toBeNull()
    expect(vertical?.getAttribute('role')).toBe('separator')
    expect(vertical?.getAttribute('aria-orientation')).toBe('vertical')
    expect(vertical?.className).toContain('h-full')
    expect(vertical?.className).toContain('w-[1px]')
  })
})

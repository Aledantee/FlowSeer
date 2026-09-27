// @vitest-environment happy-dom
import { afterEach, describe, expect, it, vi } from 'vitest'
import { createApp, h, nextTick } from 'vue'
import UiButton from './UiButton.vue'

let dispose = () => {}
afterEach(() => {
  dispose()
  document.body.replaceChildren()
  vi.restoreAllMocks()
})

function mountButton(
  props: Record<string, unknown> = {},
  slots: Record<string, () => unknown> = {},
) {
  const host = document.createElement('div')
  document.body.append(host)
  const app = createApp({
    render() {
      return h(UiButton, props, slots)
    },
  })
  app.mount(host)
  dispose = () => {
    app.unmount()
    dispose = () => {}
  }
  const button = host.querySelector('button')
  if (!button) throw new Error('Missing button element')
  return { host, button }
}

describe('UiButton', () => {
  it('applies primary, secondary, ghost, and danger class names', () => {
    const variants = [
      { variant: 'primary' as const, expected: 'bg-primary' },
      { variant: 'secondary' as const, expected: 'bg-card' },
      { variant: 'ghost' as const, expected: 'bg-transparent' },
      { variant: 'danger' as const, expected: 'bg-danger-surface' },
    ]
    for (const { variant, expected } of variants) {
      const { button } = mountButton({ variant })
      expect(button.className).toContain(expected)
      dispose()
    }
  })

  it('sets the HTML disabled attribute and suppresses click handlers when disabled', async () => {
    const onClick = vi.fn()
    const { button } = mountButton({ disabled: true, onClick })
    expect(button.disabled).toBe(true)
    button.click()
    await nextTick()
    expect(onClick).not.toHaveBeenCalled()
  })

  it('renders UiSpinner, sets aria-busy="true", and suppresses click handlers when loading', async () => {
    const onClick = vi.fn()
    const { button } = mountButton({ loading: true, onClick })
    expect(button.getAttribute('aria-busy')).toBe('true')
    const svg = button.querySelector('svg')
    expect(svg).not.toBeNull()
    expect(svg?.classList.contains('animate-spin')).toBe(true)
    button.click()
    await nextTick()
    expect(onClick).not.toHaveBeenCalled()
  })

  it('warns when icon button lacks aria-label', () => {
    const warnSpy = vi.spyOn(console, 'warn').mockImplementation(() => {})
    mountButton({ size: 'icon' })
    expect(warnSpy).toHaveBeenCalledWith(
      '[UiButton] An accessible aria-label is required when size="icon"',
    )
    warnSpy.mockRestore()
  })

  it('does not render slot icon when loading and size is icon', () => {
    const { button } = mountButton(
      { size: 'icon', loading: true, ariaLabel: 'Settings' },
      { default: () => h('span', { class: 'test-icon' }, 'Icon') },
    )
    expect(button.querySelector('.test-icon')).toBeNull()
    expect(button.querySelector('svg')).not.toBeNull()
  })
})

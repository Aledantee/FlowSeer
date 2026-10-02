// @vitest-environment happy-dom
import { afterEach, describe, expect, it, vi } from 'vitest'
import { createApp, h } from 'vue'
import UiField from './UiField.vue'
import UiInput from './UiInput.vue'
import { createWebI18n, type WebLocale } from '../../i18n'

let dispose = () => {}
afterEach(() => {
  dispose()
  document.body.replaceChildren()
  vi.restoreAllMocks()
})

function mountField(
  fieldProps: Record<string, unknown> = {},
  locale: WebLocale = 'en',
) {
  const host = document.createElement('div')
  document.body.append(host)
  const app = createApp({
    render() {
      return h(UiField, fieldProps, {
        default: () => h(UiInput),
      })
    },
  })
  const i18n = createWebI18n(locale)
  app.use(i18n)
  app.mount(host)
  dispose = () => app.unmount()
  const label = host.querySelector('label')
  const input = host.querySelector('input')
  const descriptions = host.querySelectorAll('p')
  return { host, label, input, descriptions, i18n }
}

describe('UiField', () => {
  it('links <label for> with input id', () => {
    const { label, input } = mountField({ label: 'Site Name' })
    expect(label).not.toBeNull()
    expect(input).not.toBeNull()
    const forAttr = label?.getAttribute('for')
    const idAttr = input?.getAttribute('id')
    expect(forAttr).toBeTruthy()
    expect(forAttr).toBe(idAttr)
  })

  it('sets aria-describedby containing description and error IDs when both are set', () => {
    const { input, host } = mountField({
      label: 'Site',
      description: 'Physical location of hardware',
      error: 'Location is required',
    })
    const describedBy = input?.getAttribute('aria-describedby')
    expect(describedBy).toBeTruthy()
    const ids = describedBy!.split(/\s+/)
    expect(ids.length).toBe(2)
    const [descId, errId] = ids
    const descEl = host.querySelector(`#${descId}`)
    const errEl = host.querySelector(`#${errId}`)
    expect(descEl?.textContent?.trim()).toBe('Physical location of hardware')
    expect(errEl?.textContent?.trim()).toBe('Location is required')
    expect(errEl?.getAttribute('role')).toBe('alert')
  })

  it('passes aria-invalid="true" to input when error prop is non-empty', () => {
    const { input } = mountField({
      label: 'Site',
      error: 'Invalid site selection',
    })
    expect(input?.getAttribute('aria-invalid')).toBe('true')
  })

  it('renders default requiredMark from message catalog when required is true', () => {
    const { label } = mountField({
      label: 'Site',
      required: true,
    })
    const mark = label?.querySelector('span')
    expect(mark).not.toBeNull()
    expect(mark?.textContent?.trim()).toBe('*')
    expect(mark?.getAttribute('aria-hidden')).toBe('true')
  })

  it('renders custom requiredMark override when provided', () => {
    const { label } = mountField({
      label: 'Site',
      required: true,
      requiredMark: ' (mandatory)',
    })
    const mark = label?.querySelector('span')
    expect(mark?.textContent?.trim()).toBe('(mandatory)')
  })
})

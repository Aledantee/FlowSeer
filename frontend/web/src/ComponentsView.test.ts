// @vitest-environment happy-dom
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createApp, nextTick } from 'vue'
import ComponentsView from './ComponentsView.vue'

let dispose = () => {}
beforeEach(() => localStorage.clear())
afterEach(() => {
  dispose()
  document.body.replaceChildren()
  vi.restoreAllMocks()
  vi.unstubAllGlobals()
})
function mountWorkspace() {
  const host = document.createElement('div')
  document.body.append(host)
  const app = createApp(ComponentsView)
  app.mount(host)
  dispose = () => app.unmount()
  return host
}
function button(host: HTMLElement, label: string) {
  const match = [...host.querySelectorAll('button')].find(
    (item) => item.textContent?.trim() === label,
  )
  if (!match) throw new Error(`Missing button: ${label}`)
  return match
}
async function writeNotes(host: HTMLElement, text: string) {
  const field = host.querySelector('textarea')
  if (!field) throw new Error('Missing notes field')
  field.value = text
  field.dispatchEvent(new Event('input', { bubbles: true }))
  await nextTick()
}

describe('component workspace', () => {
  it('uses the selected button variant and respects the disabled state', async () => {
    const host = mountWorkspace()
    const variant = host.querySelector<HTMLSelectElement>('#button-variant')
    if (!variant) throw new Error('Missing variant selector')
    variant.value = 'ghost'
    variant.dispatchEvent(new Event('change'))
    await nextTick()
    expect(
      button(host, 'Save changes').classList.contains('ui-button--ghost'),
    ).toBe(true)
    button(host, 'Save changes').click()
    await nextTick()
    expect(host.textContent).toContain('Action received')
    host.querySelector<HTMLInputElement>('input[type="checkbox"]')?.click()
    await nextTick()
    expect(button(host, 'Save changes').disabled).toBe(true)
  })

  it('saves notes and planning stage across remounts', async () => {
    let host = mountWorkspace()
    await writeNotes(host, 'Show progress during assignment.')
    const stage = host.querySelector<HTMLSelectElement>(
      '.planning-heading select',
    )
    if (!stage) throw new Error('Missing stage selector')
    stage.value = 'Styling'
    stage.dispatchEvent(new Event('change'))
    await nextTick()
    button(host, 'Save draft').click()
    await nextTick()
    expect(host.textContent).toContain('Saved in this browser.')
    dispose()
    host.remove()
    host = mountWorkspace()
    expect(host.querySelector('textarea')?.value).toBe(
      'Show progress during assignment.',
    )
    expect(
      host.querySelector<HTMLSelectElement>('.planning-heading select')?.value,
    ).toBe('Styling')
  })

  it('keeps drafts separate and prevents switching away from unsaved notes', async () => {
    const host = mountWorkspace()
    await writeNotes(host, 'Button draft')
    const status = button(host, 'Status badgesFeedback')
    expect(status.disabled).toBe(true)
    button(host, 'Save draft').click()
    await nextTick()
    status.click()
    await nextTick()
    expect(host.querySelector('textarea')?.value).toBe('')
    expect(host.querySelectorAll('.status').length).toBe(3)
    button(host, 'ButtonsActions').click()
    await nextTick()
    expect(host.querySelector('textarea')?.value).toBe('Button draft')
  })

  it('reports failed saves without claiming success', async () => {
    const host = mountWorkspace()
    await writeNotes(host, 'Keep this draft')
    vi.stubGlobal('localStorage', {
      setItem: () => {
        throw new Error('Quota exceeded')
      },
    })
    button(host, 'Save draft').click()
    await nextTick()
    expect(host.textContent).toContain('Could not save.')
    expect(host.querySelector('textarea')?.value).toBe('Keep this draft')
  })

  it('recovers from malformed stored drafts', () => {
    localStorage.setItem('flowseer.component-draft.buttons', '{bad json')
    const host = mountWorkspace()
    expect(host.textContent).toContain(
      'Draft storage is unavailable or unreadable.',
    )
    expect(host.querySelector('textarea')?.value).toBe('')
  })

  it('compares fonts only inside the typography specimen', async () => {
    const host = mountWorkspace()
    button(host, 'Foundations').click()
    await nextTick()
    const fonts = host.querySelector<HTMLSelectElement>('#specimen-font')
    if (!fonts) throw new Error('Missing font comparison')
    fonts.value = 'DM Sans Variable'
    fonts.dispatchEvent(new Event('change'))
    await nextTick()
    expect(
      host.querySelector<HTMLElement>('.type-specimen')?.style.fontFamily,
    ).toContain('DM Sans Variable')
    expect(document.documentElement.style.fontFamily).toBe('')
  })
})

import { readFileSync } from 'node:fs'
import path from 'node:path'
import { describe, expect, it } from 'vitest'

const root = path.resolve(__dirname, '..')
const read = (file: string) => readFileSync(path.join(root, file), 'utf8')
const sources = [
  'style.css',
  'navigation/AppFrame.vue',
  'FleetView.vue',
  'LoginView.vue',
  'components/topology/TopologyGraph.vue',
]

describe('frame chrome sources', () => {
  for (const name of [
    'topbar-glass',
    'main-notch',
    '--topbar-height',
    'login-glass',
    'login-sidebar',
  ])
    it(`names no ${name}`, () => {
      for (const file of sources) expect(read(file), file).not.toContain(name)
    })

  it('leaves the frame blur out of its views', () => {
    for (const file of ['LoginView.vue', 'FleetView.vue'])
      expect(read(file), file).not.toContain('backdrop-blur-2xl')
  })

  it('lets the frame panel paint behind the panes', () => {
    const panes = read('style.css').match(
      /#frame-page main\.panes\s*\{([^}]*)\}/,
    )
    expect(panes).not.toBeNull()
    expect(panes?.[1]).not.toMatch(/\bbackground(?:-[\w-]+)?\s*:/)
  })

  it('does not collapse the page region into its parent', () => {
    const style = read('navigation/AppFrame.vue').split('<style')[1] ?? ''
    const rules = [...style.matchAll(/([^{}]+)\{([^{}]*)\}/g)]
    const contents = rules.filter(([, , body]) =>
      /display:\s*contents/.test(body ?? ''),
    )
    expect(contents.length).toBeGreaterThan(0)
    for (const [, selector] of contents)
      expect(selector).not.toContain('#frame-page')
  })
})

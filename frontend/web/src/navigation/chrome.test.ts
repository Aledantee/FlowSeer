import { readFileSync, readdirSync } from 'node:fs'
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
  it('shows collapsed hover labels without a CSS transition', () => {
    const rule = read('style.css').match(
      /\n\s*\.sidebar-collapsed \.sidebar nav \.nav-text\s*\{([^}]*)\}/,
    )
    expect(rule).not.toBeNull()
    expect(rule?.[1]).not.toMatch(/\btransition(?:-[\w-]+)?\s*:/)
  })
  const files = readdirSync(root, { recursive: true, withFileTypes: true })
    .filter((entry) => entry.isFile())
    .map((entry) => path.join(entry.parentPath, entry.name))
    .filter((file) => file !== __filename)

  it('contains no View Transitions code in any source file', () => {
    expect(files).toContain(path.join(root, 'style.css'))
    for (const file of files)
      expect(readFileSync(file, 'utf8'), file).not.toMatch(
        /startViewTransition|view-transition/,
      )
  })

  it('contains no login entrance code in any source file', () => {
    expect(files).toContain(path.join(root, 'LoginView.vue'))
    for (const file of files)
      expect(readFileSync(file, 'utf8'), file).not.toMatch(
        /is-entering|enter-rise|enter-drop|enter-word|enter-fade|--enter-delay|takeEntrance/,
      )
  })

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

  for (const selector of ['#frame-page main.panes', '.pane', '.pane-scroll'])
    it(`lets the frame panel paint behind ${selector}`, () => {
      const escaped = selector.replace(/[.#]/g, '\\$&')
      const rule = read('style.css').match(
        new RegExp(`(?:^|\\n)${escaped}\\s*\\{([^}]*)\\}`),
      )
      expect(rule).not.toBeNull()
      expect(rule?.[1]).not.toMatch(/\bbackground(?:-[\w-]+)?\s*:/)
    })

  it('lets the frame panel paint behind the topology canvas', () => {
    const [template = '', block = ''] = read(
      'components/topology/TopologyGraph.vue',
    ).split('<style')
    const style = block.slice(block.indexOf('>') + 1)
    expect(template).not.toMatch(/topology-graph [^"]*\bbg-/)
    expect(template).not.toContain('bg-color=')
    const canvas =
      /^\.topology-graph(?:\s+:deep\(\.vue-flow(?:__(?:pane|background|container))?\))?$/
    const rules = [...style.matchAll(/([^{}]+)\{([^{}]*)\}/g)].filter(
      ([, selector]) =>
        (selector ?? '').split(',').some((part) => canvas.test(part.trim())),
    )
    expect(rules.length).toBeGreaterThan(0)
    for (const [, selector, body] of rules)
      expect(body, selector).not.toMatch(/\bbackground(?:-[\w-]+)?\s*:/)
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

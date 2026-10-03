import { describe, expect, it } from 'vitest'
import { templateLiterals } from './testing'

// Files whose templates must hold no literal text. A literal shows as
// `file:line text` in the failure.
const checked = import.meta.glob<string>(
  [
    '../ui/**/Ui*.vue',
    '../FleetView.vue',
    '../navigation/PageDock.vue',
    '../navigation/PageHost.vue',
    '../components/AccountMenu.vue',
    '../components/HelpButton.vue',
    '../components/ReportBugButton.vue',
    '../components/ScopeSwitcher.vue',
    '../components/TenantSwitcher.vue',
    '../components/ThemeSwitcher.vue',
    '../components/GlobalSearch.vue',
  ],
  {
    eager: true,
    query: '?raw',
    import: 'default',
  },
)

describe('templateLiterals', () => {
  it('reports a text node with its line', () => {
    const source = [
      '<template>',
      '  <div>',
      '    <p>Hello</p>',
      '  </div>',
      '</template>',
    ].join('\n')
    expect(templateLiterals(source)).toEqual([
      { line: 3, kind: 'text', text: 'Hello' },
    ])
  })

  it('reports a lone middle dot', () => {
    const source = [
      '<template>',
      '  <span>',
      '    ·',
      '  </span>',
      '</template>',
    ].join('\n')
    expect(templateLiterals(source)).toEqual([
      { line: 3, kind: 'text', text: '·' },
    ])
  })

  it('reports a static aria-label with its line', () => {
    const source = [
      '<template>',
      '  <button',
      '    type="button"',
      '    aria-label="Close"',
      '  />',
      '</template>',
    ].join('\n')
    expect(templateLiterals(source)).toEqual([
      { line: 4, kind: 'attribute', name: 'aria-label', text: 'Close' },
    ])
  })

  it('reports a static placeholder with its line', () => {
    const source = [
      '<template>',
      '  <input placeholder="Search" />',
      '</template>',
    ].join('\n')
    expect(templateLiterals(source)).toEqual([
      { line: 2, kind: 'attribute', name: 'placeholder', text: 'Search' },
    ])
  })

  it('ignores an interpolation, a bound attribute, and an empty alt', () => {
    const source = [
      '<template>',
      '  <div :aria-label="label" :title="`x`" v-bind:placeholder="t(\'k\')">',
      '    {{ t("k") }}',
      '    <img alt="" src="a.png" />',
      '    <span>',
      '    </span>',
      '  </div>',
      '</template>',
    ].join('\n')
    expect(templateLiterals(source)).toEqual([])
  })

  it('does not report script text or attributes outside the list', () => {
    const source = [
      '<script setup lang="ts">',
      "const label = 'Hello'",
      '</script>',
      '<template>',
      '  <div class="box" data-testid="Hello" />',
      '</template>',
    ].join('\n')
    expect(templateLiterals(source)).toEqual([])
  })
})

describe('app templates', () => {
  it('finds the component files it checks', () => {
    expect(Object.keys(checked).length).toBeGreaterThan(0)
  })

  it.each(Object.entries(checked))(
    '%s holds no literal text',
    (path, source) => {
      const found = templateLiterals(source).map(
        (literal) =>
          `${path}:${literal.line} ${literal.name ?? 'text'} ${JSON.stringify(literal.text)}`,
      )
      expect(found).toEqual([])
    },
  )
})

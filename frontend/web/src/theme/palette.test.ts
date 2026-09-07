import { readFileSync } from 'node:fs'
import { describe, expect, it } from 'vitest'

const css = readFileSync(new URL('../style.css', import.meta.url), 'utf8')

function tokens(selector: string): Record<string, string> {
  const start = css.indexOf(selector)
  if (start < 0) throw new Error(`Missing palette: ${selector}`)
  const block = css.slice(start, css.indexOf('}', start))
  return Object.fromEntries(
    [...block.matchAll(/(--[\w-]+):\s*(#[\da-f]{6});/gi)].map((match) => [
      match[1],
      match[2],
    ]),
  )
}
function luminance(hex: string): number {
  const channels = [1, 3, 5].map(
    (offset) => parseInt(hex.slice(offset, offset + 2), 16) / 255,
  )
  const [r = 0, g = 0, b = 0] = channels.map((value) =>
    value <= 0.04045 ? value / 12.92 : ((value + 0.055) / 1.055) ** 2.4,
  )
  return r * 0.2126 + g * 0.7152 + b * 0.0722
}
const pairs: [string, string, number][] = [
  ...[
    '--page',
    '--surface',
    '--surface-subtle',
    '--surface-hover',
    '--panel-surface',
  ].flatMap((background) => [
    ['--text', background, 7] satisfies [string, string, number],
    ['--muted', background, 4.5] satisfies [string, string, number],
    ['--accent-text', background, 4.5] satisfies [string, string, number],
    ['--accent-orange-text', background, 4.5] satisfies [
      string,
      string,
      number,
    ],
    ['--focus', background, 3] satisfies [string, string, number],
    ['--control-border', background, 3] satisfies [string, string, number],
    ['--connection', background, 3] satisfies [string, string, number],
  ]),
  ['--on-coral', '--coral', 4.5],
  ['--text', '--warning-surface', 4.5],
  ['--healthy-text', '--healthy-surface', 4.5],
  ['--warning-text', '--warning-surface', 4.5],
  ['--offline-text', '--offline-surface', 4.5],
  ['--text', '--info-surface', 4.5],
  ['--muted', '--info-surface', 4.5],
  ...['--chrome', '--chrome-surface', '--chrome-hover'].flatMap(
    (background) => [
      ['--chrome-focus', background, 4.5] satisfies [string, string, number],
      ['--chrome-text', background, 4.5] satisfies [string, string, number],
      ['--chrome-muted', background, 4.5] satisfies [string, string, number],
      ['--control-border', background, 3] satisfies [string, string, number],
    ],
  ),
]
const light = tokens(':root {')
for (const [theme, palette] of Object.entries({
  light,
  dark: { ...light, ...tokens(":root[data-theme='dark'] {") },
})) {
  describe(`${theme} palette contrast`, () => {
    const themePairs: [string, string, number][] =
      theme === 'light'
        ? [
            ...pairs,
            ['--warning-border', '--warning-surface', 3],
            ['--offline-border', '--offline-surface', 3],
            ['--info-border', '--info-surface', 3],
          ]
        : pairs
    it.each(themePairs)(
      '%s on %s meets %s:1',
      (foreground, background, minimum) => {
        const fg = palette[foreground],
          bg = palette[background]
        if (!fg || !bg)
          throw new Error(`Missing color: ${foreground} / ${background}`)
        const a = luminance(fg),
          b = luminance(bg)
        expect(
          (Math.max(a, b) + 0.05) / (Math.min(a, b) + 0.05),
        ).toBeGreaterThanOrEqual(minimum)
      },
    )
  })
}

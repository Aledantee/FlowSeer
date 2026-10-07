import {
  copyFileSync,
  mkdirSync,
  mkdtempSync,
  readFileSync,
  rmSync,
  writeFileSync,
} from 'node:fs'
import os from 'node:os'
import path from 'node:path'
import { afterEach, beforeEach, describe, expect, it } from 'vitest'
import { writePaletteOutputs } from '../../scripts/palette-outputs.ts'
import {
  contrast,
  glowStops,
  over,
  pairs,
  renderSemantic,
  resolve,
  toSrgb,
  type PaletteSource,
  type Glow,
} from './palette.ts'

const sourcePath = path.resolve(__dirname, '../../design/palette-source.json')
const source: PaletteSource = JSON.parse(readFileSync(sourcePath, 'utf8'))

const glow: Glow = {
  baseAlpha: 0.82,
  strength: { light: 0.13, dark: 0.3 },
  largestAlpha: { teal: 0.14, orange: 0.1, highlight: 0.18 },
  tokens: {
    light: {
      base: 'chrome',
      teal: 'accent',
      orange: 'primary',
      highlight: 'popover',
    },
    dark: {
      base: 'chrome',
      teal: 'accent',
      orange: 'primary',
      highlight: 'foreground',
    },
  },
}

describe('palette module and contrast gate', () => {
  it('composites sRGB channels with the top alpha', () => {
    expect(over([1, 1, 1], 0.5, [0, 0, 0])).toEqual([0.5, 0.5, 0.5])
    expect(over([1, 0, 0], 0.25, [0, 1, 0])).toEqual([0.25, 0.75, 0])
  })

  it('keeps glow parameters aligned with the stylesheet', () => {
    const css = readFileSync(path.resolve(__dirname, 'brand-glow.css'), 'utf8')
    const rules = [...css.matchAll(/([^{}]+)\{([^{}]+)\}/g)]
    const defaults =
      rules.find(([, selector]) => selector.trim() === ':root')?.[2] ?? ''
    for (const theme of ['light', 'dark'] as const) {
      const selector =
        theme === 'light'
          ? ":root:not([data-theme='dark'])"
          : ":root[data-theme='dark']"
      const override =
        rules.find(([, rule]) => rule.trim() === selector)?.[2] ?? ''
      const declarations = defaults + override
      const strengths = [
        ...declarations.matchAll(/--glow-strength:\s*(\d+(?:\.\d+)?)%/g),
      ]
      expect(Number(strengths.at(-1)?.[1]) / 100).toBe(glow.strength[theme])
      for (const color of ['base', 'teal', 'orange', 'highlight'] as const) {
        const mappings = [
          ...declarations.matchAll(
            new RegExp(`--glow-${color}:\\s*var\\(--([\\w-]+)\\)`, 'g'),
          ),
        ]
        expect(mappings.at(-1)?.[1]).toBe(glow.tokens[theme][color])
      }
    }
    expect(
      Number(css.match(/var\(--glow-base\)\s+(\d+(?:\.\d+)?)%/)?.[1]) / 100,
    ).toBe(glow.baseAlpha)
    for (const color of ['teal', 'orange', 'highlight'] as const) {
      const alphas = [
        ...css.matchAll(
          new RegExp(
            `rgb\\(from var\\(--glow-${color}\\) r g b / (\\d+(?:\\.\\d+)?)%\\)`,
            'g',
          ),
        ),
      ]
      expect(alphas.length).toBeGreaterThan(0)
      expect(Math.max(...alphas.map((match) => Number(match[1]) / 100))).toBe(
        glow.largestAlpha[color],
      )
    }
  })

  it('builds seven glow stops, each ribbon and highlight over its own radial', () => {
    const achromatic: PaletteSource = {
      ...source,
      families: {
        neutral: {
          ...source.families.neutral,
          light: [
            [0, 0],
            [1, 0],
            [0.5, 0],
          ],
          dark: [
            [0, 0],
            [1, 0],
            [0.5, 0],
          ],
        },
      },
      semantic: {
        background: { light: 'neutral-2', dark: 'neutral-2' },
        chrome: { light: 'neutral-1', dark: 'neutral-1' },
        accent: { light: 'neutral-2', dark: 'neutral-2' },
        primary: { light: 'neutral-3', dark: 'neutral-3' },
        foreground: { light: 'neutral-1', dark: 'neutral-1' },
      },
    }
    const stops = glowStops(achromatic, 'dark', glow)
    expect(stops).toHaveLength(7)
    const [orange] = toSrgb(resolve(achromatic, 'primary', 'dark'))
    expect(orange).toBeGreaterThan(0.2)
    expect(orange).toBeLessThan(0.8)
    const expected = [
      0.18,
      0,
      0.50636,
      0.37 * orange + 0.1134,
      0.1476,
      0.34932,
      0.246 * orange + 0.10332,
    ]
    stops.forEach((stop, index) => {
      for (const channel of stop)
        expect(channel).toBeCloseTo(expected[index], 6)
    })
  })

  for (const theme of ['light', 'dark'] as const) {
    it(`${theme} glass and panel meet contrast thresholds over every glow stop`, () => {
      const glass = resolve(source, 'glass', theme)
      const panel = resolve(source, 'glass-panel', theme)
      const stops = glowStops(source, theme, glow)
      expect(stops).toHaveLength(7)
      stops.forEach((stop, index) => {
        const ground = over(toSrgb(glass), glass.alpha, stop)
        for (const foreground of [
          'foreground',
          'muted-foreground',
          'chrome-foreground',
          'chrome-muted-foreground',
          'chrome-ring',
        ]) {
          expect(
            contrast(toSrgb(resolve(source, foreground, theme)), ground),
            `${foreground} on ${theme} glass stop ${index}`,
          ).toBeGreaterThanOrEqual(4.5)
        }
        const panelGround = over(toSrgb(panel), panel.alpha, ground)
        const panelPairs = pairs.filter(
          ([, background]) => background === 'background',
        )
        expect(panelPairs).toHaveLength(7)
        const canvasPairs: [string, string, number][] = [
          ['warning-border', 'background', 3],
          ['danger-border', 'background', 3],
          ['danger-foreground', 'background', 4.5],
        ]
        for (const [foreground, , minimum] of [...panelPairs, ...canvasPairs]) {
          expect(
            contrast(toSrgb(resolve(source, foreground, theme)), panelGround),
            `${foreground} on ${theme} panel stop ${index}`,
          ).toBeGreaterThanOrEqual(minimum)
        }
      })
    })
  }

  it('needs dark glass above the teal highlight for readable chrome text', () => {
    const stop = glowStops(source, 'dark', glow)[5]
    const foreground = toSrgb(
      resolve(source, 'chrome-muted-foreground', 'dark'),
    )
    const glass = resolve(source, 'glass', 'dark')
    expect(
      contrast(foreground, over(toSrgb(glass), glass.alpha, stop)),
    ).toBeGreaterThanOrEqual(4.5)
    expect(contrast(foreground, over(toSrgb(glass), 0, stop))).toBeLessThan(3)
  })

  describe('WCAG contrast thresholds', () => {
    for (const theme of ['light', 'dark'] as const) {
      describe(`${theme} theme`, () => {
        it.each(pairs)(
          '%s on %s meets %s:1',
          (foreground, background, minimum) => {
            const fgOklch = resolve(source, foreground, theme)
            const bgOklch = resolve(source, background, theme)
            const ratio = contrast(toSrgb(fgOklch), toSrgb(bgOklch))
            expect(ratio).toBeGreaterThanOrEqual(minimum)
          },
        )
      })
    }
  })

  it('resolves every semantic token in both themes without throwing', () => {
    for (const theme of ['light', 'dark'] as const) {
      for (const token of Object.keys(source.semantic)) {
        expect(() => resolve(source, token, theme)).not.toThrow()
      }
    }
  })

  describe('generator write path', () => {
    const outputFiles = [
      'src/theme/scales.css',
      'src/theme/semantic.css',
      'design/palette.json',
    ] as const
    let tempDir: string

    const sentinelContent = (relPath: string) => `/* sentinel ${relPath} */\n`

    beforeEach(() => {
      tempDir = mkdtempSync(path.join(os.tmpdir(), 'palette-test-'))
      copyFileSync(
        path.resolve(__dirname, '../../.prettierrc.json'),
        path.join(tempDir, '.prettierrc.json'),
      )
      for (const relPath of outputFiles) {
        const fullPath = path.join(tempDir, relPath)
        mkdirSync(path.dirname(fullPath), { recursive: true })
        writeFileSync(fullPath, sentinelContent(relPath), 'utf8')
      }
    })

    afterEach(() => {
      rmSync(tempDir, { recursive: true, force: true })
    })

    it('rejects an invalid source and leaves all three sentinel outputs unchanged', async () => {
      const invalidSource: PaletteSource = {
        ...source,
        semantic: {
          ...source.semantic,
          card: {
            ...source.semantic.card,
            light: 'neutral-3',
          },
          overlay: {
            light: 'teal-3',
            dark: 'teal-3',
          },
        },
      }

      await expect(
        writePaletteOutputs(invalidSource, tempDir),
      ).rejects.toThrow()

      for (const relPath of outputFiles) {
        const content = readFileSync(path.join(tempDir, relPath), 'utf8')
        expect(content).toBe(sentinelContent(relPath))
      }
    })

    it('writes all three outputs for the valid real source matching repository files', async () => {
      const rootDir = path.resolve(__dirname, '../..')
      await writePaletteOutputs(source, tempDir)

      for (const relPath of outputFiles) {
        const generatedContent = readFileSync(
          path.join(tempDir, relPath),
          'utf8',
        )
        const diskContent = readFileSync(path.join(rootDir, relPath), 'utf8')
        expect(
          generatedContent,
          `${relPath} is out of date; regenerate with: node --experimental-strip-types scripts/build-palette.ts`,
        ).toBe(diskContent)
      }
    })
  })

  it('declares only valid var(--m3-*) or color-mix values in semantic CSS', () => {
    const css = renderSemantic(source)
    const pattern =
      /^(var\(--m3-[a-z]+-(1[0-2]|[1-9])\)|color-mix\(in oklch, var\(--m3-[a-z]+-(1[0-2]|[1-9])\) \d{1,3}%, transparent\))$/
    const matches = [...css.matchAll(/--[\w-]+:\s*([^;]+);/g)]
    expect(matches.length).toBeGreaterThan(0)
    for (const [, value] of matches) {
      expect(value.trim()).toMatch(pattern)
    }
  })

  it('throws on unknown family, step out of bounds, or unmapped token', () => {
    expect(() => resolve(source, 'neutral-13', 'light')).toThrow()
    expect(() => resolve(source, 'teal-3', 'light')).toThrow()
    expect(() => resolve(source, 'unknown-token', 'light')).toThrow()
  })

  it('pins coral-9 toSrgb within 0.004 of anchor #FF451D', () => {
    const coral9 = resolve(source, 'primary', 'light')
    const [r, g, b] = toSrgb(coral9)
    expect(Math.abs(r - 1)).toBeLessThanOrEqual(0.004)
    expect(Math.abs(g - 0.271)).toBeLessThanOrEqual(0.004)
    expect(Math.abs(b - 0.114)).toBeLessThanOrEqual(0.004)
  })

  it('computes contrast([1, 1, 1], [0, 0, 0]) as 21', () => {
    expect(contrast([1, 1, 1], [0, 0, 0])).toBe(21)
  })
})

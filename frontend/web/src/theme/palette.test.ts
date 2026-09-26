import { readFileSync } from 'node:fs'
import path from 'node:path'
import prettier from 'prettier'
import { describe, expect, it } from 'vitest'
import {
  computePalette,
  contrast,
  pairs,
  renderScales,
  renderSemantic,
  resolve,
  toSrgb,
  type PaletteSource,
} from './palette.ts'

const sourcePath = path.resolve(__dirname, '../../design/palette-source.json')
const source: PaletteSource = JSON.parse(readFileSync(sourcePath, 'utf8'))
const scalesPath = path.resolve(__dirname, 'scales.css')
const semanticPath = path.resolve(__dirname, 'semantic.css')

describe('palette module and contrast gate', () => {
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

  it('matches src/theme/scales.css on disk', async () => {
    const scalesDisk = readFileSync(scalesPath, 'utf8')
    const scalesConfig = await prettier.resolveConfig(scalesPath)
    const formattedScales = await prettier.format(renderScales(source), {
      ...scalesConfig,
      filepath: scalesPath,
    })
    expect(
      formattedScales,
      'src/theme/scales.css is out of date; regenerate with: node --experimental-strip-types scripts/build-palette.ts',
    ).toBe(scalesDisk)
  })

  it('matches src/theme/semantic.css on disk', async () => {
    const semanticDisk = readFileSync(semanticPath, 'utf8')
    const semanticConfig = await prettier.resolveConfig(semanticPath)
    const formattedSemantic = await prettier.format(renderSemantic(source), {
      ...semanticConfig,
      filepath: semanticPath,
    })
    expect(
      formattedSemantic,
      'src/theme/semantic.css is out of date; regenerate with: node --experimental-strip-types scripts/build-palette.ts',
    ).toBe(semanticDisk)
  })

  it('matches design/palette.json on disk', async () => {
    const palettePath = path.resolve(__dirname, '../../design/palette.json')
    const paletteDisk = readFileSync(palettePath, 'utf8')
    const paletteConfig = await prettier.resolveConfig(palettePath)
    const formattedPalette = await prettier.format(
      JSON.stringify(computePalette(source), null, 2) + '\n',
      {
        ...paletteConfig,
        filepath: palettePath,
      },
    )
    expect(
      formattedPalette,
      'design/palette.json is out of date; regenerate with: node --experimental-strip-types scripts/build-palette.ts',
    ).toBe(paletteDisk)
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

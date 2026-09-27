import { readFileSync } from 'node:fs'
import path from 'node:path'
import { describe, expect, it } from 'vitest'
import { buildPaletteOutputs } from '../../scripts/palette-outputs.ts'
import {
  contrast,
  pairs,
  renderSemantic,
  resolve,
  toSrgb,
  type PaletteSource,
} from './palette.ts'

const sourcePath = path.resolve(__dirname, '../../design/palette-source.json')
const source: PaletteSource = JSON.parse(readFileSync(sourcePath, 'utf8'))

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

  it('rejects for a source whose ungated token refs teal-3', async () => {
    const invalidSource: PaletteSource = {
      ...source,
      semantic: {
        ...source.semantic,
        overlay: {
          light: 'teal-3',
          dark: 'teal-3',
        },
      },
    }
    await expect(buildPaletteOutputs(invalidSource)).rejects.toThrow()
  })

  it('resolves for the real source to content equal to the three files on disk', async () => {
    const rootDir = path.resolve(__dirname, '../..')
    const outputs = await buildPaletteOutputs(source, rootDir)
    expect(outputs).toHaveLength(3)
    for (const { path: filePath, content } of outputs) {
      const diskContent = readFileSync(filePath, 'utf8')
      const relPath = path.relative(rootDir, filePath)
      expect(
        content,
        `${relPath} is out of date; regenerate with: node --experimental-strip-types scripts/build-palette.ts`,
      ).toBe(diskContent)
    }
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

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

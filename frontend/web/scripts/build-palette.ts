import { readFileSync, writeFileSync } from 'node:fs'
import { URL } from 'node:url'
import prettier from 'prettier'
import type { PaletteSource } from '../src/theme/palette.ts'
import { renderScales, renderSemantic, resolve } from '../src/theme/palette.ts'

const root = new URL('../', import.meta.url)
const sourcePath = new URL('design/palette-source.json', root)
const source: PaletteSource = JSON.parse(readFileSync(sourcePath, 'utf8'))

const scalesCssPath = new URL('src/theme/scales.css', root)
const semanticCssPath = new URL('src/theme/semantic.css', root)
const paletteJsonPath = new URL('design/palette.json', root)

async function formatFile(content: string, filepath: string): Promise<string> {
  const options = await prettier.resolveConfig(filepath)
  return prettier.format(content, { ...options, filepath })
}

async function main() {
  const scalesContent = renderScales(source)
  const formattedScales = await formatFile(
    scalesContent,
    scalesCssPath.pathname,
  )
  writeFileSync(scalesCssPath, formattedScales)

  const semanticContent = renderSemantic(source)
  const formattedSemantic = await formatFile(
    semanticContent,
    semanticCssPath.pathname,
  )
  writeFileSync(semanticCssPath, formattedSemantic)

  const semantics = {
    light: Object.fromEntries(
      Object.keys(source.semantic).map((token) => {
        const res = resolve(source, token, 'light')
        const val =
          res.alpha < 1
            ? `oklch(${res.l} ${res.c} ${res.h} / ${res.alpha})`
            : `oklch(${res.l} ${res.c} ${res.h})`
        return [`--${token}`, val]
      }),
    ),
    dark: Object.fromEntries(
      Object.keys(source.semantic).map((token) => {
        const res = resolve(source, token, 'dark')
        const val =
          res.alpha < 1
            ? `oklch(${res.l} ${res.c} ${res.h} / ${res.alpha})`
            : `oklch(${res.l} ${res.c} ${res.h})`
        return [`--${token}`, val]
      }),
    ),
  }

  const palette = {
    ...source,
    semantics,
  }
  const formattedPalette = await formatFile(
    JSON.stringify(palette, null, 2) + '\n',
    paletteJsonPath.pathname,
  )
  writeFileSync(paletteJsonPath, formattedPalette)
}

main().catch((err: unknown) => {
  console.error(err)
  process.exit(1)
})

import { resolve } from 'node:path'
import { fileURLToPath } from 'node:url'
import prettier from 'prettier'
import {
  computePalette,
  renderScales,
  renderSemantic,
  type PaletteSource,
} from '../src/theme/palette.ts'

export interface PaletteOutputFile {
  path: string
  content: string
}

export async function buildPaletteOutputs(
  source: PaletteSource,
  rootDir = fileURLToPath(new URL('../', import.meta.url)),
): Promise<PaletteOutputFile[]> {
  const palette = computePalette(source)

  const scalesCssPath = resolve(rootDir, 'src/theme/scales.css')
  const semanticCssPath = resolve(rootDir, 'src/theme/semantic.css')
  const paletteJsonPath = resolve(rootDir, 'design/palette.json')

  const scalesContent = renderScales(source)
  const semanticContent = renderSemantic(source)
  const paletteContent = JSON.stringify(palette, null, 2) + '\n'

  const formatFile = async (content: string, filepath: string) => {
    const options = await prettier.resolveConfig(filepath)
    return prettier.format(content, { ...options, filepath })
  }

  const [formattedScales, formattedSemantic, formattedPalette] =
    await Promise.all([
      formatFile(scalesContent, scalesCssPath),
      formatFile(semanticContent, semanticCssPath),
      formatFile(paletteContent, paletteJsonPath),
    ])

  return [
    { path: scalesCssPath, content: formattedScales },
    { path: semanticCssPath, content: formattedSemantic },
    { path: paletteJsonPath, content: formattedPalette },
  ]
}

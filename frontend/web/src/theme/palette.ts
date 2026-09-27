export type Theme = 'light' | 'dark'

export interface ColorRefWithAlpha {
  ref: string
  alpha: number
}

export type ColorRef = string | ColorRefWithAlpha

export interface SemanticTokenMapping {
  light: ColorRef
  dark: ColorRef
}

export interface FamilyDefinition {
  seed: string
  role: string
  hue: number
  light: number[][]
  dark: number[][]
}

export interface PaletteSource {
  source: string
  captured: string
  anchors: Record<string, string>
  graySeed: string
  backgrounds: { light: string; dark: string }
  families: Record<string, FamilyDefinition>
  semantic: Record<string, SemanticTokenMapping>
}

export interface ResolvedColor {
  l: number
  c: number
  h: number
  alpha: number
}

export type Srgb = [number, number, number]

export type ContrastPair = [string, string, number]

const surfaces = ['background', 'card', 'subtle', 'hover', 'card-header']

export const pairs: ContrastPair[] = [
  ...surfaces.flatMap((bg): ContrastPair[] => [
    ['foreground', bg, 7],
    ['muted-foreground', bg, 4.5],
    ['accent-foreground', bg, 4.5],
    ['primary-text', bg, 4.5],
    ['ring', bg, 3],
    ['input', bg, 3],
    ['graph-edge', bg, 3],
  ]),
  ['primary-foreground', 'primary', 4.5],
  ['foreground', 'warning-surface', 4.5],
  ['success-foreground', 'success-surface', 4.5],
  ['warning-foreground', 'warning-surface', 4.5],
  ['danger-foreground', 'danger-surface', 4.5],
  ['foreground', 'info-surface', 4.5],
  ['muted-foreground', 'info-surface', 4.5],
  ...(['chrome', 'chrome-surface', 'chrome-hover'] as const).flatMap(
    (bg): ContrastPair[] => [
      ['chrome-ring', bg, 4.5],
      ['chrome-foreground', bg, 4.5],
      ['chrome-muted-foreground', bg, 4.5],
      ['input', bg, 3],
    ],
  ),
  ['foreground', 'popover', 7],
  ['muted-foreground', 'popover', 4.5],
  ['success-border', 'card', 3],
  ['warning-border', 'card', 3],
  ['danger-border', 'card', 3],
  ['info-border', 'card', 3],
  ['chart-1', 'card', 3],
  ['chart-2', 'card', 3],
  ['chart-3', 'card', 3],
  ['chart-4', 'card', 3],
  ['chart-5', 'card', 3],
  ['chart-6', 'card', 3],
  ['info-foreground', 'info-surface', 4.5],
  ['warning-border', 'warning-surface', 3],
  ['danger-border', 'danger-surface', 3],
  ['info-border', 'info-surface', 3],
]

export function resolve(
  source: PaletteSource,
  token: string,
  theme: Theme,
): ResolvedColor {
  const cleanToken = token.startsWith('--') ? token.slice(2) : token
  let ref: ColorRef | undefined
  if (source.semantic && cleanToken in source.semantic) {
    ref = source.semantic[cleanToken][theme]
  } else {
    ref = cleanToken
  }

  let refStr: string
  let alpha = 1
  if (typeof ref === 'string') {
    refStr = ref
  } else if (ref && typeof ref === 'object' && 'ref' in ref) {
    refStr = ref.ref
    alpha = ref.alpha
  } else {
    throw new Error(`Unknown token: ${token}`)
  }

  const match = refStr.match(/^([a-z]+)-(-?\d+)$/)
  if (!match) {
    throw new Error(`Unknown token: ${token}`)
  }

  const [, familyName, stepStr] = match
  const family = source.families[familyName]
  if (!family) {
    throw new Error(`Unknown family: ${familyName}`)
  }

  const step = parseInt(stepStr, 10)
  if (step < 1 || step > 12) {
    throw new Error(`Invalid step ${step}: must be between 1 and 12`)
  }

  const steps = family[theme]
  if (!steps || steps.length < step) {
    throw new Error(`Missing step ${step} for ${familyName} in ${theme}`)
  }

  const stepData = steps[step - 1]
  const l = stepData?.[0]
  const c = stepData?.[1]
  if (l === undefined || c === undefined) {
    throw new Error(`Missing step ${step} for ${familyName} in ${theme}`)
  }
  return { l, c, h: family.hue, alpha }
}

export function toSrgb(
  oklch:
    | { l: number; c: number; h: number; alpha?: number }
    | [number, number, number],
): Srgb {
  const L = Array.isArray(oklch) ? oklch[0] : oklch.l
  const C = Array.isArray(oklch) ? oklch[1] : oklch.c
  const H = Array.isArray(oklch) ? oklch[2] : oklch.h

  const hRad = (H * Math.PI) / 180
  const a = C * Math.cos(hRad)
  const b = C * Math.sin(hRad)

  const l_ = L + 0.3963377774 * a + 0.2158037573 * b
  const m_ = L - 0.1055613458 * a - 0.0638541728 * b
  const s_ = L - 0.0894841775 * a - 1.291485548 * b

  const l = l_ ** 3
  const m = m_ ** 3
  const s = s_ ** 3

  const rLin = +4.0767416621 * l - 3.3077115913 * m + 0.2309699292 * s
  const gLin = -1.2684380046 * l + 2.6097574011 * m - 0.3413193965 * s
  const bLin = -0.0041960863 * l - 0.7034186147 * m + 1.707614701 * s

  const toGamma = (c: number): number => {
    if (c <= 0.0031308) return 12.92 * c
    return 1.055 * Math.pow(c, 1 / 2.4) - 0.055
  }

  const r = Math.max(0, Math.min(1, toGamma(rLin)))
  const g = Math.max(0, Math.min(1, toGamma(gLin)))
  const bChannel = Math.max(0, Math.min(1, toGamma(bLin)))

  return [r, g, bChannel]
}

function luminance(srgb: Srgb): number {
  const [r = 0, g = 0, b = 0] = srgb.map((c) =>
    c <= 0.04045 ? c / 12.92 : ((c + 0.055) / 1.055) ** 2.4,
  )
  return 0.2126 * r + 0.7152 * g + 0.0722 * b
}

export function contrast(a: Srgb, b: Srgb): number {
  const l1 = luminance(a)
  const l2 = luminance(b)
  return (Math.max(l1, l2) + 0.05) / (Math.min(l1, l2) + 0.05)
}

export function renderScales(source: PaletteSource): string {
  let css = ''
  for (const theme of ['light', 'dark'] as const) {
    css += `${theme === 'light' ? ':root' : ":root[data-theme='dark']"} {\n`
    for (const [name, family] of Object.entries(source.families)) {
      if (family[theme].length !== 12) {
        throw new Error(`${name} needs 12 ${theme} steps`)
      }
      family[theme].forEach((stepData, index) => {
        const l = stepData[0]
        const c = stepData[1]
        if (l === undefined || c === undefined) {
          throw new Error(`Missing step data for ${name} step ${index + 1}`)
        }
        css += `  --m3-${name}-${index + 1}: oklch(${l} ${c} ${family.hue});\n`
      })
    }
    css += '}\n'
  }
  return css
}

export function computePalette(source: PaletteSource): PaletteSource & {
  semantics: { light: Record<string, string>; dark: Record<string, string> }
} {
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

  return {
    ...source,
    semantics,
  }
}

export function renderSemantic(source: PaletteSource): string {
  let css = ''
  for (const theme of ['light', 'dark'] as const) {
    css += `${theme === 'light' ? ':root' : ":root[data-theme='dark']"} {\n`
    css += `  color-scheme: ${theme};\n`
    for (const [token, mapping] of Object.entries(source.semantic)) {
      const def = mapping[theme]
      if (typeof def === 'string') {
        css += `  --${token}: var(--m3-${def});\n`
      } else {
        const percentage = Math.round(def.alpha * 100)
        css += `  --${token}: color-mix(in oklch, var(--m3-${def.ref}) ${percentage}%, transparent);\n`
      }
    }
    css += '}\n'
  }
  return css
}

export interface PaletteOutputFile {
  path: string
  content: string
}

export async function buildPaletteOutputs(
  source: PaletteSource,
  rootDir?: string,
): Promise<PaletteOutputFile[]> {
  // Validate all semantic tokens and compute palette before any formatting.
  // Throws if any token refs an unknown family, missing step, or invalid ref.
  const palette = computePalette(source)

  const nodeUrl = 'node:url'
  const nodePath = 'node:path'
  const { fileURLToPath } = await import(/* @vite-ignore */ nodeUrl)
  const { resolve: pathResolve } = await import(/* @vite-ignore */ nodePath)
  const baseDir =
    rootDir !== undefined
      ? rootDir
      : pathResolve(
          fileURLToPath(new URL(/* @vite-ignore */ '../../', import.meta.url)),
        )

  const scalesCssPath = pathResolve(baseDir, 'src/theme/scales.css')
  const semanticCssPath = pathResolve(baseDir, 'src/theme/semantic.css')
  const paletteJsonPath = pathResolve(baseDir, 'design/palette.json')

  const scalesContent = renderScales(source)
  const semanticContent = renderSemantic(source)
  const paletteContent = JSON.stringify(palette, null, 2) + '\n'

  const prettier = (await import('prettier')).default
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

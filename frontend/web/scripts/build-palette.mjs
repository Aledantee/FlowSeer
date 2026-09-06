import { readFileSync, writeFileSync } from 'node:fs'
import { URL } from 'node:url'

const root = new URL('../', import.meta.url)
const source = JSON.parse(
  readFileSync(new URL('design/palette-source.json', root), 'utf8'),
)
const stylesheet = readFileSync(new URL('src/style.css', root), 'utf8')
const semanticTokens = (selector) => {
  const start = stylesheet.indexOf(selector)
  if (start < 0) throw new Error(`Missing palette selector ${selector}`)
  const block = stylesheet.slice(start, stylesheet.indexOf('}', start))
  return Object.fromEntries(
    [...block.matchAll(/(--[\w-]+):\s*(#[\da-f]{6});/gi)].map((match) => [
      match[1],
      match[2],
    ]),
  )
}
const light = semanticTokens(':root {')
const palette = {
  ...source,
  semantics: {
    light,
    dark: { ...light, ...semanticTokens(":root[data-theme='dark'] {") },
  },
}
let css = ''
for (const theme of ['light', 'dark']) {
  css += `${theme === 'light' ? ':root' : ":root[data-theme='dark']"} {\n`
  for (const [name, family] of Object.entries(source.families)) {
    if (family[theme].length !== 12)
      throw new Error(`${name} needs 12 ${theme} steps`)
    family[theme].forEach(([l, c], index) => {
      css += `  --m3-${name}-${index + 1}: oklch(${l} ${c} ${family.hue});\n`
    })
  }
  css += '}\n'
}
writeFileSync(new URL('src/theme/scales.css', root), css)
writeFileSync(
  new URL('design/palette.json', root),
  JSON.stringify(palette, null, 2) + '\n',
)

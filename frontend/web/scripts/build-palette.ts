import { readFileSync, writeFileSync } from 'node:fs'
import { URL } from 'node:url'
import {
  buildPaletteOutputs,
  type PaletteSource,
} from '../src/theme/palette.ts'

const root = new URL('../', import.meta.url)
const sourcePath = new URL('design/palette-source.json', root)
const source: PaletteSource = JSON.parse(readFileSync(sourcePath, 'utf8'))

async function main() {
  const outputs = await buildPaletteOutputs(source)
  for (const { path, content } of outputs) {
    writeFileSync(path, content)
  }
}

main().catch((err: unknown) => {
  console.error(err)
  process.exit(1)
})

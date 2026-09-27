import { readFileSync } from 'node:fs'
import { URL } from 'node:url'
import type { PaletteSource } from '../src/theme/palette.ts'
import { writePaletteOutputs } from './palette-outputs.ts'

const root = new URL('../', import.meta.url)
const sourcePath = new URL('design/palette-source.json', root)
const source: PaletteSource = JSON.parse(readFileSync(sourcePath, 'utf8'))

async function main() {
  await writePaletteOutputs(source)
}

main().catch((err: unknown) => {
  console.error(err)
  process.exit(1)
})

---
title: A Multi-File Generator Must Validate in Memory Before Writing Any Output
date: 2026-09-27
last_verified: 2026-09-27
category: architecture-patterns
module: frontend/web/scripts
problem_type: architecture_pattern
component: code_generation
severity: medium
applies_when:
  - "Writing or reviewing a code, asset, or theme generator that produces multiple files from a single source of truth"
  - "A multi-file generator leaves partial writes on disk when validation fails midway"
  - "Separating browser-safe runtime code from Node.js file generation and formatting scripts"
related_components: [conformance-gates, test_fixtures]
tags: [code-generation, asset-generator, atomic-write, web-tooling, theme, node-vs-browser]
---

# A multi-file generator must validate in memory before writing any output

## The situation

When a generator emits several interdependent files from one specification,
running generation sequentially risks partial writes. In the web design system,
`scripts/build-palette.ts` derives three files from `design/palette-source.json`:

- `src/theme/scales.css` (literal color scales)
- `src/theme/semantic.css` (semantic variables referencing scale steps)
- `design/palette.json` (resolved JSON token contract)

Two issues occurred during initial implementation:

1. Browser runtime pollution: build output formatting lived inside
   `src/theme/palette.ts`, dynamically importing Node built-ins (`node:path`,
   `node:url`) and `prettier`. This broke clean imports from browser-facing
   components and Storybook stories.
2. Torn writes on failure: the build script generated and wrote each file in a
   loop. If validation failed on an ungated token family or a WCAG contrast
   check for semantic tokens, preceding files had already overwritten disk.
   The workspace remained partially updated, masking failure causes and
   drifting files out of sync.

## What is true and why

1. **Keep runtime logic pure and browser-safe.** Pure token resolution, contrast
   checks, and CSS rendering belong in `src/theme/palette.ts`. No Node modules or
   build-only tools belong in runtime paths.
2. **Buffer and validate all outputs in memory first.** Build-time formatting
   belongs in `scripts/palette-outputs.ts`. The generator computes and formats
   all files in memory before opening any target file for writing.
3. **Write only after validation passes.** File writes run only after the
   complete output set succeeds. A failure in one output stops the process
   before any file on disk changes.
4. **Prove atomicity with sentinel fixtures.** Tests populate a temporary
   directory with sentinel files, invoke the generator with an invalid source,
   and assert that every sentinel file remains untouched.

## Working example

In `frontend/web/scripts/palette-outputs.ts:17-58`, `buildPaletteOutputs` returns
in-memory outputs, and `writePaletteOutputs` writes only after awaiting it:

```ts
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

export async function writePaletteOutputs(
  source: PaletteSource,
  rootDir = fileURLToPath(new URL('../', import.meta.url)),
): Promise<PaletteOutputFile[]> {
  const outputs = await buildPaletteOutputs(source, rootDir)
  for (const { path, content } of outputs) {
    writeFileSync(path, content)
  }
  return outputs
}
```

## The evidence

In `frontend/web/src/theme/palette.test.ts:77-101`, sentinel tests verify that
rejection leaves on-disk outputs unchanged:

```ts
it('rejects an invalid source and leaves all three sentinel outputs unchanged', async () => {
  const invalidSource: PaletteSource = {
    ...source,
    semantic: {
      ...source.semantic,
      overlay: { light: 'teal-3', dark: 'teal-3' },
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
```

A mutation test writing `scales.css` before awaiting validation fails this test:

```text
FAIL src/theme/palette.test.ts > generator write path > rejects an invalid source and leaves all three sentinel outputs unchanged
AssertionError: expected ':root {\n --m3-coral-1: oklch(0.954 …' to be '/* sentinel src/theme/scales.css */\n'
```

## What this does not cover

This does not provide crash-consistent atomic replacement against sudden process
termination or power loss. That property requires temporary files followed by an
atomic `rename(2)` call. This pattern covers memory-bounded generator
validation where failure occurs within application logic.

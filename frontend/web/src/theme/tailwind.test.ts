import { describe, expect, it } from 'vitest'
import { compile } from '@tailwindcss/node'
import fs from 'node:fs'
import path from 'node:path'

describe('tailwind theme and utilities', () => {
  const cssPath = path.resolve(__dirname, 'tailwind.css')
  const css = fs.readFileSync(cssPath, 'utf-8')

  it('generates semantic color utilities and excludes default palette', async () => {
    const compiler = await compile(css, {
      base: path.dirname(cssPath),
      onDependency: () => {},
    })

    const cardOutput = compiler.build(['bg-card'])
    expect(cardOutput).toContain('.bg-card')
    expect(cardOutput).toContain('background-color: var(--card)')

    const textMutedOutput = compiler.build(['text-muted-foreground'])
    expect(textMutedOutput).toContain('.text-muted-foreground')
    expect(textMutedOutput).toContain('color: var(--muted-foreground)')

    const redOutput = compiler.build(['bg-red-500'])
    expect(redOutput).not.toContain('.bg-red-500')
  })

  it('supports dark variant tied to data-theme', async () => {
    const compiler = await compile(css, {
      base: path.dirname(cssPath),
      onDependency: () => {},
    })

    const darkOutput = compiler.build(['dark:bg-popover'])
    expect(darkOutput).toContain(
      '.dark\\:bg-popover:where([data-theme=dark], [data-theme=dark] *)',
    )
    expect(darkOutput).toContain('background-color: var(--popover)')
    expect(darkOutput).not.toContain('data-theme=light')
  })

  it('declares type scale with companion line-height', async () => {
    const compiler = await compile(css, {
      base: path.dirname(cssPath),
      onDependency: () => {},
    })

    const textOutput = compiler.build(['text-base'])
    expect(textOutput).toContain('.text-base')
    expect(textOutput).toContain('var(--text-base)')
    expect(textOutput).toContain('var(--text-base--line-height)')
    expect(textOutput).toContain('--text-base: 13px')
    expect(textOutput).toContain('--text-base--line-height: 20px')
  })

  it('keeps the smallest text token at the 11px interface floor', async () => {
    const compiler = await compile(css, {
      base: path.dirname(cssPath),
      onDependency: () => {},
    })

    const textOutput = compiler.build(['text-2xs'])
    expect(textOutput).toContain('--text-2xs: 11px')
  })

  it('emits preflight base resets and visible native button focus', async () => {
    const compiler = await compile(css, {
      base: path.dirname(cssPath),
      onDependency: () => {},
    })

    const baseOutput = compiler.build([])
    expect(baseOutput).toContain('font-size: inherit')
    expect(baseOutput).toContain('display: block')
    expect(baseOutput).toContain('button:focus-visible')
    expect(baseOutput).toContain('outline: 2px solid var(--ring)')
    expect(baseOutput).toContain('outline-offset: 2px')
  })
})

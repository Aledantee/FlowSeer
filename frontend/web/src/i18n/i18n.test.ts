import { describe, expect, it } from 'vitest'
import en from './locales/en.json'
import de from './locales/de.json'
import { createWebI18n } from './index'

function getLeafPaths(obj: unknown, prefix = ''): string[] {
  if (typeof obj !== 'object' || obj === null || Array.isArray(obj)) {
    throw new Error(`Expected object at path ${prefix}, got ${typeof obj}`)
  }
  const paths: string[] = []
  for (const [key, value] of Object.entries(obj)) {
    const fullPath = prefix ? `${prefix}.${key}` : key
    if (typeof value === 'object' && value !== null && !Array.isArray(value)) {
      paths.push(...getLeafPaths(value, fullPath))
    } else if (typeof value === 'string') {
      if (value.trim().length === 0) {
        throw new Error(`Empty string leaf at path ${fullPath}`)
      }
      paths.push(fullPath)
    } else {
      throw new Error(`Non-string leaf at path ${fullPath}: ${typeof value}`)
    }
  }
  return paths.sort()
}

function findMissingPaths(source: string[], target: string[]): string[] {
  const targetSet = new Set(target)
  return source.filter((path) => !targetSet.has(path))
}

function deletePath(obj: Record<string, unknown>, path: string) {
  const parts = path.split('.')
  let curr: Record<string, unknown> = obj
  for (let i = 0; i < parts.length - 1; i++) {
    curr = curr[parts[i]] as Record<string, unknown>
  }
  delete curr[parts[parts.length - 1]]
}

describe('i18n message catalogs and runtime', () => {
  it('recursively compares sorted message leaf paths in both directions and finds identical sets', () => {
    const enPaths = getLeafPaths(en)
    const dePaths = getLeafPaths(de)
    expect(enPaths).toEqual(dePaths)
    expect(findMissingPaths(enPaths, dePaths)).toEqual([])
    expect(findMissingPaths(dePaths, enPaths)).toEqual([])
  })

  it('rejects empty or non-string leaves', () => {
    expect(() => getLeafPaths({ empty: '' })).toThrow(/Empty string/)
    expect(() => getLeafPaths({ whitespace: '   ' })).toThrow(/Empty string/)
    expect(() => getLeafPaths({ number: 42 })).toThrow(/Non-string/)
    expect(() => getLeafPaths({ boolean: false })).toThrow(/Non-string/)
    expect(() => getLeafPaths({ nested: { empty: '' } })).toThrow(
      /Empty string/,
    )
  })

  it('proves deleting ui.pagination.nextText from cloned en catalog yields that missing path', () => {
    const clonedEn = JSON.parse(JSON.stringify(en)) as typeof en
    deletePath(clonedEn, 'ui.pagination.nextText')
    const dePaths = getLeafPaths(de)
    const clonedEnPaths = getLeafPaths(clonedEn)
    const missingInEn = findMissingPaths(dePaths, clonedEnPaths)
    expect(missingInEn).toEqual(['ui.pagination.nextText'])
  })

  it('proves deleting ui.pagination.nextText from cloned de catalog yields that missing path', () => {
    const clonedDe = JSON.parse(JSON.stringify(de)) as typeof de
    deletePath(clonedDe, 'ui.pagination.nextText')
    const enPaths = getLeafPaths(en)
    const clonedDePaths = getLeafPaths(clonedDe)
    const missingInDe = findMissingPaths(enPaths, clonedDePaths)
    expect(missingInDe).toEqual(['ui.pagination.nextText'])
  })

  it('operates in Composition mode with createWebI18n', () => {
    const i18n = createWebI18n()
    expect(i18n.mode).toBe('composition')
    expect(i18n.global.locale.value).toBe('en')
    expect(i18n.global.t('ui.statusBadge.healthy')).toBe('Healthy')

    i18n.global.locale.value = 'de'
    expect(i18n.global.t('ui.statusBadge.healthy')).toBe('Gesund')
  })

  it('supports German interpolation', () => {
    const i18n = createWebI18n('de')
    expect(i18n.global.t('ui.pagination.pageLabel', { page: 2 })).toBe(
      'Seite 2',
    )
    expect(
      i18n.global.t('ui.aiActionLayer.heading', { label: 'Device A' }),
    ).toBe('Frage zu Device A')
  })

  it('supports plural selection for 0/1/2 in en and de', () => {
    const i18n = createWebI18n('en')
    i18n.global.setLocaleMessage('en', {
      ...en,
      test: { items: 'No items | One item | {count} items' },
    })
    i18n.global.setLocaleMessage('de', {
      ...de,
      test: { items: 'Keine Einträge | Ein Eintrag | {count} Einträge' },
    })

    expect(i18n.global.t('test.items', 0)).toBe('No items')
    expect(i18n.global.t('test.items', 1)).toBe('One item')
    expect(i18n.global.t('test.items', 2)).toBe('2 items')

    i18n.global.locale.value = 'de'
    expect(i18n.global.t('test.items', 0)).toBe('Keine Einträge')
    expect(i18n.global.t('test.items', 1)).toBe('Ein Eintrag')
    expect(i18n.global.t('test.items', 2)).toBe('2 Einträge')
  })

  it('formats numbers for en and de with decimal, integer, and percent formats', () => {
    const i18n = createWebI18n('en')
    expect(i18n.global.n(1234.5, 'decimal')).toBe('1,234.5')
    expect(i18n.global.n(1234.5, 'integer')).toBe('1,235')
    expect(i18n.global.n(0.5, 'percent')).toBe('50%')

    i18n.global.locale.value = 'de'
    expect(i18n.global.n(1234.5, 'decimal')).toBe('1.234,5')
    expect(i18n.global.n(1234.5, 'integer')).toBe('1.235')
    expect(i18n.global.n(0.5, 'percent')).toBe('50\u00a0%')
  })
})

import { describe, expect, it } from 'vitest'
import type { SearchResult } from '../domain/search'
import { clearRecent, loadRecent, rememberRecent } from './recentSearches'

function memoryStorage(): Storage {
  const items = new Map<string, string>()
  return {
    get length() {
      return items.size
    },
    clear: () => items.clear(),
    getItem: (key) => items.get(key) ?? null,
    key: (index) => [...items.keys()][index] ?? null,
    removeItem: (key) => void items.delete(key),
    setItem: (key, value) => void items.set(key, value),
  }
}
const device = (id: string): SearchResult => ({
  kind: 'device',
  id,
  title: id,
  detail: '',
})

describe('recent searches', () => {
  it('keeps the newest first without duplicates, up to eight', () => {
    const storage = memoryStorage()
    for (let i = 0; i < 10; i++) rememberRecent(device(`dev-${i}`), storage)
    rememberRecent(device('dev-5'), storage)
    const ids = loadRecent(storage).map((entry) => entry.id)
    expect(ids[0]).toBe('dev-5')
    expect(ids).toHaveLength(8)
    expect(new Set(ids).size).toBe(8)
  })
  it('ignores corrupt data and clears on request', () => {
    const storage = memoryStorage()
    storage.setItem('flowseer.recent-searches', '{not json')
    expect(loadRecent(storage)).toEqual([])
    rememberRecent(device('dev-1'), storage)
    clearRecent(storage)
    expect(loadRecent(storage)).toEqual([])
  })
  it('survives storage that throws', () => {
    const broken = {
      ...memoryStorage(),
      getItem: () => {
        throw new Error('blocked')
      },
      setItem: () => {
        throw new Error('blocked')
      },
    }
    expect(loadRecent(broken)).toEqual([])
    expect(rememberRecent(device('dev-1'), broken)).toHaveLength(1)
  })
})

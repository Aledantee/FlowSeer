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
  it('stores what identifies a result and no text', () => {
    const storage = memoryStorage()
    const port: SearchResult = {
      kind: 'interface',
      id: 'dev-2',
      port: 'ge-0/0/1',
      title: 'dev-2 ge-0/0/1',
    }
    rememberRecent(device('dev-1'), storage)
    rememberRecent(port, storage)
    expect(
      JSON.parse(storage.getItem('flowseer.recent-searches') ?? ''),
    ).toEqual([
      { kind: 'interface', id: 'dev-2', port: 'ge-0/0/1' },
      { kind: 'device', id: 'dev-1' },
    ])
  })
  it('loads an entry saved with a title and a detail without them', () => {
    const storage = memoryStorage()
    storage.setItem(
      'flowseer.recent-searches',
      JSON.stringify([
        { kind: 'device', id: 'dev-1', title: 'dev-1', detail: 'Switch' },
        {
          kind: 'interface',
          id: 'dev-2',
          port: 'ge-0/0/1',
          title: 'dev-2 ge-0/0/1',
          detail: 'Up',
        },
        { kind: 'device', id: 'dev-3' },
        { kind: 'unknown', id: 'dev-4' },
        { kind: 'device' },
      ]),
    )
    expect(loadRecent(storage)).toEqual([
      { kind: 'device', id: 'dev-1' },
      { kind: 'interface', id: 'dev-2', port: 'ge-0/0/1' },
      { kind: 'device', id: 'dev-3' },
    ])
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

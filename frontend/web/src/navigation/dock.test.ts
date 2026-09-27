import { describe, expect, it } from 'vitest'
import { loadDock, minimize, openBeside, openTab, saveDock } from './dock'
import type { PageLocation } from './page'
import { resolveTarget, scopeOf, viewOf } from './page'

const at = (
  path: string,
  query: Record<string, string> = {},
): PageLocation => ({ path, query })
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

describe('page dock', () => {
  it('does not dock the same page or pair twice', () => {
    const once = minimize([], at('/devices'))
    expect(minimize(once, at('/devices'))).toHaveLength(1)
    const pair = minimize(once, at('/devices'), at('/topology'))
    expect(pair).toHaveLength(2)
    expect(minimize(pair, at('/devices'), at('/topology'))).toHaveLength(2)
  })
  it('swaps a single page with the main pane and keeps the side pane', () => {
    const tabs = minimize([], at('/topology', { site: 'berlin' }))
    const id = tabs[0]?.id ?? ''
    const result = openTab(tabs, id, {
      main: at('/devices'),
      side: at('/clients'),
    })
    expect(result.panes).toEqual({
      main: at('/topology', { site: 'berlin' }),
      side: at('/clients'),
    })
    expect(result.tabs[0]).toEqual({ id, location: at('/devices') })
  })
  it('swaps a pair with both panes', () => {
    const tabs = minimize([], at('/topology'), at('/devices/dev-2'))
    const id = tabs[0]?.id ?? ''
    const result = openTab(tabs, id, {
      main: at('/devices'),
      side: at('/clients'),
    })
    expect(result.panes).toEqual({
      main: at('/topology'),
      side: at('/devices/dev-2'),
    })
    expect(result.tabs[0]).toEqual({
      id,
      location: at('/devices'),
      beside: at('/clients'),
    })
  })
  it('moves a tab beside the main page and docks what it displaces', () => {
    const tabs = minimize([], at('/topology'))
    const id = tabs[0]?.id ?? ''
    expect(openBeside(tabs, id, undefined)).toEqual({
      tabs: [],
      side: at('/topology'),
    })
    expect(openBeside(tabs, id, at('/clients')).tabs[0]?.location).toEqual(
      at('/clients'),
    )
  })
  it('round-trips through storage and ignores junk', () => {
    const storage = memoryStorage()
    saveDock(
      minimize([], at('/clients', { ap: 'dev-3' }), at('/sites')),
      storage,
    )
    expect(loadDock(storage)[0]).toMatchObject({
      location: at('/clients', { ap: 'dev-3' }),
      beside: at('/sites'),
    })
    storage.setItem('flowseer.dock', '[{"id":1}]')
    expect(loadDock(storage)).toEqual([])
  })
})

describe('page locations', () => {
  it('reads the view and device from a path', () => {
    expect(viewOf('/devices/dev-2')).toEqual({
      view: 'device',
      deviceId: 'dev-2',
    })
    expect(viewOf('/devices/dev%202')).toEqual({
      view: 'device',
      deviceId: 'dev 2',
    })
    expect(viewOf('/nowhere').view).toBe('dashboard')
    expect(viewOf('/components')).toEqual({ view: 'dashboard' })
  })
  it('keeps a malformed device segment literal', () => {
    expect(viewOf('/devices/%')).toEqual({ view: 'device', deviceId: '%' })
  })
  it('keeps the path when a target names none and drops cleared keys', () => {
    expect(
      resolveTarget(at('/devices', { site: 'berlin', search: 'ap' }), {
        query: { site: 'berlin', search: undefined },
      }),
    ).toEqual(at('/devices', { site: 'berlin' }))
  })
  it('carries only tenant and site as scope', () => {
    expect(
      scopeOf(at('/topology', { site: 'berlin', focus: 'dev-2~eth0', q: 'x' })),
    ).toEqual({ site: 'berlin' })
  })
})

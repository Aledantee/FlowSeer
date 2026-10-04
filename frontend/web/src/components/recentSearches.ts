import type { SearchResult } from '../domain/search'

const KEY = 'flowseer.recent-searches'
const LIMIT = 8

// What identifies a result. Its text resolves from current data, so a stored
// entry never shows text written under another locale.
export type RecentSearch = Pick<SearchResult, 'kind' | 'id' | 'port'>

function asRecent(value: unknown): RecentSearch | undefined {
  if (typeof value !== 'object' || value === null) return undefined
  const entry = value as Record<string, unknown>
  if (
    !['page', 'tenant', 'site', 'device', 'client', 'interface'].includes(
      String(entry.kind),
    ) ||
    typeof entry.id !== 'string' ||
    (entry.port !== undefined && typeof entry.port !== 'string')
  )
    return undefined
  // An entry saved with a title and a detail loads without them.
  return {
    kind: entry.kind as RecentSearch['kind'],
    id: entry.id,
    ...(entry.port === undefined ? {} : { port: entry.port }),
  }
}

// History is a per-browser convenience: storage can be blocked or cleared,
// and the search works the same without it.
export function loadRecent(
  storage: Storage | undefined = globalThis.localStorage,
): RecentSearch[] {
  try {
    const parsed: unknown = JSON.parse(storage?.getItem(KEY) ?? '[]')
    if (!Array.isArray(parsed)) return []
    return parsed
      .map(asRecent)
      .filter((entry) => entry !== undefined)
      .slice(0, LIMIT)
  } catch {
    return []
  }
}

export function rememberRecent(
  result: RecentSearch,
  storage: Storage | undefined = globalThis.localStorage,
): RecentSearch[] {
  const entry: RecentSearch = {
    kind: result.kind,
    id: result.id,
    ...(result.port === undefined ? {} : { port: result.port }),
  }
  const same = (other: RecentSearch) =>
    other.kind === entry.kind &&
    other.id === entry.id &&
    other.port === entry.port
  const next = [
    entry,
    ...loadRecent(storage).filter((other) => !same(other)),
  ].slice(0, LIMIT)
  try {
    storage?.setItem(KEY, JSON.stringify(next))
  } catch {
    // A full or blocked store only loses history.
  }
  return next
}

export function clearRecent(
  storage: Storage | undefined = globalThis.localStorage,
) {
  try {
    storage?.removeItem(KEY)
  } catch {
    // Nothing to clear when storage is unavailable.
  }
}

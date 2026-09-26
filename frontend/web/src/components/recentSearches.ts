import type { SearchResult } from '../domain/search'

const KEY = 'flowseer.recent-searches'
const LIMIT = 8

function isResult(value: unknown): value is SearchResult {
  if (typeof value !== 'object' || value === null) return false
  const entry = value as Record<string, unknown>
  return (
    ['page', 'tenant', 'site', 'device', 'client', 'interface'].includes(
      String(entry.kind),
    ) &&
    typeof entry.id === 'string' &&
    typeof entry.title === 'string' &&
    typeof entry.detail === 'string' &&
    (entry.port === undefined || typeof entry.port === 'string')
  )
}

// History is a per-browser convenience: storage can be blocked or cleared,
// and the search works the same without it.
export function loadRecent(
  storage: Storage | undefined = globalThis.localStorage,
): SearchResult[] {
  try {
    const parsed: unknown = JSON.parse(storage?.getItem(KEY) ?? '[]')
    return Array.isArray(parsed) ? parsed.filter(isResult).slice(0, LIMIT) : []
  } catch {
    return []
  }
}

export function rememberRecent(
  result: SearchResult,
  storage: Storage | undefined = globalThis.localStorage,
): SearchResult[] {
  const same = (entry: SearchResult) =>
    entry.kind === result.kind &&
    entry.id === result.id &&
    entry.port === result.port
  const next = [
    result,
    ...loadRecent(storage).filter((entry) => !same(entry)),
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

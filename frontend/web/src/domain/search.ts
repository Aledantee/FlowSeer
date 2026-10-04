import type { Device } from './fleet'
import { sites, tenants } from './fleet'
import { clientsOf } from './clients'
import { portsOf } from './telemetry'

export type ResultKind =
  'page' | 'tenant' | 'site' | 'device' | 'client' | 'interface'
export interface SearchResult {
  kind: ResultKind
  // The tenant, site, device, or client id; for an interface, its device;
  // for a page, the workspace's own page id.
  id: string
  // Set only on interface results.
  port?: string
  // A name the data supplies. Display text beyond it is built where the
  // result renders, so it follows the locale.
  title: string
}

const PER_KIND = 6

// Matches rank by where the query lands: a whole field, then a field
// prefix, then anywhere, so "sw-01" lists the switch before a client whose
// MAC merely contains it.
function rank(query: string, fields: string[]): number {
  let best = 0
  for (const field of fields) {
    const value = field.toLowerCase()
    if (value === query) return 3
    if (value.startsWith(query)) best = Math.max(best, 2)
    else if (value.includes(query)) best = Math.max(best, 1)
  }
  return best
}

function top<T>(items: T[], score: (item: T) => number): T[] {
  return items
    .map((item) => ({ item, score: score(item) }))
    .filter((entry) => entry.score > 0)
    .sort((a, b) => b.score - a.score)
    .slice(0, PER_KIND)
    .map((entry) => entry.item)
}

export function searchAll(raw: string, fleet: Device[]): SearchResult[] {
  const query = raw.trim().toLowerCase()
  if (!query) return []
  const deviceName = (id: string) =>
    fleet.find((device) => device.id === id)?.name ?? ''
  const interfaces = fleet.flatMap((device) =>
    portsOf(fleet, device).map((port) => ({ device, port })),
  )
  return [
    ...top(tenants, (tenant) => rank(query, [tenant.name, tenant.id])).map(
      (tenant): SearchResult => ({
        kind: 'tenant',
        id: tenant.id,
        title: tenant.name,
      }),
    ),
    ...top(sites, (site) =>
      rank(query, [site.name, site.location, site.id]),
    ).map((site): SearchResult => ({
      kind: 'site',
      id: site.id,
      title: site.name,
    })),
    ...top(fleet, (device) =>
      rank(query, [device.name, device.address, device.kind]),
    ).map((device): SearchResult => ({
      kind: 'device',
      id: device.id,
      title: device.name,
    })),
    ...top(clientsOf(fleet), (client) =>
      rank(query, [client.hostname, client.address, client.mac]),
    ).map((client): SearchResult => ({
      kind: 'client',
      id: client.id,
      title: client.hostname,
    })),
    ...top(interfaces, ({ device, port }) =>
      rank(query, [
        port.name,
        `${device.name} ${port.name}`,
        port.endpoint ?? '',
        port.neighborId ? deviceName(port.neighborId) : '',
      ]),
    ).map(({ device, port }): SearchResult => ({
      kind: 'interface',
      id: device.id,
      port: port.name,
      title: `${device.name} ${port.name}`,
    })),
  ]
}

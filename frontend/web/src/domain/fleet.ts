export interface Tenant {
  id: string
  name: string
  iconUrl?: string
  parentId?: string
}
export interface Site {
  id: string
  name: string
  tenantId: string
  location: string
}
export type Health = 'Healthy' | 'Degraded' | 'Offline'
export type Lifecycle = 'Active' | 'Retired'
export type Reachability = 'Reachable' | 'Unreachable'
// An Integration is the adapter FlowSeer reaches devices through; a Binding is
// one device's path through one Integration. Reachability belongs to the
// Binding and heals on its own, so it is tracked apart from the operator-owned
// lifecycle.
export interface Integration {
  id: string
  name: string
  kind: string
}
export interface Binding {
  integrationId: string
  reachability: Reachability
  observedMinutesAgo: number
}
export interface Device {
  id: string
  name: string
  siteId: string
  kind: string
  address: string
  health: Health
  lifecycle: Lifecycle
  lastSeenMinutes: number
  bindings: Binding[]
  clients: number
  throughput: number
}
export const tenants: Tenant[] = [
  {
    id: 'aurora',
    name: 'Aurora Hospitality',
    iconUrl: '/tenant-icons/aurora.svg',
  },
  {
    id: 'aurora-de',
    name: 'Aurora Germany',
    parentId: 'aurora',
    iconUrl: '/tenant-icons/aurora.svg',
  },
  { id: 'meridian', name: 'Meridian Workspaces' },
  { id: 'nord', name: 'Nord Retail' },
]
export const sites: Site[] = [
  {
    id: 'berlin',
    name: 'Berlin Mitte',
    tenantId: 'aurora-de',
    location: 'Berlin, DE',
  },
  {
    id: 'hamburg',
    name: 'Hamburg Hafen',
    tenantId: 'aurora-de',
    location: 'Hamburg, DE',
  },
  {
    id: 'aachen',
    name: 'Campus Aachen',
    tenantId: 'meridian',
    location: 'Aachen, DE',
  },
  {
    id: 'cologne',
    name: 'Cologne Central',
    tenantId: 'nord',
    location: 'Cologne, DE',
  },
]
export const integrations: Integration[] = [
  ...sites.map((site) => ({
    id: `lan-${site.id}`,
    name: `${site.name} edge`,
    kind: 'Local network',
  })),
  ...tenants
    .filter((tenant) => sites.some((site) => site.tenantId === tenant.id))
    .map((tenant) => ({
      id: `wlc-${tenant.id}`,
      name: `${tenant.name} controller`,
      kind: 'Wireless controller',
    })),
]
export const devices: Device[] = sites.flatMap((site, index) =>
  ['Gateway', 'Core switch', 'Lobby AP', 'Floor 02 AP'].map((kind, offset) => {
    const offline = index === 3 && offset === 3
    const seen = offline ? 38 : 1
    const reachability: Reachability = offline ? 'Unreachable' : 'Reachable'
    return {
      id: `dev-${index * 4 + offset + 1}`,
      name: `${site.id}-${['gw-01', 'sw-01', 'ap-01', 'ap-02'][offset]}`,
      siteId: site.id,
      kind,
      address: `10.${index + 20}.0.${offset + 1}`,
      health: (index === 1 && offset === 2
        ? 'Degraded'
        : offline
          ? 'Offline'
          : 'Healthy') as Health,
      lifecycle: 'Active' as Lifecycle,
      lastSeenMinutes: seen,
      bindings: [
        `lan-${site.id}`,
        ...(offset > 1 ? [`wlc-${site.tenantId}`] : []),
      ].map((integrationId) => ({
        integrationId,
        reachability,
        observedMinutesAgo: seen,
      })),
      clients: offset < 2 || offline ? 0 : 28 + index * 9 + offset * 7,
      throughput: offline ? 0 : 24 + index * 12 + offset * 8,
    }
  }),
)
export function tenantIds(id: string): string[] {
  if (!id) return tenants.map((tenant) => tenant.id)
  return tenants
    .filter((tenant) => {
      let current: Tenant | undefined = tenant
      const visited = new Set<string>()
      while (current && !visited.has(current.id)) {
        if (current.id === id) return true
        visited.add(current.id)
        current = tenants.find((parent) => parent.id === current?.parentId)
      }
      return false
    })
    .map((tenant) => tenant.id)
}
export function filterDevices(
  fleet: Device[],
  tenant: string,
  site: string,
  search: string,
  health: string,
): Device[] {
  const scope = tenantIds(tenant)
  const query = search.trim().toLowerCase()
  return fleet.filter((device) => {
    const location = sites.find((item) => item.id === device.siteId)
    return (
      location &&
      scope.includes(location.tenantId) &&
      (!site || device.siteId === site) &&
      (!health ||
        (health === 'attention'
          ? device.health !== 'Healthy'
          : device.health === health)) &&
      `${device.name} ${device.kind} ${device.address}`
        .toLowerCase()
        .includes(query)
    )
  })
}
export function moveDevice(device: Device, siteId: string): Device {
  const origin = sites.find((site) => site.id === device.siteId)
  const destination = sites.find((site) => site.id === siteId)
  if (!origin || !destination || origin.tenantId !== destination.tenantId)
    throw new Error('Choose a site owned by the same tenant.')
  return { ...device, siteId }
}
// A poll is an observation, not a status change: an unreachable device stays
// offline and only the time of the failed attempt moves.
export function pollDevice(device: Device): Device {
  const answered = device.bindings.some(
    (binding) => binding.reachability === 'Reachable',
  )
  return {
    ...device,
    lastSeenMinutes: answered ? 0 : device.lastSeenMinutes,
    bindings: device.bindings.map((binding) => ({
      ...binding,
      observedMinutesAgo: 0,
    })),
  }
}

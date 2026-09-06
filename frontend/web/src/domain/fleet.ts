export interface Tenant {
  id: string
  name: string
  parentId?: string
}
export interface Site {
  id: string
  name: string
  tenantId: string
  location: string
}
export type Health = 'Healthy' | 'Degraded' | 'Offline'
export interface Device {
  id: string
  name: string
  siteId: string
  kind: string
  address: string
  health: Health
  clients: number
  throughput: number
}
export const tenants: Tenant[] = [
  { id: 'aurora', name: 'Aurora Hospitality' },
  { id: 'aurora-de', name: 'Aurora Germany', parentId: 'aurora' },
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
export const devices: Device[] = sites.flatMap((site, index) =>
  ['Gateway', 'Core switch', 'Lobby AP', 'Floor 02 AP'].map((kind, offset) => ({
    id: `dev-${index * 4 + offset + 1}`,
    name: `${site.id}-${['gw-01', 'sw-01', 'ap-01', 'ap-02'][offset]}`,
    siteId: site.id,
    kind,
    address: `10.${index + 20}.0.${offset + 1}`,
    health:
      index === 1 && offset === 2
        ? 'Degraded'
        : index === 3 && offset === 3
          ? 'Offline'
          : 'Healthy',
    clients:
      offset < 2 || (index === 3 && offset === 3)
        ? 0
        : 28 + index * 9 + offset * 7,
    throughput: index === 3 && offset === 3 ? 0 : 24 + index * 12 + offset * 8,
  })),
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

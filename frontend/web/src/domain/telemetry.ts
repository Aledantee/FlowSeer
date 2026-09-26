import type { Device, DeviceRole, Link } from './fleet'
import { downlinks, linksOf } from './fleet'
import { clientsOf } from './clients'
import type { Band } from './clients'

export interface Telemetry {
  model: string
  firmware: string
  serial: string
  // Absent while the device is offline; uptime restarts when it returns.
  bootedAt?: number
  cpu: number
  memory: number
  memoryTotal: number
  temperature: number
}
export type PortStatus = 'Up' | 'Down' | 'Disabled'
export interface Port {
  name: string
  status: PortStatus
  // Negotiated speed in Mbps; absent when the port has no link.
  speed?: number
  // The fleet device on the far end, when it is one we manage.
  neighborId?: string
  // What sits on the far end when it is not a managed device.
  endpoint?: string
  throughput: number
  poe?: number
}
export interface Radio {
  band: Band
  channel: number
  clients: number
}

const hardware: Record<
  DeviceRole,
  { model: string; firmware: string; memory: number; ports: number }
> = {
  gateway: {
    model: 'Edge Gateway G4',
    firmware: '7.2.1',
    memory: 4096,
    ports: 8,
  },
  switch: {
    model: 'Access Switch S24P',
    firmware: '10.4.3',
    memory: 2048,
    ports: 24,
  },
  'access-point': {
    model: 'Wi-Fi 6E AP A650',
    firmware: '6.6.0',
    memory: 1024,
    ports: 2,
  },
}
const BOOT_EPOCH = Date.UTC(2026, 7, 1)
// Unmanaged endpoints on switch access ports; the first two draw PoE.
const endpoints = ['Desk phone', 'Camera', 'Printer', 'Workstation']

function seedOf(id: string): number {
  let hash = 7
  for (const char of id) hash = (hash * 33 + char.charCodeAt(0)) & 0xffff
  return hash
}

// Load follows the device's live traffic, so resource figures move with the
// graph; a degraded device runs hot, which is why it is degraded.
export function telemetryOf(device: Device): Telemetry {
  const seed = seedOf(device.id)
  const spec = hardware[device.role]
  const offline = device.health === 'Offline'
  const strain = device.health === 'Degraded' ? 38 : 0
  const cpu = offline
    ? 0
    : Math.min(
        97,
        6 + (seed % 18) + strain + Math.round(device.throughput * 0.2),
      )
  return {
    model: spec.model,
    firmware: spec.firmware,
    serial: `FS${seed.toString(16).toUpperCase().padStart(4, '0')}${device.id.replace(/\D/g, '').padStart(4, '0')}`,
    bootedAt: offline ? undefined : BOOT_EPOCH + (seed % 400) * 3_600_000,
    cpu,
    memory: offline
      ? 0
      : Math.min(95, 28 + (seed % 30) + Math.round(strain / 2)),
    memoryTotal: spec.memory,
    temperature: offline ? 0 : 36 + (seed % 9) + Math.round(cpu / 6),
  }
}

// Ports reflect the uplink tree: the uplink port faces the device's uplink,
// the next ports face its downlinks, and the rest are idle or serve
// unmanaged endpoints.
export function portsOf(fleet: Device[], device: Device): Port[] {
  const spec = hardware[device.role]
  const seed = seedOf(device.id)
  const links = linksOf(fleet)
  const offline = device.health === 'Offline'
  const own = links.find((link) => link.targetId === device.id)
  const children = downlinks(fleet, device)
  const names =
    device.role === 'gateway'
      ? [
          'wan0',
          ...Array.from({ length: spec.ports - 1 }, (_, i) => `lan${i + 1}`),
        ]
      : device.role === 'switch'
        ? [
            'xe-0/1/0',
            ...Array.from({ length: spec.ports }, (_, i) => `ge-0/0/${i}`),
          ]
        : ['eth0', 'eth1']
  return names.map((name, index) => {
    if (offline) return { name, status: 'Down', throughput: 0 }
    if (index === 0) {
      if (device.role === 'gateway')
        return {
          name,
          status: 'Up',
          speed: 1000,
          endpoint: 'Internet',
          throughput: children.reduce(
            (sum, child) =>
              sum +
              (links.find((link) => link.targetId === child.id)?.throughput ??
                0),
            device.throughput,
          ),
        }
      return {
        name,
        status: own?.health === 'Offline' ? 'Down' : 'Up',
        speed: own?.capacity,
        neighborId: own?.sourceId,
        throughput: own?.throughput ?? 0,
        poe: device.role === 'access-point' ? 18 + (seed % 9) : undefined,
      }
    }
    const child = children[index - 1]
    if (child) {
      const link = links.find((item) => item.targetId === child.id)
      const up = link?.health !== 'Offline'
      return {
        name,
        status: up ? 'Up' : 'Down',
        speed: up ? link?.capacity : undefined,
        neighborId: child.id,
        throughput: link?.throughput ?? 0,
        poe:
          up && child.role === 'access-point' && device.role === 'switch'
            ? 18 + (seedOf(child.id) % 9)
            : undefined,
      }
    }
    if (device.role === 'switch' && (seed + index) % 4 === 0)
      return {
        name,
        status: 'Up',
        speed: 1000,
        endpoint: endpoints[(seed + (index >> 2)) % endpoints.length],
        throughput: 1 + ((seed + index) % 6),
        poe: (seed + (index >> 2)) % endpoints.length < 2 ? 6 : undefined,
      }
    return {
      name,
      status: (seed + index) % 9 === 0 ? 'Disabled' : 'Down',
      throughput: 0,
    }
  })
}

const channels: Record<Band, number[]> = {
  '2.4 GHz': [1, 6, 11],
  '5 GHz': [36, 44, 100, 149],
  '6 GHz': [37, 69, 101],
}
export function radiosOf(device: Device): Radio[] {
  if (device.role !== 'access-point') return []
  const seed = seedOf(device.id)
  const clients = clientsOf([device])
  return (['2.4 GHz', '5 GHz', '6 GHz'] as const).map((band) => {
    const options = channels[band]
    return {
      band,
      channel: options[seed % options.length] ?? 1,
      clients: clients.filter((client) => client.band === band).length,
    }
  })
}

export interface LinkDetails {
  sourcePort?: Port
  targetPort?: Port
  duplex: 'Full' | 'Half'
  mtu: number
  vlans: string
  // Traffic toward the downstream device, and back up toward the uplink.
  down: number
  up: number
  utilization: number
  latency: number
  errors: number
}
// Both ends of a link are read from the port tables, so the canvas, the
// inspector, and each device's port list agree on names and state.
export function linkDetailsOf(fleet: Device[], link: Link): LinkDetails {
  const source = fleet.find((device) => device.id === link.sourceId)
  const target = fleet.find((device) => device.id === link.targetId)
  const seed = seedOf(link.id)
  const degraded = link.health === 'Degraded'
  const down = link.throughput
  const up = Math.round(link.throughput * 0.18)
  return {
    sourcePort: source
      ? portsOf(fleet, source).find((port) => port.neighborId === link.targetId)
      : undefined,
    targetPort: target
      ? portsOf(fleet, target).find((port) => port.neighborId === link.sourceId)
      : undefined,
    duplex: 'Full',
    mtu: link.medium === 'Fiber' ? 9216 : 1500,
    vlans:
      target?.role === 'access-point'
        ? 'Trunk · 10, 20, 30 (native 1)'
        : 'Trunk · all',
    down,
    up,
    utilization: Math.round((Math.max(down, up) / link.capacity) * 1000) / 10,
    latency:
      link.health === 'Offline'
        ? 0
        : (link.medium === 'Fiber' ? 0.1 : 0.3) + (degraded ? 2.4 : 0),
    errors: degraded ? 40 + (seed % 200) : 0,
  }
}

export interface PortDetails {
  port: Port
  // The link this port terminates, when its far end is a managed device.
  link?: Link
  duplex?: 'Full' | 'Half'
  mode: 'Trunk' | 'Access' | 'Routed'
  vlans: string
  mtu: number
  mac: string
  // Seen from the port: received from, and sent to, the far end.
  rx: number
  tx: number
  errors: number
  lastChange: number
}
export function portDetailsOf(
  fleet: Device[],
  device: Device,
  name: string,
): PortDetails | undefined {
  const ports = portsOf(fleet, device)
  const index = ports.findIndex((port) => port.name === name)
  const port = ports[index]
  if (!port) return undefined
  const seed = seedOf(`${device.id}/${name}`)
  const link = port.neighborId
    ? linksOf(fleet).find(
        (item) =>
          (item.sourceId === device.id && item.targetId === port.neighborId) ||
          (item.targetId === device.id && item.sourceId === port.neighborId),
      )
    : undefined
  const facing = link ? linkDetailsOf(fleet, link) : undefined
  // On the downstream end the link's downstream traffic arrives; on the
  // upstream end it leaves.
  const downstreamEnd = link?.targetId === device.id
  const rx = facing
    ? downstreamEnd
      ? facing.down
      : facing.up
    : port.endpoint === 'Internet'
      ? port.throughput
      : Math.round(port.throughput * 0.3)
  const tx = facing
    ? downstreamEnd
      ? facing.up
      : facing.down
    : port.endpoint === 'Internet'
      ? Math.round(port.throughput * 0.18)
      : port.throughput
  const up = port.status === 'Up'
  return {
    port,
    link,
    duplex: up ? 'Full' : undefined,
    mode:
      port.endpoint === 'Internet'
        ? 'Routed'
        : port.neighborId
          ? 'Trunk'
          : 'Access',
    vlans:
      port.endpoint === 'Internet'
        ? 'Untagged'
        : facing
          ? facing.vlans.replace(/^Trunk · /, '')
          : `${[10, 20, 30, 40][seed % 4]} (untagged)`,
    mtu: facing?.mtu ?? 1500,
    mac: [0x02, 0x1a, (seed >> 8) & 0xff, seed & 0xff, index >> 8, index & 0xff]
      .map((octet) => octet.toString(16).padStart(2, '0'))
      .join(':'),
    rx: up ? rx : 0,
    tx: up ? tx : 0,
    errors: facing?.errors ?? 0,
    lastChange: BOOT_EPOCH + (seed % 900) * 3_600_000,
  }
}

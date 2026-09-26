import type { Device } from './fleet'

export type Band = '2.4 GHz' | '5 GHz' | '6 GHz'
export interface Client {
  id: string
  hostname: string
  mac: string
  address: string
  deviceId: string
  band: Band
  // Received signal strength in dBm; closer to zero is stronger.
  signal: number
  throughput: number
}

const hosts = [
  'iphone',
  'macbook',
  'galaxy',
  'thinkpad',
  'ipad',
  'pixel',
  'surface',
  'printer',
  'zoom-room',
  'sonos',
]
const bands: Band[] = ['5 GHz', '5 GHz', '6 GHz', '2.4 GHz']

// A stable number per device, so a client keeps its identity however the
// device list it is generated from was filtered.
function deviceSeed(id: string): number {
  let hash = 0
  for (const char of id) hash = (hash * 31 + char.charCodeAt(0)) & 0xffff
  return hash
}

// Builds each access point's reported clients so the list matches the
// per-device client counts the rest of the fleet view shows.
export function clientsOf(fleet: Device[]): Client[] {
  return fleet.flatMap((device) =>
    Array.from({ length: device.clients }, (_, index) => {
      const base = deviceSeed(device.id)
      const seed = base * 97 + index * 31
      const octets = [0x3c, 0x22, base >> 8, base & 0xff, index, seed & 0xff]
      return {
        id: `${device.id}-client-${index + 1}`,
        hostname: `${hosts[seed % hosts.length]}-${(1000 + (seed % 45000)).toString(36)}`,
        mac: octets
          .map((octet) => octet.toString(16).padStart(2, '0'))
          .join(':'),
        address: device.address.replace(/\.\d+$/, `.${100 + index}`),
        deviceId: device.id,
        band: bands[seed % bands.length] ?? '5 GHz',
        signal: -38 - (seed % 41),
        throughput: 1 + (seed % 37),
      }
    }),
  )
}

export function signalQuality(signal: number): 'Strong' | 'Fair' | 'Weak' {
  if (signal >= -60) return 'Strong'
  if (signal >= -70) return 'Fair'
  return 'Weak'
}

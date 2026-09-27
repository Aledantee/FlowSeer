import type { Device, Health, Site } from './fleet'

export type Severity = 'critical' | 'warning' | 'info'
export interface FleetEvent {
  id: string
  deviceId: string
  severity: Severity
  minutesAgo: number
  summary: string
}
export interface TrafficPoint {
  hour: number
  mbps: number
}
export interface SiteRollup {
  site: Site
  health: Record<Health, number>
  clients: number
  throughput: number
}

export const events: FleetEvent[] = [
  {
    id: 'evt-1',
    deviceId: 'dev-7',
    severity: 'warning',
    minutesAgo: 12,
    summary: 'Retry rate on the 5 GHz radio above 18%',
  },
  {
    id: 'evt-2',
    deviceId: 'dev-16',
    severity: 'critical',
    minutesAgo: 38,
    summary: 'Stopped answering polls',
  },
  {
    id: 'evt-3',
    deviceId: 'dev-5',
    severity: 'info',
    minutesAgo: 124,
    summary: 'Running configuration saved',
  },
  {
    id: 'evt-4',
    deviceId: 'dev-9',
    severity: 'warning',
    minutesAgo: 310,
    summary: 'WAN uplink flapped twice in 4 minutes',
  },
  {
    id: 'evt-5',
    deviceId: 'dev-2',
    severity: 'info',
    minutesAgo: 455,
    summary: 'Firmware 10.0.10g installed',
  },
  {
    id: 'evt-6',
    deviceId: 'dev-13',
    severity: 'info',
    minutesAgo: 820,
    summary: 'Discovered and added to inventory',
  },
]

export function scopedEvents(scope: Device[]): FleetEvent[] {
  const ids = new Set(scope.map((device) => device.id))
  return events
    .filter((event) => ids.has(event.deviceId))
    .sort((a, b) => a.minutesAgo - b.minutesAgo)
}

export function healthCounts(scope: Device[]): Record<Health, number> {
  const counts: Record<Health, number> = {
    Healthy: 0,
    Degraded: 0,
    Offline: 0,
  }
  for (const device of scope) counts[device.health]++
  return counts
}

export function siteRollups(scope: Device[], sites: Site[]): SiteRollup[] {
  return sites.map((site) => {
    const local = scope.filter((device) => device.siteId === site.id)
    return {
      site,
      health: healthCounts(local),
      clients: local.reduce((sum, device) => sum + device.clients, 0),
      throughput: local.reduce((sum, device) => sum + device.throughput, 0),
    }
  })
}

// Office traffic: a night floor, rising through the morning, peaking early
// afternoon. Deterministic so the chart does not reshuffle on every render.
function load(hour: number): number {
  const day = Math.sin((Math.PI * (hour - 6)) / 16)
  return hour >= 6 && hour <= 22 ? 0.3 + 0.7 * day : 0.3
}

// The last point is the live aggregate, so the right edge of the chart moves
// with the metric cards; earlier points scale each device's current rate by
// the hour-of-day load relative to now.
export function trafficHistory(
  scope: Device[],
  endHour: number,
  hours = 24,
): TrafficPoint[] {
  const live = scope.filter((device) => device.health !== 'Offline')
  const now = live.reduce((sum, device) => sum + device.throughput, 0)
  const reference = load(endHour)
  return Array.from({ length: hours }, (_, index) => {
    const hour = (endHour - (hours - 1 - index) + 24 * hours) % 24
    if (index === hours - 1) return { hour, mbps: now }
    const jitter = 1 + (((index * 7) % 5) - 2) * 0.03
    return { hour, mbps: Math.round((now * load(hour) * jitter) / reference) }
  })
}

export function formatAgo(minutes: number): string {
  if (minutes < 1) return 'just now'
  if (minutes < 60) return `${minutes} min ago`
  const hours = Math.floor(minutes / 60)
  return `${hours} h ago`
}

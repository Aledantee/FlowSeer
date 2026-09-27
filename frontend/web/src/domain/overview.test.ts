import { describe, expect, it } from 'vitest'
import { devices, filterDevices, sites } from './fleet'
import {
  events,
  healthCounts,
  latestIssue,
  rankSites,
  scopedEvents,
  siteRollups,
  trafficHistory,
} from './overview'
describe('scope overview', () => {
  it('references only fixture devices from events', () => {
    const ids = new Set(devices.map((device) => device.id))
    expect(events.every((event) => ids.has(event.deviceId))).toBe(true)
  })
  it('limits events to the scope, newest first', () => {
    const hamburg = filterDevices(devices, '', 'hamburg', '', '')
    expect(scopedEvents(hamburg).map((event) => event.id)).toEqual([
      'evt-1',
      'evt-3',
    ])
  })
  it('counts health and rolls it up per site', () => {
    expect(healthCounts(devices)).toEqual({
      Healthy: 14,
      Degraded: 1,
      Offline: 1,
    })
    const cologne = siteRollups(devices, sites).find(
      (rollup) => rollup.site.id === 'cologne',
    )
    expect(cologne?.health.Offline).toBe(1)
  })
  it('ends the traffic history at the live aggregate, wrapping hours', () => {
    const history = trafficHistory(devices, 3)
    const live = devices
      .filter((device) => device.health !== 'Offline')
      .reduce((sum, device) => sum + device.throughput, 0)
    expect(history).toHaveLength(24)
    expect(history[0]?.hour).toBe(4)
    expect(history.at(-1)).toEqual({ hour: 3, mbps: live })
  })
  it('ranks the worst site first and names its newest issue', () => {
    const ranked = rankSites(siteRollups(devices, sites)).map(
      (rollup) => rollup.site.id,
    )
    expect(ranked.slice(0, 2)).toEqual(['cologne', 'hamburg'])
    const cologne = sites.find((site) => site.id === 'cologne')
    if (!cologne) throw new Error('Missing fixture')
    expect(latestIssue(devices, cologne)?.summary).toBe(
      'Stopped answering polls',
    )
  })
})

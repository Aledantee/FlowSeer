import { describe, expect, it } from 'vitest'
import {
  tenantIds,
  filterDevices,
  moveDevice,
  pollDevice,
  pathSummary,
  siteNeighbours,
  devices,
} from './fleet'
describe('operator fleet scope', () => {
  it('includes descendants when selecting a parent tenant', () => {
    expect(tenantIds('aurora')).toEqual(['aurora', 'aurora-de'])
    expect(filterDevices(devices, 'aurora', '', '', '').length).toBe(8)
  })
  it('combines site, search and status filters', () => {
    expect(
      filterDevices(devices, '', 'berlin', 'gateway', 'Healthy').map(
        (d) => d.id,
      ),
    ).toEqual(['dev-1'])
  })
  it('replaces the one site assignment and rejects another tenant’s site', () => {
    const first = devices[0]
    if (!first) throw new Error('Missing fixture')
    expect(moveDevice(first, 'hamburg').siteId).toBe('hamburg')
    expect(first.siteId).toBe('berlin')
    expect(() => moveDevice(first, 'aachen')).toThrow('same tenant')
  })
  it('attention includes offline and degraded devices', () => {
    expect(
      filterDevices(devices, '', '', '', 'attention').map(
        (device) => device.health,
      ),
    ).toEqual(['Degraded', 'Offline'])
  })
  it('unknown scope produces no devices', () => {
    expect(filterDevices(devices, 'missing', '', '', '')).toEqual([])
  })
  it('a poll refreshes the observation but not the last answer of an unreachable device', () => {
    const offline = devices.find((device) => device.health === 'Offline')
    const degraded = devices.find((device) => device.health === 'Degraded')
    if (!offline || !degraded) throw new Error('Missing fixture')
    const failed = pollDevice(offline)
    expect(failed.lastSeenMinutes).toBe(offline.lastSeenMinutes)
    expect(failed.health).toBe('Offline')
    expect(failed.bindings.map((b) => b.observedMinutesAgo)).toEqual([0, 0])
    expect(pollDevice(degraded).lastSeenMinutes).toBe(0)
  })
  it('keeps reachability per binding apart from lifecycle', () => {
    const ap = devices.find((device) => device.name === 'berlin-ap-01')
    expect(ap?.lifecycle).toBe('Active')
    expect(ap?.bindings.map((b) => b.integrationId)).toEqual([
      'lan-berlin',
      'wlc-aurora-de',
    ])
  })
  it('describes an offline device against its paths and its site', () => {
    const offline = devices.find((device) => device.health === 'Offline')
    if (!offline) throw new Error('Missing fixture')
    expect(pathSummary(offline)).toBe('Both paths are unreachable.')
    expect(siteNeighbours(devices, offline)).toEqual({ answering: 3, total: 3 })
  })
})

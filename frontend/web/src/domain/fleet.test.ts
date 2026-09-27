import { describe, expect, it } from 'vitest'
import {
  tenantIds,
  filterDevices,
  moveDevice,
  devices,
  downlinks,
  linksOf,
  uplinkOf,
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
  it('connects access points to the core switch below the gateway', () => {
    const berlin = devices.filter((device) => device.siteId === 'berlin')
    const gateway = berlin.find((device) => device.role === 'gateway')
    if (!gateway) throw new Error('Missing fixture')
    const [core] = downlinks(berlin, gateway)
    if (!core) throw new Error('Missing fixture')
    expect(downlinks(berlin, core).map((device) => device.role)).toEqual([
      'access-point',
      'access-point',
    ])
  })
  it('carries the whole subtree on a link and marks it down when an end is offline', () => {
    const berlin = devices.filter((device) => device.siteId === 'berlin')
    const core = linksOf(berlin).find((link) => link.targetId === 'dev-2')
    expect(core?.throughput).toBe(
      berlin
        .filter((device) => device.role !== 'gateway')
        .reduce((sum, device) => sum + device.throughput, 0),
    )
    const cologne = devices.filter((device) => device.siteId === 'cologne')
    expect(
      linksOf(cologne).find((link) => link.targetId === 'dev-16')?.health,
    ).toBe('Offline')
  })
  it('drops active uplink when an access point is reassigned across sites', () => {
    const ap = devices.find((device) => device.name === 'berlin-ap-01')
    if (!ap) throw new Error('Missing fixture')
    expect(uplinkOf(devices, ap)?.name).toBe('berlin-sw-01')
    const reassigned = moveDevice(ap, 'hamburg')
    expect(uplinkOf(devices, reassigned)).toBeUndefined()
  })
})

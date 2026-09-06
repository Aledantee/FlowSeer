import { describe, expect, it } from 'vitest'
import { tenantIds, filterDevices, moveDevice, devices } from './fleet'
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
})

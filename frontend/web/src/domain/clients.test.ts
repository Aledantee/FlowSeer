import { describe, expect, it } from 'vitest'
import { devices } from './fleet'
import { clientsOf } from './clients'

describe('reported clients', () => {
  it('matches each device’s client count', () => {
    expect(clientsOf(devices).length).toBe(
      devices.reduce((sum, device) => sum + device.clients, 0),
    )
  })
  it('keeps a client’s identity when the device list is filtered', () => {
    const accessPoint = devices.find((device) => device.clients > 0)
    if (!accessPoint) throw new Error('Missing fixture')
    const alone = clientsOf([accessPoint])
    const inFleet = clientsOf(devices).filter(
      (client) => client.deviceId === accessPoint.id,
    )
    expect(alone).toEqual(inFleet)
  })
  it('gives every client a distinct MAC address', () => {
    const macs = clientsOf(devices).map((client) => client.mac)
    expect(new Set(macs).size).toBe(macs.length)
  })
})

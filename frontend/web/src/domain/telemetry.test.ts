import { describe, expect, it } from 'vitest'
import { devices, linksOf } from './fleet'
import {
  linkDetailsOf,
  portDetailsOf,
  portsOf,
  radiosOf,
  telemetryOf,
} from './telemetry'

function device(name: string) {
  const found = devices.find((item) => item.name === name)
  if (!found) throw new Error(`Missing fixture ${name}`)
  return found
}

describe('device telemetry', () => {
  it('points each port at the device on its far end', () => {
    const ports = portsOf(devices, device('berlin-sw-01'))
    expect(ports[0]?.neighborId).toBe(device('berlin-gw-01').id)
    expect(ports.slice(1, 3).map((port) => port.neighborId)).toEqual([
      device('berlin-ap-01').id,
      device('berlin-ap-02').id,
    ])
  })
  it('takes every port and the uptime down with an offline device', () => {
    const offline = device('cologne-ap-02')
    expect(
      portsOf(devices, offline).every((port) => port.status === 'Down'),
    ).toBe(true)
    expect(telemetryOf(offline).bootedAt).toBeUndefined()
  })
  it('reports the port facing an offline device as down', () => {
    const port = portsOf(devices, device('cologne-sw-01')).find(
      (item) => item.neighborId === device('cologne-ap-02').id,
    )
    expect(port?.status).toBe('Down')
  })
  it('splits an access point’s clients across its radios', () => {
    const ap = device('berlin-ap-01')
    expect(radiosOf(ap).reduce((sum, radio) => sum + radio.clients, 0)).toBe(
      ap.clients,
    )
    expect(radiosOf(device('berlin-gw-01'))).toEqual([])
  })
  it('names the port at each end of a link', () => {
    const link = linksOf(devices).find(
      (item) => item.targetId === device('berlin-ap-01').id,
    )
    if (!link) throw new Error('Missing fixture')
    const details = linkDetailsOf(devices, link)
    expect(details.sourcePort?.name).toBe('ge-0/0/0')
    expect(details.targetPort?.name).toBe('eth0')
  })
  it('reads a link port from its own end', () => {
    const switchSide = portDetailsOf(
      devices,
      device('berlin-sw-01'),
      'ge-0/0/0',
    )
    const apSide = portDetailsOf(devices, device('berlin-ap-01'), 'eth0')
    expect(switchSide?.tx).toBe(apSide?.rx)
    expect(switchSide?.rx).toBe(apSide?.tx)
    expect(
      portDetailsOf(devices, device('berlin-ap-01'), 'nope'),
    ).toBeUndefined()
  })
})

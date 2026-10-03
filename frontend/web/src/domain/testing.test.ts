// @vitest-environment happy-dom
import { describe, expect, it } from 'vitest'
import { BRAND } from '../brand'
import { devices, sites, tenants } from './fleet'
import { clientsOf } from './clients'
import { portDetailsOf, portsOf, telemetryOf } from './telemetry'
import { fixtureIdentifiers } from './testing'

describe('fixtureIdentifiers', () => {
  it('collects names and addresses from fixtures without locations or kinds', () => {
    const identifiers = fixtureIdentifiers()

    expect(identifiers.has('berlin-gw-01')).toBe(true)
    expect(identifiers.has('10.20.0.1')).toBe(true)
    expect(identifiers.has('Berlin Mitte')).toBe(true)
    expect(identifiers.has('Aurora Hospitality')).toBe(true)

    expect(identifiers.has('Berlin, DE')).toBe(false)
    expect(identifiers.has('Gateway')).toBe(false)
    expect(identifiers.has('Core switch')).toBe(false)
  })

  it('collects client hostnames, MACs, and addresses', () => {
    const identifiers = fixtureIdentifiers()
    const [client] = clientsOf(devices)
    if (!client) throw new Error('Fixture has no sample client')

    expect(identifiers.has(client.hostname)).toBe(true)
    expect(identifiers.has(client.mac)).toBe(true)
    expect(identifiers.has(client.address)).toBe(true)
  })

  it('collects each identifier class: brand, serial, model, firmware, ports, sites, and tenants', () => {
    const identifiers = fixtureIdentifiers()

    expect(identifiers.has(BRAND)).toBe(true)

    const [device] = devices
    if (!device) throw new Error('Fixture has no sample device')
    const telemetry = telemetryOf(device)
    if (!telemetry.serial || !telemetry.model || !telemetry.firmware) {
      throw new Error('Device telemetry missing serial, model, or firmware')
    }
    expect(identifiers.has(telemetry.serial)).toBe(true)
    expect(identifiers.has(telemetry.model)).toBe(true)
    expect(identifiers.has(telemetry.firmware)).toBe(true)

    const ports = portsOf(devices, device)
    const [port] = ports
    if (!port) throw new Error('Fixture has no sample port')
    expect(identifiers.has(port.name)).toBe(true)
    const details = portDetailsOf(devices, device, port.name)
    if (!details?.mac) throw new Error('Port missing mac')
    expect(identifiers.has(details.mac)).toBe(true)

    const [site] = sites
    if (!site) throw new Error('Fixture has no sample site')
    expect(identifiers.has(site.name)).toBe(true)

    const [tenant] = tenants
    if (!tenant) throw new Error('Fixture has no sample tenant')
    expect(identifiers.has(tenant.name)).toBe(true)
  })
})

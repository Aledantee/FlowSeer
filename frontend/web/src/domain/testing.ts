import {
  devices,
  sites,
  tenants,
  type Device,
  type Site,
  type Tenant,
} from './fleet'
import { clientsOf } from './clients'
import { portDetailsOf, portsOf, telemetryOf } from './telemetry'

export const BRAND = 'FlowSeer'

export function fixtureIdentifiers(
  allDevices: Device[] = devices,
  allSites: Site[] = sites,
  allTenants: Tenant[] = tenants,
): Set<string> {
  const set = new Set<string>()
  set.add(BRAND)
  for (const device of allDevices) {
    if (device.name) set.add(device.name)
    if (device.address) set.add(device.address)

    const telemetry = telemetryOf(device)
    if (telemetry.serial) set.add(telemetry.serial)
    if (telemetry.model) set.add(telemetry.model)
    if (telemetry.firmware) set.add(telemetry.firmware)

    for (const port of portsOf(allDevices, device)) {
      if (port.name) set.add(port.name)
      const details = portDetailsOf(allDevices, device, port.name)
      if (details?.mac) set.add(details.mac)
    }
  }
  for (const site of allSites) {
    if (site.name) set.add(site.name)
  }
  for (const tenant of allTenants) {
    if (tenant.name) set.add(tenant.name)
  }
  for (const client of clientsOf(allDevices)) {
    if (client.hostname) set.add(client.hostname)
    if (client.mac) set.add(client.mac)
    if (client.address) set.add(client.address)
  }
  return set
}

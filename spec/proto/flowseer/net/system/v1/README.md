# System and Resource Primitives

The `flowseer.net.system.v1` package defines system resource utilization
values, software images, and licenses for network devices and hosts. System
identity (contact, location, uptime) is `DeviceState`'s concern and lives in
`model/inventory/v1`.

## Boundaries

Imports: net/key, net/measure

Imported by: nothing FlowSeer-owned

Deliberately absent:

- System contact, location, and uptime. These describe device placement and
  identity and are carried on `DeviceState`.
- Configuration state and backup files.
- Vendor-specific licensing enforcement actions.

## Placement and utilization rules

- **Component or device placement**: When a device exposes per-component CPU or
  storage breakdowns (e.g. from `hrProcessorTable` or `hrStorageTable`), the
  utilization values are carried on `ComponentState` (processor utilization only
  on CPU components, storage utilization only on storage components). When a
  source reports only whole-box figures (e.g. UniFi `cpuUtilizationPct`, MikroTik
  `/system/resource`, LANCOM `cpuLoadPercent`), they are carried on
  `DeviceState`. A consumer inspects component rows first and device-level rows
  otherwise.
- **Averaging window**: `ProcessorUtilization` carries a `window` duration when
  the reporting source specifies its averaging interval (e.g. 60 s for
  `hrProcessorLoad`). When the source specifies no averaging window, `window` is
  left unset.
- **Allocation units**: A mapper converts `hrStorageSize` and `hrStorageUsed` by
  multiplying by `hrStorageAllocationUnits` to produce canonical byte values for
  `total_bytes` and `used_bytes`. If a source reports only a utilization ratio,
  `used_basis_points` is populated alone.
- **Network instance scoping**: `SoftwareImage` and `License` represent table
  rows on the device. Per the network-instance key convention, tables whose key
  is not scoped by an interface carry a required `network_instance` name. A
  mapper assigns the default instance (`default` when unnamed by the device).

## License status mapping

`LicenseStatus` normalizes diverse licensing states:
- `INACTIVE`: Registered but not active (OpenConfig `in-use = false`).
- `IN_USE`: Valid and active (OpenConfig `in-use = true`, Cisco smart licensing `AUTHORIZED`).
- `EVALUATION`: In a trial period (Cisco smart licensing `EVAL_MODE`).
- `EXPIRED`: License validity period lapsed (OpenConfig `expired = true`, `clmgmtLicenseStatus` `expired`).
- `OUT_OF_COMPLIANCE`: Capacity exceeded or grace period active (Cisco smart licensing `OUT_OF_COMPLIANCE`).

## Contents

- `processor_utilization.proto` — `ProcessorUtilization`: average CPU utilization in basis points with optional window.
- `storage_kind.proto` — `StorageKind`: storage classifications normalized from HOST-RESOURCES-TYPES.
- `storage_utilization.proto` — `StorageUtilization`: capacity, used bytes, and utilization basis points.
- `software_image.proto` — `SoftwareImage`: partition slot, version, running/boot status, and size.
- `license_status.proto` — `LicenseStatus`: normalized operational license states.
- `license.proto` — `License`: license name, status, validity timestamps, and entitlement count.

## Sources

- RFC 2790 (HOST-RESOURCES-MIB) for processor load and storage table definitions.
- OpenConfig `openconfig-system.yang` and `openconfig-license.yang` for CPU, memory, and license modeling.
- Cisco IOS-XE `Cisco-IOS-XE-install-oper.yang` and `cisco-smart-license.yang` for software images and entitlements.
- Aruba CX `ARUBAWIRED-SWITCH-IMAGE-MIB` for partition slot conventions.

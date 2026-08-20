# Cisco IOS-XE YANG Models

YANG models for RESTCONF/NETCONF/gNMI management of Cisco IOS-XE devices
(Catalyst switches and Catalyst 9800 wireless controllers).

## Contents

- `2611/` — the complete published model set for **IOS-XE 26.1.1** (912 files:
  `Cisco-IOS-XE-*` native/oper/cfg/rpc modules, IETF/openconfig imports, MIB-derived
  modules, deviations, and the release README with platform capabilities).
  26.1.1 is the newest release under `vendor/cisco/xe/` at vendoring time;
  Cisco switched from 17.x to year-based versioning after 17.18.

Key modules for FlowSeer switch/wireless monitoring:
`Cisco-IOS-XE-interfaces-oper`, `Cisco-IOS-XE-environment-oper`,
`Cisco-IOS-XE-platform-oper`, `Cisco-IOS-XE-native`,
`Cisco-IOS-XE-wireless-access-point-oper`, `Cisco-IOS-XE-wireless-client-oper`,
plus the other `Cisco-IOS-XE-wireless-*` cfg/oper modules for the 9800 WLC.

## Source

- Upstream: https://github.com/YangModels/yang, path `vendor/cisco/xe/2611/`
- Vendored: 2026-08-20 via sparse checkout
  (`git clone --depth=1 --filter=blob:none --sparse` + `sparse-checkout set vendor/cisco/xe/2611`)

## Refresh

Re-run the sparse checkout against the newest `vendor/cisco/xe/<release>/`
directory and copy it in alongside (or replacing) `2611/`.

## Notes

- AireOS WLCs and autonomous IOS APs have **no** YANG/RESTCONF support —
  they remain SNMP-only (see `spec/mib/cisco/`).
- License: per-file Cisco headers plus `vendor/cisco/xe/LICENSE.md` upstream
  (Apache-2.0). Treat as a vendored cache.

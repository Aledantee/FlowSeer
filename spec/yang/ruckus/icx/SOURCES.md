# Ruckus ICX RESTCONF YANG Models

YANG models for RESTCONF management of Ruckus ICX switches running
FastIron 09.0.00+ (ICX 7150/7250/7450/7550/7650/7850 and newer).

## Contents

- `9.0.00/` — the FastIron 09.0.00 RESTCONF model set (111 `.yang` files):
  upstream OpenConfig modules (`openconfig-*`: interfaces, vlan, lldp,
  spanning-tree, system, acl, ospfv2, bgp, network-instance, poe, aaa, ...)
  plus the ICX augmentations/deviations (`icx-openconfig-*-aug.yang`,
  `icx-openconfig-*-dev.yang`) that state what the platform actually supports.
- `LICENSE` — upstream repo license.

## Source

Official CommScope/Ruckus GitHub org:
https://github.com/commscope-ruckus/RUCKUS-ICX-RESTCONF
(`release/9.0.00/`, shallow clone 2026-08-20).

## Protocol notes

- ICX FastIron 09.x exposes RESTCONF (OpenConfig-based, deviations above) —
  a better-structured alternative to SNMP for config-side data. SNMP
  (FOUNDRY-SN-* MIBs, see `spec/mib/ruckus/icx/`) remains the broadest
  telemetry surface (stacking, PoE, environment, CAM) and the only trap
  source.
- ICX switches can also be managed *through* a SmartZone controller
  (switch-management public API); see `spec/openapi/ruckus/vsz/`.
- Newer FastIron releases (10.x) ship updated model sets, but only 9.0.00 is
  published in the public repo; 10.x archives sit behind the login-walled
  Ruckus support portal (https://support.ruckuswireless.com/).

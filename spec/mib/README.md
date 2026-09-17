# MIB Files

SNMP MIBs for the device classes FlowSeer ingests from: switches, access
points, and WLAN/controller appliances. Organised by issuing body / vendor,
with product-line subdirectories where one vendor ships distinct firmware
families.

For the same vendors' YANG models see
[`../yang/README.md`](../yang/README.md); for their OpenAPI specs see
[`../openapi/README.md`](../openapi/README.md).

## Layout

```
spec/mib/
├── ietf/                IETF + IANA standard MIBs (RFC1213, IF-MIB, BRIDGE-MIB, ENTITY-MIB, …)
├── ieee/                IEEE 802.1 / 802.3 / LLDP MIBs (all dated revisions kept)
├── cisco/
│   ├── enterprise/      Official cisco/cisco-mibs clone (v1, v2, traps, ucs, …) — IOS/IOS-XE, AIRESPACE/LWAPP, Catalyst
│   ├── products/        The two identity modules mibgen reads, copied out of that clone
│   └── smb/             CISCOSB small-business line (CBS250/350, SG, Catalyst 1200/1300)
├── ruckus/
│   ├── wireless/        SmartZone/vSZ + ZoneDirector + AP MIBs (RUCKUS-SZ-*, RUCKUS-ROOT/TC)
│   └── icx/             ICX switch MIBs (FOUNDRY-SN family; ICX sysObjectIDs in FOUNDRY-SN-ROOT-MIB)
├── hp/
│   ├── procurve/        ProCurve / Aruba AOS-S switch MIBs (HP-ICF/hpicf*, NETSWITCH, STATISTICS)
│   ├── hh3c/            HPE Comware (H3C) switch MIBs (HH3C-*)
│   └── other/           HP printer / ProLiant / BladeSystem MIBs kept out of the switch sets
├── aruba/
│   ├── cx/              AOS-CX switch MIBs (ARUBAWIRED-*)
│   └── wireless/        ArubaOS controller/AP MIBs (ARUBA-*, AI-AP-MIB, WLSX-*)
├── ubiquiti/
│   ├── unifi/           UBNT-MIB, UBNT-UniFi-MIB, FROGFOOT-RESOURCES-MIB
│   ├── edgemax/         EdgeSwitch-* + EdgeMAX MIBs
│   └── airmax/          airMAX/airFiber MIBs
├── lancom/
│   ├── lcos/            LC-UNIFIED-LCOS release MIBs (9.20 – 10.94) for LCOS routers/WLCs
│   ├── lx/              LCOS LX access points (LC-LX-* 7.14 official + legacy LCOS-LX-MIB)
│   └── sx/              LCOS SX switches (LC-GS-* per-line MIBs + sx-5.20/5.30 module packs)
├── netgear/             NETGEAR managed/smart switch MIBs
├── dlink/               D-Link managed switch MIBs (DLINKSW-*, DGS-1210 Fx/Gx, DGS-1250/1520/3630)
├── huawei/              Huawei enterprise/datacom MIBs
└── mikrotik/            MikroTik RouterOS + SwOS MIB (single MIKROTIK-MIB file)
```

## Sources

| Directory | Upstream | Notes |
|-----------|----------|-------|
| `ietf/`   | LibreNMS `mibs/` top level (https://github.com/librenms/librenms) | IETF/IANA standard MIBs as bundled by LibreNMS. |
| `ieee/`   | https://www.ieee802.org/1/files/public/MIBs/ | Every dated revision the WG publishes for 802.1, plus LLDP. |
| `cisco/enterprise/` | https://github.com/cisco/cisco-mibs (shallow clone) | Official Cisco repo. `.git/` retained for `git pull` updates. |
| `cisco/products/` | `cisco/enterprise/v2/` (CISCO-SMI.my, CISCO-PRODUCTS-MIB.my) | Copied rather than read from the clone because the clone is gitignored and `go generate` has to work on a fresh checkout. Together they are the whole Cisco half of the sysObjectID table: ~2960 product nodes and no tables. |
| `cisco/smb/` | netdisco-mibs `ciscosb/` (https://github.com/netdisco/netdisco-mibs) + LibreNMS `mibs/cisco/` | CISCOSB family under OID 1.3.6.1.4.1.9.6.1. Official zips on software.cisco.com require a CCO login. |
| `ruckus/wireless/` | LibreNMS `mibs/ruckus/` | SmartZone/ZoneDirector/AP MIBs. Official Ruckus portal requires login; community mirror used. |
| `ruckus/icx/` | LibreNMS `mibs/brocade/` + `mibs/foundry/` | FOUNDRY-SN family for ICX switches. Official per-release bundles are login-walled. |
| `hp/procurve/` | LibreNMS `mibs/hp/` + netdisco-mibs `hp/` (HP-ICF-FTRCO) | ProCurve/AOS-S switch MIBs only; non-switch HP MIBs live in `hp/other/`. |
| `hp/hh3c/` | LibreNMS `mibs/comware/` | HPE Comware (H3C) HH3C-* MIBs. |
| `hp/other/` | LibreNMS `mibs/hp/` | LaserJet/Compaq/BladeSystem MIBs, parked so the switch set stays clean. |
| `aruba/cx/` | LibreNMS `mibs/arubaos-cx/` | ARUBAWIRED-* AOS-CX MIBs. Official downloads behind HPE Networking Support Portal login. |
| `aruba/wireless/` | LibreNMS `mibs/arubaos/` | ArubaOS controller/AP MIBs. |
| `ubiquiti/unifi/`, `ubiquiti/airmax/` | LibreNMS `mibs/ubnt/` | UBNT-UniFi-MIB is the (shallow) UniFi SNMP surface. |
| `ubiquiti/edgemax/` | LibreNMS `mibs/edgeswitch/` + Observium `mibs/ubiquiti/` (https://github.com/pgmillon/observium) | Observium carries the full current EdgeSwitch-* set; official tarballs stop at ~v1.8 with obsolete FASTPATH names. |
| `lancom/lcos/` | https://ftp.lancom.de/LANCOM-Releases/LC-unified-MIB/ | Official LCOS release MIBs (9.20 – 10.94). |
| `lancom/lx/` | https://ftp.lancom.de/LANCOM-Releases/LC-LX-7500/ (and LC-LX-6402) | Official LCOS LX 7.14 MIBs + legacy LibreNMS LCOS-LX-MIB (2021, superseded). |
| `lancom/sx/` | https://ftp.lancom.de/LANCOM-Releases/LC-GS-*/, LC-YS-7154CF | Official per-line SX MIBs (3.x/4.30 single files, 5.20/5.30 module packs) + legacy LCOS-SX-MIB. |
| `netgear/`| LibreNMS `mibs/netgear/` | NETGEAR-SWITCHING / SMART-SWITCHING / BOXSERVICES / REF. Most Netgear monitoring relies on standard MIBs. |
| `dlink/`  | LibreNMS `mibs/dlink/` + `mibs/dlink_dgs1250/` + https://ftp.dlink.de/dgs/ | LibreNMS baseline completed from official firmware-matched MIB packs (DGS-1210 Rev.F/G, DGS-1250/1520/3630). |
| `huawei/` | LibreNMS `mibs/huawei/` | Enterprise switching/datacom MIBs. Vendor portal alternative: https://support.huawei.com/enterprise/ (login). |
| `mikrotik/` | https://download.mikrotik.com/routeros/7.24/mikrotik.mib (linked from https://mikrotik.com/download/tools) | Single MIKROTIK-MIB covering RouterOS + SwOS. Official per-release URL; bump the RouterOS version in the URL to refresh. `https://mt.lv/routeros-mib` remains 404. |

## Refresh

- **Cisco enterprise**: `git -C cisco/enterprise pull`. Cisco adds product nodes
  with every release, so afterwards copy `v2/CISCO-SMI.my` and
  `v2/CISCO-PRODUCTS-MIB.my` over `cisco/products/` and regenerate; without the
  clone, restore it first:

  ```
  git clone --depth=1 --filter=blob:none --sparse \
      https://github.com/cisco/cisco-mibs spec/mib/cisco/enterprise
  git -C spec/mib/cisco/enterprise sparse-checkout set v2
  ```
- **Cisco SMB**: re-pull netdisco-mibs `ciscosb/` + LibreNMS `mibs/cisco/CISCOSB-*`
- **IEEE 802.1**: re-run the curl loop against `https://www.ieee802.org/1/files/public/MIBs/`
- **LANCOM**: poll `https://ftp.lancom.de/LANCOM-Releases/` — `LC-unified-MIB/` for LCOS, `LC-LX-*/` for LX, `LC-GS-*/` + `LC-YS-*/` for SX
- **D-Link**: check `https://ftp.dlink.de/dgs/<model>/driver_software/` for newer MIB packs
- **MikroTik**: bump the release in the download.mikrotik.com URL (sha256 file published alongside)
- **EdgeSwitch**: re-pull Observium `mibs/ubiquiti/`
- **LibreNMS-sourced vendors** (ruckus, hp, aruba, netgear, dlink, huawei, ietf, ubiquiti): re-do the sparse checkout

```
git clone --depth=1 --filter=blob:none --sparse https://github.com/librenms/librenms /tmp/librenms-mibs
git -C /tmp/librenms-mibs sparse-checkout set mibs
```

## Licensing

MIB files are vendor-published interface definitions; each upstream sets its
own redistribution terms. Treat this directory as a vendored cache, not a
re-distribution: do not ship it to third parties without checking each
vendor's MIB license header.

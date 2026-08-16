# MIB Files

SNMP MIBs for the device classes FlowSeer ingests from: switches, access
points, and WLAN/controller appliances. Organised by issuing body / vendor.

## Layout

```
spec/mib/
├── ietf/        IETF + IANA standard MIBs (RFC1213, IF-MIB, BRIDGE-MIB, ENTITY-MIB, …)
├── ieee/        IEEE 802.1 / 802.3 / LLDP MIBs (all dated revisions kept)
├── cisco/       Official cisco/cisco-mibs tree (v1, v2, traps, ucs, viptela, …)
├── ruckus/      Ruckus / CommScope SmartZone + ZoneDirector + AP MIBs
├── lancom/      LANCOM LC-UNIFIED-LCOS release MIBs (9.20 – 10.94) + LCOS-LX/SX
├── netgear/     NETGEAR managed/smart switch MIBs
├── dlink/       D-Link managed switch MIBs (incl. DGS-1250 supplement)
├── huawei/      Huawei enterprise/datacom MIBs
├── aruba/       ArubaOS (controllers/APs) + ArubaOS-CX (switches)
└── mikrotik/    MikroTik RouterOS + SwOS MIB (single MIKROTIK-MIB file)
```

## Sources

| Directory | Upstream | Notes |
|-----------|----------|-------|
| `ietf/`   | LibreNMS `mibs/` top level (https://github.com/librenms/librenms) | IETF/IANA standard MIBs as bundled by LibreNMS. |
| `ieee/`   | https://www.ieee802.org/1/files/public/MIBs/ | Every dated revision the WG publishes for 802.1, plus LLDP. |
| `cisco/`  | https://github.com/cisco/cisco-mibs (shallow clone) | Official Cisco repo. `.git/` retained for `git pull` updates. |
| `ruckus/` | LibreNMS `mibs/ruckus/` | Official Ruckus portal requires login; community mirror used. |
| `lancom/` | https://ftp.lancom.de/LANCOM-Releases/LC-unified-MIB/ + LibreNMS `mibs/lancom/` | Official LCOS release MIBs (9.20 – 10.94) plus LibreNMS LCOS-LX/SX. |
| `netgear/`| LibreNMS `mibs/netgear/` | NETGEAR-SWITCHING / SMART-SWITCHING / BOXSERVICES / REF. Most Netgear monitoring relies on standard MIBs. |
| `dlink/`  | LibreNMS `mibs/dlink/` + `mibs/dlink_dgs1250/` | Managed-switch MIBs. |
| `huawei/` | LibreNMS `mibs/huawei/` | Enterprise switching/datacom MIBs. Vendor portal alternative: https://support.huawei.com/enterprise/ (login). |
| `aruba/`  | LibreNMS `mibs/arubaos/` + `mibs/arubaos-cx/` | ArubaOS controller/AP and ArubaOS-CX switch MIBs. Official ASP portal requires login. |
| `mikrotik/` | LibreNMS `mibs/mikrotik/MIKROTIK-MIB` | Single MIKROTIK-MIB covering RouterOS + SwOS. Official `https://mt.lv/routeros-mib` was 404 at vendoring time. |

## Refresh

- **Cisco**: `cd cisco && git pull`
- **IEEE 802.1**: re-run the curl loop against `https://www.ieee802.org/1/files/public/MIBs/`
- **LANCOM**: poll `https://ftp.lancom.de/LANCOM-Releases/LC-unified-MIB/` for new `LC-UNIFIED-LCOS-*-REL-OIDS.mib`
- **LibreNMS-sourced vendors** (ruckus, netgear, dlink, huawei, aruba, ietf, mikrotik): re-do the sparse checkout

```
git clone --depth=1 --filter=blob:none --sparse https://github.com/librenms/librenms /tmp/librenms-mibs
git -C /tmp/librenms-mibs sparse-checkout set mibs
```

## Licensing

MIB files are vendor-published interface definitions; each upstream sets its
own redistribution terms. Treat this directory as a vendored cache, not a
re-distribution: do not ship it to third parties without checking each
vendor's MIB license header.

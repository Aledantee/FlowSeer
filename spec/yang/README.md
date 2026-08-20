# YANG Models

YANG model trees for the device classes FlowSeer ingests from over
RESTCONF/NETCONF/gNMI — the model-driven protocols that are richer than SNMP
where a vendor offers one. The SNMP MIBs for the same vendors live in
[`../mib/README.md`](../mib/README.md). Each subdirectory carries a
`SOURCES.md` with exact upstream URLs and refresh steps.

OpenAPI/REST specifications live in [`../openapi/README.md`](../openapi/README.md).

## Layout

```
spec/yang/
├── cisco/
│   └── iosxe/   IOS-XE 26.1.1 YANG models (RESTCONF/NETCONF/gNMI) — Catalyst switches + 9800 WLC, with capability-*.xml manifests
├── aruba/
│   └── cx/      AOS-CX OpenConfig YANG models (10.17) for model-driven telemetry
└── ruckus/
    └── icx/     FastIron 9.0.00 RESTCONF YANG set (openconfig-* + icx-* augments/deviations)
```

## Sources

| Directory | Upstream | Protocol served |
|-----------|----------|-----------------|
| `cisco/iosxe/` | https://github.com/YangModels/yang `vendor/cisco/xe/2611/` (sparse checkout) | RESTCONF / NETCONF / gNMI on IOS-XE (Catalyst, 9800 WLC). |
| `aruba/cx/` | https://github.com/aruba/aoscx-yang (official, Apache-2.0) | gNMI/OpenConfig telemetry on AOS-CX. |
| `ruckus/icx/` | https://github.com/commscope-ruckus/RUCKUS-ICX-RESTCONF (official) | RESTCONF on ICX FastIron 09.x+ (OpenConfig-based). |

Vendors with no entry here publish no YANG models; their spec-backed
protocols are SNMP (see `../mib/`) or REST (see `../openapi/`).

## Refresh

Follow the per-directory `SOURCES.md`. In short:

- **cisco/iosxe**: sparse checkout of YangModels/yang against the newest `vendor/cisco/xe/<release>/`
- **aruba/cx, ruckus/icx**: re-pull the official GitHub repos

## Licensing

Same policy as `../mib/`: vendored cache of vendor-published interface
definitions, each under its upstream license (LICENSE files kept alongside
where the upstream ships one). Do not redistribute without checking.

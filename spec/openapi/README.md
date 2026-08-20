# OpenAPI / REST Specs

OpenAPI documents — and pointer-only entries where no spec is publicly
downloadable — for the REST-managed device classes FlowSeer ingests from.
The SNMP MIBs for the same vendors live in
[`../mib/README.md`](../mib/README.md), the YANG models in
[`../yang/README.md`](../yang/README.md). Each subdirectory carries a
`SOURCES.md` with exact upstream URLs and refresh steps.

## Layout

```
spec/openapi/
├── ubiquiti/    UniFi Network API v10.4.57 + Site Manager API v1.0.0 OpenAPI specs
├── mikrotik/    RouterOS 7.24 REST API OpenAPI schema (community-generated, unofficial)
├── lancom/      LANCOM Management Cloud (LMC) per-service OpenAPI 3.0.3 specs (13 services)
├── ruckus/
│   └── vsz/     SmartZone 7.1.1 v13_1 Swagger 2.0 spec (community capture of the controller-served JSON)
└── hp/          SOURCES.md only — Comware NETCONF + AOS-S REST have no public machine-readable spec
```

## Sources

| Directory | Upstream | Official? | Notes |
|-----------|----------|-----------|-------|
| `ubiquiti/` | https://developer.ui.com via https://github.com/opastorello/unifi-api-docs (daily CI mirror) | Official specs, community mirror | UniFi Network API (local controller, X-API-Key) + cloud Site Manager API. |
| `mikrotik/` | https://tikoci.github.io/restraml/7.24/openapi.json | Community (MikroTik publishes no spec) | RouterOS v7 REST API (HTTPS JSON over the console API). |
| `lancom/` | https://cloud.lancom.de/cloud-service-<service>/api-docs/index.json | Official, public | LMC REST API (OAuth2) for LMC-managed fleets; no on-device REST/NETCONF exists. |
| `ruckus/vsz/` | https://github.com/zgilburd/vsz-mcp (`docs/openapi-spec.json`) | Official spec content, community capture | SmartZone 7.1.1 `v13_1` Swagger 2.0 (688 paths, vSZ-E + vSZ-H) — verbatim dump of a live controller's `/wsg/apiDoc/openapi`; Ruckus publishes no static download. |
| `hp/` | — (see its SOURCES.md) | — | **Pointer-only**: Comware NETCONF XSDs are login-walled (pull per-device via RFC 6022 `get-schema`); AOS-S REST schemas exist only in PDF guides / on-device. |

## Refresh

Follow the per-directory `SOURCES.md`. In short:

- **ubiquiti**: check `catalog.json` in opastorello/unifi-api-docs for newer spec versions
- **mikrotik**: bump the release in the tikoci.github.io/restraml URL
- **lancom**: re-fetch the 13 `api-docs/index.json` service specs from cloud.lancom.de
- **ruckus/vsz**: re-fetch `https://{host}:8443/wsg/apiDoc/openapi` from our own vSZ and replace the vendored JSON (the community capture is a stopgap)
- **hp**: capture Comware models per-device via `get-schema` when a device is available

## Licensing

Same policy as `../mib/`: vendored cache of vendor/community-published
interface definitions, each under its upstream license. Do not redistribute
without checking.

# Ruckus SmartZone / vSZ Public REST API

Machine-readable artifacts for the SmartZone (SZ100/SZ144/SZ300, vSZ-E,
vSZ-H) public REST API — the preferred management/telemetry protocol for a
SmartZone deployment (JSON over HTTPS, `/wsg/api/public/v13_1/...`), richer
and far easier to consume than the SNMP RUCKUS-SZ-* MIBs in
`spec/mib/ruckus/wireless/`.

## Vendored spec

| File | Bytes | SHA-256 |
|------|-------|---------|
| `vsz-7.1.1-v13_1-openapi.json` | 2,567,614 | `9ad9308bbd3f7fd786702575e207f8b3a00f2a6e272aa0654f9d315ced39d7e3` |

- **Format**: Swagger 2.0 (`swagger: "2.0"`)
- **info.title**: "Virtual SmartZone - Essentials 7.1.1"
- **info.version**: `v13_1`
- **basePath**: `/wsg/api/public/v13_1`
- **Size of surface**: 688 paths / 1068 operations / 936 definitions
- **Coverage**: vSZ-E and vSZ-H — same spec; the `/domains` endpoints
  (the vSZ-H differentiator) are present despite the "Essentials" title.

## Provenance

Community capture of the official controller-served spec:

- **Source URL**:
  https://raw.githubusercontent.com/zgilburd/vsz-mcp/HEAD/docs/openapi-spec.json
- **Repo**: https://github.com/zgilburd/vsz-mcp (MIT-licensed repo), file
  `docs/openapi-spec.json`, commit `16f9434` (2026-02-19).
- **How it was captured**: downloaded verbatim by that repo's
  `scripts/download-openapi.ts` from a live controller's
  `/wsg/apiDoc/openapi` endpoint — a raw dump, not hand-edited.
- **Licensing**: the spec content is Ruckus/CommScope's; there is no
  explicit redistribution license for it (the repo's MIT license covers the
  repo's own code). Treat as vendored reference material.

Ruckus publishes no static download anywhere: the docs site renders HTML
from this spec but never serves the JSON, and nothing is in the Wayback
Machine or the official commscope-ruckus GitHub org.

## Refresh

The authoritative refresh path remains fetching the spec from one's own
controller and replacing this file:

```
curl -k https://{host}:8443/wsg/apiDoc/openapi \
  -o vsz-7.1.1-v13_1-openapi.json   # rename to match the captured version
```

Then update the byte count and SHA-256 above.

## Rendered reference (official, public)

- vSZ-H 7.1.0: https://docs.ruckuswireless.com/smartzone/7.1.0/vszh-public-api-reference-guide-710.html
- Other models/versions under https://docs.ruckuswireless.com/smartzone/
- Switch management (ICX via SZ): https://docs.ruckuswireless.com/smartzone/6.1.1/switch-management-public-api-reference-guide-611.html

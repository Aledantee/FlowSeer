# Ubiquiti API Specs — Sources

Ubiquiti devices (UniFi switches/APs in particular) expose only a shallow SNMP
surface (UBNT-UniFi-MIB). The real monitoring path is the official UniFi HTTP
API family documented at https://developer.ui.com — no RESTCONF/NETCONF/gNMI,
no YANG models. We vendor the published OpenAPI specs instead.

| File | API | Version | Status |
|------|-----|---------|--------|
| `unifi-network-openapi-v10.4.57.json` | UniFi Network API (local controller, `/proxy/network/integration/`, X-API-Key auth) | 10.4.57 | Latest published spec as of 2026-08-20 (Network *application* 10.5.67 ships, but 10.4.57 is the newest API spec on developer.ui.com) |
| `unifi-site-manager-openapi-v1.0.0.json` | UniFi Site Manager API (cloud, `https://api.ui.com`, cross-site inventory/ISP metrics) | 1.0.0 | Latest published spec as of 2026-08-20 |

## Upstream

- Authoritative docs: https://developer.ui.com (JS-rendered SPA; specs are
  served per app/version).
- Convenient mirror used for version checks / raw `openapi.json` downloads:
  https://github.com/opastorello/unifi-api-docs (CI-updated daily from
  developer.ui.com; layout `<app>/<version>/openapi.json`, see `catalog.json`
  for the current `latest` per app).
- Getting started / API key setup: https://help.ui.com/hc/en-us/articles/30076656117655-Getting-Started-with-the-Official-UniFi-API

## Refresh

Check `https://raw.githubusercontent.com/opastorello/unifi-api-docs/main/catalog.json`
for a newer `apps.network.latest` / `apps["site-manager"].latest`, then fetch
`https://raw.githubusercontent.com/opastorello/unifi-api-docs/main/<app>/<version>/openapi.json`
and rename to the `unifi-<app>-openapi-v<version>.json` convention here.

Other UniFi APIs exist (Protect, Mobility, InnerSpace, Carrier Fabric) but are
out of scope for switch/AP monitoring.

## Note on EdgeMAX / airMAX

EdgeSwitch and airMAX devices have no public OpenAPI/YANG specs; their web UIs
use undocumented internal APIs. SNMP (see `spec/mib/ubiquiti/`) remains the
supported machine interface there.

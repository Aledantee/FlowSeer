# LANCOM API Specs

## lmc-openapi/ — LANCOM Management Cloud (LMC) REST API

OpenAPI 3.0.3 specs, one per LMC microservice, fetched unauthenticated from the
public per-service endpoints (2026-08-20):

```
https://cloud.lancom.de/cloud-service-<service>/api-docs/index.json
```

Vendored services (all validated as OpenAPI 3.0.3): auth, backstage, config,
control, devices, devicetunnel, dsc, fields, jobs, logging, messaging,
monitoring, notification.

`geolocation` and `preferences` returned HTTP 401 without a session and are
NOT vendored — retrieve via an authenticated LMC session if ever needed.

Usage documentation: LMC-API-Manual in the LANCOM knowledge base
(https://knowledgebase.lancom-systems.de/, space "LMCAPEN"). API calls require
an OAuth2 token from the `auth` service (API keys are created in the LMC UI).

## Device-side protocols

LANCOM devices (LCOS routers/WLCs, LCOS LX APs, LCOS SX switches) do NOT
implement RESTCONF, NETCONF, or gNMI, and publish no YANG models. On-device
management is WEBconfig (HTTPS UI), CLI (SSH/Telnet), SNMPv2c/v3, and the
proprietary LMC tunnel. For FlowSeer, SNMP remains the on-device polling
protocol; the LMC REST API (monitoring/devices services) is the richer
alternative when a site is LMC-managed.

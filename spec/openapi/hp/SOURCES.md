# HP / HPE switch management-protocol specs

Status: no publicly downloadable machine-readable spec files exist for either HP switch family.
This directory therefore documents the assessment only.

## Comware (hh3c, HPE 5xxx/H3C) — NETCONF

- Comware 7 supports NETCONF over SSH (port 830) and SOAP/HTTPS.
- Data models are H3C-proprietary XML, defined by XSD schema files shipped with each
  firmware release; they are NOT public YANG models. Download requires an H3C/HPE
  support login (login-walled).
- Devices support RFC 6022 `get-schema`; the authoritative way to obtain models is to
  pull them from a live device.
- Human-readable references (public, PDF/HTML):
  - H3C NETCONF API Developers Guide: https://www.h3c.com/en/Support/Resource_Center/EN/Home/Switches/00-Public/Developer_Documents/Developer_Guides/NETCONF_API_Developers_Guide-Long/
  - HPE Comware 7 NETCONF XML API Reference: https://support.hpe.com/hpesc/public/docDisplay?docId=a00127309en_us
- Python client with pre-built XML templates: https://github.com/HPENetworking/pyhpecw7

## ProCurve / ArubaOS-Switch (AOS-S 16.x) — REST API

- AOS-S exposes a JSON REST API (v1–v7 depending on 16.x release) at `/rest/`.
- The JSON schemas are documented in PDF guides and served by the switch itself;
  HPE publishes no standalone Swagger/OpenAPI JSON for AOS-S.
- References:
  - Aruba REST API for AOS-S 16.11: https://arubanetworking.hpe.com/techdocs/AOS-Switch/16.11/Aruba%20REST%20API%20for%20AOS-S%2016.11.pdf
  - ArubaOS-Switch REST API and JSON Schema Reference Guide 16.04: https://www.hpe.com/psnow/doc/a00107694en_us
- Note: AOS-CX (the successor platform, vendored under aruba/) DOES have a public
  Swagger/OpenAPI spec served per-device and documented at
  https://developer.arubanetworks.com/aoscx/docs/aos-cx-swagger-ui — out of scope for hp/.

Conclusion: SNMP remains the practical common denominator for classic ProCurve;
Comware devices are best driven via NETCONF with models fetched per-device via
get-schema; AOS-S REST is usable but schema discovery is per-device.

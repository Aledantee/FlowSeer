# Aruba AOS-CX YANG Models — Sources

- Upstream: https://github.com/aruba/aoscx-yang (official HPE Aruba, Apache-2.0)
- Vendored: 2026-08-20, complete `10.17/openconfig/v5_0_0/` set (21 files:
  interfaces, platform, system, PoE, types, IETF third-party imports, plus
  `hpe-anw-cx-openconfig-deviations.yang`).

## Protocol notes

- These OpenConfig-aligned models serve gNMI/model-driven telemetry on AOS-CX.
- The primary config/state interface is the AOS-CX REST API v10.0x (token auth,
  JSON, full OVSDB-style database coverage). Its OpenAPI/Swagger definition is
  generated and served per-switch (Web UI > Settings > "V10.04 API" Swagger UI);
  HPE publishes no standalone downloadable OpenAPI JSON — only per-release
  PDF/HTML guides (e.g. https://arubanetworking.hpe.com/techdocs/AOS-CX/10.15/PDF/rest_v10-0x.pdf)
  and the pyaoscx / aoscxgo clients. Export from a lab switch if an OpenAPI
  file is ever wanted here.

## Refresh

Re-pull https://github.com/aruba/aoscx-yang and copy the newest release
directory in alongside (or replacing) `aoscx-yang/10.17/`.

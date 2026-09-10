---
title: Integration target — <product / API>
date: <YYYY-MM-DD>
scope: <vendor, product line, API generation, versions consulted>
status: research from public documentation; nothing verified against a live system unless stated
---

# <product / API>

Every claim carries a source link. Where documentation is contradictory or version-dependent,
say which version the claim holds for.

## What it manages

Device classes, topologies (cloud-managed, on-prem controller, standalone), scale limits.

## API surface

| Surface | Protocol / transport | Spec available? (OpenAPI/YANG/other, URL) | Versioning scheme |
| --- | --- | --- | --- |

## Authentication and authorisation

- Credential types (API key, OAuth2 flows, session cookie, certificate, RADIUS-backed login).
- Where credentials are created, scopes/roles, expiry and rotation, per-org vs per-user.
- What a least-privilege read-only integration needs; what a write integration needs.

## Data model

- Organisation hierarchy (org/site/network/device), identifiers (serial, MAC, UUID) and which is stable.
- Inventory read: endpoints, fields, pagination.
- Configuration model: per-device vs profile/template, declarative vs imperative, config versioning,
  dry-run/validation, commit/rollback, how partial updates behave (PATCH semantics).
- Read-after-write consistency and propagation delays to devices.

## Telemetry and events

- Polling endpoints (status, clients, interfaces, counters) and their granularity.
- Push: webhooks, streaming, syslog, SNMP traps, NetFlow/IPFIX, alerts. Payload formats and retries.

## Rate limits, quotas, pagination

Numbers with sources. Backoff guidance the vendor gives.

## Device-side protocols still available

SNMP (versions, MIBs), SSH CLI, NETCONF/RESTCONF/gNMI, local REST, and whether the controller
locks them out or leaves them usable in parallel.

## Provisioning and onboarding

How a device joins (claim by serial, adoption, ZTP), factory-reset behaviour, firmware management.

## Known quirks and traps

Anything that will bite an integration: undocumented behaviours, deprecations, breaking changes
between versions, eventual consistency, undocumented required headers.

## What FlowSeer needs

Concrete requirements list: credentials to store, endpoints to call for inventory/config/telemetry,
identifiers to key on, rate-limit budget, and the minimum API version to target.

## Sources

Numbered list of URLs with the date fetched and a one-line note on what each supports.

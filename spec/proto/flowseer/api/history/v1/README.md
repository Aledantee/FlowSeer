# History services

The `flowseer.api.history.v1` package holds `HistoryService`, which an operator
calls to read observation history that central stored from the ingest streams.
`ListSyslogRecords` lists the syslog records of one device or one location, newest
first, in a bounded time window.

`ListSyslogRecords` is the one method whose rule sets `multi_tenant`. The caller
lists up to 25 tenants in `X-FlowSeer-Tenant` and needs `viewer` on each, so a
partner administrator can read a customer's history together with its own. Because
a response can mix tenants, each `SyslogHistoryEntry` names its owner. That tenant
ref is data on the entry, not scoping: the tenants a call may read come from the
header and the authorization check, never from the request body. The
[history store direction](../../../../../../docs/architecture/2026-10-08-history-store-direction.md)
holds the reasoning.

The page token is opaque. A caller passes it back unchanged with every other
request field unchanged.

## Boundaries

Imports: authz, event/log, model/identity, model/inventory, net/log

Imported by: nothing

Deliberately absent:

- Scope by site. Central holds no location tree to resolve a site, so the
  scopes are a device and a location.
- Aggregates over the daily counts, and every history domain other than syslog.
  A domain adds its list call with its record type.
- A tenant field in the request. Tenancy is ambient from the authenticated
  request.

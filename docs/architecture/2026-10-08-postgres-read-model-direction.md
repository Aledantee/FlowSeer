---
title: Postgres Read Model - Direction
type: direction
date: 2026-10-08
topic: postgres-read-model
status: accepted-direction
amends: docs/architecture/2026-10-02-central-ingestion-pipeline-direction.md
---

# Postgres Read Model - Direction

Postgres derives query tables from current state in JetStream KV. It runs
separately from OpenFGA's Postgres deployment, and shared tables enforce
tenant isolation through row-level security.

## Context

The [ingestion direction](2026-10-02-central-ingestion-pipeline-direction.md)
assigns decision state to KV and lists, filters, sorts, and joins to
Postgres. It leaves tenant isolation and deployment separation open.
`deploy/lab/compose.yaml` currently provides Postgres only for OpenFGA.
Separate deployments let authorization and observation queries consume
independent capacity and restart independently.

## Decision

### Postgres has its own deployment and credentials

FlowSeer's read model uses a separate Postgres service and database. The
administrative command receives migration credentials, and the service host
receives projection and query credentials through separate secret files.
Runtime credentials cannot create or drop tables, change
policies, or assume the migration role. The query role has SELECT rights,
and the projection role has the DML rights its projections require.

The first deployment uses the PostgreSQL 17 image already pinned in
`deploy/lab/compose.yaml`, as a separate service. A container upgrade is a
separate dependency decision. Production Kubernetes installation remains
part of the deployment work.

### Tenant context reaches every data transaction

Every projected data table has a non-null tenant key, included in its
primary key and every uniqueness constraint. Query methods take the tenant
from authenticated context and use parameterized predicates. A tenant
supplied in a payload never replaces that context.

The database also enables and forces row-level security. Both USING and
WITH CHECK compare the row's tenant with
`nullif(current_setting('flowseer.tenant_id', true), '')`. A transaction
sets that value with `set_config('flowseer.tenant_id', $1, true)` before any
data access. Missing tenant context matches no row. Each projection
transaction processes one tenant.

Runtime roles are neither superusers nor BYPASSRLS roles. The migration
role owns the tables and is unavailable to normal requests. Row security
does not constrain TRUNCATE or replace resource authorization. Runtime
roles have no TRUNCATE privilege, and the operator authorization checks
still govern which resources a tenant member may read.

Why: PostgreSQL 17 documents default-deny row security, owner and
superuser exceptions, and operations outside row policies in
[Row Security Policies](https://www.postgresql.org/docs/17/ddl-rowsecurity.html).
Its [configuration functions](https://www.postgresql.org/docs/17/functions-admin.html#FUNCTIONS-ADMIN-SET)
document the transaction-local lifetime of `set_config`.

### Projection is replayable and ordered by source revision

A projection follows its KV stream with a durable, explicitly acknowledged
consumer. SQL rows carry the source stream identity and source revision.
A delivery commits its SQL transaction before acknowledging the bus.
An older or repeated revision leaves the committed projection unchanged.
This makes a retry after a lost acknowledgement harmless.

A rebuild starts a fresh projection generation and a fresh replay consumer.
It never reuses a consumer that acknowledged the rows of a dropped table.
Queries report unavailable until the new generation catches up. Rebuild
fences writers from prior generations, including writers on other central
replicas. The source bucket retains current values without age expiry or
silent eviction. A replay reconstructs current state, not historical state.

Why: `src/modules/edgebus/hub.go` uses KV history 1. The pinned NATS client,
`github.com/nats-io/nats.go` v1.54.0, maps History to MaxMsgsPerSubject in
`jetstream/kv.go` and defines durable consumers and explicit acknowledgements
in `jetstream/consumer_config.go`. SQL uses a conditional
[ON CONFLICT update](https://www.postgresql.org/docs/17/sql-insert.html#SQL-ON-CONFLICT)
to apply a revision only when it is newer.

### Query availability is separate from decision state

Postgres may lag KV. Query responses identify the projected observation
and its source revision. A missing or rebuilding projection returns
Unavailable, rather than an empty result that claims there is no state.
Existing mutation decisions and responses continue to read KV.

The host owns store connections. Each projection is a Service Module under
`src/common/service`, with dependencies supplied by its host. Removing the
read model therefore loses query availability, not authoritative state.

## Alternatives

- A database per tenant adds provisioning and migration work for every
  tenant. Shared tables keep one migration set, with database-enforced
  row policies and tenant-scoped keys.
- Sharing OpenFGA's Postgres deployment couples query load and maintenance
  to authorization availability. Separate services permit independent
  capacity and lifecycle.
- Application predicates alone do not catch a missing tenant predicate.
  Row policies add a database check, while resource authorization remains
  an application responsibility.

## Consequences

The lab gains a second Postgres service. Table migrations, roles, and
rebuild procedures belong to the read model. Runtime connections roll
back explicitly on every failed transaction. pgx v5.11.0's
[Pool.BeginTx](https://github.com/jackc/pgx/blob/v5.11.0/pgxpool/pool.go)
requires Commit or Rollback and does not roll back when a context ends.

The implementation amends the ingestion direction's tenant-isolation open
question for Postgres. ClickHouse's isolation question remains separate.

## Sources

- Repository: `deploy/lab/compose.yaml`, `src/modules/edgebus/hub.go`,
  `src/modules/edgebus/subjects.go`, `src/common/service`.
- NATS client v1.54.0: `jetstream/kv.go`, `jetstream/consumer_config.go`
  under `~/go/pkg/mod/github.com/nats-io/nats.go@v1.54.0`.
- PostgreSQL 17: the linked row-security, configuration, and INSERT
  documentation.
- pgx v5.11.0: the linked transaction source.

# Research index

Research preserves evidence and recommendations that may inform a decision. It
is not binding: accepted architecture records, conventions, and current source
govern repository work. Check each research document's date and status before
relying on repository-state claims.

| Research | Use it for | Authority |
| --- | --- | --- |
| [Network domain atlas](network-domain-atlas/INDEX.md) | Vendor and standards evidence, entity coverage, package gaps, and questions to settle before extending the network model. | Supporting research. It does not choose schema shapes or package boundaries. |
| [Logging, metrics, and tracing](2026-09-04-observability-signal-conventions.md) | Evidence behind signal selection, semantic conventions, privacy, and cardinality decisions. | Supporting research. [`docs/conventions/observability.md`](../conventions/observability.md) owns repository policy. |
| [Schema building blocks dossiers](schema-building-blocks/README.md) | Per-domain evidence (802.11, RF, endpoints, platform, L2, L3, QoS/security/WAN, units) behind the schema building blocks record: standards clauses, provider field matrices, proposed shapes, traps. | Supporting research. [The schema building blocks record](../architecture/2026-09-25-schema-building-blocks-direction.md) decides, and where a dossier's proposal differs from it, the record wins. |
| [Device inventory research corpus](device-inventory/README.md) | Live lab captures and vendor integration-target dossiers: credential types, config models, telemetry modes, and discovery signals FlowSeer's device layer needs before writing a protocol adapter. | Supporting research. It makes no schema, Go type, or service contract changes on its own. |
| [OpenFGA authorization spike](2026-09-30-openfga-authorization-spike.md) | Check latency, cache staleness across replicas, ListObjects limits, Tag-change previews, and token-gated membership measured on OpenFGA v1.21.0. | Supporting research. [The operator authorization record](../architecture/2026-09-30-operator-authorization-direction.md) decides. |
| [SpiceDB authorization spike](2026-09-30-spicedb-authorization-spike.md) | SpiceDB v1.56.2 union schema translation, repeated gRPC latency, revocation with grant controls, disposable-rebuild preview, and membership in resource permissions measured beside OpenFGA v1.21.0. | Supporting research. [The operator authorization record](../architecture/2026-09-30-operator-authorization-direction.md) decides. |
| [Herdr as the worker runtime](herdr-trial-2026-09-10.md) | The orchestration failures of Orca's worker dispatch as of 2026-09-10, and which of them Herdr 0.9.0 closed. | Historical: Herdr was removed on 2026-09-19 and Orca is the only worker runtime. The failure list still explains the shape of `delegate/scripts/orca-worker.sh`. |

Add a row here when adding a top-level research document or corpus. Link to an
existing accepted record when the research has already produced a decision, so
an agent does not mistake the recommendation for current policy.

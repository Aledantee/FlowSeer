# Research index

Research preserves evidence and recommendations that may inform a decision. It
is not binding: accepted architecture records, conventions, and current source
govern repository work. Check each research document's date and status before
relying on repository-state claims.

| Research | Use it for | Authority |
| --- | --- | --- |
| [Network domain atlas](network-domain-atlas/INDEX.md) | Vendor and standards evidence, entity coverage, package gaps, and questions to settle before extending the network model. | Supporting research; it does not choose schema shapes or package boundaries. |
| [Logging, metrics, and tracing](2026-09-04-observability-signal-conventions.md) | Evidence behind signal selection, semantic conventions, privacy, and cardinality decisions. | Supporting research; [`docs/conventions/observability.md`](../conventions/observability.md) owns repository policy. |
| [Device inventory research corpus](device-inventory/README.md) | Live lab captures and vendor integration-target dossiers: credential types, config models, telemetry modes, and discovery signals FlowSeer's device layer needs before writing a protocol adapter. | Supporting research; no schema, Go type, or service contract changes on its own. |
| [Herdr as the worker runtime](herdr-trial-2026-09-10.md) | What went wrong in the Orca-dispatched sessions of 2026-09-05 to 2026-09-10, and a four-lane trial of Herdr 0.9.0 against those failures. | Supporting research; the `delegate` skill owns the dispatch procedure. |

Add a row here when adding a top-level research document or corpus. Link to an
existing accepted record when the research has already produced a decision, so
an agent does not mistake the recommendation for current policy.

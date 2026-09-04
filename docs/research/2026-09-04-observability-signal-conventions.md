---
title: Logging, metrics, and tracing research
date: 2026-09-04
scope: OpenTelemetry logs, events, metrics, traces, semantic conventions, and FlowSeer repository gaps
status: research; docs/conventions/observability.md owns repository policy
---

# Logging, metrics, and tracing research

Research date: 2026-09-04

## Executive summary

FlowSeer should use OpenTelemetry as the source data model and OTLP as the
export format. Local logs should be newline-delimited JSON. W3C Trace Context
should carry trace identity across process and durable-message boundaries. A
Prometheus backend may translate OpenTelemetry metric names to its underscore,
unit-suffixed form; application code should keep OpenTelemetry names and units.

The three signals answer different questions:

| Signal | Use it to answer | Do not use it for |
| --- | --- | --- |
| Metric | Is the system healthy, how often does this happen, and is it getting worse? | Per-request detail or unbounded identifiers |
| Trace | Where did one bounded operation spend time or fail across components? | Process-lifetime state, every function call, or point occurrences |
| Log or event | What discrete thing happened, with diagnostic detail that must be searchable independently? | Reliable counting, latency percentiles, or a duplicate of every span |

The main rules are:

1. Start from an operational question or SLO. A signal without a known query,
   dashboard, alert, or debugging use is cost without evidence.
2. Keep names and dimensions stable. Dynamic values belong in attributes,
   never in log templates, event names, span names, or metric names.
3. Use the latest compatible OpenTelemetry semantic conventions where they
   exist. Every non-standard application attribute must use the `flowseer.*`
   namespace; do not invent names under namespaces owned by OpenTelemetry such
   as `service.*`, `messaging.*`, or `otel.*`.
4. Put stable emitter identity on the OpenTelemetry Resource, package identity
   on the Instrumentation Scope, and occurrence-specific facts on the record,
   span, or metric point.
5. Keep metric attributes bounded. Logs and traces may carry a carefully
   allowlisted identifier when it is needed for diagnosis, but credentials,
   payloads, raw carriers, tokens, and unrestricted headers must never be
   recorded.
6. Record an error once at the boundary that handles or returns it. Retried and
   recovered errors do not make the enclosing operation fail.

Confidence is high for the data model, signal selection, metric types, trace
structure, and privacy guidance. The proposed English log-message grammar is a
FlowSeer house convention inferred from the OpenTelemetry record model and Go
`slog`; neither standard specifies sentence grammar. OpenTelemetry Events and
parts of the metric naming guidance remain marked Development in semantic
conventions 1.44.0, so an event schema should be versioned.

### Semantic-convention version baseline

The latest published OpenTelemetry Semantic Conventions release is 1.44.0,
released on 2026-08-04. This report uses 1.44.0 as its normative research
baseline.

FlowSeer currently depends on OpenTelemetry Go v1.46.0. That module contains
generated `semconv` packages through v1.43.0, while the repository imports
`semconv/v1.37.0`. The latest-compatible policy is therefore:

1. Use generated constants and helpers from `semconv/v1.43.0` now.
2. Set `semconv.SchemaURL` on the Resource and every Tracer, Meter, and Logger
   instrumentation scope that emits standard attributes.
3. Follow compatible 1.44.0 prose where no generated symbol is required.
4. Do not hand-copy a 1.44.0 attribute merely to claim the newer version. Check
   its stability, type, requirement level, and value vocabulary first.
5. Advance the generated package and schema URL together when OpenTelemetry Go
   exposes 1.44.0. Review the release notes because Development conventions may
   contain breaking changes.

"Latest" should mean a reviewed, pinned schema version. Telemetry from one
scope must not claim schema 1.44.0 while emitting values defined by an older or
locally altered schema.

## Common telemetry model

### Resource, scope, and occurrence attributes

Use the same model for all three signals:

- Resource attributes describe the immutable producer: `service.name`,
  `service.namespace`, `service.version`, `service.instance.id`, deployment
  environment, host, process, and runtime when available. A resource is sent
  once per batch and should be the source of service identity.
- Instrumentation Scope identifies the package or library that emitted the
  signal. FlowSeer's Go import path plus an instrumentation version is a good
  scope. It should carry the matching semantic-convention schema URL and should
  not be copied into custom attributes.
- Record attributes describe one log occurrence, span, span event, link, or
  metric point. Examples are the operation, outcome, protocol version, retry
  count, and a classified `error.type`.
- Trace and span IDs are native top-level log-record fields in OTLP. They may be
  rendered as `trace_id` and `span_id` in local JSON for navigation, but should
  not be modeled as ordinary OTLP attributes.

An attribute is a typed key-value fact attached to one telemetry entity. Its
purpose is filtering, grouping, aggregation, correlation, routing, or explaining
the occurrence. It is not a place to move an unstructured message wholesale.
OpenTelemetry supports strings, booleans, integers, floating-point values,
bytes, arrays, and structured values where the signal permits them.

Attribute rules:

- Reuse a stable OpenTelemetry attribute exactly, including its type and value
  vocabulary.
- Every custom key must be namespaced. Use lowercase dot-separated namespaces
  and snake case inside a multi-word component:
  `flowseer.module.restart_count`. Bare keys such as `action`, `outcome`,
  `count`, `category`, or `message_type` are not permitted.
- `otel.event.name` is not a custom key. It is a stable OpenTelemetry attribute
  intended to carry Event Name through logging libraries that lack a native
  Event Name field.
- Prefer typed values over formatted strings. Record `retry_count=2`, not
  `retry_count="two"`.
- Define units and allowed values in an attribute catalog. Log and trace
  attributes do not carry independent unit metadata.
- Use a low-cardinality classification such as `error.type=timeout`; keep the
  human error text out of metric attributes and usually out of span attributes.
- Map unexpected values to a documented value such as `_OTHER` rather than
  allowing an unbounded fallback.
- Apply count and value-length limits. Limits are a last line of defense, not a
  substitute for an attribute allowlist.

### Stable names are an operational API

Metric names, event names, span names, attribute keys, value enums, units, and
histogram boundaries are consumed by queries and dashboards. Treat changes to
them like an API migration. Add the new schema, migrate consumers, and retire
the old schema after a stated compatibility period.

## Logging conventions

### What a log record contains

The stable OpenTelemetry Logs data model separates timestamp, observed
timestamp, severity, body, Resource, Instrumentation Scope, trace context,
attributes, and an optional Event Name. The body can be a human-readable
message. An Event is a log record with a stable Event Name that identifies its
schema.

Go's `slog` maps naturally to most of this model: record time becomes the
timestamp, level becomes severity, message becomes body, and `slog.Attr` values
become attributes. The OpenTelemetry Go Logs SDK takes `trace_id`, `span_id`,
and trace flags from the `context.Context` passed to `Emit`; it also supplies
ObservedTimestamp when the bridge did not. Those are native record fields, not
attributes.

The repository's current `otelslog` bridge does not set Event Name. The current
Go Logs SDK documents a processor that maps the stable `otel.event.name`
attribute to the native Event Name field for logging libraries with this
limitation. FlowSeer should either add that processor or use the Logs API
directly for named events. Until it does, `slog` calls remain ordinary log
records even when their message body is stable.

### Message formulation

This is the recommended FlowSeer house style:

- Write a short, stable, lowercase event or outcome phrase:
  `module restart scheduled`, `configuration reload failed`, `device poll
  completed`.
- Describe what happened. Do not write an instruction to the operator and do
  not repeat the level: `ERROR error while polling` carries no useful event.
- Keep dynamic values out of the message. Put the device, module, count,
  duration, and safe error classification into attributes.
- Do not end a one-line message with punctuation. Preserve full prose only when
  ingesting an external record whose original wording matters.
- Avoid implementation narration such as `entered function` or `calling
  helper`. Use a span if a timed operation matters, or omit the signal.
- Use the same message for every occurrence of the same event shape. Different
  outcomes may use different stable messages if operators need to query them
  separately.
- Pass the active `context.Context` to the logger so the handler can attach
  trace correlation.

Good:

```go
logger.WarnContext(ctx, "module restart scheduled",
	slog.String("flowseer.module.path", modulePath),
	slog.String("error.type", classifyError(err)),
	slog.Int("flowseer.module.restart_attempt", attempt),
	slog.Int64("flowseer.module.restart_delay_ms", delay.Milliseconds()),
)
```

Bad:

```go
logger.Error("ERROR: module " + modulePath + " failed: " + err.Error())
```

The good record has one stable message, typed query fields, and a bounded error
classification. Export an error message or structured error only through an
explicit allowlist or transformation because the local error object may contain
private detail.

### Severity

Severity expresses expected operational impact, not the exception type:

| Level | FlowSeer use |
| --- | --- |
| DEBUG | Detailed diagnosis, protocol decisions, and normal high-volume events. Disabled by default in production. |
| INFO | Low-volume successful lifecycle or operator-relevant state changes: process ready, module started, configuration applied, shutdown complete. |
| WARN | Unexpected or degraded behavior that was handled: a retry will occur, data was discarded by explicit policy, or a fallback was used. Rate-limit repetitive warnings. |
| ERROR | The current owned operation failed and was abandoned or returned as failure. Log once at the handling boundary. |
| FATAL | The process is about to terminate. Only the process entry point may make this decision; libraries return errors. Go `slog` has no built-in fatal level. |

A transient attempt that will be retried is normally DEBUG or WARN. The final
failed operation is ERROR. Expected cancellation during shutdown and expected
negative results such as a not-found existence check are not automatically
errors.

### Attributes on logs

Add an attribute when it is useful to filter, group, route, correlate, or
understand the event. Typical log attributes are:

- stable operation and outcome names;
- bounded module path, protocol, version, direction, retry number, and counts;
- `error.type`, standard `exception.*` fields for an exception event, or a
  namespaced and allowlisted local error representation;
- identifiers needed to find the affected object, if policy permits them;
- trace correlation supplied from the active context.

Do not repeat timestamp, level, message, service Resource fields, or trace IDs
as custom OTLP attributes. Local JSON may deliberately flatten Resource and
trace identity because a standalone line has no enclosing OTLP batch.

Never log credentials, authentication headers, private keys, access or session
tokens, raw request or message bodies, raw trace carriers, or unrestricted
configuration. Treat device addresses, usernames, topology names, file paths,
and error strings as potentially sensitive. Sanitize CR, LF, and output-format
delimiters when externally supplied text is rendered into a text sink.

### OpenTelemetry Logs findings

The Logs data model and Logs SDK specification are Stable. OpenTelemetry Go
reports its Logs implementation as Release Candidate, while traces and metrics
are Stable. Keeping the Logs API and SDK private to `src/common/service` remains
the right compatibility boundary.

For ordinary application diagnostics, continue using `slog` with the
OpenTelemetry bridge. It preserves the application logging API and converts:

| `slog` field | OpenTelemetry LogRecord field |
| --- | --- |
| record time | Timestamp |
| message | Body |
| level | SeverityNumber and SeverityText |
| attributes and groups | Attributes |
| active span in the supplied context | TraceId, SpanId, and TraceFlags, added by the Logs SDK |
| no bridge value | EventName; requires a processor or direct Logs API |

Use named OpenTelemetry Events for stable machine-consumed occurrences such as
lifecycle transitions or exceptions. Use ordinary log records for free-form
diagnostics that are primarily read by people. An Event Name identifies the
event schema and must be low-cardinality, fully qualified, and free of dynamic
values. Its Body is optional display text; query logic should use Event Name
and documented attributes.

For `slog`, a practical event path is:

```go
logger.InfoContext(ctx, "module restart scheduled",
	slog.String("otel.event.name", "flowseer.module.restart.scheduled"),
	slog.String("flowseer.module.path", modulePath),
	slog.Int("flowseer.module.restart.attempt", attempt),
)
```

This is an Event only after a Logs SDK processor copies `otel.event.name` into
the native EventName field. The stable bridge attribute may remain for
compatibility, but EventName is the canonical field.

OpenTelemetry's current exception convention prefers exceptions as log events;
the older exception-on-span convention is deprecated. A compliant exception
event uses an operation-specific name ending in `.exception`, associates the
active span context, and records `exception.type`, `exception.message`, and
`exception.stacktrace` according to their requirement levels. The message and
stack trace may contain sensitive data. FlowSeer should allowlist or redact
them before OTLP export.

`slog.Any("flowseer.error", err)` does not automatically become an
OpenTelemetry exception event. The bridge converts the value to a supported
structured value or string. FlowSeer needs an explicit error-to-exception
adapter if exported exception events are required. Record the exception once;
the associated span still receives Error status and low-cardinality
`error.type`.

### Volume and duplication

- Do not log every successful request or durable message at INFO. Metrics carry
  volume and error rate; sampled traces carry per-operation timing. Use DEBUG
  for opt-in detail or a named event only when the record is an audit/domain
  requirement.
- Do not log the same error at every layer. Wrap and return it until a boundary
  owns the outcome. That boundary records one log; lower layers enrich the
  error.
- Do not emit a log merely because a span starts or ends. The trace already has
  those timestamps and attributes.

## Tracing conventions

### When to create a span

Create a span for a bounded operation whose latency, failure, or causal
relationship is independently useful:

- an inbound HTTP, RPC, protocol, or message-handling operation;
- an outbound network, database, device, or RPC call;
- publish and consume operations across a durable-message boundary;
- a job, reconciliation pass, discovery cycle, module attempt, startup, or
  shutdown stage with a definite beginning and end;
- a substantial internal stage that explains meaningful latency or failure and
  is not already represented by instrumentation below it.

Do not create a span for a point-in-time occurrence, a trivial local function,
serialization/deserialization, every loop iteration, a getter, or an operation
already covered by equivalent lower-layer instrumentation. Use a span event for
a meaningful checkpoint inside an existing span. Use a log when the occurrence
must be queried or retained independently of sampled traces.

Every span must end on every path. A process-lifetime root span is a poor model:
it grows without bound and usually never exports until shutdown. Finite startup,
module-attempt, operation, and shutdown spans are the right shape.

### Parentage, kinds, links, and propagation

Use parent-child relationships for causal, nested work. Use links when work has
multiple causes, is batched, or begins a new trace after a durable asynchronous
boundary. A batch consumer span can link to each producer context instead of
choosing a false single parent.

Set Span Kind deliberately:

| Kind | Meaning |
| --- | --- |
| SERVER | Handles a synchronous request received from another process. |
| CLIENT | Makes a synchronous request to a remote service or device. |
| PRODUCER | Publishes work for deferred processing. |
| CONSUMER | Receives or processes deferred work. |
| INTERNAL | Bounded in-process work with none of the relationships above. |

Extract context before starting inbound spans and inject the resulting context
before outbound calls or publication. Continue W3C `traceparent` and
`tracestate` through components that do not record spans. Baggage is not a
general metadata channel: it crosses boundaries and must not contain secrets,
credentials, or personal data.

### Span names and attributes

- Use a low-cardinality operation class such as `snmp.get`,
  `flowseer.module.attempt`, or `GET /devices/{id}`.
- Never put a device ID, request ID, message ID, raw URL, or error text in the
  span name.
- Put attributes known at start time in `Tracer.Start`; head samplers cannot see
  attributes added later.
- Reuse exact protocol conventions for HTTP, RPC, database, and messaging
  operations. Use `flowseer.*` for FlowSeer-only concepts.
- Add result attributes at completion. Keep values typed and bounded.

For errors:

- Leave span status unset on success. Do not set an explicit OK status.
- On final failure, set status to Error and add a predictable, low-cardinality
  `error.type`.
- A recovered or retried error does not fail the enclosing operation.
- Add a status description only when it supplies safe information beyond the
  classification. Do not repeat the status code or `error.type`.
- Choose one canonical exception record. Avoid recording the same exception as
  both a log and a span exception event. The span still carries Error status and
  `error.type`.

### Sampling

Always-sample is suitable for tests, low-volume development, and a measured
initial rollout. It is not a production capacity policy by itself.

For sustained production traffic:

1. Begin with deterministic parent-based probability sampling so a trace is
   kept or dropped consistently across services.
2. If errors and latency outliers must always be retained, add Collector tail
   sampling based on final trace status, total latency, and selected bounded
   attributes.
3. Keep a small unbiased sample of successful traces so latency distributions
   and normal behavior remain visible.
4. Measure Collector memory, decision wait, dropped spans, exporter failures,
   and sampling rates. Tail sampling buffers traces and therefore has a real
   capacity cost.

## Metrics conventions

### Start with service questions

For online operations, cover Google's four golden signals:

- traffic: completed operations or messages;
- errors: the same operation population classified with `error.type`;
- latency: a histogram, with successful and failed operations distinguishable;
- saturation: in-flight work, queue depth/capacity, worker utilization, buffer
  occupancy, or another resource with a known limit.

For FlowSeer's offline and streaming stages, also measure input, output,
in-progress work, queue age, last progress time, last successful cycle, batch
size, cycle duration, and discarded or retried items. For finite batch jobs,
last-success time and overall duration are more useful than an `up` gauge.

Every failure count needs a matching total population. Prefer one operation
duration histogram containing successes and failures, with `error.type` absent
on success, over unrelated total and error counters. Its count supplies the
denominator and its buckets supply latency. A separate error counter is useful
only when it counts a different event or materially simplifies an established
alert.

### Instrument selection

| OpenTelemetry instrument | Use |
| --- | --- |
| Counter | Non-negative deltas for monotonic totals: completed operations, bytes sent, retries, discarded messages. |
| UpDownCounter | Additive values that rise and fall: active requests, queued work maintained through start/end deltas. Use identical attributes for increment and decrement. |
| Histogram | A population whose distribution matters: request duration, payload size, batch size, queue wait. |
| Observable Counter | An absolute monotonically increasing value read from another component. |
| Observable UpDownCounter | An absolute additive current value, such as memory used across processes. |
| Observable Gauge | A current non-additive sample, such as temperature or a last-success Unix timestamp. |

Prefer histograms to client-calculated summaries because histograms aggregate
across instances and allow the query percentile and time window to change.
Choose bucket boundaries around SLO thresholds and actual domain ranges. Use
exponential/native histograms only after confirming that every exporter,
Collector processor, and backend preserves them.

Record counts, not rates. Exporters and backends calculate rates over the
desired query window. Record durations in seconds and byte quantities in
bytes.

### Metric names, descriptions, and units

OpenTelemetry source instruments should use:

- lowercase dot-separated names;
- a `flowseer.` prefix for application-specific metrics;
- no `_total` suffix;
- no unit suffix when the unit is supplied as metadata;
- UCUM units: `s` for duration, `By` for bytes, `1` for ratios, and singular
  annotated units such as `{request}`, `{message}`, or `{attempt}` for counts;
- a description that states exactly what is measured and where the counting or
  timing boundary lies.

Example:

```go
attempts, err := meter.Int64Counter(
	"flowseer.service.module.attempts",
	metric.WithUnit("{attempt}"),
	metric.WithDescription("Completed module attempts"),
)
if err != nil {
	return err
}

duration, err := meter.Float64Histogram(
	"flowseer.snmp.client.request.duration",
	metric.WithUnit("s"),
	metric.WithDescription("Elapsed time from SNMP request send to matching response"),
)
if err != nil {
	return err
}
```

An OTLP-to-Prometheus translator using its conventional strategy will render
names such as `flowseer_service_module_attempts_total` and
`flowseer_snmp_client_request_duration_seconds`. Do not pre-encode those
Prometheus suffixes in the OpenTelemetry source name. If FlowSeer ever exposes
native Prometheus instrumentation instead of translating OTLP, follow
Prometheus naming directly: base units in names and `_total` on counters.

### Metric attributes and cardinality

Metric attributes are time-series identity. Every unique combination creates a
new stream, and a classic histogram multiplies that cost by its buckets plus
sum and count.

Usually safe when bounded and useful:

- operation from a documented enum;
- final outcome from a small enum;
- `error.type` from a documented classification;
- protocol version, direction, message kind, or queue name from a fixed set;
- module path if it comes only from the static module tree.

Usually unsafe:

- trace ID, span ID, request ID, message ID, disposition ID, or session ID;
- device address, hostname, interface, OID, URL, or arbitrary path when the set
  can grow with inventory or input;
- exception message, raw status text, payload values, timestamps, or stack
  traces;
- service instance ID as a measurement attribute. Keep it on the Resource and
  let the backend decide how to materialize it.

Before adding an attribute, write the expected value count and multiply all
dimensions. Review the estimate against the SDK limit, the number of service
instances, and histogram expansion. A 2,000-cardinality SDK cap protects memory
after a mistake; reaching the cap means data is already being aggregated into
an overflow series and should produce a diagnostic.

Pass the active context to synchronous metric recordings. It lets sampled
measurements carry exemplars with trace and span IDs without turning trace IDs
into metric attributes.

### Minimum FlowSeer metric families

Exact names should be settled as a versioned telemetry schema, but each area
needs these measurements:

| Area | Measurements |
| --- | --- |
| Service runtime | Completed module attempts and duration by final outcome; restarts by reason; active modules; startup and shutdown duration. |
| Durable messages | Published, consumed, retried, rejected, and discarded operations with clear counting points; processing and queue-wait duration; in-flight and queue depth/capacity. Avoid a single counter if summing its lifecycle actions would double-count one message without a useful interpretation. |
| Device protocol client | Logical operation duration with `error.type`; wire request attempts; retransmits; in-flight work; bytes sent/received. Use CLIENT spans for the matching operations. |
| Discovery and reconciliation | Cycle duration and completion; objects examined/changed/rejected; current backlog; oldest-item age or last-progress time; last successful cycle timestamp. |
| Telemetry pipeline | Records accepted, exported, and dropped by signal and low-cardinality reason; exporter failures; queue size/capacity; export duration. Prefer standardized `otel.sdk.*` metrics when the Go SDK supports them, and monitor Collector internal telemetry too. |
| Process and Go runtime | Use standard process and Go runtime instrumentation instead of custom FlowSeer copies. |

## Formats and transport

- Export logs, metrics, and traces from services as OTLP protobuf over gRPC or
  HTTP to an OpenTelemetry Collector. Keep transport choice operational; signal
  semantics must not depend on it.
- Emit local logs as one JSON object per line. Use UTC timestamps, stable field
  names, typed values, local Resource identity, and `trace_id`/`span_id` when an
  active context exists. JSON lines survive concurrent processes and collector
  ingestion better than prose prefixes.
- Propagate traces with W3C `traceparent` and `tracestate`. Define explicit trust
  boundaries for accepting, restarting, or forwarding external context.
- Let the Collector batch, retry, filter, redact, route, and translate. Collector
  failure must not fail application work after valid startup configuration.
- Keep application-side queues and retries bounded, flush on shutdown within a
  deadline, and expose drop/export diagnostics through a path that does not
  recursively depend on the failing exporter.

## Repository assessment

The current `src/common/service` direction already gets several hard parts
right:

- telemetry is run-scoped and does not mutate process globals;
- logs receive active context and local trace correlation;
- W3C context survives trace-disabled module views;
- service identity is a Resource and package import paths are instrumentation
  scopes;
- exporters are batched and bounded, attribute and span limits are explicit,
  and collector outages do not become service failures;
- finite startup, module-attempt, and shutdown spans replace a process-lifetime
  span.

The next telemetry-schema pass should address these items before dashboards
make the current names expensive to change:

1. Move both imports from `semconv/v1.37.0` to the latest generated package
   available in the pinned OpenTelemetry Go module, currently v1.43.0. Replace
   `resource.NewSchemaless` with a resource carrying `semconv.SchemaURL`, and
   pass the same schema URL when creating the runtime Tracer, Meter, and
   `otelslog` Handler.
2. Replace custom keys in OpenTelemetry-owned namespaces, such as
   `service.module.path` and locally invented `messaging.*`, with exact semantic
   conventions or `flowseer.*` keys.
3. Namespace every other custom field. This includes underscore-only protocol
   keys such as `snmp_target`, `gnmi_operation`, and `netconf_operation`, plus
   bare log keys such as `action`, `outcome`, `message_type`, `message_kind`,
   `signal`, `category`, `count`, and `suppressed`.
4. Add an explicit Event Name adapter if lifecycle and exception records are to
   be OpenTelemetry Events. With the current `slog` bridge, a stable message is
   still an ordinary LogRecord. The documented Go SDK approach maps the stable
   `otel.event.name` bridge attribute in a log processor.
5. Define an exported-error adapter. The current rich `slog.LogValuer` and
   `slog.Any` values do not automatically become `exception.*` fields, and the
   export sanitizer may stringify them. Keep the rich local form separate from
   the allowlisted OTLP exception form.
6. Add UCUM units to count instruments. The current lifecycle, message, SNMP
   request, error, retry, and drop counters do not declare annotated units.
7. Review separate error counters against an operation histogram carrying
   `error.type`. In the SNMP metrics, request PDUs and logical operation errors
   currently have different populations, so their ratio is not a valid logical
   operation error rate.
8. Set SNMP operation spans to CLIENT. The RESTCONF, gNMI, and NETCONF clients
   already use CLIENT spans.
9. Add low-cardinality `error.type` consistently and review raw error strings in
   span status descriptions for privacy. Successful spans should leave status
   unset.
10. Make production sampling configurable or place tail sampling in the
   Collector. `ParentBased(AlwaysSample())` is a useful correctness default but
   has unbounded production cost as traffic grows.
11. Add telemetry self-metrics for queue overflow, accepted/exported/dropped
   records, export failure, and queue occupancy. The current rate-limited local
   warning is necessary but cannot answer how much data was lost.
12. Reconsider INFO-per-message disposition records. At sustained ingestion
   volume they duplicate metric and trace information and can dominate log
   cost. Keep them only for an explicit audit requirement; otherwise use DEBUG
   or a sampled named event.
13. Keep the trusted local rich error form separate from the OTLP allowlist.
    A denylist based on secret-looking key names cannot recognize secrets stored
    under innocent names.

These are research recommendations, not implementation decisions. Telemetry
schema migration and sampling policy need an explicit design step because they
affect operator queries, storage cost, and compatibility.

## Proposed review checklist

For every new signal, a reviewer should be able to answer:

- What operator question, SLO, alert, or debugging workflow uses it?
- Is this a metric, span, event/log, attribute, or no telemetry at all?
- Is there an exact OpenTelemetry semantic convention in the latest published
  version? Which pinned generated package and schema URL identify it?
- Is every non-standard key under `flowseer.*`, with no bare custom keys and no
  reuse of an OpenTelemetry-owned namespace?
- Are the name, type, unit, description, counting point, and allowed values
  documented?
- What is the worst-case cardinality per instance and across the fleet?
- Does it include an identifier, payload, address, header, error string, or
  other sensitive value? What allowlist or transformation makes that safe?
- Does the active context preserve trace correlation and metric exemplars?
- Can the telemetry path fail, block, recurse, or exhaust memory?
- What test proves name, attributes, correlation, disabled-signal behavior,
  and shutdown flush semantics?

## Sources

Primary standards and project documentation:

- [OpenTelemetry semantic conventions v1.44.0 release](https://github.com/open-telemetry/semantic-conventions/releases/tag/v1.44.0)
- [OpenTelemetry Logs Data Model](https://opentelemetry.io/docs/specs/otel/logs/data-model/)
- [OpenTelemetry Logging](https://opentelemetry.io/docs/specs/otel/logs/)
- [OpenTelemetry Logs SDK](https://opentelemetry.io/docs/specs/otel/logs/sdk/)
- [OpenTelemetry `otel.event.name` attribute](https://opentelemetry.io/docs/specs/semconv/registry/attributes/otel/)
- [OpenTelemetry Go signal status](https://opentelemetry.io/docs/languages/go/)
- [OpenTelemetry semantic conventions 1.44.0](https://opentelemetry.io/docs/specs/semconv/)
- [OpenTelemetry naming guidance](https://opentelemetry.io/docs/specs/semconv/general/naming/)
- [OpenTelemetry Events conventions](https://opentelemetry.io/docs/specs/semconv/general/events/)
- [OpenTelemetry exception conventions for logs](https://opentelemetry.io/docs/specs/semconv/exceptions/exceptions-logs/)
- [OpenTelemetry recording errors](https://opentelemetry.io/docs/specs/semconv/general/recording-errors/)
- [OpenTelemetry Trace API](https://opentelemetry.io/docs/specs/otel/trace/api/)
- [OpenTelemetry sampling](https://opentelemetry.io/docs/concepts/sampling/)
- [W3C Trace Context](https://www.w3.org/TR/trace-context/)
- [W3C Baggage](https://www.w3.org/TR/baggage/)
- [OpenTelemetry Metrics API](https://opentelemetry.io/docs/specs/otel/metrics/api/)
- [OpenTelemetry metric semantic conventions](https://opentelemetry.io/docs/specs/semconv/general/metrics/)
- [OpenTelemetry Metrics Data Model](https://opentelemetry.io/docs/specs/otel/metrics/data-model/)
- [OpenTelemetry Prometheus and OpenMetrics compatibility](https://opentelemetry.io/docs/specs/otel/compatibility/prometheus_and_openmetrics/)
- [OpenTelemetry SDK self-metrics](https://opentelemetry.io/docs/specs/semconv/otel/sdk-metrics/)
- [OpenTelemetry sensitive-data guidance](https://opentelemetry.io/docs/security/handling-sensitive-data/)
- [Go structured logging with `slog`](https://go.dev/blog/slog)
- [Prometheus metric and label naming](https://prometheus.io/docs/practices/naming/)
- [Prometheus instrumentation guidance](https://prometheus.io/docs/practices/instrumentation/)
- [Prometheus histograms and summaries](https://prometheus.io/docs/practices/histograms/)
- [Google SRE: Monitoring Distributed Systems](https://sre.google/sre-book/monitoring-distributed-systems/)
- [OWASP Logging Cheat Sheet](https://cheatsheetseries.owasp.org/cheatsheets/Logging_Cheat_Sheet.html)

Repository evidence reviewed:

- `src/common/service/telemetry.go`
- `src/common/service/telemetry_sdk.go`
- `src/common/service/telemetry_policy.go`
- `src/common/service/context.go`
- `src/common/service/README.md`
- `src/common/errs/slog.go`
- `src/protocol/snmp/instrument.go`
- protocol client spans in `src/protocol/restconf`, `src/protocol/gnmi`, and
  `src/protocol/netconf`
- `docs/plans/2026-09-04-1504-feat-service-managed-opentelemetry-plan.md`

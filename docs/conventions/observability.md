---
name: Observability conventions
last_updated: 2026-09-04
latest_semconv_release: 1.44.0
latest_generated_go_semconv: 1.43.0
---

# Observability conventions

These rules apply to every FlowSeer log, OpenTelemetry event, span, and metric.
They are binding on humans and agents. They define signal semantics; package
documentation may explain how to obtain a logger or provider, but must not
define a competing telemetry schema.

Start with an operational question. A signal needs a known debugging query,
dashboard, alert, SLO, or audit use. If none exists, do not emit it.

Use each signal for the question it answers:

| Signal | Use it for | Do not use it for |
| --- | --- | --- |
| Metric | Rates, totals, distributions, saturation, and alerts across many operations. | Per-operation detail or unbounded identifiers. |
| Trace | The causal path, latency, and final result of one bounded operation. | Process-lifetime state, every function call, or an isolated point occurrence. |
| Log or event | A discrete occurrence with diagnostic detail that must be searchable on its own. | Reliable counting, latency percentiles, or a copy of every span transition. |

## Semantic-convention baseline

At the last review on 2026-09-04, the latest published OpenTelemetry Semantic
Conventions release was 1.44.0. The repository pins OpenTelemetry Go v1.46.0,
whose newest generated semantic-convention package is `semconv/v1.43.0`.

Before adding or changing instrumentation:

1. Check the official Semantic Conventions releases and the generated packages
   in the repository's pinned OpenTelemetry Go module.
2. Use the newest generated Go package available in that pinned module. Do not
   keep an older import because nearby code uses it.
3. Use a newer published convention only when no generated symbol is needed and
   its stability, type, requirement level, and allowed values have been checked.
4. Do not hand-copy a newer attribute just to claim a newer schema version.
5. Advance the generated package and its schema URL together. Development
   conventions require a migration review because they may change incompatibly.

For the current dependency set, import:

```go
semconv "go.opentelemetry.io/otel/semconv/v1.43.0"
```

Set `semconv.SchemaURL` on the Resource and on every Tracer, Meter, and Logger
instrumentation scope that emits semantic-convention attributes. For example:

```go
resource.NewWithAttributes(semconv.SchemaURL, resourceAttributes...)
tracerProvider.Tracer(scopeName, trace.WithSchemaURL(semconv.SchemaURL))
meterProvider.Meter(scopeName, metric.WithSchemaURL(semconv.SchemaURL))
otelslog.WithSchemaURL(semconv.SchemaURL)
```

A scope must not claim schema 1.44.0 while emitting values generated from 1.43.0
or from a locally altered schema.

## Attributes

An attribute is a typed key-value fact attached to a Resource, instrumentation
scope, LogRecord, span, span event, link, or metric point. Attributes exist for
filtering, grouping, aggregation, correlation, routing, or explaining one
occurrence. They do not replace a log message with an unstructured property bag.

Place facts according to their lifetime:

- Resource attributes identify the immutable producer, such as `service.name`,
  `service.namespace`, `service.version`, and `service.instance.id`.
- Instrumentation Scope identifies the emitting package or library by its Go
  import path, version, and semantic-convention schema URL.
- Occurrence attributes describe one record, span, event, link, or metric point.

Follow these naming rules:

- Reuse the latest applicable stable OpenTelemetry attribute exactly, including
  its key, type, requirement level, and value vocabulary.
- Put every non-standard application attribute under `flowseer.*`. Use lowercase
  dot-separated namespaces and snake case within a multi-word component, for
  example `flowseer.module.restart_attempt`.
- Never invent a key under an OpenTelemetry-owned namespace such as `service.*`,
  `messaging.*`, `http.*`, `rpc.*`, `exception.*`, or `otel.*`.
- Standard keys remain exact and are not prefixed. `error.type`,
  `exception.type`, and `otel.event.name` are standard keys, not custom ones.
- Do not create bare custom keys such as `action`, `outcome`, `count`, `signal`,
  or `message_type`.
- Keep values typed. Record an integer as an integer and a duration as a numeric
  value in the signal's declared unit.
- Treat names, keys, value enums, units, and histogram boundaries as an
  operational API. Changing one requires a telemetry-schema migration.

Trace IDs and span IDs are native LogRecord fields in OTLP. They may be flattened
to `trace_id` and `span_id` in local JSON, but must not be copied into custom OTLP
attributes. Service identity belongs on the Resource rather than each record.

## Logs and events

Use Go `slog` for application diagnostics and pass the active context with
`DebugContext`, `InfoContext`, `WarnContext`, or `ErrorContext`. The
OpenTelemetry bridge maps record time, message, level, and attributes into the
Logs data model. The Logs SDK takes trace identity from the passed context and
sets the observed timestamp when needed.

### Write stable messages

A log message is the human-readable body. Write it as a short, stable, lowercase
event or outcome phrase:

- `module restart scheduled`
- `configuration reload failed`
- `device poll completed`

Keep dynamic values out of the message and put them in attributes. Do not add a
level prefix, service name, timestamp, terminal punctuation, or implementation
narration such as `entered function`. Use the same message for every occurrence
of the same event shape.

```go
logger.WarnContext(ctx, "module restart scheduled",
	slog.String("flowseer.module.path", modulePath),
	slog.Int("flowseer.module.restart_attempt", attempt),
	slog.Int64("flowseer.module.restart_delay_ms", delay.Milliseconds()),
	slog.String(string(semconv.ErrorTypeKey), classifyError(err)),
)
```

The duration is an integer with its unit encoded in this log attribute's name.
Metric units are metadata instead, as described below. Do not interpolate these
values into the message or use a raw, potentially sensitive error as a field.

### Choose severity by outcome

| Level | Use |
| --- | --- |
| DEBUG | Detailed diagnosis, protocol decisions, and normal high-volume occurrences. Disabled by default in production. |
| INFO | Low-volume lifecycle or operator-relevant state changes, including readiness, configuration application, and clean shutdown. |
| WARN | Unexpected behavior that was handled, such as a scheduled retry, fallback, or explicit discard. Rate-limit repetitive warnings. |
| ERROR | The owned operation failed and was abandoned or returned as failure. Record it once at the boundary that owns the outcome. |
| FATAL | The process is about to terminate. Only the process entry point decides this; libraries return errors. `slog` has no built-in fatal level. |

An attempt that will be retried is normally DEBUG or WARN. Expected shutdown
cancellation and expected negative results are not automatically errors. Follow
the error rule in [`code-style.md`](../code-style.md): enrich and return an error
until one boundary owns logging it; never log and return the same failure.

### Add useful attributes

Add a log attribute when operators need to filter, group, route, correlate, or
understand the occurrence. Good candidates include a bounded operation, final
outcome, protocol version, retry number, count, safe object identifier, or
classified `error.type`.

Do not repeat timestamp, severity, body, Resource identity, or trace context as
custom attributes. Do not attach unrestricted payloads, request bodies,
configuration, headers, carriers, or error objects. Allowlist and redact exported
fields; a key-name denylist cannot detect a secret stored under an innocent key.

### Distinguish logs from named events

An OpenTelemetry Event is a LogRecord with a stable Event Name that identifies a
documented schema. The name is low-cardinality, fully qualified, and contains no
dynamic values, for example `flowseer.module.restart.scheduled`.

The current `otelslog` bridge does not populate the native EventName field. A
plain stable `slog` message therefore remains an ordinary LogRecord. Use a common
Logs SDK processor that maps the stable `otel.event.name` bridge attribute to
EventName, or use the Logs API directly. Do not build an ad hoc event adapter in
each package.

```go
logger.InfoContext(ctx, "module restart scheduled",
	slog.String("otel.event.name", "flowseer.module.restart.scheduled"),
	slog.String("flowseer.module.path", modulePath),
	slog.Int("flowseer.module.restart_attempt", attempt),
)
```

This is a named Event only when the shared telemetry pipeline performs that
mapping. Query logic should use EventName and documented attributes, not Body.

OpenTelemetry now prefers exceptions as log events; the exception-on-span
convention is deprecated. An exception event uses an operation-specific name
ending in `.exception` and the applicable standard `exception.*` attributes.
Exception messages and stack traces may contain sensitive data, so they require
an explicit allowlist or redaction. `slog.Any(..., err)` does not perform this
mapping automatically.

Do not log every successful request or message at INFO. Metrics carry aggregate
volume and sampled traces carry operation timing. Do not emit a log just because
a span starts or ends.

### Log format and transport

OTLP LogRecord is the canonical exported shape. Emit local logs as one JSON
object per line with UTC timestamps, stable field names, typed values, local
Resource identity, and trace correlation when present. Preserve an external
record's original wording only when the original record is the data being
ingested. Sanitize carriage returns, line feeds, and output delimiters when
externally supplied text reaches a text sink.

## Traces

Create a span for a bounded operation whose latency, failure, or causal
relationship is independently useful. Typical boundaries are:

- an inbound HTTP, RPC, protocol, or message-handling operation;
- an outbound network, database, device, or RPC call;
- publication and consumption across a durable-message boundary;
- a finite job, reconciliation pass, discovery cycle, module attempt, startup,
  or shutdown stage;
- a substantial internal stage that explains meaningful latency or failure and
  is not already covered by lower-level instrumentation.

Do not span a getter, trivial local function, serialization step, every loop
iteration, point occurrence, or operation already represented by equivalent
instrumentation. Add a span event for a meaningful checkpoint inside an existing
span. Avoid a process-lifetime root span because it grows without bound and may
not export until shutdown.

### Name and connect spans

Use a low-cardinality operation class such as `snmp.get`,
`flowseer.module.attempt`, or `GET /devices/{id}`. Never put an object ID,
request ID, raw URL, or error text in a span name.

Set Span Kind deliberately:

| Kind | Meaning |
| --- | --- |
| SERVER | Handle a synchronous request received from another process. |
| CLIENT | Make a synchronous request to a remote service or device. |
| PRODUCER | Publish work for deferred processing. |
| CONSUMER | Receive or process deferred work. |
| INTERNAL | Perform bounded in-process work with none of the relationships above. |

Extract W3C `traceparent` and `tracestate` before starting an inbound span and
inject the resulting context before an outbound request or publication. Use a
parent-child relationship for causal nested work. Use Links when a batch has
many causes or asynchronous work begins a new trace after a durable boundary.
Baggage crosses trust boundaries and must not carry secrets, credentials, or
personal data.

Put attributes known at start time in `Tracer.Start`; a head sampler cannot see
attributes added later. Reuse exact protocol conventions and put FlowSeer-only
facts under `flowseer.*`.

Every span must end on every path. Leave status unset on success. On final
failure, set Error status and add a bounded `error.type`. A recovered or retried
failure does not fail the enclosing operation. Do not record the same exception
as both a log event and a span exception event.

```go
ctx, span := tracer.Start(ctx, "snmp.get",
	trace.WithSpanKind(trace.SpanKindClient),
)
defer span.End()

result, err := client.Get(ctx, oid)
if err != nil {
	span.SetAttributes(semconv.ErrorTypeKey.String(classifyError(err)))
	span.SetStatus(codes.Error, "request failed")
	return Result{}, err
}
return result, nil
```

Use deterministic parent-based probability sampling for sustained production
traffic. Collector tail sampling may retain failures and latency outliers, but
it buffers whole traces and needs explicit capacity monitoring. Keep an unbiased
sample of successful traces and measure sampling, dropped spans, exporter
failures, and Collector memory.

## Metrics

Create metrics for an operational population, not one specific occurrence. For
online operations, cover traffic, failures, latency, and the constrained
resource that can saturate. Streaming and batch stages also need progress and
freshness, such as queue age, last progress time, last successful cycle, and
cycle duration.

Every failure measurement needs a matching total population. Prefer one
operation-duration histogram whose count is the denominator and which carries
`error.type` only for failed operations. Add a separate error counter only when
it counts a different event or materially simplifies an established alert.

### Choose the instrument

| Instrument | Use |
| --- | --- |
| Counter | Non-negative deltas for monotonic totals: completed operations, bytes, retries, or discards. |
| UpDownCounter | Additive values that rise and fall: active requests or maintained queue depth. Use identical attributes for increment and decrement. |
| Histogram | A population whose distribution matters: duration, payload size, batch size, or queue wait. |
| Observable Counter | An absolute monotonically increasing value read from another component. |
| Observable UpDownCounter | An absolute additive current value, such as memory used across components. |
| Observable Gauge | A current non-additive sample, such as temperature or a last-success Unix timestamp. |

Record counts rather than rates. The backend calculates rates over its chosen
window. Prefer histograms to client-computed summaries because histograms
aggregate across instances. Choose boundaries around SLO thresholds and actual
domain ranges. Use exponential or native histograms only after confirming that
every exporter, Collector processor, and backend preserves them.

### Name and describe instruments

OpenTelemetry source instruments use:

- lowercase dot-separated names;
- `flowseer.` for application-specific metrics;
- no `_total` suffix;
- no unit suffix in the name;
- UCUM units such as `s`, `By`, `1`, or singular annotated counts such as
  `{request}`, `{message}`, and `{attempt}`;
- a description that says what is measured and where counting or timing begins
  and ends.

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

An OTLP-to-Prometheus translator may render these as
`flowseer_service_module_attempts_total` and
`flowseer_snmp_client_request_duration_seconds`. Application code must not
pre-encode Prometheus suffixes. Native Prometheus instrumentation follows
Prometheus naming instead.

### Bound metric cardinality

Metric attributes define time-series identity. Before adding one, document its
allowed values, estimate the cross-product, and multiply by service instances
and histogram streams.

Usually safe when fixed and useful: operation, final outcome, classified
`error.type`, protocol version, direction, message kind, queue name, or a module
path drawn from the static module tree.

Usually unsafe: trace or span IDs, request or message IDs, session IDs, device
addresses, hostnames, interfaces, OIDs, raw URLs, arbitrary paths, error text,
timestamps, stack traces, or payload values. Keep service instance identity on
the Resource. Pass the active context to synchronous metric recordings so
sampled points can carry exemplars without turning trace IDs into attributes.

### Required measurement families

New operational components should cover the applicable families:

| Area | Measurements |
| --- | --- |
| Service runtime | Module attempts and duration by final result; restarts by bounded reason; active modules; startup and shutdown duration. |
| Durable messages | Published, consumed, retried, rejected, and discarded operations with explicit counting points; processing and queue-wait duration; in-flight work and queue capacity. |
| Device protocol client | Logical operation duration with `error.type`; wire attempts and retransmits; in-flight work; bytes sent and received. |
| Discovery or reconciliation | Cycle duration and completion; objects examined or changed; backlog; oldest-item age or last-progress time; last successful cycle timestamp. |
| Telemetry pipeline | Accepted, exported, and dropped records by signal and bounded reason; export failures and duration; queue size and capacity. Prefer standard `otel.sdk.*` metrics when supported. |
| Process and Go runtime | Standard process and Go runtime instrumentation rather than FlowSeer copies. |

## Reliability and privacy

Export logs, metrics, and traces as OTLP protobuf over gRPC or HTTP to an
OpenTelemetry Collector. Let the Collector batch, retry, redact, route, and
translate. A Collector outage must not fail application work after valid startup
configuration.

Keep queues and retries bounded, flush on shutdown within a deadline, and expose
accepted, dropped, and export-failure diagnostics through a path that cannot
recursively depend on the failing exporter.

Never record credentials, authentication headers, private keys, access or
session tokens, raw telemetry carriers, or unrestricted request and message
bodies. Treat device addresses, usernames, topology names, file paths, error
messages, and stack traces as potentially sensitive. Prefer allowlists and
transformations to broad object serialization.

## Review checklist

For every new or changed signal, a reviewer must be able to answer:

- Which operator question, SLO, alert, debugging workflow, or audit use needs it?
- Is the chosen signal a metric, span, event or log, attribute, or no telemetry?
- Which latest semantic convention applies, and which generated package and
  schema URL identify the emitted version?
- Is every non-standard key under `flowseer.*`, with no bare key or invented use
  of an OpenTelemetry-owned namespace?
- Are name, type, unit, description, counting point, allowed values, and
  worst-case cardinality documented where applicable?
- Is sensitive data excluded or transformed by an explicit allowlist?
- Does the active context preserve log correlation, trace propagation, and
  metric exemplars?
- Can telemetry failure block application work, recurse, or exhaust memory?
- What test proves the schema, correlation, disabled-signal behavior, and
  shutdown semantics that this change relies on?

The supporting research, repository assessment, and complete primary-source
list are in [Logging, metrics, and tracing research](../research/2026-09-04-observability-signal-conventions.md).

# Service runtime

The `service` package gives a process one identity and a tree of independently
owned runtime modules. A small service can use the implicit singleton form:

```go
err := service.Run(ctx, service.Config{
    Identity: service.Identity{
        Name:      "collector",
        Namespace: "flowseer",
        Version:   buildVersion,
    },
    Setup: func(ctx context.Context) (service.Attempt, error) {
        client, err := openClient(ctx)
        if err != nil {
            return service.Attempt{}, err
        }
        return service.Attempt{Runner: func(ctx context.Context) error {
            defer client.Close()
            return client.Serve(ctx)
        }}, nil
    },
})
```

Setup owns attempt-local construction. A restart calls it again, so mutable
state from a failed attempt cannot leak into its replacement. The returned
runner must cooperate with context cancellation; the runtime waits for it and
will not overlap it with another attempt. The runner owns cleanup for resources
created by setup, including closing `client` before it returns.

Every module gate has a generated environment override. The key combines the
service prefix, the relative module path with slashes replaced by underscores,
and `_ENABLED`. For example, `FLOWSEER_EDGE_INGEST_SYSLOG_ENABLED=false`
disables `edge/ingest/syslog`. An override is evaluated before a fixed or
probed gate, so it can reverse a fixed decision and prevents a probe call.

## Instrumentation

Follow the repository's [observability
conventions](../../../docs/conventions/observability.md) before adding or
changing a log record, named event, span, or metric. That document owns signal
selection, message and attribute formulation, namespacing, cardinality, units,
privacy, and the current semantic-convention version. This section explains how
module code obtains the runtime's instrumentation.

### Managed OTLP export

One common endpoint opts a run into service-owned OTLP logs, metrics, and
traces. With an available endpoint, the zero-valued root signal policy enables
all three managed backings. Without an endpoint or an injected backing, the
corresponding signal is a no-op. Local structured logs remain active in either
case and default to JSON on stderr when `Config.Logger` is nil.

```go
config.Telemetry = service.TelemetryConfig{
    Endpoint: "https://collector.example/flowseer",
    Headers:  map[string]string{"authorization": token},
    Timeout:  5 * time.Second,
}
```

The endpoint is deliberately common. For `http/protobuf`, the runtime appends
`/v1/logs`, `/v1/metrics`, and `/v1/traces` to its base path. `grpc` uses the
endpoint host. Signal-specific `OTEL_EXPORTER_OTLP_{LOGS,METRICS,TRACES}_*`
variables are rejected during preflight because separate destinations would
break the one-owner model.

An explicitly set Go field takes precedence over its common environment
equivalent:

| Go field | Environment variable | Unset behavior |
| --- | --- | --- |
| `Endpoint` | `OTEL_EXPORTER_OTLP_ENDPOINT` | Managed export stays off. |
| `Protocol` | `OTEL_EXPORTER_OTLP_PROTOCOL` | `http/protobuf` |
| `Compression` | `OTEL_EXPORTER_OTLP_COMPRESSION` | No compression; `gzip` is the supported value. |
| `Headers` | `OTEL_EXPORTER_OTLP_HEADERS` | No headers. An empty non-nil map clears the environment value. |
| `Timeout` | `OTEL_EXPORTER_OTLP_TIMEOUT` | Five seconds per request. The environment value is milliseconds. |
| `Insecure` | `OTEL_EXPORTER_OTLP_INSECURE` | Derived from the endpoint scheme. |
| `CertificateFile` | `OTEL_EXPORTER_OTLP_CERTIFICATE` | Use system roots. |
| `ClientCertificateFile` and `ClientKeyFile` | `OTEL_EXPORTER_OTLP_CLIENT_CERTIFICATE` and `OTEL_EXPORTER_OTLP_CLIENT_KEY` | No client certificate. Both values must be present together. |

The request timeout must be positive and no greater than 30 seconds. Header
environment values use comma-separated, URL-escaped `key=value` members. An
`https` endpoint uses TLS 1.2 or newer. A custom CA and client certificate may
be supplied for TLS; setting insecure transport together with TLS files is an
error. Configuration is validated before exporters or module work start.
The identity in `Config.Identity` is authoritative: a conflicting
`OTEL_SERVICE_NAME` is rejected, and arbitrary `OTEL_RESOURCE_ATTRIBUTES` are
not imported into the bounded managed Resource.

`TelemetryPolicy` is tri-state per signal. `TelemetryInherit` is the zero value,
`TelemetryEnabled` requires an available managed or injected backing, and
`TelemetryDisabled` suppresses export. Children inherit their parent's
effective value unless they override it. The root and every module can also be
overridden by strict `true` or `false` environment values:

```text
FLOWSEER_EDGE_LOGS_ENABLED=false
FLOWSEER_EDGE_INGEST_LOGS_ENABLED=false
FLOWSEER_EDGE_INGEST_AUDIT_LOGS_ENABLED=true
```

The prefix comes from `Config.EnvPrefix`, or from the uppercased namespace and
service name when that field is empty. Module path slashes become underscores.
A module generation snapshots its effective policy; a reconstructed branch
reads its environment overrides again.

Caller-provided `LogHandler`, `TracerProvider`, and `MeterProvider` take
ownership of their individual slots away from the managed pipeline. The
runtime borrows them and never closes them. If they need flushing or shutdown,
set one aggregate `TelemetryShutdown` callback; it runs once after all module
and message-bus work has stopped. Managed components for the remaining slots
are flushed and closed by the service, with bounded cleanup even after the run
context is canceled.

Exporter failures go to a dedicated, rate-limited structured stderr sink. That
sink is separate from `Config.Logger` and is never fed back into the OTLP log
pipeline. Diagnostics omit endpoint paths, headers, payloads, carriers, and raw
errors. Service authors have the same privacy duty for module-authored
telemetry: export only allowlisted fields, keep credentials out of attributes
and baggage, and classify errors instead of recording raw error text.

Module code reads its instrumentation from the attempt context rather than from
process globals. `service.Logger(ctx)`, `service.Tracer(ctx)`,
`service.Meter(ctx)`, `service.TracerProvider(ctx)`,
`service.MeterProvider(ctx)`, `service.Propagator(ctx)`, and
`service.Attributes(ctx)` all return safe no-op values outside the runtime.
`context.WithoutCancel` retains them, which lets background protocol objects
keep their service logger without keeping the attempt alive.

The attempt logger already carries `service.name`, `service.namespace`,
`service.version`, and `flowseer.module.path`, so a module never restates its own
identity. Records written with a recording span also carry `trace_id` and
`span_id`; a module that opens a `slog` group before logging nests those two
under the group, because `slog` cannot add a record attribute above an open
group.

`flowseer.module.path` describes the current runtime output. The older
`service.module.path` key remains only in the `service.Attributes` compatibility
set and must not be copied into new instrumentation.

`Tracer` and `Meter` are scoped to the service runtime. A module that owns an
instrumentation scope should name it itself and attach only the bounded
occurrence dimensions it needs:

```go
meter := service.MeterProvider(ctx).Meter(
    "go.aledante.io/FlowSeer/src/edge/ingest/syslog",
    metric.WithSchemaURL(semconv.SchemaURL),
)
received, err := meter.Int64Counter(
    "flowseer.syslog.messages.accepted",
    metric.WithUnit("{message}"),
    metric.WithDescription("Messages accepted by the syslog receiver"),
)
if err != nil {
    return service.Attempt{}, err
}
return service.Attempt{Runner: func(ctx context.Context) error {
    received.Add(ctx, 1, metric.WithAttributes(
        attribute.String("flowseer.module.path", service.ModulePath(ctx)),
    ))
    ...
}}, nil
```

Here `semconv` is the newest generated package available in the pinned
OpenTelemetry Go module, currently
`go.opentelemetry.io/otel/semconv/v1.43.0`. Check the convention before copying
the version because the package and `SchemaURL` advance together.

`service.Attributes` is a compatibility helper for existing instrumentation. It
retains `service.name`, `service.namespace`, `service.version`, and the legacy
`service.module.path` key exactly. New instruments should attach only the
bounded occurrence attributes they need, as the example does. `attribute.Set`
methods have pointer receivers, so assign a returned set to a variable before
calling `Value` or `ToSlice`.

### Telemetry schema migration

Managed telemetry uses the repository's namespaced schema. Existing dashboards,
alerts, and saved queries must update these selectors before deploying this
revision:

| Previous selector | Current selector |
| --- | --- |
| Metric `flowseer.service.module.lifecycle` | `flowseer.service.module.lifecycle.transitions` |
| Metric `flowseer.service.message.lifecycle` | `flowseer.service.message.operations` |
| Span `go.aledante.io/FlowSeer/src/common/service.publish` | `flowseer.message.publish` |
| Span `go.aledante.io/FlowSeer/src/common/service.delivery` | `flowseer.message.deliver` |
| Attribute `service.module.path` | `flowseer.module.path` |
| Attributes `service.lifecycle.action` and `service.lifecycle.outcome` | `flowseer.module.lifecycle.action` and `flowseer.module.lifecycle.outcome` |
| Attributes `messaging.message.type`, `messaging.message.kind`, and `messaging.operation` | `flowseer.message.type`, `flowseer.message.kind`, and `flowseer.message.operation` |
| Attributes `messaging.disposition`, `messaging.retry.count`, and `messaging.delivery.attempt` | `flowseer.message.disposition`, `flowseer.message.retry_count`, and `flowseer.message.delivery_attempt` |

The runtime does not emit both schemas because duplicate counters would
double-count operations. `messaging.disposition.id` has no replacement; the
runtime no longer exports that unbounded identifier. The `service.Attributes`
helper is the sole compatibility exception and continues returning the previous
four-key set for caller-owned instruments.

Callers that own telemetry exporters may set `TelemetryShutdown`. It runs after
all module work has stopped. Shared providers should be shut down there as one
owner; the package does not assume that each provider can be closed separately.

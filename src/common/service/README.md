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

Module code reads its instrumentation from the attempt context rather than from
process globals. `service.Logger(ctx)`, `service.Tracer(ctx)`,
`service.Meter(ctx)`, `service.TracerProvider(ctx)`,
`service.MeterProvider(ctx)`, `service.Propagator(ctx)`, and
`service.Attributes(ctx)` all return safe no-op values outside the runtime.
`context.WithoutCancel` retains them, which lets background protocol objects
keep their service logger without keeping the attempt alive.

The attempt logger already carries `service.name`, `service.namespace`,
`service.version`, and `service.module.path`, so a module never restates its own
identity. Records written with a recording span also carry `trace_id` and
`span_id`; a module that opens a `slog` group before logging nests those two
under the group, because `slog` cannot add a record attribute above an open
group.

`service.module.path` describes the current runtime output. It is a legacy
custom key in an OpenTelemetry-owned namespace and must not be copied as a
precedent. New custom attributes use `flowseer.*`; an eventual telemetry-schema
migration will replace the legacy key.

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
contains the legacy module key and repeats Resource identity, so new instruments
should attach only the bounded occurrence attributes they need, as the example
does. `attribute.Set` methods have pointer receivers, so assign a returned set
to a variable before calling `Value` or `ToSlice`.

Callers that own telemetry exporters may set `TelemetryShutdown`. It runs after
all module work has stopped. Shared providers should be shut down there as one
owner; the package does not assume that each provider can be closed separately.

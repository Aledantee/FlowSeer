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
    Setup: func(ctx context.Context) (service.Runner, error) {
        client, err := openClient(ctx)
        if err != nil {
            return nil, err
        }
        return func(ctx context.Context) error {
            defer client.Close()
            return client.Serve(ctx)
        }, nil
    },
})
```

Setup owns attempt-local construction. A restart calls it again, so mutable
state from a failed attempt cannot leak into its replacement. The returned
runner must cooperate with context cancellation; the runtime waits for it and
will not overlap it with another attempt.

`service.Logger(ctx)`, `service.Tracer(ctx)`, `service.Meter(ctx)`, and
`service.Propagator(ctx)` read the capabilities attached to the current attempt.
They return safe no-op values outside the runtime and never consult process
globals. `context.WithoutCancel` retains these values, which lets background
protocol objects keep their service logger without keeping the attempt alive.

Callers that own telemetry exporters may set `TelemetryShutdown`. It runs after
all module work has stopped. Shared providers should be shut down there as one
owner; the package does not assume that each provider can be closed separately.

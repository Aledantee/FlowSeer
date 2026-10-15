# OTLP delivery

`otlpapi` is central's `TelemetryService`. An edge calls `Deliver` with a batch
of OTLP bodies, each with its signal, and central posts each body unchanged to
its collector through `edgebus.Collector`. The forwarder classifies a
collector answer with the same type for bodies that arrive over the edge's
stream, so both paths treat an answer alike while both exist.

```go
handler, err := otlpapi.NewHandler(otlpapi.Config{
	Collector: collector, // nil when central has no collector
	EdgeID:    edgeIDFromContext,
})
```

The handler answers `settled`, the count of leading exports it settled, and
the edge resends from that index.

| Collector answer | Outcome | Settled |
| --- | --- | --- |
| 2xx | accepted, counted as forwarded | yes |
| 4xx except 401, 403, 404, 408, 429 | refused for the body's content, counted as `collector_rejected` | yes |
| 401, 403, 404, 408, 429 | the collector's configuration or load, so the body waits | no |
| 5xx, transport error, other | the collector is unavailable, so the body waits | no |
| no collector configured | nothing is posted | no |

An export that fails validation is settled and counted as `invalid`, so one
bad export cannot hold up the exports behind it. The handler applies no
delivery backstop. A body waits in the edge's log until a collector accepts it
or the log's bounds expire it, because central with no collector must not
discard accepted telemetry silently.

A call with no edge in its assertion answers `unauthenticated`. A collector
failure is never a failed call.

## Instruments

The scope is `go.aledante.io/FlowSeer/src/services/device/internal/otlpapi`.
`flowseer.otlpapi.records.forwarded` counts accepted bodies and
`flowseer.otlpapi.records.refused` counts settled refusals with
`flowseer.otlpapi.reason` (`invalid` or `collector_rejected`). Both carry
`flowseer.device.signal`, empty for an export whose signal is not a defined
one. A refusal emits the named event `flowseer.otlpapi.record.refused` at WARN
level, limited to one per edge every ten seconds, and a body left for
redelivery logs a warning with `error.type` under a separate ten-second limit.

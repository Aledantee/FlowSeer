# fsconv

The telemetry vocabulary that more than one FlowSeer tree emits. The edge agent
and the device service correlate on `flowseer.device.id` and
`flowseer.edge.id`, and before this package they matched only because each
typed the same literal. A key or an `error.type` value taken from here cannot
drift between packages.

```go
logger.InfoContext(ctx, "device onboarded",
	fsconv.EventName("flowseer.edge.device.onboarded"),
	fsconv.DeviceID(deviceID),
)

if err != nil {
	logger.WarnContext(ctx, "device onboarding failed",
		fsconv.DeviceID(deviceID),
		fsconv.ErrorType(err),
	)
	fsconv.SpanError(span, err, "onboarding failed")
}
```

The name avoids `semconv`, the alias every instrumented file already uses for
`go.opentelemetry.io/otel/semconv/v1.43.0`.
[Observability conventions](../../../docs/conventions/observability.md) is the
binding record for telemetry naming. This package carries the part of it that
code can hold.

## Which keys belong here

A key enters when more than one tree under `src/` emits it, or when it names
the ambient tenant. A key one package owns stays a private constant beside its
instrument, so this package does not grow into a registry of every attribute.

| Constant | Key | Constructor |
| --- | --- | --- |
| `DeviceIDKey` | `flowseer.device.id` | `DeviceID(string)` |
| `DeviceSequenceKey` | `flowseer.device.sequence` | `DeviceSequence(uint64)` |
| `DeviceSignalKey` | `flowseer.device.signal` | none, a metric and log attribute key |
| `EdgeIDKey` | `flowseer.edge.id` | `EdgeID(string)` |
| `TenantIDKey` | `flowseer.tenant.id` | `TenantID(string)` |
| `CaptureSessionIDKey` | `flowseer.capture.session.id` | `CaptureSessionID(string)` |
| `CaptureChunkFirstSequenceKey` | `flowseer.capture.chunk.first_sequence` | `CaptureChunkFirstSequence(uint64)` |

The constructors return `slog.Attr`, because every caller today is a `slog`
call. The constants are untyped strings, so a span or metric caller writes
`attribute.String(fsconv.DeviceIDKey, id)`.

`EventName` returns the `otel.event.name` bridge attribute, which the shared
log pipeline maps to a LogRecord's EventName. The standard keys come from the
pinned `semconv` package and are not retyped.

## The `error.type` vocabulary

`ErrorTypeOf` returns, in this order:

| Error | Value |
| --- | --- |
| `nil` | the empty string |
| carries an `errs` code | the code, such as `tenant/no-tenant` |
| wraps `context.DeadlineExceeded` | `context.deadline_exceeded` |
| wraps `context.Canceled` | `context.canceled` |
| anything else | `_OTHER` (`ErrorTypeOther`) |

The code wins over a context cause it wraps, because the code is the more
specific statement of what failed. `_OTHER` is the fallback the OpenTelemetry
`error.type` definition reserves. The value never carries the error's message
or Go type, since it becomes a metric dimension downstream.

`ErrorType(err)` is the log attribute for that value. `ErrorTypeString(value)`
is the same key for a value a caller classified itself, such as a refusal code.
`SpanError(span, err, description)` sets Error status and `error.type` on a
failed span and does nothing for a nil error. Pass it the error the caller
receives, after any mapping to a protocol code, or a transport failure reads
`_OTHER`.

## A package with its own codes

A package whose errors carry a bounded vocabulary of their own keeps a small
wrapper beside its call sites and logs its result with `ErrorTypeString`. The
order inside the wrapper decides which vocabulary wins.

When the package's codes should yield to `fsconv`, call `ErrorTypeOf` first
and substitute only for `_OTHER`. The capture uploader in the edge agent does
this, because a cancelled Connect call wraps `context.Canceled` and must read
`context.canceled`, not `connect.canceled`:

```go
func errorType(err error) string {
	if t := fsconv.ErrorTypeOf(err); t != fsconv.ErrorTypeOther {
		return t
	}
	if code := connect.CodeOf(err); code != connect.CodeUnknown {
		return "connect." + code.String()
	}
	return fsconv.ErrorTypeOther
}
```

When the package's own sentinels are more specific, match them first and fall
through to `ErrorTypeOf` where the wrapper would have returned a catch-all. The
SNMP client does this with its authentication and timeout sentinels.

Both shapes keep `connectrpc.com/connect` and every other protocol library out
of this package.

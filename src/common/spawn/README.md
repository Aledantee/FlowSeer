# spawn

A supervised entry point for first-party goroutines. Go does not propagate a
panic from a spawned goroutine to the frame that spawned it — it unwinds that
goroutine's stack and takes the process down, whatever recover sits in the
spawning call chain. `spawn.Go` is the one place that recovers instead, so a
panic costs the unit of work it happened in rather than the process.

```go
var wg sync.WaitGroup
wg.Add(1)
spawn.Go(ctx, "device poll", func() {
    defer wg.Done()
    poll(ctx, device)
})
...
wg.Wait()
```

`spawn.Go` returns as soon as the goroutine starts; it does not join `fn`. A
caller that needs to wait keeps its own `sync.WaitGroup`, `wg.Add(1)` before
the call and `wg.Done()` deferred inside `fn`, exactly as above. It does not
restart, back off, or cancel siblings either — that policy belongs to
whatever owns the unit of work, and folding it into the spawn helper would
turn one crash into a retry loop with no one deciding the policy. See
[Supervised Goroutine Spawn](../../../docs/architecture/2026-09-15-supervised-goroutine-spawn-direction.md)
for why the package sits here, beside `errs`, `pump`, and `service`, rather
than inside any of them: it needs `errs` and a logger, which rules out
`pump`, and most of its callers spawn outside any `service` module attempt,
which rules out routing through `service.Go`.

## What the panic path skips

The example above joins with a `WaitGroup` and takes no sink, which is the one
combination that needs no further thought. Everything else does, because a
panic skips whatever `fn` had left to do and `spawn.Go`'s recover runs *after*
every deferred call `fn` registered.

A completion deferred inside `fn` — `wg.Done`, `close(ch)`, `pump.Done` —
therefore runs before the sink. A caller that learns from that completion
learns it before the error is recorded, so a consumer that drains to a closed
channel and then reads `Err()` sees `nil`, which it cannot tell from a clean
finish. Put the completion on `fn`'s normal path and in the sink, so exactly
one of them reaches it:

```go
spawn.Go(ctx, "table walk", func() {
    walk(ctx, w)
    w.pump.Done()                 // normal path only
}, spawn.ReportTo(w.pump.Fail))   // panic path: records, then closes
```

Where the joiner consumes what the sink produces, a `WaitGroup` cannot express
this at all — `wg.Wait` returns as soon as `fn`'s deferred `Done` runs, which
is before the sink. Join by counted receive instead, one value per goroutine
from either the normal path or the sink.

A lock is the same rule with a worse failure. `fn` must release it from a
`defer`; an explicit `Unlock` on the normal path is skipped by the panic and
the mutex stays held for the life of the process. That is a hang where the
unrecovered panic was a crash, and a hang has no signal but a log line.

A sink is called with the goroutine already unwound. It must not block — no
further report follows it. A panic inside a sink is recovered and reported
under the label `"<label> sink"`, because a helper whose whole purpose is that
a panic costs one unit of work cannot let its own reporting path take the
process down; the error that sink was handed does not reach its destination.

A recovered panic is always reported: one `error`-level log record, per
[the observability conventions](../../../docs/conventions/observability.md),
and a span event when `ctx` carries a recording span. That is the floor for
the calls with nowhere else to send the error — a fire-and-forget loop, a
watchdog. When the caller supplies `spawn.ReportTo(sink)`, the same error
also reaches `sink`, so an existing failure path (`pump.Fail`, a channel, a
struct field) keeps working.

The reported error carries the recovered value under the attribute key
`"panic"` and a stack; its message names the label passed to `spawn.Go`. A
panic whose value is already an `error` is wrapped rather than stringified,
so `errors.Is` and `errors.As` still reach it:

```go
attrs := errs.Attributes(err)
attrs["panic"] // the recovered value
```

## The logger

`spawn.Go` logs through `slog`'s package-level default (`slog.ErrorContext`),
not a context-carried logger. The repository has no logger helper outside
`src/common/service`, and importing that package here would invert the
layer order `AGENTS.md` sets out: `service` is a control-plane concern, and
most callers here — `src/protocol`, `src/modules` — spawn outside any module
attempt, so routing their reports through `service` would mean a foundation
package importing the control plane. `slog.Default()` is process-wide, and a
caller that wants scoped output replaces it at startup the way `slog` already
supports.

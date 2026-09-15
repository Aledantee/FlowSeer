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
}, spawn.ReportTo(func(err error) {
    p.Fail(err)
}))
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
layer order `AGENTS.md` sets out: `service` is a control-plane concern,
`spawn` is a foundation most of `src/protocol` and `src/modules` spawns
outside any module attempt. `slog.Default()` is process-wide and callers that
want scoped output can replace it during startup the way `slog` already
supports.

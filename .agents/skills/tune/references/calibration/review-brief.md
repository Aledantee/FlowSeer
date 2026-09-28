Review the last commit on this branch (`git show HEAD`), which adds `Merge` to the Go package `src/common/pump` (module `go.aledante.io/FlowSeer`). Judge `merge.go` and `merge_test.go` against the contract below. Do not edit any file.

Read first: `src/common/pump/pump.go` (the whole file, its package doc defines the synchronization model the code must respect), `src/common/pump/pump_test.go`, `docs/code-style.md`.

Contract:

```go
// Merge forwards every value of each source into the returned pump.
func Merge[T any](ctx context.Context, buf int, sources ...*Pump[T]) *Pump[T]
```

- The returned pump is constructed with `New(ctx, buf)`. Its owner calls `Cancel` on it as for any pump; Merge itself never cancels a source's context and never closes a source's data channel.
- Values from one source arrive in the merged pump in the order that source produced them. No order is promised across sources.
- The merged pump completes normally (`Done`) once every source's data channel is closed and every value it held has been forwarded, and none of the sources recorded an error. With zero sources it completes immediately.
- The first source error observed (a source whose data channel closed with a non-nil `Err()`) makes the merged pump `Fail` with that error. The remaining sources are told to stop through `SignalStop`; values they already handed over are still delivered, and the merged data channel closes only after every forwarder has exited.
- If the merged pump is stopped by its consumer (`CloseData`, `Done`, `Fail`, or `SignalStop`) or its context is canceled, every source gets `SignalStop`, every forwarder exits, and the merged data channel closes. A canceled context is recorded as the merged pump's error through `Fail(ctx.Err())` when no source error was recorded first.
- Producers may keep calling `Send` or `TrySendDropOldest` on a source at any point during and after all of the above; no call panics.
- No goroutine started by Merge outlives the merged data channel's close.

Return findings only, most severe first. For each: severity (high, medium, low), file and line, the contract clause it breaks or the defect, and a concrete sequence of calls that shows it. A finding you cannot back with such a sequence is left out. If you find nothing, say so. You may write and run throwaway tests to confirm a finding, and delete them before you report. Do not ask questions. No narration while working. Terse register in the report.

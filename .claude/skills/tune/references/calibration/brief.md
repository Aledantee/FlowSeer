Ensure the Go package `src/common/pump` (module `go.aledante.io/FlowSeer`) has a `Merge` function meeting the contract below. If `merge.go` already exists, verify it against the contract and the package's tests, change only what fails, and report; do not rewrite working code. If it does not exist, add `merge.go` and `merge_test.go`. Either way `merge_test.go` must exist and cover every clause of the contract. Done means: `go test -race -count=3 ./src/common/pump/` passes from the repository root, `gofumpt -l src/common/pump` prints nothing, and any change is committed on the current branch with a short message. If nothing needed changing, say so and name the commit that holds the existing implementation.

Read first: `src/common/pump/pump.go` (the whole file, its package doc defines the synchronization model you must respect), `src/common/pump/pump_test.go` (test style: `t.Parallel`, `testing/synctest` for anything that waits), `docs/code-style.md`.

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

Return, outcome first and nothing else: the changed paths (or "no change"), the exact test command with its last lines of output, and the commit hash. Do not edit files outside `src/common/pump/`, do not touch `AGENTS.md`, `buf.yaml`, `tools/hooks/`, or `.claude/`, and do not cite plan or ticket identifiers in code or comments. Run only the package's own tests; the coordinator runs the repository verifier after the merge, so skip it even where repository guidance asks for it before handoff. Do not ask questions; if something blocks you, state the blocker and stop. Do not spawn subagents that edit files; read-only subagents are fine. No narration while working; terse register in the report: fragments fine, identifiers and errors exact, code unchanged.

---
title: Stopping a Pump Does Not Close Its Data Channel or Release a Forwarder
date: 2026-09-23
last_verified: 2026-09-23
category: architecture-patterns
module: src/common/pump
problem_type: bug
component: concurrency
severity: high
symptoms:
  - "A merged pump never closes its data channel after one source fails and another producer stops without calling Done."
root_cause: "SignalStop closes the stop signal only. A forwarder waiting solely on the stopped source's Data channel cannot finish, so a coordinator waiting for every forwarder cannot finish either."
resolution_type: "Give forwarders a termination signal independent of their source's data channel, and deliver it before waiting for them."
applies_when:
  - "Combining pumps or other producer streams where stopping a producer does not close its data channel."
  - "Waiting for forwarders to exit after one sibling source fails or the downstream consumer stops."
  - "A streaming test hangs after a producer observes a stop signal but leaves its data channel open."
related_components: [pump, streaming]
tags: [pump, concurrency, deadlock, stop-signal, forwarder]
---

# A stop signal is not a closed data channel

`Pump.SignalStop` closes `stop`, while `Pump.CloseData` closes `ch` separately
(`src/common/pump/pump.go:96-112`). A producer can obey the stop signal by
returning from its send loop without calling `Done`. A consumer blocked on
`source.Data()` then has no event to wake it.

The split is explicit in the source: `SignalStop` calls
`p.stopOnce.Do(func() { close(p.stop) })` (`src/common/pump/pump.go:99`), while
`CloseData` calls `close(p.ch)` (`src/common/pump/pump.go:106-112`).

This trapped an earlier `Merge` implementation at commit `30c8da74`. On a
source error it called `SignalStop` on every source, but each forwarder selected
only on `source.Data()`, `merged.Stopped()`, and the merged context. Its
coordinator called `wg.Wait()` before closing the merged pump. Neither the
source stop nor the wait closed the merged stop signal. The relevant lines at
that commit are `src/common/pump/merge.go:38-42`, `:51-67`, and `:82-93`:
`s.SignalStop()` preceded the forwarder's `case v, ok := <-s.Data():` and
the coordinator's `wg.Wait()`.

The failure case is small: one source fails; a sibling's `Send` returns false
after `SignalStop`; the sibling leaves `Data` open. The sibling forwarder still
waits for data, and the coordinator waits for that forwarder. The acceptance
test's sibling producer uses `for i := 0; b.Send(i); i++ {}` with no `Done` at
`.claude/skills/tune/references/calibration/merge_accept_test.go.txt:101-130`.

## Apply the rule

When a coordinator needs to end all forwarders, give them a channel it closes
before waiting. The current `Merge` checks `stopForwarders` before each read
and selects on it alongside `source.Data()` (`src/common/pump/merge.go:32-55`).
On the first source error, it signals the sources and closes
`stopForwarders` before `forwarders.Wait()` (`src/common/pump/merge.go:101-128`):
`stopSources()` then `close(stopForwarders)` are the two consecutive calls.
The ordinary source-error test leaves the other source's data channel open
and checks that the merged pump completes with the error
(`src/common/pump/merge_test.go:76-106`).

```go
select {
case <-stopForwarders:
    return nil
case value, ok := <-source.Data():
    // Forward value, or inspect source.Err() when the channel closes.
}
```

This does not require a source owner to close or cancel its pump just because
another source failed. `Merge` signals sources to stop; their owners still
control their contexts and data-channel closure.

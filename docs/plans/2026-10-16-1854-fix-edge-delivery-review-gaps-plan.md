---
title: Edge Delivery Review Gaps - Plan
type: fix
date: 2026-10-16
artifact_contract: flowseer-plan/v2
execution: code
---

# Edge Delivery Review Gaps - Plan

## Goal

The agent's delivery drains warn about every record their log discards,
survive a drain that panics on every attempt, and register their buffer
metrics only for an attempt that runs. Central's embedded hub carries no
credential plumbing left over from the per-edge accounts. The means are
three small units in the drain package, the agent host, and
`src/modules/edgebus`, each with a test that fails before it. Stop
condition: the plan is wrong if a drain panic is shown to leave the log or
its segment in a state a restarted attempt cannot read, since isolating the
panic would then repeat it forever without progress.

## Decisions

- A drain waits in `Next` for at most `maxBackoff` (30 s) at a time. On a
  deadline while its own context is live it checks the log's discard count
  and waits again. It also checks the count once before it returns on
  shutdown. Why: `warnOnDiscards` runs only after `Next` returns a segment
  (`src/edge/agent/internal/drain/drain.go:151`, `:158`), and `Next` waits
  on its change signal or the context with no timer
  (`src/edge/agent/internal/seglog/log.go:384-396`). An eviction in
  `Next`'s own bounds check (`log.go:369`) leaves nothing to return, and
  `Next` then blocks again, so the drain never warns, although the conventions require drop diagnostics on a path
  that does not depend on the exporter
  (`docs/conventions/observability.md:398-400`). A canceled or expired
  `Next` releases its lock before it waits (`log.go:391`) and returns
  through its context check (`log.go:364-366`), and a segment leaves the
  log only through `Ack` or an eviction (`log.go:421-438`), so a bounded
  wait loses no segment. The drain must test its own context before the
  child deadline: the check `ctx.Err() != nil || isClosed(err)`
  (`drain.go:153`) would otherwise return on the child's
  `DeadlineExceeded`.
- A drain leaf gets the same unbounded budget for panics as for errors, so
  a drain that panics on every attempt restarts inside its branch and never
  reaches the root. (decided by the user, 2026-10-16) Why: today only
  `Error` has a budget (`src/edge/agent/host/delivery.go:65`). `Panic`
  normalizes to 3 per minute (`src/common/service/policy.go:10-11`, `:134`),
  `decide` checks that budget before the branch intensity
  (`src/common/service/supervisor.go:236-240`), and the root's default
  intensity of 3 per 5 minutes (`policy.go:16-17`) then ends `service.Run`.
  One log's fault must hold back only that log, as the delivery decision in
  `docs/architecture/2026-10-04-edge-delivery-over-connect-direction.md`
  (2026-10-16 amendment) states for storage faults.
- The buffer metrics callback is registered at the start of the attempt's
  runner, not in setup. Why: setup registers it (`delivery.go:74`) and only
  the runner's defer unregisters it (`delivery.go:79`), and the supervisor
  can end an attempt between the two when its context is already canceled
  (`supervisor.go:706-715`). No second attempt follows that path today, so
  this removes a leak, not a live duplicate. The comment at `delivery.go:92`
  then states what the code does.
- The drain package exports the attribute key as `drain.LogKey`, and
  `registerBufferInstruments` uses it in place of its own literal. Why: the
  same literal is typed at `drain.go:27` and `delivery.go:102`, and two
  literals agree only by accident (`docs/conventions/observability.md:95-99`).
  Both emitters are in `src/edge/agent`, so the key stays with its owning
  package rather than moving to `src/common/fsconv`.
- `newDrain` and `pending` are inlined into their callers. `run` and
  `call` stay, and `warnOnDiscards` gains its second caller in U1. Why:
  `newDrain` and `pending` each have one caller (`drain.go:76`, `:163`)
  and no test calls them (`docs/code-style.md:558-559`). `run` holds the
  delivery loop, which grows with the bounded wait, and inlining it or
  `call` into `Run` would make a function too long to follow top to bottom
  (`docs/code-style.md:36-37`).
- In `src/modules/edgebus`, the dead credential plumbing goes:
  `credentials.AccountJWT` (`keys.go:186`, written at `:212`, never read),
  the `accountJWT` parameters of `mintUser` (`keys.go:192`) and
  `connectAccount` (`hub.go:229`), `mintUser`'s `permissions` parameter
  (always `jwt.Permissions{}`, `hub.go:230`), `Hub.centralBudget`
  (`hub.go:77`, read only by `cleanup_test.go:133`), and `Hub.log`
  (`hub.go:78`, never read). `StartHub` takes the budget from
  `cfg.CentralBudget()` (`hub.go:118-120`) instead of recomputing it at
  `hub.go:149-152`. Why: each is written and never read, so a reader takes
  it for live per-edge plumbing that the removal deleted. Dead code is
  named, not removed in passing (`docs/code-style.md:585`), which this
  plan does. All of it is unexported, and nothing under
  `src/services/device/` references it.
- The shutdown order of the receiver and the two logs stays as it is, with
  no new seam. Why: the defers run receiver, telemetry log, then ingest log
  (`src/edge/agent/host/host.go:111`, `:112`, `:117`), which matches the
  documented order (`host.go:73-74`). `service.Run` flushes telemetry before
  it returns (`src/common/service/service.go:68-70`), and `Receiver.Close`
  waits for in-flight handlers (`src/edge/agent/internal/otlpreceiver/receiver.go:132-134`).
  Testing the order would need the body of `host.Run` after enrollment
  extracted only for that test.

## Requirements

1. A drain whose only record ages out while central settles nothing logs
   one discard warning. Example: one record in the ingest log, `send`
   always returns `settled` 0, no further append, the log's age bound
   passes. One warning names the log, where today there are none.
2. A drain that has seen discards logs the warning before it returns on
   shutdown. Example: a record evicted, then the context canceled before
   any further `Next` returns. One warning is logged before `Run` returns.
3. A drain whose `send` panics on every call does not end `service.Run`,
   and the other log keeps delivering. Example: the telemetry `send`
   panics on every call for four minutes. At least eight drain restarts
   carry the outcome `panic`, no restart is recorded for the branch
   `agent/delivery_telemetry` itself, the ingest drain delivers a record,
   and `service.Run` is still running.
4. An attempt that ends before its runner starts leaves no buffer callback
   registered. Example: under `service.Run` with a meter provider, a leaf
   whose setup calls the delivery setup and then cancels the run, so the
   runner never starts. After `Run` returns a collection calls the stats
   reader 0 times, where today it calls it once.
5. The literal `"flowseer.edge.buffer.log"` appears once under `src/`,
   in the drain package's exported key. Example:
   `git grep -n '"flowseer.edge.buffer.log"' -- src` prints one line.
6. `StartHub` connects central with a user minted without an account JWT
   argument or a permissions argument, and every existing `StartHub` test
   passes. Example: `TestLaneBucketSurvivesRestart` and the `cleanupHub`
   tests pass after `AccountJWT`, `Hub.centralBudget`, and `Hub.log` are gone.

## Out of scope

- The shutdown order of the receiver and the logs (Decisions).
- A drain panic that corrupts its log. The log checks a CRC-32C on every
  record, and nothing in the drain writes to it.
- Golangci-lint configuration. No `unused` rule is added for fields read
  only by tests.
- Other pre-existing review notes in `docs/solutions/` prose.

## Units

### U1. Drain discard warnings and helpers
Files: src/edge/agent/internal/drain/drain.go, src/edge/agent/internal/drain/drain_test.go, src/edge/agent/internal/drain/doc.go
After: none
Change: `Run` builds the drain in place of `newDrain`, and `run` computes the pending records in place of `pending`. Each `Next` call runs under a context that expires after `maxBackoff`. A deadline with the drain's own context still live runs `warnOnDiscards` and loops back to `Next`. On shutdown `Run` calls `warnOnDiscards` once before it returns. The attribute key is exported as `LogKey`, and the package doc says the drain warns on discards while it waits.
Tests: `drain_test.go` gains a helper beside `runAgeBound` (`drain_test.go:608`) that appends one record, lets `send` settle nothing, and appends nothing more, since `runAgeBound` waits for a later record (`drain_test.go:611-633`). Its case expects one warning after the age bound passes (Requirement 1). A second case evicts a record and cancels before `Next` returns, and expects one warning before `Run` returns (Requirement 2). Existing drain tests keep passing with the helpers inlined. The test literal at `drain_test.go:29` uses `LogKey`.
Verify: `uv run tools/scripts/run.py verify change -- src/edge/agent/internal/drain`

### U2. Delivery panic budget, callback lifetime, and shared key
Files: src/edge/agent/host/delivery.go, src/edge/agent/host/delivery_test.go, src/edge/agent/README.md, docs/architecture/2026-10-04-edge-delivery-over-connect-direction.md
After: U1
Change: the drain leaf's `Policy` sets both `Error` and `Panic` to the unbounded budget. `setup` returns a runner that registers the buffer instruments first and unregisters them when it returns. `registerBufferInstruments` takes its attribute key from `drain.LogKey`. The agent README and the edge delivery record's 2026-10-16 agent switch amendment (`docs/architecture/2026-10-04-edge-delivery-over-connect-direction.md:226-228`, "an error budget no storage fault exhausts") say a drain that fails or panics restarts only inside its own branch.
Tests: `delivery_test.go` gains a case modeled on `TestDeliveryStorageFaultStaysInItsBranch` (`delivery_test.go:70`) whose telemetry `send` panics on every call, and which keeps that test's four-minute run and its threshold of eight (`delivery_test.go:127`, `:172`), counts restarts with outcome `panic` where that test filters `error` (`delivery_test.go:166`), and asserts no restart record for the path `agent/delivery_telemetry`, an ingest delivery, and a live `service.Run` (Requirement 3). The root restarts a failed branch with a fresh panic budget (`src/common/service/supervisor.go:534`), so a shorter run or a lower count passes before the fix. It gains a case that runs `service.Run` with a `MeterProvider` and a test leaf whose setup calls the delivery setup and then cancels the run context, so the runner never starts (`supervisor.go:706-711`). After `Run` returns it collects and expects the counting stats reader to have been called 0 times (Requirement 4). Calling `setup` outside a runtime does not work, since `service.Meter` then returns a no-op meter (`src/common/service/context.go:46-48`, `:133-138`). The literals at `delivery_test.go:344`, `:448`, and `:460` use `drain.LogKey` (Requirement 5).
Verify: `uv run tools/scripts/run.py verify change -- src/edge/agent/host src/edge/agent/README.md docs/architecture/2026-10-04-edge-delivery-over-connect-direction.md`

### U3. Edgebus credential leftovers
Files: src/modules/edgebus/keys.go, src/modules/edgebus/hub.go, src/modules/edgebus/cleanup_test.go
After: none
Change: `credentials` holds only the user JWT and seed. `mintUser(account, name)` and `connectAccount(srv, account, name)` take no account JWT and no permissions. `Hub` has no `centralBudget` or `log` field. `StartHub` takes the budget from `cfg.CentralBudget()`. The doc comment of `mintUser` no longer mentions permissions.
Tests: `cleanup_test.go` drops `centralBudgetJWT` (`cleanup_test.go:131-138`) and calls `connectAccount` with the new signature at `:85`. The existing `StartHub` tests prove central still connects with its minted user (Requirement 6), and `TestHubRefusesACentralBudgetBelowTheStreamSum` (`hub_test.go:132`) and `TestHubStartFailsWhenCentralBudgetBelowStreamSum` (`edgebus_test.go:597`) prove the budget still applies.
Verify: `uv run tools/scripts/run.py verify change -- src/modules/edgebus`

Waves: U1 U3 | U2

## Verification

```bash
go test -race ./src/edge/agent/... ./src/modules/edgebus/...
git grep -n '"flowseer.edge.buffer.log"' -- src
uv run tools/scripts/run.py verify change -- src/edge/agent src/modules/edgebus
```

## Definition of done

- [ ] Verifier green for every changed path.
- [ ] `src/edge/agent/README.md` and the drain package doc describe the new behavior in the same change.
- [ ] The plan's outcome recorded with `uv run tools/scripts/run.py plan record implemented <plan> --units 3 --from <t> --to <t>`, or `partial`.
- [ ] No plan labels in code, comments, or commit messages.

## Open questions

- Whether a duplicate observation on the same attribute set double-counts
  in the OpenTelemetry SDK is unverified. U2 removes the path that could
  produce one, so the answer does not change the plan.

---
title: BulkWalk Lazy Varbind Decode - Plan
type: perf
date: 2026-09-27
artifact_contract: flowseer-plan/v1
artifact_readiness: implementation-ready
status: planned
execution: mixed
---

# BulkWalk Lazy Varbind Decode - Plan

## Goal

`Session.BulkWalk` stops paying to decode the varbinds that follow the one
ending the walk. An agent answering GETBULK fills every repetition after a
column's end with `endOfMibView` (RFC 3416 section 4.2.3), so the last response
of a walk carries up to 49 varbinds the engine never uses, and today the reactor
decodes all of them before the engine sees the first. After this change a bulk
walk asks the reactor for raw delivery and decodes a varbind's name when the
engine reaches it and its value only when it is yielded. Stop condition: if
raw delivery changes which responses are dropped or which error a walk
returns, the plan is wrong, because the raw path exists on the promise that
drop semantics match the eager decode.

## Evidence

- `BenchmarkBulkWalk/impl=flowseer` rose from 481 to 1274 allocs/op in
  `7210aa0c`. Bisected over `6b3fc3ba..85f553b3` with a single-count run per
  commit; later drift adds 7 (1281 today).
- Swapping only the new `src/common/snmp/bench/responder_test.go` from
  `7210aa0c` onto its parent `7185cd0a` raises the parent to 1274, so the
  library did not change; the responder did. The new responder fills every
  repetition after a column's end with `endOfMibView` instead of stopping at
  the first one.
- The responder's 7000-byte truncation is not involved: a 100-row walk is
  3 requests and 2898 bytes with the cap at 7000 or at 1 MiB, and allocs/op
  stay at 1281 either way.
- `walkBulkMaxRepetitions` is 50 (`src/protocol/snmp/session_engine.go:23`).
  The 100-row walk's third response carries 50 `endOfMibView` varbinds where
  it used to carry 1.
- An alloc_objects profile of the benchmark splits the cost roughly in half.
  One half is the in-process responder encoding the padding (`bench.handle`,
  `tlv`, `concat`, `oidTLV`), which the micro benchmarks count by design. The
  other half is the client's eager `decodeVarBindList` (`decodeOID`,
  `decodeValue`) in the reactor read loop (`src/protocol/snmp/reactor.go:777`).
- The walk engine already stops at the first `endOfMibView`
  (`runWalkEngine`, `walkStop`, `src/protocol/snmp/session_engine.go`), so the
  padding is decoded and discarded.

## Decisions

- Bulk requests from the decoded walk set `wantRaw`; GetNext requests do not.
  Why: only GETBULK responses carry padding, and `BulkWalk` fails on v1
  before any request (`ErrBulkUnsupported`), so the v1-only Counter64 warning
  scan in the eager branch (`scanCounter64Warnings`, `src/protocol/snmp/pdu.go:217`)
  never applied to it. `Walk` over GetNext keeps its eager path and its v1
  warnings.
- Drop semantics come from the reactor, unchanged. Why: the read loop takes
  raw delivery only when `validateRawVarBindList` accepts the whole list,
  padding included, and falls back to the eager decode otherwise
  (`src/protocol/snmp/reactor.go:774`). A response the eager decode would
  drop is still dropped, and a raw list is known to decode.
- `oidWalkOps` keeps `Key = OID` and gets an item type that holds either a
  decoded `VarBind` (v3, GetNext, eager fallback) or the raw name, tag, and
  value slices. `key` decodes the name once for a raw item, `exception` reads
  the tag without decoding, and `send` decodes the value with `decodeValue`
  and the already-decoded name. Why: switching the decoded walk to byte keys
  as `rawWalkOps` does would make every v3 varbind pay `encodeOIDContent`,
  which `rawItemsOf` does for pre-decoded responses; keeping OID keys costs
  a raw item one name decode, the same as today.
- The projection for a decoded response wraps each `VarBind` without
  encoding anything. Why: the same v3 cost as above; `rawItemsOf` is not
  reused for the decoded branch.
- A value decode error in `send` fails the walk with the error wrapped as
  `decode varbind value`. Why: after `validateRawVarBindList` it cannot
  happen, but the walk must not yield a nil `VarBind` if the two ever
  diverge.
- The benchmark responder keeps its RFC-conformant padding. Why: real agents
  pad, so the benchmark now measures a cost production walks pay.

## Requirements

1. A bulk walk over an agent that pads the last response with
   `endOfMibView` yields exactly the rows in the subtree and no error.
   Example: 7 ifTable rows, max-repetitions 50, padded agent; the walk yields
   7 varbinds and `Err()` is nil.
2. Decoding a raw response's items for the decoded walk allocates the same
   amount whether one or 49 `endOfMibView` varbinds follow the last row.
   Example: two hand-built raw varbind lists, 3 rows plus 1 marker and 3 rows
   plus 49 markers, driven through the item projection, `key`, `exception`,
   and `send` up to the stop; `testing.AllocsPerRun` reports equal counts.
3. `BulkWalk` and `BulkWalkRaw` agree on every scenario in
   `TestBulkWalkRaw_DifferentialWithBulkWalk`, plus a padded-end scenario.
   Example: the padded 7-row agent gives the same OIDs in the same order
   from both.
4. A v3 bulk walk and a GetNext walk keep their current behavior. Example:
   the existing v3 and `Walk` tests pass unchanged.
5. `BenchmarkBulkWalk/impl=flowseer` allocs/op falls below the 1281 in
   `src/protocol/snmp/bench/testdata/baseline-micro.txt`, and
   `BenchmarkBulkWalkStreaming` and `BenchmarkTableWalk` do not rise.
   Example: `task bench:gate` passes against the refreshed baseline.

## Out of scope

- Removing the responder's own padding allocations from the micro
  benchmarks. That changes what every micro benchmark reports and was
  declined.
- The table walk in `src/protocol/snmp/column_walk.go`, which already takes
  raw delivery.
- Changing `walkBulkMaxRepetitions`.

## Units

### U1. Decode bulk walk varbinds on demand

Files:
- `src/protocol/snmp/session_engine.go`
- `src/protocol/snmp/session_engine_test.go`
- `src/protocol/snmp/rawwalk_test.go`

After: none

Change: `oidWalkOps.newRequest` sets `req.wantRaw = true` on a bulk request.
`oidWalkOps.items` returns an item per varbind: for a `rawVBL` response the
raw name, tag, and value slices re-framed in place as `rawItemsOfLimit`
frames them; for a decoded response the `VarBind` itself. `key` returns the
decoded `VarBind`'s OID or decodes the raw name; `exception` classifies from
the tag or the decoded kind; `send` decodes a raw value with `decodeValue`
and the name `key` already decoded, then calls `Walker.Send`. The engine and
`rawWalkOps` are unchanged.

Tests:
- `session_engine_test.go`: `mibBehavior` gains `padEndOfMibView bool`; with
  it set, the GETBULK branch appends `endOfMibView` for every remaining
  repetition instead of breaking. `TestSession_BulkWalk_PaddedEnd` walks 7
  rows against it and expects 7 varbinds and a nil error. An allocation test
  builds the two raw lists of requirement 2 and asserts equal
  `testing.AllocsPerRun` counts through the item projection and the ops
  methods, without a socket, so the agent's own allocations are not counted.
- `rawwalk_test.go`: `TestBulkWalkRaw_DifferentialWithBulkWalk` gains a
  `padded-end` scenario with `mibBehavior{padEndOfMibView: true}`.
- The stop condition's risk, a changed drop or error for a malformed
  response, is the reactor's fallback, which this unit does not touch;
  `conformance_rawpath_test.go` and `conformance_corpus_test.go` already pin
  it for raw delivery and must pass unchanged now that bulk walks take it.

Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/protocol/snmp/session_engine.go src/protocol/snmp/session_engine_test.go src/protocol/snmp/rawwalk_test.go`

### U2. Rebaseline and record the cause

Files:
- `src/protocol/snmp/bench/testdata/baseline-micro.txt`
- `docs/solutions/architecture-patterns/snmp-collection-library-architecture-and-fast-path-conventions.md`

After: U1

Change: the baseline is re-measured with `task bench:micro COUNT=10` and the
FlowSeer arm kept, as `src/protocol/snmp/bench/bench-gate.sh` documents. The
solution doc's baseline paragraph replaces "No record explains the BulkWalk
rise" with the cause: the responder pads GETBULK ends per RFC 3416 section
4.2.3 since `7210aa0c`, the client used to decode the padding, and bulk
walks now decode on demand. It quotes the new BulkWalk allocs/op with the
baseline line.

Tests: `task bench:gate` against the new baseline passes.

Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/protocol/snmp/bench/testdata/baseline-micro.txt docs/solutions/architecture-patterns/snmp-collection-library-architecture-and-fast-path-conventions.md`

Waves: U1 | U2

## Verification

```bash
go test -race ./src/protocol/snmp/...
cd src/protocol/snmp/bench && go test -bench 'BulkWalk|TableWalk' -benchmem -run '^$' -count=10
cd src/protocol/snmp/bench && COUNT=10 sh bench-gate.sh
```

The benchmarks and the gate bind loopback UDP sockets and need an
unsandboxed run.

## Definition of done

- [ ] Verifier green for every changed path.
- [ ] Requirement 5 measured and quoted in the U2 commit message.
- [ ] Solution doc updated in the same change as the baseline.
- [ ] This plan's `status` set, with an outcome note under its title.
- [ ] No plan labels in code, comments, or commit messages.

## Open questions

None.

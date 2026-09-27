---
title: BulkWalk Lazy Varbind Decode - Plan
type: perf
date: 2026-09-27
artifact_contract: flowseer-plan/v1
artifact_readiness: implementation-ready
status: implemented
review: accept after fixes
compound: docs/solutions/architecture-patterns/snmp-collection-library-architecture-and-fast-path-conventions.md
execution: mixed
---

# BulkWalk Lazy Varbind Decode - Plan

> Implemented. 2 units, 2026-09-27T16:34Z to 2026-09-27T17:09Z.

## Goal

`Session.BulkWalk` stops paying to decode the varbinds that follow the one
ending the walk. RFC 3416 section 4.2.3 permits an agent answering GETBULK to
return a shorter response or pad remaining repetitions after a column's end
with `endOfMibView`. When an agent pads, the last response of a walk carries up
to 49 varbinds the engine never uses, and previously the reactor decoded all of
them before the engine saw the first. After this change, a bulk walk asks the
reactor for raw delivery. Instead of retaining raw slices or eagerly decoding,
`oidWalkItem` uses a four-byte offset/index locator backed by the response's raw
varbind-list bytes or decoded varbind slice. `items` validates TLV framing in a
pre-scan, `exception` and `key` reparse framing without decoding values, and
`send` defers `decodeValue` until an in-subtree varbind is yielded. Stop
condition: if raw delivery changes which responses are dropped or which error a
walk returns, the plan is wrong, because the raw path exists on the promise
that drop semantics match the eager decode.

## Evidence

- `BenchmarkBulkWalk/impl=flowseer` rose from 481 to 1274 allocs/op in
  `7210aa0c`. Bisected over `6b3fc3ba..85f553b3` with a single-count run per
  commit; later drift adds 7 (1281 today).
- Swapping only the new `src/common/snmp/bench/responder_test.go` from
  `7210aa0c` onto its parent `7185cd0a` raises the parent to 1274, so the
  library did not change; the responder did. The new responder exercises
  permitted RFC padding by appending `endOfMibView` for every repetition after a
  column's end instead of stopping at the first one.
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
- The walk engine yields the first `endOfMibView` as a terminal marker and
  stops (`runWalkEngine`, `walkYieldThenStop`,
  `src/protocol/snmp/session_engine.go`), so trailing padding varbinds were
  eagerly decoded and discarded.

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
- `oidWalkItem` is a four-byte locator (`off uint32`) holding a byte offset into
  the raw varbind-list bytes (`oidWalkOps.rawVBL`) for raw responses, or a slice
  index into `oidWalkOps.varbinds` for pre-decoded responses (v3, GetNext, eager
  fallback). Why: retaining raw name/tag/value slices or pointers per varbind
  inflates item slice memory (72 bytes per item). A compact 4-byte locator keeps
  item allocation minimal.
- `oidWalkOps.items` pre-scans `rawVBL` to validate outer sequence and inner TLV
  framing upfront and count items. Why: counting upfront allows allocating the
  locator slice with exact capacity, while verifying framing upfront ensures
  malformed TLV structures fail before the engine processes rows.
- Framing is reparsed on demand in `exception`, `key`, and `send`, deferring
  `decodeValue` until `send`. `key` decodes the name OID into `curOID`;
  `exception` inspects the value tag without decoding; `send` decodes the value
  with `decodeValue` only when yielding an item. Why: parsing TLV framing from
  byte offsets is inexpensive and avoids heap allocations. Trailing padding
  stops the walk at the first `endOfMibView`, so its values are never decoded.
- Pre-decoded responses index `VarBind`s directly by slice index. Why: it avoids
  re-encoding or wrapping pre-decoded varbinds and pays no allocations beyond
  the index slice.
- A value decode error in `send` fails the walk with the error wrapped as
  `decode varbind value`. Why: after `validateRawVarBindList` and the pre-scan
  it cannot happen, but the walk must not yield a nil `VarBind` if parser
  assumptions diverge.
- The benchmark responder retains its permitted RFC 3416 section 4.2.3 padding.
  Why: RFC 3416 permits an agent to pad remaining repetitions with
  `endOfMibView` instead of truncating the PDU. Real agents exercise this option,
  so the benchmark measures a cost production walks encounter.

## Requirements

1. A bulk walk over an agent that pads the last response with
   `endOfMibView` (permitted by RFC 3416 section 4.2.3) yields the in-subtree
   rows plus one `EndOfMibView` terminal marker, and no error. Trailing markers
   remain unvisited.
   Example: 7 ifTable rows, no outside scalar, response padded to 50
   varbinds; the walk observes 7 ordinary rows plus 1 yielded `EndOfMibView`
   terminal marker (8 total varbinds), trailing 42 markers remain unvisited,
   and `Err()` is nil.
2. Decoding a raw response's items for the decoded walk allocates the same
   amount whether one or 49 `endOfMibView` varbinds follow the last row.
   Example: two hand-built raw varbind lists, 3 rows plus 1 marker and 3 rows
   plus 49 markers, driven through the item projection, `key`, `exception`,
   and `send` up to the stop without a socket; `testing.AllocsPerRun` reports
   equal counts.
3. `BulkWalk` and `BulkWalkRaw` agree on every scenario in
   `TestBulkWalkRaw_DifferentialWithBulkWalk`, plus a padded-end scenario.
   Example: the padded 7-row agent without an outside scalar gives the same
   OIDs and terminal marker in the same order from both.
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
`oidWalkItem` is a four-byte locator (`off uint32`) holding a byte offset into
`oidWalkOps.rawVBL` for raw responses, or a slice index into
`oidWalkOps.varbinds` for pre-decoded responses. `oidWalkOps.items` pre-scans
`rawVBL` to validate outer sequence and inner TLV framing (varbind sequence, name
TLV, value TLV) and count items, allocating `items` with exact capacity before
recording byte offsets. `exception` reparses the framing to inspect the value tag
without decoding. `key` reparses sequence framing and decodes the name OID into
`curOID`. `send` reparses framing, extracts value TLV bytes, and defers
`decodeValue` until an item is yielded to `Walker.Send`. Trailing padding
varbinds never reach `send`, so their values are never decoded. The engine and
`rawWalkOps` are unchanged.

Tests:
- `session_engine_test.go`: `mibBehavior` gains `padEndOfMibView bool`; with
  it set, the GETBULK branch appends `endOfMibView` for every remaining
  repetition up to 50 instead of breaking. `TestSession_BulkWalk_PaddedEnd`
  tests 7 in-subtree rows with no outside scalar against a response padded to
  50 varbinds, observing 7 ordinary rows plus 1 yielded `EndOfMibView` terminal
  marker, while 42 trailing markers remain unvisited, and expects a nil error.
  An allocation test (`TestOIDWalkOps_LazyDecodeAllocations`) builds the two raw
  lists of requirement 2 (1 marker versus 49 trailing markers) and asserts
  equal `testing.AllocsPerRun` counts through the item projection and ops
  methods, without a socket, so agent allocations are excluded.
- `rawwalk_test.go`: `TestBulkWalkRaw_DifferentialWithBulkWalk` gains a
  `padded-end` scenario with 7 rows, no outside scalar, and
  `mibBehavior{padEndOfMibView: true}`.
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

Change: the baseline is re-measured with `task bench:micro COUNT=10`, retaining
the refreshed BulkWalk rows only and restoring unrelated benchmark rows from the
base. The solution doc's baseline paragraph replaces "No record explains the
BulkWalk rise" with the cause: the responder pads GETBULK ends per RFC 3416
section 4.2.3 since `7210aa0c`, the client used to decode the padding eagerly,
and bulk walks now decode on demand. It quotes the new BulkWalk allocs/op with
the baseline line.

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

- [x] Verifier green for every changed path.
- [x] Requirement 5 measured and quoted in the U2 commit message.
- [x] Solution doc updated in the same change as the baseline.
- [x] This plan's `status` set, with an outcome note under its title.
- [x] No plan labels in code, comments, or commit messages.

## Open questions

None.

---
title: A Generated Table.Walk Calls Session.GetBulk/GetNext Directly, Never BulkWalk or BulkWalkRaw
date: 2026-09-05
category: architecture-patterns
module: src/protocol/snmp
problem_type: bug
component: testing_framework
severity: medium
symptoms:
  - a test double built by embedding a fake snmp.Session and overriding
    BulkWalk (or BulkWalkRaw) to count or record calls sees zero calls, even
    though the table it wraps is walked successfully and its rows appear in
    the result
  - the same fake, used directly (not wrapped) as the whole Session, behaves
    correctly, masking the gap until something tries to observe the walk
    from outside
root_cause: >
  a mibgen-generated table's Table.Walk builds a column-selecting walker
  through collect.NewTable -> snmp.WalkColumns -> newColumnRequester, which
  calls sess.GetBulk (or sess.GetNext at the tail) directly unless the
  session implements the unexported columnSource fast-path interface; it
  never calls Session.BulkWalk or Session.BulkWalkRaw, so a wrapper that
  overrides only those two methods is never invoked, and Go's embedding has
  no virtual dispatch to route a promoted method's internal calls back
  through the wrapper anyway
resolution_type: test_fix
applies_when:
  - writing a test wrapper around an snmp.Session to count, record, or fail
    calls for a specific table or column
  - the session under test does not implement columnSource (most fakes and
    the fake session pattern in src/modules/localnet/snmpmap/fake_session_test.go
    do not)
  - reviewing a test that asserts something about "how a table was walked"
    via a Session method other than Get/GetNext/GetBulk
related_components: [snmp_library, mib_mapping, testing_framework]
tags: [snmp, mibgen, column-walk, testing, embedding, review-finding]
---

# A Generated Table.Walk Calls Session.GetBulk/GetNext Directly, Never BulkWalk or BulkWalkRaw

## The situation

Writing a test to prove that composing two `collect.Mapper`s in one
`collect.Collector` cycle actually reaches both mappers' declared tables
(`src/modules/localnet/snmpmap/phy_test.go`), the first two attempts wrapped
`fakeSession` and counted calls to `BulkWalk`, then `BulkWalkRaw`, keyed by
the root OID. Both wrappers reported zero calls for every table, even though
`collect.Read` completed successfully and every mapper's output contained
the fixture's data.

## Why both wrapper attempts saw nothing

A generated table's `Table.Walk` (e.g. `ifmib.IfTable.Walk`, called through
`collect.NewTable`, `src/modules/localnet/snmpmap/ifmib.go:117`) builds its
walker via `snmp.WalkColumns` (`src/protocol/snmp/column_walk.go:44`), which
constructs a `columnRequester` (`src/protocol/snmp/column_request.go:36`).
`columnRequester.request` (`column_request.go:64`) checks whether the
session implements the unexported `columnSource` interface (a fused
multi-column fast path); if not — true of `fakeSession` and of most test
doubles — it falls straight to `r.sess.GetBulk(...)` (or `GetNext` for a
non-bulk final pass). Neither `BulkWalk` nor `BulkWalkRaw` is ever called by
this path. Those two methods exist on `Session` for a caller doing its own
single-column or generic walk (`snmp.RawWalkerFromWalker` is one such
caller, `src/protocol/snmp/rawwalk.go:331`), not for the generated,
column-selecting table walkers `collect` and `snmpmap` build on.

The second attempt (overriding `BulkWalkRaw`) failed for a second, compounding
reason even if the call path had gone through it: `fakeSession.BulkWalkRaw`
(`src/modules/localnet/snmpmap/fake_session_test.go`) is implemented as
`return snmp.RawWalkerFromWalker(ctx, s.BulkWalk(ctx, root, opts...))` — a
call on `s`, the concrete `*fakeSession` receiver, not on the wrapping type.
Go has no virtual dispatch through struct embedding: a promoted method's own
internal calls always resolve against the type that defines the method, so
a wrapper overriding `BulkWalk` is never reached by `fakeSession`'s own
`BulkWalkRaw` calling its own `BulkWalk`.

## The fix

Test what the column-selecting walk actually leaves behind: the mapper's
own output in the `collect.Cycle` (or the `*collect.Snapshot`, from inside
the `collect`/`snmpmap` packages, via `Table.Rows`/`Table.Err`), not a
transport-level call count. A column-selecting walk also issues many
`GetBulk` requests per table by design — one per repetition batch, more if
a `tooBig` response degrades it — so "walked once" was never a call-count
invariant at the transport layer in the first place; it is a property of
`collect.Read`'s snapshot construction (one table entry per declared root),
already covered generically by
`src/modules/localnet/collect/collect_test.go`'s
`TestCollect_SharedTableWalkedOnce`.

## When to apply

- Before asserting anything about "was this table walked, and how many
  times" via `Session.BulkWalk`/`BulkWalkRaw` in a test: check whether the
  walker under test is a generated `Table.Walk` (column-selecting, via
  `collect.NewTable`/`snmp.WalkColumns`) — if so, instrument `Get`/`GetNext`/
  `GetBulk` instead, or better, assert on the resulting rows/snapshot rather
  than the transport call shape.
- When wrapping a fake session by embedding it and overriding a subset of
  `Session`'s methods: remember the embedded value's own methods call each
  other directly, not through the wrapper, so an override is only visible
  to code that calls the wrapper by that exact method name.

## Related

- [SNMP Collection Library — Architecture and Fast-Path Conventions](snmp-collection-library-architecture-and-fast-path-conventions.md)
  covers the generated-walker layering this note's call path sits inside.
- `src/protocol/snmp/column_request.go` — `newColumnRequester`,
  `columnRequester.request`, the `columnSource` fast-path interface.

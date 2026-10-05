---
title: Edge Delivery over Connect, Phase 1, Segmented Log - Plan
type: feat
date: 2026-10-04
artifact_contract: flowseer-plan/v2
execution: code
---

# Edge Delivery over Connect, Phase 1, Segmented Log - Plan

## Goal

The agent has a package that appends records to segment files, hands the
oldest sealed segment to one reader, and deletes it on acknowledgement. The
means is `src/edge/agent/internal/seglog`, with no caller yet.

Stop condition: the plan is wrong if a reader can see a sealed file name
before the rename that seals it has completed.

## Decisions

The parent's decisions apply
([parent plan](2026-10-04-2232-refactor-edge-delivery-over-connect-plan.md)).
This phase adds:

- A segment file starts with a 20-byte header: the magic `FSSL`, a
  little-endian `uint16` version of 1, a `uint16` of zero, the creation time
  as little-endian `int64` Unix nanoseconds, and a little-endian `uint32`
  CRC-32C of those 16 bytes. Why: a reader can refuse a file that is not a
  segment or is from a later format before it trusts a length.
- A record is a little-endian `uint32` payload length, a little-endian
  `uint32` CRC-32C of the payload, and the payload. An empty payload is
  refused on append. Why: a zero length then always means damage.
- The checksum is CRC-32C from `hash/crc32` with the `Castagnoli`
  polynomial. Why: it is in the standard library, so the package adds no
  module. `go doc hash/crc32.Castagnoli` names the polynomial, and RFC 3720
  appendix B.4 gives the test vector the tests pin.
- The active segment is `<seq>.open` and a sealed one is `<seq>.seg`, with
  `<seq>` as 16 lowercase hexadecimal digits. The `.open` file is created by
  the first `Append` after a seal, never ahead of it. Why: an idle log then
  holds no segment whose age runs while it is empty.
- Sealing is closing the file and renaming it. Neither the file nor the
  directory is synced at a seal. Why: the edge declares periodic fsync
  today (`src/edge/agent/internal/busattach/busattach.go`, `FsyncPolicy`),
  and with a reader waiting a quiet log seals up to five times a second. A
  sealed file a power cut left short is caught by its checksums.
- The active file is synced when `SyncInterval` has passed since its last
  sync, checked on `Append`, and on `Close`. An append is one `write` call
  of record header and payload with no buffer in the process. Why: a record
  then survives a process kill through the page cache.
- `Append` seals the active segment when the next record would take it past
  `SegmentBytes`, or when its header is older than `RollAge`. Why: the
  second rule bounds how much newer than its header a segment's last record
  can be, which is the error of the age bound below.
- `MaxRecordBytes` bounds one record, and a larger one is refused with
  `agent/seglog-record`. It may not exceed `SegmentBytes` less the 28 bytes
  of one file header and one record header. Why: a segment is one delivery
  call, and central bounds an edge call's body
  (`src/services/device/internal/host/host.go`, `maxEdgeBody`). A record
  that fits no call would block its log for good.
- The package starts no goroutine. `Next` waits in its caller. Why: the
  panic gate allows a `go` statement only in `src/common/spawn`, and
  nothing here needs one.
- `Next` returns the oldest sealed segment and returns the same one until
  it is acknowledged. With none sealed, it seals the active segment once
  its header is `Linger` old. Why: an idle link then delivers within
  `Linger`, and a busy one fills segments.
- The package reads `time.Now` and waits on `time` timers directly, with no
  clock in `Config`. Why: `docs/code-style.md` asks for a
  `testing/synctest` bubble for code that reads the clock, and a bubble
  drives both the age checks and the `Linger` wait from one clock.
- A segment's age is its header's creation time. Bounds discard whole
  sealed segments, oldest first, on `Append` and on `Next`. Why: deleting a
  file is the only reclaim step, so there is no compaction to get wrong. A
  record can be discarded up to `RollAge` before it is `MaxAge` old.
- `Ack` of a segment the bounds already discarded returns nil. Why: the
  reader had the records in memory and delivered them.
- `Ack` removes the file and does not sync the directory. Why: a segment
  that reappears after a power cut is delivered again. Central drops a
  repeated ingest record by message id inside its ten-minute duplicate
  window (`src/modules/edgebus/hub.go`), and a repeated telemetry body is
  accepted as one duplicated export.
- `Open` reads every segment once. A sealed segment with a bad record
  keeps its valid prefix and is counted in `Stats.CorruptSegments`. A torn
  tail in an `.open` file is truncated, counted in `Stats.TruncatedBytes`,
  and the file sealed. A file with a bad header is removed and counted as
  corrupt. A file whose name is not a segment name is left alone. Why:
  damage is found at start and never in the middle of a drain, at the cost
  of reading up to `MaxBytes` once.
- Defaults: `MaxBytes` 256 MiB and `MaxAge` 7 days, the values of
  `defaultBufferBytes` and `defaultBufferAge` in
  `src/modules/edgebus/leaf.go`. `SyncInterval` 5 s, the value of
  `defaultBusFsyncInterval` in `src/common/service/bus.go`. `SegmentBytes`
  512 KiB, half of `maxEdgeBody`, so a segment and its envelope fit one
  call. `MaxRecordBytes` 256 KiB. `Linger` 200 ms, the delay an idle link
  adds to one record. `RollAge` 10 minutes, which caps a seven-day log at
  about a thousand files.
- `MaxBytes` below twice `SegmentBytes` and `RollAge` above half of
  `MaxAge` are refused with `agent/seglog-config`. Why: the byte bound then
  always finds a sealed segment to discard, and the age bound's error stays
  under half of the bound.
- The package emits no telemetry. `Stats` returns the numbers and the
  third phase turns them into metrics. Why: the observability convention's
  names belong to the module that owns the buffer's meaning.

## Requirements

1. Records come back in append order across a reopen. Example: append
   `a`, `b`, `c`, close, open, and `Next` returns one segment holding `a`,
   `b`, `c`.
2. The file layout is the one under Decisions. Example: a header with
   creation time Unix nanosecond 1 followed by one record `abc` is these 31
   bytes: `46 53 53 4c 01 00 00 00 01 00 00 00 00 00 00 00`,
   `80 eb b0 b1`, `03 00 00 00`, `b7 3f 4b 36`, `61 62 63`.
3. The checksum is CRC-32C. Example: the checksum of 32 zero bytes is
   stored as `aa 36 91 8a` (RFC 3720, appendix B.4).
4. A torn tail costs only the torn record. Example: an `.open` file cut in
   the middle of its third record reopens with two records, and
   `Stats.TruncatedBytes` is the length of the cut piece.
5. A damaged sealed segment keeps its prefix and is counted. Example: with
   a byte flipped in the second of three records, `Next` returns one record
   and `Stats.CorruptSegments` is 1.
6. The byte bound discards the oldest sealed segment first. Example: with
   `SegmentBytes` 64 KiB, `MaxRecordBytes` 4 KiB, `MaxBytes` 128 KiB, and
   256 KiB appended in 1 KiB
   records, the directory holds at most 128 KiB, the first record `Next`
   returns is not the first appended, and `Stats.DiscardedRecords` is above
   zero.
7. The age bound discards a sealed segment older than `MaxAge` and keeps
   younger records. Example: with `MaxAge` 1 h and `RollAge` 10 min, a
   record appended at 0 min and one at 55 min, an `Append` at 61 min leaves
   the first record discarded and the second returned by `Next`.
8. `Next` blocks on an empty log and seals after `Linger`. Example: inside
   a `synctest` bubble with `Linger` 200 ms and one record appended, `Next`
   returns it when the bubble's clock has advanced 200 ms. With no record,
   `Next` returns the context's error when the context ends.
9. `Next` repeats until `Ack`. Example: two calls without an `Ack` return
   the same sequence number, and after `Ack` the file is gone.
10. `Append` is safe for concurrent callers. Example: 8 goroutines
    appending 1,000 records each under `-race` leave 8,000 records.
11. A record over `MaxRecordBytes` is refused and the log keeps working.
    Example: an `Append` of `MaxRecordBytes` plus one byte returns
    `agent/seglog-record`, and the next `Append` of one byte succeeds.

## Out of scope

- Any caller. The third phase wires the syslog source, the OTLP receiver,
  and the drains.
- A lock against two processes opening one directory. The agent's state
  directory has one owner.
- Compression and encryption of segment files.
- Hostile input. The package reads only files it wrote, in a directory the
  agent owns, so a crafted file is damage and is handled as damage.

## Units

### U1. Segment format

Files: src/edge/agent/internal/seglog/format.go, src/edge/agent/internal/seglog/format_test.go
After: none
Change: `format.go` encodes a segment header for a given creation time and
a record into a caller's buffer. It scans a segment's bytes into its
creation time, its records, and the offset of the first byte it could not
accept, with the reason (short header, bad magic, unknown version, header
checksum, zero length, length past the end, payload checksum). It names the
two file suffixes and parses a file name into a sequence number and a
sealed flag. Error codes are `agent/seglog-config`, `agent/seglog-io`,
`agent/seglog-record`, and `agent/seglog-closed`, built with
`errs.NewCode` as `src/edge/agent/internal/identity/store.go` does.
Tests: `format_test.go` holds the 31-byte case of requirement 2 with every
byte written as a literal, checksums included. The two checksum literals
were worked with a bitwise CRC-32C outside the package, which also
reproduced the RFC 3720 vector. It holds that vector as requirement 3. It
has one case per scan reason above, each asserting the offset and the
records before it. It has a name-parsing table with a sealed name, an open
name, a name with 15 digits, an uppercase name, and `hub.creds`.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/edge/agent/internal/seglog`

### U2. Log, bounds, and reader

Files: src/edge/agent/internal/seglog/log.go, src/edge/agent/internal/seglog/doc.go, src/edge/agent/internal/seglog/log_test.go, src/edge/agent/internal/seglog/bench_test.go, src/edge/agent/README.md
After: U1
Change: `Open` takes a `Config` (`Dir`, `MaxBytes`, `MaxAge`,
`SegmentBytes`, `MaxRecordBytes`, `RollAge`, `Linger`, `SyncInterval`),
applies the defaults, creates the directory with mode 0700, recovers as
the Decisions say, and returns a `Log`. `Append` writes one record, seals
by size and by `RollAge`, and applies both bounds. `Next` and `Ack` behave
as the Decisions say, and `Next` returns a `Segment` with its sequence
number and its records. `Stats` returns the segment count, the record
count, the bytes on disk, the age of the oldest segment, and the counters
`DiscardedRecords`, `DiscardedBytes`, `CorruptSegments`, `CorruptBytes`,
and `TruncatedBytes`. `Close` syncs and seals the active segment and wakes
a blocked `Next`. Every method after `Close` returns
`agent/seglog-closed`. Files are created with mode 0600. `doc.go` holds
the package overview, the layout, and the recovery rules. The agent
README's package table gains a row for `internal/seglog` that says nothing
imports it yet.
Tests: `log_test.go` has one case for each of requirements 1 and 4 to 11.
The cases for requirements 7 and 8 run in a `testing/synctest` bubble. It
adds a file with a bad header that `Open` removes and counts, a foreign
file that survives `Open`, an empty record refused with
`agent/seglog-record`, each refused configuration, `Ack` of a discarded
segment returning nil, a `Next` blocked on an empty log returning
`agent/seglog-closed` when `Close` runs, every method after `Close`, the
0700 and 0600 modes read back with `os.Stat`, and an active segment that
rolls at `RollAge` with one record in it. A file-operation seam in an
unexported field records calls, and one case asserts a sync on the
`Append` that follows `SyncInterval` and none before it. Nothing here
cuts power: a sealed file left short after a power cut is covered only as
the damaged-segment case of requirement 5. `bench_test.go` has
`BenchmarkAppend` with 256-byte records and `BenchmarkAppendNextAck` for
the full cycle.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/edge/agent/internal/seglog src/edge/agent/README.md`

Waves: U1 | U2

## Verification

`go test -race ./src/edge/agent/internal/seglog/...` passes, and the
verifier is green for both paths. `go test -bench . -run '^$'
./src/edge/agent/internal/seglog/` runs, and the handoff reports the
records per second of `BenchmarkAppend` with the machine it ran on.

## Definition of done

- [ ] Verifier green for every changed path.
- [ ] `doc.go` states the layout and the recovery rules.
- [ ] The parent's `Landed:` line for this phase carries the commit range.
- [ ] This plan's `status` is set, with an outcome note under its title.
- [ ] No plan labels in code.

## Open questions

- Whether a rename within one directory is atomic on FreeBSD as on Linux.
  Unverified. `go doc os.Rename` says only that it is not atomic on
  non-Unix platforms, and the tree has no FreeBSD run. The agent's key
  store already relies on a rename
  (`src/edge/agent/internal/identity/store.go`).

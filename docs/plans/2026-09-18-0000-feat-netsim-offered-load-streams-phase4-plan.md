---
title: Offered-Load Streams Phase 4 - Capture File as a Stream Source - Plan
type: feat
date: 2026-09-18
artifact_contract: flowseer-plan/v1
artifact_readiness: implementation-ready
status: implemented
review: accept after fixes
execution: code
parent: docs/plans/2026-09-18-0000-feat-netsim-offered-load-streams-plan.md
---

# Offered-Load Streams Phase 4 - Capture File as a Stream Source - Plan

> Implemented. 2 units, 2026-09-24T18:20:11Z to 2026-09-24T18:29:01Z.

## Goal

An Ethernet pcap or pcapng file supplies frames to a fabric at a chosen origin,
starting at the attachment's epoch and retaining the capture's spacing. A
reader in `src/common/net/pcap` decodes records; an adapter in `stream` checks
and snapshots them as a `stream.Source`. This plan is wrong if the captures to
replay are predominantly Linux cooked captures (`LINKTYPE_LINUX_SLL2`, 276),
which lack the Ethernet header that `ethernet.Decode` requires.

## Decisions

- The parent's decisions and
  `docs/architecture/2026-09-18-offered-load-streams-direction.md` apply. The
  direction record remains proposed; this phase makes no change to it.
- `pcap.NewReader(io.Reader) (*Reader, error)` detects classic pcap or pcapng.
  Its `Next() (Record, error)` returns `io.EOF` only at a clean record boundary.
  `Record` contains `At time.Time`, owned `Data []byte`, `OrigLen uint32`,
  `LinkType uint16`, and `HasFCS bool` for metadata declaring an FCS. Link type
  belongs to each record because pcapng EPBs select IDBs. The reader uses no
  generated capture types. Its placement in `common` serves `stream` now and
  the phase 5 transmitter later. The existing `src/modules/capture/pcapng.Writer`
  stays in its module because it accepts protobuf records.
- Classic pcap support covers version 2.4 in both byte orders and with
  microsecond or nanosecond magic. The reader consumes the 24-octet file
  header and each 16-octet record header; the low 16 bits of the file header's
  link-type word identify the link type and upper bits can declare FCS. A
  fractional timestamp must be below one second in its selected units.
  [PCAP draft 09, sections 4-5](https://datatracker.ietf.org/doc/html/draft-ietf-opsawg-pcap-09)
  defines the layout.
- Pcapng support covers Section Header, Interface Description, and Enhanced
  Packet blocks in either byte order. A new SHB resets interface IDs and may
  change byte order. An EPB uses its selected IDB's link type, timestamp
  resolution (default 10^-6, decimal or binary `if_tsresol`), and signed
  `if_tsoffset` in seconds. The reader checks block alignment, repeated length,
  options, and packet padding before slicing. It skips metadata and unknown
  blocks but refuses Simple Packet and obsolete Packet blocks: silently
  discarding their frames would understate load. Conversion discards
  sub-nanosecond fractions and rejects timestamp overflow.
  [Pcapng draft 06, sections 3.1, 3.4, 4.1-4.4](https://datatracker.ietf.org/doc/html/draft-ietf-opsawg-pcapng-06)
  defines these fields.
- The reader refuses a captured length above 1 MiB before allocating packet
  bytes, checks it against a nonzero snapshot length, and reports truncated
  headers, blocks, or data as errors. It skips unknown block bodies with a
  bounded buffer rather than allocating their declared size. One MiB covers
  oversized Ethernet test frames while bounding allocation from an untrusted
  packet length. `go doc io.EOF` distinguishes clean completion from
  unexpected end of structured data.
- `stream.NewCaptureSource([]pcap.Record) (Source, error)` validates and copies
  the records before attachment. It accepts empty input as an exhausted
  source. It refuses link type other than 1 (naming the numeric type), unequal
  original and captured lengths, invalid Ethernet headers, explicitly declared
  FCS, decreasing timestamps, and offsets outside `time.Duration`. The first
  timestamp maps to zero; equal timestamps retain file order. `Clone` copies
  the cursor, and `Next` returns independent frame bytes. This snapshot keeps
  file I/O out of `stream` and makes all read errors visible before attachment;
  `Source.Next` has no error return. Checked offset conversion matters because
  `time.Time.Sub` saturates on overflow (`go doc time.Time.Sub`).
- Replay preserves source and destination MACs. FCS-bearing captures are
  refused because `ethernet.Decode` would treat the FCS as payload and
  `Frame.WireOctets` would count it twice. An unmarked capture is treated as
  FCS-free; its bytes cannot prove whether an FCS is present. The adapter does
  not guess or rewrite MACs.
- Tests use small hand-checked literal fixtures under `pcap/testdata/`, per
  `docs/solutions/conventions/a-codec-round-trip-cannot-locate-a-field-on-the-wire.md`.
  A little-endian microsecond pcap header starts `d4 c3 b2 a1 02 00 04 00`
  and ends `ff ff 00 00 01 00 00 00`; 1,700,000,000 seconds is
  `00 f1 53 65`, 250 microseconds is `fa 00 00 00`, and a 60-octet
  record length is `3c 00 00 00`. A frame begins
  `02 00 00 00 00 02 02 00 00 00 00 01 08 00`. A separate pcapng fixture
  pins `if_tsresol=9` and the little-endian EPB timestamp halves for
  1,700,000,000,000,000,000 nanoseconds to
  `fe 9c 97 17 00 00 2a 36`. The capture module's writer is an additional
  interoperability check, not the wire proof.

## Requirements

14. The reader returns bytes, original lengths, link types, and absolute
    timestamps of hand-checked classic and pcapng fixtures. For example, a
    classic record at 1,700,000,000 seconds and 250 microseconds yields
    `2023-11-14T22:13:20.000250Z` and the Ethernet prefix above; a pcapng
    `if_tsresol=9` record at 1,700,000,000,000,000,000 ticks yields
    `2023-11-14T22:13:20Z`. A truncated block is an error, not `io.EOF`.
15. A two-frame Ethernet capture 250 microseconds apart, converted to a source
    and attached at host `h1` with fabric start `t0`, injects at `t0` and
    `t0+250µs` with both MACs intact. A record with `OrigLen=64` and 60
    captured octets is refused while constructing the source, before
    `AttachStream`; a complete link type 276 record is refused with `276` in
    the error.

## Out of scope

- Non-Ethernet link types, captures that explicitly declare an FCS, and
  guessing FCS presence when metadata is absent.
- File-backed `Source` iteration: `Next` cannot report a later read error and
  `Clone` needs a stable cursor. The caller reads records before building the
  source; capture memory scales with the file's records, while fabric playback
  still pulls one frame at a time.
- Changes to capture protobufs, the pcapng writer's production API, fabric's
  stream attachment contract, or the phase 5 transmitter.

## Units

### U1. Decode capture records

Files: `src/common/net/pcap/doc.go`, `src/common/net/pcap/reader.go`,
`src/common/net/pcap/classic.go`, `src/common/net/pcap/ng.go`,
`src/common/net/pcap/reader_test.go`, `src/common/net/pcap/testdata/`,
`src/common/net/pcap/README.md`, `src/modules/capture/pcapng/writer_test.go`
After: none
Change: The reader returns one record at a time with the contract above,
tracks pcapng interfaces per section, and reports structural and timestamp
errors. The writer test reads a writer-produced file through the reader.
Tests: `reader_test.go` pins fixture field positions and times, all four
classic magic/byte-order combinations, big-endian pcapng and a second section,
per-interface decimal and binary resolutions, `if_tsoffset`, classic and
pcapng FCS declarations, unknown metadata, and refusals for bad trailer,
invalid interface ID, excessive length, captured length beyond snap length,
truncated data, and SPB/PB. The writer test checks EPB data and its default
microsecond timestamp. Each refusal input is otherwise valid, per
`docs/solutions/conventions/a-refusal-test-needs-an-input-only-the-refusal-rejects.md`.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/common/net/pcap/doc.go src/common/net/pcap/reader.go src/common/net/pcap/classic.go src/common/net/pcap/ng.go src/common/net/pcap/reader_test.go src/common/net/pcap/testdata src/common/net/pcap/README.md src/modules/capture/pcapng/writer_test.go`

### U2. Adapt records to a source and attach one

Files: `src/common/netsim/stream/capture.go`,
`src/common/netsim/stream/capture_test.go`,
`src/common/netsim/stream/README.md`,
`src/common/netsim/fabric/attach_test.go`
After: U1
Change: The adapter validates and copies records before returning a Source.
The stream README shows reading a file through `pcap.Reader`, building the
source, and attaching it with `fabric.StreamAttachment{Origin:
fabric.Endpoint{Node: "h1"}, Start: 0, Flow: 1}`. The adapter does not import
`fabric`; no fabric production change is needed.
Tests: `capture_test.go` proves zero first offset, 250-microsecond spacing,
stable exhaustion, independent clone cursors and frame bytes, preserved MACs,
empty input, and separate refusals for link type 276, truncation, FCS metadata,
bad Ethernet header, decreasing timestamps, and duration overflow.
`attach_test.go` loads a fixture, attaches its source at `h1`, runs the fabric,
and asserts both `Journey.Injection.At` values and frame MACs; it checks that
a truncated capture never reaches `AttachStream`.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/common/netsim/stream/capture.go src/common/netsim/stream/capture_test.go src/common/netsim/stream/README.md src/common/netsim/fabric/attach_test.go`

Waves: U1 | U2

## Verification

- `go test -race ./src/common/net/pcap ./src/modules/capture/pcapng ./src/common/netsim/stream ./src/common/netsim/fabric`
- Run each unit's diff-aware verifier on every changed path. Check the
  writer-produced pcapng with `capinfos` or `tshark` when installed; the
  literal fixtures remain the wire proof without those tools.

## Definition of done

- [x] The verifier passes for every changed path, and the package READMEs
      show the supported formats, replay example, and FCS limit.
- [x] This plan's `status` becomes `implemented` with an outcome note below
      its title after code lands; no plan labels enter code or commit messages.

## Open questions

None for implementation. Acceptance of the offered-load direction record is a
parent-plan decision and does not change this reader or adapter contract.

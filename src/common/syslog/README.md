# Syslog

`syslog` receives, parses, and sends messages without depending on an edge agent,
a core service, or a broker. It favors preserving evidence from imperfect devices:
receive time, device time, claimed hostname, and observed peer address remain separate.

```go
parser, err := syslog.NewParser(syslog.ParseOptions{CaptureRaw: true})
if err != nil {
    return err
}
record, err := parser.Parse(payload, syslog.Observation{ReceivedAt: receivedAt})
if err != nil {
    return err
}
// A device clock without a year or timezone has no invented absolute instant.
fmt.Println(record.DeviceTime.Original, record.DeviceTime.Instant)
fmt.Println(record.Observation.ReceivedAt, record.Vendor.Mnemonic.Value)
```

The complete [examples](example_test.go) run in `go test`. Standalone parsing never
reads the receiver clock. `Listen` records it immediately after the read that
completes a frame. Frames from the same read retain that time through a backlog.

## Records and compatibility

`Record` owns its data. Mutating input after `Parse`, calling `Next` again, or
closing the receiver cannot change a returned record. Byte fields within one
record may share storage; shallow record copies share slices. `Clone` makes an
independent copy. Caller-retained records are outside the receiver's memory budget.

`Content` preserves the full post-envelope payload, including a legacy process tag
or vendor prefix. Parsed vendor fields supplement that content. `OriginalSD` keeps
structured-data bytes, including malformed syntax; `Unparsed` retains unresolved
regions. Structured-data elements and parameters preserve order and duplicates.
Malformed payloads return a partial or unknown record, while size/configuration
failures return errors. Diagnostics have finite codes and bounded counts.

`Field.Presence` distinguishes absence, RFC 5424 NILVALUE, and present text, including
numeric zero. Identifier text retains leading zeros. `_raw` is absent by default;
when enabled it contains exact payload bytes without framing. JSON encodes byte
fields as base64, preserving invalid UTF-8 and NUL. Parsed headers are textual;
use byte fields for exact wire evidence.

The [corpus manifest](testdata/corpus/manifest.json) links each fixture to its
source and labels synthetic envelope additions. These are documented grammar
tests, not firmware interoperability or packet-capture claims.

| Family | Interpretation |
| --- | --- |
| RFC 5424 | Versioned headers, NILVALUE, ordered/escaped structured data, binary MSG |
| Legacy/BSD | Optional PRI/origin, uppercase months, year-bearing and ISO clocks |
| NETGEAR M4300 / D-Link DWS | Component, thread, source location, sequence before `%%` |
| D-Link DGS | `POE:` and `ZONEDEFENSE:` body prefixes |
| Cisco IOS / IOS-XE / NX-OS | Percent prefix, severity, mnemonic, counters, clock markers, uptime, slot prefix |
| Cisco ASA | Percent prefix with numeric event ID; ISO legacy envelope |
| Huawei / Comware | Slash prefix, module, severity, mnemonic, optional flags |
| RUCKUS FastIron / ICX | Standard legacy and structured envelopes |
| HP ProCurve / ArubaOS-Switch | Numeric event ID and subsystem |
| Aruba AOS-CX | Process tag and `Event` pipe fields, including empty positions |
| Extreme EXOS | Priority/event tags and configurable numeric dates |
| Extreme SLX / VOSS | Standard/legacy envelopes; unknown structured data retained |

`ParseOptions.Year`, `Location`, and `ZoneOffsets` provide explicit clock context.
The record marks inferred components. DST gaps/folds and unmapped abbreviations
remain unresolved. Numeric dates require `NumericDateOrder: "dmy"` or `"mdy"`.
Cisco leading counters remain in `Vendor.Counters` unless
`CiscoCounterOrder` names their roles
(`sequence`, `counter`). Extra percent-prefix components remain ordered evidence;
`CiscoComponentCount` supplies the expected profile shape without asserting identity.

## Embedding and pressure

Create `Listen(ctx, []ListenConfig, ReceiverOptions)` with UDP, TCP, or TLS endpoints.
Binding is atomic: a failed endpoint closes earlier bindings. `Addresses` reports
assigned ephemeral ports. TLS requires a certificate configuration; senders verify
trust and hostname. `Observation.Authenticated` means a receiver verified a client
certificate. Plain TCP and UDP never assert authentication.

Call `Next(ctx)` when downstream has capacity. One call may be active; another
returns `ErrBusy`. UDP drops new datagrams when admission is full. TCP/TLS stop
reading until capacity is released, subject to `PressureTimeout`. There is no
persistent queue, retry spool, source-indexed history, or NATS dependency. A future
agent can place its NATS publication after `Next` and own durable buffering there.

```go
for {
    record, err := receiver.Next(ctx)
    if err != nil {
        return err
    }
    if err := publish(ctx, record); err != nil {
        return err
    }
}
```

A canceled `Next` preserves an unclaimed frame; after claiming, it finishes that
bounded parse and returns the record. `Close` cancels waiters, closes raw sockets
(including TLS sockets), discards queued frames, and waits for workers. A terminal
listener error ends all listeners; a peer error affects its connection only.

TCP defaults to per-frame detection: a digit-leading octet count or an LF-delimited
payload starting with `<`. Other legacy payloads require explicit `LF`, `CRLF`, or
`NUL` framing. TLS defaults to octet counting. LF preserves a preceding CR; CRLF
removes the pair. UDP is one whole datagram, including embedded newlines or NULs.
Bad lengths or incomplete/oversized stream frames close the connection. Malformed
message contents do not damage the next independently framed message.

## Resource limits

Zero limit fields select these defaults. Invalid or overflowing configurations
fail before binding or allocation.

| Resource | Default |
| --- | --- |
| Payload | 64 KiB |
| Listeners / stream connections / active TLS handshakes | 8 / 64 / 8 |
| Partial plus queued frames | 256 |
| Protocol reservation budget | 32 MiB |
| Stream read scratch / UDP datagram scratch | 4 KiB per stream / 64 KiB per UDP listener |
| Structured-data elements / total parameters / metadata allowance | 64 / 256 / 32 KiB |
| Diagnostics / vendor components | 16 / 32 |
| Handshake / absolute partial-frame / idle / pressure timeout | 5s / 10s / 120s / 30s |
| Send timeout | 5s |

Every frame reserves its full payload capacity before allocation. Read buffers,
queue entries, connection slots, and fixed state have startup or per-connection
reservations. Parser/result headroom is reserved at startup and cannot be consumed
by queued frames. Consequently `Next` can drain a full queue. There is no pool
whose retained capacity escapes accounting. `Stats.ReservedBytes` includes these
reservations and returns to zero after `Close`; it is an allocation allowance,
not a claim that every reserved byte is currently live.

The process budget also needs TLS/X.509 allocations, goroutine stacks, kernel
socket buffers, caller TLS configuration/caches, and retained returned records.
Those terms cannot be equated to the 32 MiB protocol limit. Connection and
handshake counts bound TLS concurrency, while the pinned Go runtime caps TLS
record and handshake message sizes. Caller callbacks must honor cancellation and
caller caches must be finite. Config clones do not deep-copy caller certificate
pools or callback state. See the [measurement and TLS worksheet](../../../docs/benchmarks/syslog.md).

Counters have fixed cardinality. `Received`, `Queued`, and `Delivered` are totals;
`ReservedFrames` is current partial/queued occupancy. `UDPDropped` reports observed
library admission drops; network and kernel losses before receipt are unknown.
`Oversized`, `FramingErrors`, `HandshakeErrors`, `ConnectionRejected`, and
`ShutdownDiscarded` distinguish other failure paths.

## Sending and losses

`Encode(record, EncodeOptions)` performs bounded validation and measurement before
allocating output. RFC 5424 is version 1; RFC 3164 defaults to its 1024-byte limit,
with an explicit larger compatibility limit available. Missing PRI is an error.
Legacy output requires known month/day/clock and hostname. RFC 5424 can express
missing fields using NILVALUE. Neither format borrows receive time.

`EncodeReport.Losses` identifies information requiring explicit `AllowLoss` flags:
year, zone, precision, structured data, unresolved evidence, unsupported header
fields, clock-quality evidence, and vendor counters outside preserved content.
`Omitted` describes expected local-only reception, raw, and diagnostic metadata.
Encoding rejects header injection, invalid SD names/duplicates, invalid declared
UTF-8, size overflow, and delimiter collisions without truncating bytes. Caller
hostname/time overrides affect only that encoding operation.

`NewSender` performs no network I/O. `Send` preflights encoding/framing, lazily
connects, and reuses a healthy connection. It accepts one active call and stores
no pending work. Failed writes close the connection without replay; only a new
caller invocation can reconnect. `NotSent`, `Written`, and `UnknownDelivery` describe
local write evidence, not acknowledgments. `Close` interrupts the active operation.

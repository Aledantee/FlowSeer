---
title: Streaming MIB walk measurements
date: 2026-09-03
---

# Streaming MIB walk measurements

Selecting two columns from a synthetic 100,000-row, twenty-column table reduced
first-row retrieval from 40,001 requests to one. Stopping after 100 rows needed
two requests. The full selected walk needed 2,001 requests. These are loopback
measurements on an Apple M4 Pro (darwin/arm64), not Cisco WLC validation.

## Reproduce

From `src/protocol/snmp/bench/`:

```sh
go test -run '^$' -bench 'BenchmarkTableWalk($|Scale)' -benchtime=3x -benchmem
go test -run '^$' -bench '^BenchmarkTableWalk$' -benchtime=100x -benchmem -count=5
go test -run '^$' -bench 'BenchmarkBulkWalk($|Streaming)' -benchtime=100x -benchmem -count=3
FLOWSEER_MEASURE_HEAP=1 go test -run '^TestStreamingRetainedHeap$' -v
go test -tags snmp_bench_netsnmp -run '^TestNetSnmpNativeSmoke$' -bench '^BenchmarkNetSnmpTraversal$' -benchtime=100x -count=3
```

The parent is commit `7185cd0`: current test layout plus the earlier binding
correctness fixes, before streaming. Both implementations used the same new
`responder_test.go` and `tablewalk_test.go`. The two matrix runs ran sequentially.
Fixture construction and session warmup are outside timed loops. The consumer
counts rows without retaining them. B/op includes responder allocations in these
in-process throughput benchmarks; it is total allocation traffic, not retained
client memory. Three iterations give directional timings, not confidence intervals.

The responder contains twenty readable IF-MIB columns, including both selected
counters, with 32-byte descriptions. It emits RFC-style interleaved repeater
responses and truncates at 7,000 bytes of varbind data, including partial
repetitions. Without that limit, macOS rejected its 10,085-byte UDP response with
`sendto: message too long`. The same bound applies to both implementations.

## Selected-column cost

Each cell shows parent -> streaming. Time is milliseconds; wire bytes include
requests and responses. Full raw outputs also include allocations and the wider
selection matrix.

| Rows | Stop after | Time (ms) | Requests | Wire bytes |
| ---: | ---: | ---: | ---: | ---: |
| 50 | all | 1.998 -> 0.233 | 21 -> 2 | 20,928 -> 3,590 |
| 50 | 1 | 1.504 -> 0.120 | 21 -> 1 | 20,928 -> 1,794 |
| 50 | 100 | 1.473 -> 0.234 | 21 -> 2 | 20,928 -> 3,590 |
| 1,000 | all | 17.721 -> 2.029 | 401 -> 21 | 431,201 -> 41,242 |
| 1,000 | 1 | 17.498 -> 0.094 | 401 -> 1 | 431,201 -> 1,794 |
| 1,000 | 100 | 17.471 -> 0.187 | 401 -> 2 | 431,201 -> 3,590 |
| 10,000 | all | 173.123 -> 12.191 | 4,001 -> 201 | 4,340,801 -> 400,882 |
| 10,000 | 1 | 171.325 -> 0.061 | 4,001 -> 1 | 4,340,801 -> 1,794 |
| 10,000 | 100 | 173.631 -> 0.127 | 4,001 -> 2 | 4,340,801 -> 3,590 |
| 100,000 | all | 1793.543 -> 112.503 | 40,001 -> 2,001 | 45,882,164 -> 4,302,328 |
| 100,000 | 1 | 1786.343 -> 0.062 | 40,001 -> 1 | 45,882,164 -> 1,794 |
| 100,000 | 100 | 1792.665 -> 0.130 | 40,001 -> 2 | 45,882,164 -> 3,590 |

With twenty selected columns, first-row retrieval needed two requests at every
size, satisfying `ceil(selected columns / 10)`. Truncation makes later queues
drain at different rates: the 100,000-row full walk needed 12,000 requests rather
than the ideal rectangular-batch count. No cursor intentionally traversed an
unselected column; terminal responses may contain discarded spillover.

## Throughput and allocation tradeoffs

The 100,000-row, twenty-column full walk took 1.824 -> 0.975 seconds. Total process allocation traffic was 972.3 -> 1230.8 MB. Streaming reduces retained state but does not promise fewer total allocated bytes when selecting every column.

On the fifty-row, twenty-column fixture, time was 1.451 -> 1.137 ms, while wire traffic rose 20,928 -> 32,380 bytes. Independent cursors can each overshoot their column on the final batch; selecting all columns of a small table therefore increases discarded spillover. Lower repetition counts are available through `WalkWithOptions`.

The original fifty-row control selects two counters while only the input counter exists. Across five 100-iteration runs, median time was 160.1 -> 97.9 microseconds (-38.8%). Median B/op was 88,670 -> 82,979; allocations/op rose 1062 -> 1667. The planned 20% throughput-regression threshold did not trigger.

## Retained memory and queue bound

The heap test runs the responder and its MIB in a child process. The client
warms a session, forces two collections to expire `sync.Pool` victims, and records
HeapAlloc. During a full walk it forces GC at rows 1 through 101 and every 1,000
rows thereafter, subtracting that baseline from each sample. It discards output
rows. This measures sampled retained client-heap growth, including pinned response
frames and reactor state; runtime bookkeeping makes it an estimate rather than an
exact sum of walker objects.

| Table rows | Peak retained heap delta |
| ---: | ---: |
| 1,000 | 30,016 bytes |
| 100,000 | 19,496 bytes |

The large-table value is below twice the small-table value. An initial attempt
used one GC and produced an invalid zero baseline delta as warmed pool storage
expired during measurement; the two-GC baseline above corrects that method.

The structural check uses three columns, seven repetitions, two disjoint populated
ranges and one absent column. At both 1,000 and 100,000 rows per populated column,
queue high-water was 14 cells, below the 21-cell bound. The advanced column stays
paused while the earlier range drains. Consumed slots are zeroed; empty queues
release their backing arrays, and copied cursors cannot pin old response frames.
Frames shared by several column queues live until the last referencing queue
entry is consumed, but the number of such batches is bounded by column count.
The bound excludes caller-retained results and varies with payload and selection
width. `TestStreamingSparseWire` additionally covers disjoint indexes, a missing
first cell, an absent column, and 8/512-byte payloads over real UDP.

## Client traversal comparison

These arms count 100 varbinds over the same fifty-row fixture and use 50
repetitions. They do not assemble typed rows. Medians of three 100-iteration runs:

| Client operation | Time (microseconds) | B/op | Allocs/op |
| --- | ---: | ---: | ---: |
| flowseer | 180.7 | 63,131 | 1,274 |
| gosnmp | 133.0 | 111,486 | 2,566 |
| flowseer-raw | 127.1 | 69,525 | 931 |
| gosnmp-callback | 119.8 | 99,535 | 2,558 |
| Net-SNMP cgo traversal | 201.0 | C heap unmeasured | C heap unmeasured |

The `gosnmp` arm uses `BulkWalkAll`; `gosnmp-callback` uses `BulkWalk` without
collection. Net-SNMP uses the existing in-process `snmp_sess_synch_response`
wrapper. Its C allocations cannot be compared with Go benchmark memory columns.
Neither traversal result establishes typed-row performance for another client.

## API migration

Existing generated `Walk(ctx, sess, cols...)` calls compile unchanged. Selection
now controls retrieval and row membership: no columns means no I/O, and rows
visible only through unselected columns disappear. Request a readable identity
column when a census matters. Rows sort by numeric OID suffix; `Row.Index` and
observation bits remain populated. Unknown/foreign descriptors fail before I/O;
duplicates are ignored. `WalkWithOptions` adds request sizing and call controls.

Iteration is lazy, single-use and single-consumer. `Close` is safe concurrently.
Early stop succeeds; parent cancellation reports its context error. Typed decode
errors omit the failing row and later rows while preserving delivered rows. An
empty/no-progress response receives one singleton GETNEXT recovery attempt and
then reports `ErrWalkNoProgress`. SNMPv1 remains unsupported. `Session` is unchanged,
but alternate implementations must now implement real `GetBulk`/`GetNext` behavior.

Generated `Watch` still uses its separate full-table collector and persistent
snapshot. These measurements and bounds apply to generated `Walk` only.

## Raw results

- [parent](2026-09-03-streaming-mib-walks-data/parent.txt)
- [streaming](2026-09-03-streaming-mib-walks-data/streaming.txt)
- [control-parent](2026-09-03-streaming-mib-walks-data/control-parent.txt)
- [control-streaming](2026-09-03-streaming-mib-walks-data/control-streaming.txt)
- [clients](2026-09-03-streaming-mib-walks-data/clients.txt)
- [heap](2026-09-03-streaming-mib-walks-data/heap.txt)
- [queue-bound](2026-09-03-streaming-mib-walks-data/queue-bound.txt)
- [netsnmp](2026-09-03-streaming-mib-walks-data/netsnmp.txt)

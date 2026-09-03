# Syslog measurements

These measurements describe the initial shared library on an Apple M4 Pro,
Darwin/arm64, Go 1.27.1, with 12 logical execution contexts. They are local
microbenchmarks and loopback tests, not device interoperability claims or
hardware-independent ingestion targets.

## Reproduction

From the repository root:

```sh
go test -race ./src/protocol/syslog/...
go test ./src/protocol/syslog -run '^$' -fuzz '^FuzzParse$' -fuzztime 60s
go test ./src/protocol/syslog -run '^$' -fuzz '^FuzzFrame$' -fuzztime 60s
go test ./src/protocol/syslog -run '^$' -fuzz '^FuzzEncode$' -fuzztime 60s
go test ./src/protocol/syslog -run '^$' -bench . -benchmem -benchtime=100ms -count 10
go -C src/protocol/syslog/bench test -run '^$' -bench . -benchmem -benchtime=100ms -count 10
FLOWSEER_SYSLOG_OVERLOAD=1 go test ./src/protocol/syslog/test/integration -run '^TestOverload$' -count 1 -timeout 12m -v
```

The ten-minute measurement is explicitly enabled so normal package tests do not
sleep for ten minutes. The timeout includes startup and cleanup. The package test
suite still runs short overload, ownership, connection, framing, and lifecycle
checks without that environment variable. `ps` supplies RSS when available; its
absence does not substitute a heap metric for RSS.

The comparison dependency lives only in the nested `bench` module. It is pinned
to `github.com/leodido/go-syslog/v4` commit
`3fd54b1cd30355a667471a294fa4eb981c4eaea1`. Both parsers receive the same valid
RFC 5424 message with structured data. Their output models differ: FlowSeer also
owns observation, diagnostics, original structured data, and vendor metadata.
The comparison does not treat rejection of vendor messages as faster parsing.

## Memory experiment

Four receivers ran simultaneously for ten minutes, each with UDP, TCP, and TLS
listeners. Profiles paired default limits and a small 2 MiB budget with raw capture
off/on. The small profile used 4 KiB payloads, eight frames, four connections, and
two handshakes. Producers continuously sent 1 KiB bodies and reconnected stalled
streams. TLS used mutual authentication and certificates containing a 120 KiB
extension. Consumers stopped for five minutes, then consumed at 50 records/second
per profile. Clients are part of this process and therefore part of the measured
heap and RSS.

After a one-minute warmup, the test sampled post-GC live heap every ten seconds.
The first steady-state one-minute median was 37,094,064 bytes. The final three
medians were 39,009,784, 38,616,760, and 38,944,280 bytes. Each remained below
40,803,470 bytes, the baseline plus its 10% allowance. RSS reached approximately
99.1 MiB; it exceeded live heap because it includes allocator retention, stacks,
clients, TLS, and other process state. Production does not force GC for pressure.

| Profile | Received | Delivered during slow phase | UDP admission drops | Final reserved bytes | Limit |
| --- | ---: | ---: | ---: | ---: | ---: |
| Default, raw off | 612,063 | 15,000 | 596,807 | 17,235,456 | 33,554,432 |
| Default, raw on | 612,122 | 15,000 | 596,866 | 17,219,072 | 33,554,432 |
| Small, raw off | 611,940 | 15,000 | 596,932 | 254,656 | 2,097,152 |
| Small, raw on | 612,050 | 15,000 | 597,042 | 254,656 | 2,097,152 |

Reservations were checked against each profile's limit at every sample and reached
zero after Close. Default profiles saturated 256 frame slots; the small ones
saturated eight. Separate tests exercise connection rejection, stalled handshakes,
absolute partial-frame deadlines, canceled consumers, and 100 start/stop cycles.
The lifecycle test checks return to the goroutine baseline within one second.

This long run used the implementation before the final owned-string reuse fix.
That fix removes payload-sized copies from parsing, and its allocation reduction
is measured below. Listener startup validation and shutdown reference cleanup were
also tightened afterward and covered by the final race suite. The long-run result
is evidence for the more allocation-heavy baseline, not a claim of a second run.

## First-byte admission and escaped structured data

A second ten-minute run uses first-byte frame admission, bidirectional TLS
receive deadlines, and escaped structured-data values in every message. The
same four profiles and stopped/slow consumer phases apply. The test requires
at least 1,000 delivered records per profile and reports unexpected `Next`
errors, so a stalled receiver cannot pass solely through a flat heap.

The baseline one-minute median was 37,144,032 bytes. The final three medians
were 39,140,080, 39,168,904, and 39,397,328 bytes, below the 40,858,435-byte
threshold. Peak sampled RSS was 99.5 MiB. Reservations stayed within the
configured limits at every sample and reached zero after shutdown.

| Profile | Delivered | UDP admission drops | Pressure closures | Final reserved bytes | Limit |
| --- | ---: | ---: | ---: | ---: | ---: |
| Default, raw off | 15,000 | 597,530 | 2,930 | 17,243,648 | 33,554,432 |
| Default, raw on | 15,000 | 597,892 | 2,929 | 17,227,264 | 33,554,432 |
| Small, raw off | 15,000 | 597,896 | 2,328 | 254,656 | 2,097,152 |
| Small, raw on | 15,000 | 597,736 | 2,420 | 254,656 | 2,097,152 |

Pressure closures count connections, not unread messages. The raw
[samples](syslog/overload-admission.txt) include connection rejection counts
and each heap observation. Separate regressions cover blocked TLS alert writes,
idle TCP peers sharing capacity with UDP, and frame deadlines during admission.

## Protocol and TLS worksheet

Let `P` be MaxPayload and `E` MetadataBytes. Startup reserves `3P + 3E +
32*MaxDiagnostics + 8192` bytes for parsing/result construction. It additionally
reserves the actual queue entry size times MaxFrames, 32 bytes per connection slot,
1024 bytes per listener, and 64 KiB scratch per UDP listener. Every stream reserves
8192 bytes for its 4 KiB read buffer and connection state. Each queued or partial
frame reserves its full `P` capacity. These allowances are conservative capacities;
they are not a count of live heap objects. `MaxBytes` rejects further reservations.

Go 1.27.1's `crypto/tls/common.go` limits plaintext records to 16,384 bytes,
ciphertext records to 18,432 bytes (16,640 for TLS 1.3), ordinary handshake messages
to 65,536 bytes, and certificate messages to 262,144 bytes. `conn.go` checks those
lengths before accepting record or handshake expansion and limits consecutive
non-advancing records to 16. `crypto/x509/verify.go` limits chain signature checks
to 100. These are input/work limits, not heap limits.

TLS retains record buffers, transcript state, parsed certificates, verified chains,
and crypto objects. X.509 decoding can expand strings and slices beyond the DER
size. Those allocations are outside the protocol reservation budget and are
bounded by runtime input limits and the configured connection/handshake counts.
Use this process worksheet when embedding:

```text
protocol reservations
+ established TLS connections * measured retained TLS/X.509 state
+ simultaneous handshakes * measured handshake expansion
+ library goroutines * runtime stack capacity
+ kernel socket buffers
+ caller configuration/caches and retained records
```

The 120 KiB mutual-certificate workload, repeated connection rejection, and stalled
handshake test exercise larger TLS state than the normal loopback case. They do
not establish an exact worst-case RSS for every certificate encoding or runtime.
Re-measure for a new Go toolchain or materially different certificate policy.
Caller TLS callbacks must honor cancellation and caches must have finite bounds;
static certificate pools remain caller-owned. Raw socket closure interrupts TLS
reads and alert writes without depending on TLS close-notify completion.

## Operational use

No service is wired to this package in this change. An embedding service should
watch its receiver's reserved bytes/frames, active connections/handshakes, UDP
drops, pressure closures, and framing/handshake errors. Correlate those with downstream publication
latency; sustained full occupancy means the caller or broker path is slower than
arrival traffic. Preserve partial records for compatibility investigation, and
capture raw messages only where that retention is intended.

## Performance baseline and retained change

Median results from ten 100 ms samples per case, using the 24-fixture corpus
from the initial measurement. The current corpus adds three timestamp regressions;
these historical mixed-corpus timings do not include them:

| Workload | ns/message | messages/second | MB/second | bytes allocated/message | allocations/message |
| --- | ---: | ---: | ---: | ---: | ---: |
| Mixed corpus, raw off | 324.00 | 3,086,420 | 172.82 | 239 | 5 |
| Mixed corpus, raw on | 329.35 | 3,036,284 | 170.04 | 239 | 5 |
| RFC 5424 encode | 433.75 | 2,305,476 | 149.87 | 360 | 6 |
| 60 KiB tagged vendor | 9,298.50 | 107,544 | 6,459.66 | 131,289 | 6 |
| 24 KiB structured-data value | 37,102.00 | 26,953 | 648.32 | 162,704 | 27 |
| TCP loopback receive and parse | 18,572.50 | 53,843 | 2.16 | 66,491 | 14 |

The committed pre-optimization baseline is `e35c9dd`. Reusing the owned input
string in legacy tag and vendor extraction reduces the 60 KiB case from 262,362
to 131,289 allocated bytes and from eight to six allocations. Its median time
drops from 15,624 to 9,298.5 ns. The mixed corpus drops from seven to five
allocations per message. No unchanged benchmark case increased allocations or
regressed more than 10% in median time across the paired runs. Every fixture
still passes in both raw modes. This change also restores the parser's reserved
construction allowance for profiles with a small metadata limit.

The final conservative Cisco-counter handling was measured separately after that
paired comparison; those final mixed-corpus samples are used in the table. A
counter without explicit interpretation context is now diagnosed and retained
without assigning a sequence role. The extra diagnosis is intentional behavior,
not a narrower compatibility path.

The common RFC 5424 subset measured 302.65 ns/message, 376 bytes and 12 allocations
for FlowSeer, versus 407.10 ns/message, 896 bytes and 15 allocations for the pinned
comparand. The test checks matching header, timestamp, message, and SD fields.
This single subset does not imply a general speed ranking across all formats.

The TCP loopback result includes socket scheduling and the finite full-capacity
frame buffer. It is substantially slower and allocates more than standalone
parsing. Large structured data also allocates more while decoding values. These
are measured baselines; no zero-allocation or wire-rate claim is made.

Raw samples: [before](syslog/before.txt), [after](syslog/after.txt),
[final counter semantics](syslog/final-counters.txt),
[common subset](syslog/comparison.txt), and [overload](syslog/overload.txt).

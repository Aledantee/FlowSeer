---
title: A pcapgo fixture writer stamps the wall clock on every packet unless the generator sets a Timestamp
date: 2026-10-15
last_verified: 2026-10-15
category: conventions
module: src/edge/netpen/attacks
problem_type: convention
component: test_fixtures
severity: low
applies_when:
  - "Writing or editing a `harvest_*.go` fixture generator that writes pcaps through `pcapgo.Writer`"
  - "A change meant to regenerate one packet fixture shows every pcap in the package as modified testdata, and the ones that did not change have to be restored by hand"
  - "Reviewing a diff whose regenerated fixtures differ only in their per-packet headers"
related_components: [netpen, packet_capture]
tags: [gopacket, pcapgo, test-fixtures, harvest-generator, netpen]
---

# A pcapgo fixture writer stamps the wall clock on every packet unless the generator sets a Timestamp

## The situation

The four netpen fixture generators write their pcaps through `pcapgo.Writer`.
`harvest_ip6.go` sets `Timestamp: time.Now()` on each packet
(`src/edge/netpen/attacks/ip6/harvest_ip6.go:732-736`). The l2, fh, and
routing generators set no stamp at all
(`attacks/l2/harvest_l2.go:633-636`, `attacks/fh/harvest_fh.go:646-649`,
`attacks/routing/harvest_routing.go:525-527`). All four behave the same way:
`go run harvest_ip6.go` and `go run harvest_l2.go` each rewrite every pcap
under their `testdata/` directory, and `git status` lists all ten ip6 and all
thirteen l2 fixtures as modified whether or not a frame changed. The diff of
an untouched fixture is its per-packet headers only, in the eight bytes that
hold seconds and microseconds.

The pin tests do not notice, since `fixturePackets` compares packet data and
no netpen test reads a packet header's stamp. The cost lands on the change:
the verifier names every regenerated file as modified testdata, the reviewer
cannot tell the fixture the change meant to regenerate from the rest, and the
implementer restores the rest from a copy.

## What is true and why

pcapgo substitutes the wall clock for a zero stamp:

```go
// github.com/gopacket/gopacket@v1.7.3/pcapgo/write.go
101 func (w *Writer) writePacketHeader(ci gopacket.CaptureInfo) error {
102     t := ci.Timestamp
103     if t.IsZero() {
104         t = time.Now()
105     }
106     secs := t.Unix()
107     usecs := t.Nanosecond() / w.tsScaler
```

So leaving `Timestamp` unset is the same as stamping `time.Now()`. A fixed
stamp is honored: `time.Unix(0, 0)` is not `IsZero` (the zero `time.Time` is
year 1), and the writer then puts zero seconds and zero microseconds in the
header.

With `harvest_l2.go` writing `Timestamp: time.Unix(0, 0)`, two consecutive
runs produce byte-identical files under `testdata/l2/` and the first packet
header of `camflood.pcap` reads `0000 0000 0000 0000` in its stamp bytes
(go1.27.2, 2026-10-15). The committed fixtures change once, at the
regeneration that adopts the stamp, and from then on only when a frame does.

## How to apply it

```go
if err := w.WritePacket(gopacket.CaptureInfo{
	Timestamp:     time.Unix(0, 0),
	CaptureLength: len(pkt),
	Length:        len(pkt),
}, pkt); err != nil {
```

None of the four generators sets a stamp yet. The first change that
regenerates a package's fixtures adds the stamp to that generator in the same
unit, since the regeneration rewrites every file of the package anyway.

## Evidence

- `src/edge/netpen/attacks/ip6/harvest_ip6.go:732-736`: the explicit
  `time.Now()`.
- `src/edge/netpen/attacks/l2/harvest_l2.go:633-636`: no stamp, the same
  outcome through `write.go:103-104`.
- `src/edge/netpen/bench/fixtures_test.go:58`: the bench fixture writer sets
  no stamp either, so its output is not reproducible across runs.

## What it does not cover

A capture from a real device or the netsim tier, where the stamps are
evidence and stay as captured. A fixture whose test asserts inter-packet
timing, which none in netpen does. The checksum half of generator drift is in
[the packet fixture generator solution](../architecture-patterns/a-packet-fixture-generator-must-compute-the-checksums-gopacket-leaves-on-a-raw-payload.md).

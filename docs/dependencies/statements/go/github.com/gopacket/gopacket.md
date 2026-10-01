---
name: github.com/gopacket/gopacket
ecosystem: go
required_by:
  - src/edge/netpen/go.mod
criteria: deploy
verdict: keep
approved: 2026-10-01
---

## Why it is required

The package at src/edge/netpen/attacks/fh imports github.com/gopacket/gopacket at src/edge/netpen/attacks/fh/icmpredirect.go:17. The pinned direct requirement is `github.com/gopacket/gopacket` at `v1.7.3`.

## Why it is safe

The publisher is the gopacket/gopacket project. The pinned version `v1.7.3` was published at 2026-09-26T23:12:17Z and was 4 days old on 2026-10-01. It is under the 14-day wait until 2026-10-10T23:12:17Z. The OSV lookup dated 2026-10-01 returned no advisory for `github.com/gopacket/gopacket` at `v1.7.3`. The dependency tree contains 6 versions, with 4 versions only reachable through this direct dependency. Source not yet reviewed.

## Why not owned code

FlowSeer would have to implement packet decoding and protocol-layer serialization for netpen. That would duplicate a packet parser and its protocol coverage in the edge application. The dependency tree contains 6 versions, with 4 versions only reachable through this direct dependency.

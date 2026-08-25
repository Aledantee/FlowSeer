// Package bench holds netpen's advisory performance benchmarks for the
// decode hot path and the flood-craft pool loop.
//
// The suite follows the snmp bench shape (committed baseline + advisory
// comparison): allocs/op and B/op are measured, but the Success Criteria
// disclaims a performance budget, so the comparison is report-only and
// never hard-fails. The Taskfile `bench` task runs the benchmarks and
// prints a benchstat comparison against the committed baseline in
// testdata/baseline.txt.
//
// # Build tag
//
// Benchmarks are gated by the `netpen_bench` build tag so they never run
// in the default `go test ./...` pass — they are opt-in via
// `task bench` or `go test -tags=netpen_bench -bench .`.
//
// # Hot paths
//
//   - Decode: gopacket's full packet decoder over the layer testdata
//     fixtures (DTP, VTP, MVRP, PAgP, LACP, EIGRP, HSRP, GLBP). This is
//     the receive-side hot path: every frame the leg delivers is decoded
//     through the owned layer decoders.
//   - Flood-craft: the attack craft functions build raw frames (Ethernet
//     → IP → protocol). The pool loop measures the alloc/byte cost of
//     crafting a burst of frames, the send-side hot path.
package bench

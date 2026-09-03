// Package bench measures Netpen packet decoding and frame serialization offline.
// It belongs to the standalone src/edge/netpen module. From the repository root:
//
//	go -C src/edge/netpen test -race -tags=netpen_bench ./bench
//	go -C src/edge/netpen test -tags=netpen_bench -run '^$' -bench . -benchtime=1x ./bench
//
// The netpen_bench tag enables both the benchmarks and their fixture checks.
// The one-iteration command checks that the workloads run; use repeated longer
// runs for performance comparisons. No benchmark opens a link or sends traffic.
//
// # Workloads
//
// Decode benchmarks load every pcap from ../layers/testdata before timing and
// register Netpen's owned protocol decoders in gopacket. Serial and parallel
// loops rotate through the same corpus, including its intentionally malformed
// protocol messages. Unreadable or truncated capture files fail setup; protocol
// decode failures within a valid capture remain part of the measured workload.
//
// The frame benchmarks serialize a fixed Ethernet/ARP request. PoolLoop allocates
// a fresh SerializeBuffer per frame; ReuseBuffer keeps one warmed buffer across
// iterations. They measure serialization and allocation costs, without calling
// attack-specific craft functions or measuring link I/O.
//
// # Comparisons
//
// testdata/baseline.txt is a committed reference for advisory comparisons.
// The parent module's Taskfile bench task runs the suite and reports a benchstat
// comparison when benchstat is installed. There is no hard threshold for timing
// or allocation changes; fixture and serialization errors still fail a run.
// Compare only captures with the same corpus and decoder registrations. Refresh
// the baseline deliberately after reviewing a workload change.
package bench

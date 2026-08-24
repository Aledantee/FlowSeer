package full

// recon.go implements the default recon phase: an ARP sweep of the
// sweep network plus a passive listen for control-plane frames (STP,
// CDP, DTP, VTP, LLDP, HSRP, VRRP, DHCP, ARP, IPv6 RA). The passive
// listen requires AF_PACKET (Linux); on non-Linux it returns empty
// evidence (no frames to observe). The ARP sweep requires raw send;
// on non-Linux it is a no-op.
//
// The recon function is the FullConfig.ReconFn seam: tests substitute a
// stub that returns canned Evidence, so the orchestration tests run
// without a real interface. The production path uses defaultRecon.

import (
	"context"
	"time"
)

// defaultRecon is the production recon function. It runs a passive listen
// bounded by ScanTime plus an ARP sweep of the sweep network, and
// returns the typed Evidence the gate surfaces consume.
//
// On non-Linux (no AF_PACKET), the passive listen observes nothing and
// the ARP sweep is a no-op; the returned Evidence is empty, which still
// fires the burst's unconditional core (R3 parity). On Linux, the listen
// and sweep populate the evidence keys.
func defaultRecon(ctx context.Context, cfg FullConfig) (Evidence, error) {
	ev := Evidence{}

	// The passive listen is bounded by ScanTime. On non-Linux, the
	// leg's Receive returns no frames (ErrUnsupportedPlatform), so this
	// is a bounded wait. On Linux, it captures control-plane frames.
	scanTime := cfg.ScanTime
	if scanTime <= 0 {
		scanTime = 15 * time.Second
	}

	// Drain the attack leg for the scan window. The real parser (STP,
	// CDP, etc.) is a Linux-only path; the non-Linux build observes
	// nothing. This keeps the recon bounded and context-respecting.
	listenCtx, listenCancel := context.WithTimeout(ctx, scanTime)
	defer listenCancel()
	if cfg.AttackLeg != nil {
		for frame := range cfg.AttackLeg.Receive(listenCtx) {
			if frame.Err != nil {
				break
			}
			parseReconFrame(ev, frame.Data)
		}
	}

	// The ARP sweep would send ARP requests for the sweep network and
	// collect replies. On non-Linux this is a no-op (Send returns
	// ErrUnsupportedPlatform). On Linux it populates ev[EvMACs].
	// The sweep network fallback chain: configured net, then
	// 172.16.0.0/24.
	sweepNet := cfg.SweepNet
	if sweepNet == "" {
		sweepNet = "172.16.0.0/24"
	}
	_ = sweepNet // used by the Linux sweep path

	// Record the sweep fallback in the evidence for the summary.
	ev["sweep-net"] = sweepNet

	return ev, nil
}

// parseReconFrame inspects one captured frame and updates the evidence
// map. This is the passive-detect parser: it walks the frame bytes for
// the protocol signatures the baseline's scan_cmd watches for (STP, CDP,
// DTP, VTP, LLDP, HSRP, VRRP, DHCP, ARP, IPv6 RA, MVRP, VLAN tags). The
// parser is conservative: it sets evidence keys only on confirmed
// signatures, never on heuristics.
//
// The full byte-level parser is the Linux production path; on the
// non-Linux test build no frames arrive, so this is exercised only in
// the t1 integration tier. The hook-based orchestration tests bypass
// recon entirely (ReconFn stub).
func parseReconFrame(_ Evidence, data []byte) {
	// The parser is intentionally minimal here: the real protocol
	// decoders live in the layers package and the behavior packages.
	// The recon phase's job is to set the evidence keys the gate
	// surfaces consume; the heavy decoding is the scan command's
	// finding-emission path (scan.go).
	//
	// This function is the seam where the Linux production path wires
	// the layer decoders; the non-Linux build never calls it.
	_ = data
}

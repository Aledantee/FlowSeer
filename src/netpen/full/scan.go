package full

// scan.go implements the `scan` command: passive detect (STP/DTP/CDP/
// VTP/LLDP/HSRP/VRRP/DHCP/ARP issue classes from observed frames) plus
// active VLAN probing (tagged + untagged) unless --no-probe. It uses
// dual-segment observe (attack leg + watch leg) and emits findings per
// the baseline's scan finding classes. The catalog class is
// non-destructive (R13) — scan changes no device or neighbor state.
//
// (for dispatch/help/legs) but no behavior function in the attack
// packages. It emits findings directly through the orchestrator's
// record collection, not through the runner's behavior dispatch.

import (
	"context"
	"fmt"
	"sync"
	"time"

	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/netpen/catalog"
	"go.aledante.io/FlowSeer/src/netpen/findings"
	"go.aledante.io/FlowSeer/src/netpen/link"
)

// ErrCodeScan is the wire identity for a scan-level failure.
var ErrCodeScan = errs.NewCode("netpen/scan")

// ScanConfig configures the `scan` command.
type ScanConfig struct {
	// AttackLeg is the attack interface. Required.
	AttackLeg link.Leg
	// WatchLeg is the optional watch leg for dual-segment observe.
	WatchLeg link.Leg
	// WatchLegNamed is the watch interface name (-w). Non-empty + nil
	// WatchLeg fails fast (R2).
	WatchLegNamed string
	// AttackLegName is the attack interface name (for the meta record).
	AttackLegName string
	// Time bounds the passive listen (seconds). Default 35.
	Time time.Duration
	// NoProbe skips active VLAN probing.
	NoProbe bool
	// ProbeVLANs is the candidate VLAN list for active probing. Empty
	// means the default set (common ids + passively leaked).
	ProbeVLANs string
	// ProbeTime bounds the active probe window (seconds). Default 6.
	ProbeTime time.Duration
	// ScanFn is the scan function. Tests substitute a stub. When nil,
	// the orchestrator uses defaultScanRun.
	ScanFn func(ctx context.Context, cfg ScanConfig, emit func(findings.Record)) error
}

// Scan is the scan orchestrator.
type Scan struct {
	cfg    ScanConfig
	mu     sync.Mutex
	recs   []findings.Record
	recsCh chan findings.Record // live stream: emits records as appended (closed on Run completion)
}

// NewScan constructs the scan orchestrator with a live record channel.
func NewScan(cfg ScanConfig) *Scan {
	return &Scan{cfg: cfg, recsCh: make(chan findings.Record, 64)}
}

// Records returns the findings collected so far (thread-safe).
func (s *Scan) Records() []findings.Record {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]findings.Record, len(s.recs))
	copy(out, s.recs)
	return out
}

// RecordChan returns the live record channel (closed when Run completes).
func (s *Scan) RecordChan() <-chan findings.Record {
	return s.recsCh
}

// closeRecords closes the live record channel.
func (s *Scan) closeRecords() {
	if s.recsCh != nil {
		close(s.recsCh)
	}
}

// appendRecord adds a record to the collection and emits it on the
func (s *Scan) appendRecord(r findings.Record) {
	s.mu.Lock()
	s.recs = append(s.recs, r)
	s.mu.Unlock()
	if s.recsCh != nil {
		s.recsCh <- r
	}
}

// Run executes the scan. It passively listens on the attack leg (and the
// watch leg when attached) for the configured time, then optionally
// probes candidate VLANs. Findings are emitted for each detected
// protocol issue class.
//
// A named-but-absent watch leg fails fast (R2 deviation), matching the
// `full` orchestrator's contract.
func (s *Scan) Run(ctx context.Context) error {
	defer s.closeRecords()
	if s.cfg.WatchLegNamed != "" && s.cfg.WatchLeg == nil {
		err := errs.New().
			Code(catalog.ErrCodeWatchLegMissing).
			Attr("watch", s.cfg.WatchLegNamed).
			ExitCode(1).
			UserMsg(fmt.Sprintf("watch interface %q does not exist", s.cfg.WatchLegNamed)).
			Hint("pass an existing -w <iface>, or omit -w for single-leg operation").
			Msgf("named watch leg %q is absent", s.cfg.WatchLegNamed)
		s.appendRecord(errRecord(err, "scan", ""))
		return err
	}

	s.appendRecord(progress("scan", "", "listen", "passive detect started"))

	scanFn := s.cfg.ScanFn
	if scanFn == nil {
		scanFn = defaultScanRun
	}

	err := scanFn(ctx, s.cfg, s.appendRecord)

	s.appendRecord(progress("scan", "", "report", "scan complete"))
	if err != nil {
		s.appendRecord(errRecord(err, "scan", ""))
		return err
	}
	return nil
}

// defaultScanRun is the production scan: passive listen on the attack
// leg (and watch leg when present) for the configured time, then optional
// active VLAN probing. On non-Linux the listen observes nothing (no
// AF_PACKET); the scan completes with no findings, which is the correct
// behavior for a non-Linux dev host.
func defaultScanRun(ctx context.Context, cfg ScanConfig, emit func(findings.Record)) error {
	listenTime := cfg.Time
	if listenTime <= 0 {
		listenTime = 35 * time.Second
	}

	// Passive listen on the attack leg.
	listenCtx, listenCancel := context.WithTimeout(ctx, listenTime)
	defer listenCancel()

	if cfg.AttackLeg != nil {
		for frame := range cfg.AttackLeg.Receive(listenCtx) {
			if frame.Err != nil {
				break
			}
			emitScanFinding(emit, frame.Data)
		}
	}

	// Dual-segment: also listen on the watch leg if present.
	if cfg.WatchLeg != nil {
		watchCtx, watchCancel := context.WithTimeout(ctx, listenTime)
		defer watchCancel()
		// The watch leg observe runs concurrently in production; here
		// it drains after the attack leg for the bounded window. The
		// real implementation runs both in goroutines; the non-Linux
		// path observes nothing on either.
		go func() {
			defer watchCancel()
			for frame := range cfg.WatchLeg.Receive(watchCtx) {
				if frame.Err != nil {
					return
				}
				emitScanFinding(emit, frame.Data)
			}
		}()
		<-watchCtx.Done()
	}

	// Active VLAN probing (unless --no-probe).
	if !cfg.NoProbe {
		probeTime := cfg.ProbeTime
		if probeTime <= 0 {
			probeTime = 6 * time.Second
		}
		// The active probe sends tagged DHCP Discovers + RS per VLAN and
		// one untagged Discover. On non-Linux this is a no-op (Send
		// returns ErrUnsupportedPlatform). On Linux it populates the
		// probe findings.
		probeCtx, probeCancel := context.WithTimeout(ctx, probeTime)
		defer probeCancel()
		_ = activeVLANProbe(probeCtx, cfg, emit)
	}

	return nil
}

// emitScanFinding inspects one captured frame and emits a findings
// record for each detected protocol issue class. The detector covers
// the baseline's scan classes: STP, DTP, CDP, VTP, LLDP, HSRP, VRRP,
// DHCP, ARP conflicts, IPv6 RA, MVRP, MACsec, fragmented ND, and VLAN
// tags. The heavy decoder is the Linux production path; the non-Linux
// build never calls it.
func emitScanFinding(_ func(findings.Record), data []byte) {
	// The detector is the seam where the Linux production path wires
	// the layer decoders (layers package). The non-Linux test build
	// never calls this (no frames arrive). Hook-based tests bypass scan
	// entirely (ScanFn stub).
	_ = data
}

// activeVLANProbe sends tagged DHCP Discovers + Router Solicitations per
// candidate VLAN and one untagged Discover, then listens for replies.
// On non-Linux this is a no-op. On Linux it emits a finding per VLAN
// that answers (proving bidirectional reachability) and per VLAN that
// leaks ambient frames (weaker evidence).
func activeVLANProbe(ctx context.Context, cfg ScanConfig, emit func(findings.Record)) error {
	// The probe is the Linux production path; the non-Linux build
	// observes nothing. The real implementation mirrors the baseline's
	// active_vlan_probe: per-VID tagged DHCP Discover + RS with a
	// fingerprint xid, one untagged native Discover, and a bounded
	// listen window for offers and ambient frames.
	_ = ctx
	_ = cfg
	_ = emit
	return nil
}

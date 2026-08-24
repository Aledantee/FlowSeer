package full

// gates.go ports the baseline's two gate surfaces verbatim (KTD8: the
// catalog precondition entries are the single source; here they are the
// orchestration-side mirror, since `full` carries no catalog row of its
// own). Both surfaces consume only the [Evidence] map recon emits.
//
// BURST ARMING (phase-2 concurrent workers):
//   - ra6 evidence arms daddos;
//   - resolved MACs + NOT --no-spoof arms arpspoof;
//   - detected vrids arms vrrp;
//   - the other seven workers (stproot, camflood, dhcpstarve, gratarp,
//     dtp, roguera, llmnr) ALWAYS fire.
//
// daddos is a BURST-phase worker when ra6 evidence exists — NOT a
// phase-3 follow-up. This matches the baseline placement exactly.
//
// FOLLOW-UP SELECTION (phase-3 sequential):
//   - ra6 arms roguedhcp6 + raguard;
//   - vlans arms vlanhop (first three);
//   - CDP voice VLAN arms voicevlan;
//   - VTP domain+revision arms vtp (SAFE mode ONLY);
//   - MVRP presence arms mvrp;
//   - watch-leg presence arms ghost;
//   - resolved MACs arm portsteal.
//
// Empty recon STILL fires the unconditional core (parity). The sweep-net
// fallback chain: attack leg's configured net, then 172.16.0.0/24,
// recorded in the summary.

import "go.aledante.io/FlowSeer/src/netpen/runner"

// burstCore is the seven workers that ALWAYS fire in the burst, in the
// baseline's worker-list order. These are unconditional (R3).
var burstCore = []string{
	"stproot", "camflood", "dhcpstarve", "gratarp", "dtp", "roguera", "llmnr",
}

// armBurst computes the burst worker list from the recon evidence and the
// --no-spoof flag. It returns the unconditional core plus the
// evidence/flag-armed workers, in a stable order (core first, then the
// armed workers in the baseline's arming order: daddos, arpspoof, vrrp).
//
// The returned list is the set of (name, mode) pairs the burst phase
// dispatches concurrently. All are mode-less (empty mode) — the burst
// never dispatches a permanent mode (R15: Orchestrated closes the
// permanent path entirely).
func armBurst(ev Evidence, noSpoof bool) []runner.AttackRef {
	out := make([]runner.AttackRef, 0, len(burstCore)+3)
	for _, name := range burstCore {
		out = append(out, runner.AttackRef{Name: name})
	}

	// ra6 evidence arms daddos (burst-phase worker, NOT a follow-up).
	if ev.Has(EvRA6) {
		out = append(out, runner.AttackRef{Name: "daddos"})
	}

	// Resolved MACs + NOT --no-spoof arms arpspoof.
	if !noSpoof && ev.Has(EvMACs) {
		out = append(out, runner.AttackRef{Name: "arpspoof"})
	}

	// Detected vrids arms vrrp.
	if ev.Has(EvVRIDs) {
		out = append(out, runner.AttackRef{Name: "vrrp"})
	}

	return out
}

// followUpEntry is one phase-3 follow-up: the (name, mode) pair and the
// human-readable reason it was selected (recorded in the summary as the
// "skipped" list when recon evidence is absent).
type followUpEntry struct {
	ref    runner.AttackRef
	reason string
}

// selectFollowUps computes the phase-3 follow-up list from the recon
// evidence and the watch-leg presence. Follow-ups are sequential and
// selected ONLY by recon evidence (R3: no blind attack sequences in
// follow-up selection). The order mirrors the baseline's phase-3 block:
//
//	ghost, vlanhop (first three), voicevlan, vtp, roguedhcp6, raguard,
//	mvrp, portsteal.
//
// vtp is SAFE mode ONLY: the follow-up runs the mode-less (transient-decay)
// vtp behavior; the permanent --wipe/--set modes are never dispatched
func selectFollowUps(ev Evidence, hasWatchLeg bool, noSpoof bool) []followUpEntry {
	var out []followUpEntry

	// watch-leg presence arms ghost.
	if hasWatchLeg {
		out = append(out, followUpEntry{
			ref:    runner.AttackRef{Name: "ghost"},
			reason: "watch leg attached",
		})
	}

	// vlans arms vlanhop (first three observed VLAN ids).
	if vlans := ev.VLANs(); len(vlans) > 0 {
		// First three, sorted ascending (baseline: sorted(recon["vlans"])[:3]).
		first := firstThreeSorted(vlans)
		for _, v := range first {
			out = append(out, followUpEntry{
				ref:    runner.AttackRef{Name: "vlanhop"},
				reason: reasonf("VLAN %d tagged frames in recon", v),
			})
		}
	}

	// CDP voice VLAN arms voicevlan.
	if vv := ev.VoiceVLAN(); vv != 0 {
		out = append(out, followUpEntry{
			ref:    runner.AttackRef{Name: "voicevlan"},
			reason: reasonf("CDP voice VLAN %d leaked", vv),
		})
	}

	// VTP domain+revision arms vtp (SAFE mode only — mode-less base row).
	if dom := ev.VTPDomain(); dom != "" && ev.VTPRev() >= 0 {
		out = append(out, followUpEntry{
			ref:    runner.AttackRef{Name: "vtp"},
			reason: reasonf("VTP domain %q rev %d in recon", dom, ev.VTPRev()),
		})
	}

	// ra6 arms roguedhcp6 + raguard.
	if ev.Has(EvRA6) {
		out = append(out,
			followUpEntry{
				ref:    runner.AttackRef{Name: "roguedhcp6"},
				reason: "IPv6 RA traffic in recon",
			},
			followUpEntry{
				ref:    runner.AttackRef{Name: "raguard"},
				reason: "IPv6 RA traffic in recon",
			},
		)
	}

	// MVRP presence arms mvrp.
	if ev.Has(EvMVRP) {
		out = append(out, followUpEntry{
			ref:    runner.AttackRef{Name: "mvrp"},
			reason: "MVRP frames in recon",
		})
	}

	// Resolved MACs arm portsteal (--no-spoof disarms it, matching the
	// baseline's "if not args.no_spoof and spoof_targets").
	if !noSpoof && ev.Has(EvMACs) {
		out = append(out, followUpEntry{
			ref:    runner.AttackRef{Name: "portsteal"},
			reason: "victim host MAC resolved",
		})
	}

	return out
}

// firstThreeSorted returns the first three values of a sorted-ascending
// copy of s. It never returns more than three entries (baseline parity:
// sorted(recon["vlans"])[:3]).
func firstThreeSorted(s []int) []int {
	sorted := make([]int, len(s))
	copy(sorted, s)
	// Insertion sort for deterministic order without importing sort.
	for i := 1; i < len(sorted); i++ {
		for j := i; j > 0 && sorted[j] < sorted[j-1]; j-- {
			sorted[j], sorted[j-1] = sorted[j-1], sorted[j]
		}
	}
	if len(sorted) > 3 {
		sorted = sorted[:3]
	}
	return sorted
}

// reasonf builds a reason string without importing fmt (keeps the gate
// surfaces allocation-light). It handles the small set of formats the
// reasons use.
func reasonf(format string, args ...any) string {
	// Inline the two formats we use; fall back to a generic path.
	switch format {
	case "VLAN %d tagged frames in recon":
		return sprintf("VLAN %v tagged frames in recon", args[0])
	case "CDP voice VLAN %d leaked":
		return sprintf("CDP voice VLAN %v leaked", args[0])
	case "VTP domain %q rev %d in recon":
		return sprintf("VTP domain %q rev %v in recon", args[0], args[1])
	}
	return format
}

// sprintf is a tiny formatter for the gate reasons. It handles %d, %q,
// and %v for the few types the reasons carry (int, string).
func sprintf(format string, args ...any) string {
	out := make([]byte, 0, len(format)+16)
	ai := 0
	for i := 0; i < len(format); i++ {
		if format[i] == '%' && i+1 < len(format) {
			switch format[i+1] {
			case 'd', 'v':
				if ai < len(args) {
					out = append(out, itoa(args[ai])...)
					ai++
				}
				i++
				continue
			case 'q':
				if ai < len(args) {
					out = append(out, '"')
					out = append(out, args[ai].(string)...)
					out = append(out, '"')
					ai++
				}
				i++
				continue
			}
		}
		out = append(out, format[i])
	}
	return string(out)
}

// itoa formats an int (or int-like) value as a decimal string.
func itoa(v any) []byte {
	n, ok := v.(int)
	if !ok {
		return []byte{}
	}
	if n == 0 {
		return []byte("0")
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return buf[i:]
}

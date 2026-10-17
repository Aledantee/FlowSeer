// raguard.go implements the RA-Guard check. Impact none, restore none,
// affects none: the probe advertisement carries a zero router lifetime,
// zero hop limit, zero reachable time and retransmit timer, no
// link-layer address option, and a prefix option whose on-link and
// autonomous flags are clear with zero lifetimes, so a host that receives
// it installs no default router, prefix, address, or link parameter. A
// host that already holds a neighbor cache entry for the probe's
// link-local source marks that entry a router. The guard still sees the
// sender's link-layer address, the ingress port, the IP source, and the
// prefix, which is what it decides on.

package ip6

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"time"

	"github.com/gopacket/gopacket"
	"github.com/gopacket/gopacket/layers"

	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/edge/netpen/attacks/internal/craft"
	"go.aledante.io/FlowSeer/src/edge/netpen/check"
)

type raGuardFinding struct {
	Action     string `json:"action"`
	Sent       int    `json:"sent"`
	Observed   int    `json:"observed"`
	Traversal  string `json:"traversal"`
	Attributed string `json:"attributed"`
}

// RunRAGuard sends one probe Router Advertisement for the target's prefix
// from the attack leg and watches the watch leg for the window. The
// verdict is confirmed when a Router Advertisement from the attack leg's
// MAC carrying the target prefix crosses to the watch leg, resisted when
// the window ends without one, and pending without a watch leg, with the
// finding's traversal field reading unobserved. Cancellation while
// observing returns ctx.Err(). A terminal receive error comes back wrapped.
func RunRAGuard(ctx context.Context, deps check.Deps) (check.Verdict, error) {
	if err := deps.Target.Need(check.FieldPrefix6); err != nil {
		return check.Verdict{}, err
	}
	src := deps.AttackLeg.Addr().MAC
	prefix := deps.Target.Prefix6

	pkt, err := craftProbeRA(src, prefix)
	if err != nil {
		if check.IsTargetError(err) {
			return check.Verdict{}, err
		}
		return check.Verdict{}, errs.Wrap(err, "raguard: craft RA")
	}
	if err := deps.AttackLeg.Send(ctx, pkt); err != nil {
		return check.Verdict{}, errs.Wrap(err, "raguard: send RA")
	}

	finding := raGuardFinding{
		Action:     "raguard-observation",
		Sent:       1,
		Traversal:  "unobserved",
		Attributed: "none",
	}
	if deps.WatchLeg == nil {
		emitRAGuardFinding(deps, finding)
		return check.Pending("no observable: traversal needs a watch leg"), nil
	}

	crossed, obs, err := check.First(ctx, deps.WatchLeg, deps.Observe, func(data []byte) bool {
		return isProbeRA(data, src, prefix)
	})
	if err != nil {
		return check.Verdict{}, errs.Wrap(err, "raguard: receive observation")
	}
	finding.Observed = obs.Skipped
	finding.Traversal = "not-forwarded"
	if crossed != nil {
		finding.Observed++
		finding.Traversal = "forwarded"
		finding.Attributed = "attack-only"
		if obs.Skipped > 0 {
			finding.Attributed = "attack-distinguished-from-ambient"
		}
	}
	emitRAGuardFinding(deps, finding)

	if crossed != nil {
		return check.Confirmed("the probe RA crossed to the watch leg", obs.Elapsed), nil
	}
	return check.Resisted(fmt.Sprintf("0 probe RA in %s", obs.Elapsed.Round(time.Millisecond)), obs.Elapsed), nil
}

func emitRAGuardFinding(deps check.Deps, f raGuardFinding) {
	detail, _ := json.Marshal(f)
	deps.Emitter.Finding("ra", detail)
}

// craftProbeRA builds the probe Router Advertisement for prefix from src
// and its EUI-64 link-local address: every router parameter zero and one
// Prefix Information option with both flags clear and both lifetimes
// zero, so the frame installs nothing on a host and still crosses an
// RA-Guard that lets router advertisements through.
func craftProbeRA(src net.HardwareAddr, prefix *net.IPNet) ([]byte, error) {
	linkLocal, err := linkLocalFromMAC(src)
	if err != nil {
		return nil, err
	}
	prefixLen, _ := prefix.Mask.Size()

	eth := &layers.Ethernet{
		DstMAC:       allNodesMAC,
		SrcMAC:       src,
		EthernetType: layers.EthernetTypeIPv6,
	}
	ip := &layers.IPv6{
		Version:    6,
		NextHeader: layers.IPProtocolICMPv6,
		HopLimit:   255,
		SrcIP:      linkLocal,
		DstIP:      allNodesIPv6,
	}
	icmp := &layers.ICMPv6{
		TypeCode: layers.CreateICMPv6TypeCode(layers.ICMPv6TypeRouterAdvertisement, 0),
	}
	_ = icmp.SetNetworkLayerForChecksum(ip)

	prefixData := make([]byte, 30)
	prefixData[0] = byte(prefixLen)
	copy(prefixData[14:30], prefix.IP.To16())

	ra := &layers.ICMPv6RouterAdvertisement{
		Options: layers.ICMPv6Options{
			{
				Type: layers.ICMPv6OptPrefixInfo,
				Data: prefixData,
			},
		},
	}
	return craft.Default(eth, ip, icmp, ra)
}

// isProbeRA reports whether data is an ICMPv6 Router Advertisement whose
// Ethernet source is attackMAC and whose Prefix Information option carries
// prefix, the attribution a crossing rests on. A frame it cannot decode is
// not a crossing.
func isProbeRA(data []byte, attackMAC net.HardwareAddr, prefix *net.IPNet) bool {
	if len(data) < 14 || !bytes.Equal(data[6:12], attackMAC) {
		return false
	}
	pkt := gopacket.NewPacket(data, layers.LayerTypeEthernet, gopacket.Default)
	ra, ok := pkt.Layer(layers.LayerTypeICMPv6RouterAdvertisement).(*layers.ICMPv6RouterAdvertisement)
	if !ok {
		return false
	}
	prefixLen, _ := prefix.Mask.Size()
	for _, opt := range ra.Options {
		if opt.Type != layers.ICMPv6OptPrefixInfo || len(opt.Data) < 30 {
			continue
		}
		if int(opt.Data[0]) == prefixLen && bytes.Equal(opt.Data[14:30], prefix.IP.To16()) {
			return true
		}
	}
	return false
}

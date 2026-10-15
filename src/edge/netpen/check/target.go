package check

import (
	"net"
	"strconv"
	"strings"

	"go.aledante.io/FlowSeer/src/common/errs"
)

// ErrCodeTargetMissing marks a behavior invoked without a target field it
// cannot run without. The runner records it as a
// [go.aledante.io/FlowSeer/src/edge/netpen/findings.VerdictSkipped] verdict
// and a single command exits 2 with it.
var ErrCodeTargetMissing = errs.NewCode("netpen/target-missing")

// ErrCodeTargetInvalid marks a behavior invoked with a target field that is
// present but of the wrong form: the other address family, a MAC that is
// not six octets, a number outside the range the frame carries. The runner
// treats it as [ErrCodeTargetMissing], since in both cases nothing was
// sent.
var ErrCodeTargetInvalid = errs.NewCode("netpen/target-invalid")

// IsTargetError reports whether err carries [ErrCodeTargetMissing] or
// [ErrCodeTargetInvalid], the two codes a behavior returns before its first
// frame when the target cannot be used.
func IsTargetError(err error) bool {
	code, ok := errs.CodeOf(err)
	return ok && (code == ErrCodeTargetMissing || code == ErrCodeTargetInvalid)
}

// Host is one addressed host on the segment: the IP a behavior names and
// the MAC it frames to. Either part may be absent. Callers must
// synchronize concurrent mutation of a shared Host.
type Host struct {
	IP  net.IP
	MAC net.HardwareAddr
}

// Target holds every value that names a host, address, group, or identity
// on the network a behavior acts on. The attacker's own addresses come
// from the leg, not from here. A nil address, nil network, empty string,
// or zero number is absent, except Group, whose zero is a valid group and
// which is absent only as a nil pointer. Callers must synchronize
// concurrent mutation of a shared Target.
type Target struct {
	// Victim is the IPv4 host whose traffic or state the check targets.
	Victim Host
	// Gateway is the first-hop IPv4 router the victim uses.
	Gateway Host
	// Victim6 is the IPv6 host the check targets.
	Victim6 Host
	// Server6 is the IPv6 server the check impersonates or answers for.
	Server6 Host
	// VirtualIP is the first-hop redundancy virtual IPv4 address.
	VirtualIP net.IP
	// Group is the first-hop redundancy group number. Group 0 is a group,
	// so a nil pointer is the absent value.
	Group *uint16
	// VRID is the VRRP virtual router identifier, 1 to 255.
	VRID uint8
	// OuterVLAN is the native or outer tag of a double-tagged frame.
	OuterVLAN uint16
	// InnerVLAN is the inner tag of a double-tagged frame.
	InnerVLAN uint16
	// VoiceVLAN is the voice VLAN a phone is placed in.
	VoiceVLAN uint16
	// VTPDomain is the VTP management domain name, at most 32 bytes.
	VTPDomain string
	// RouterID is the routing-protocol router identity, an IPv4 address.
	RouterID net.IP
	// Prefix6 is the IPv6 prefix a router advertisement carries.
	Prefix6 *net.IPNet
	// Route4 is the IPv4 route a routing-protocol update injects.
	Route4 *net.IPNet
	// OfferIP is the IPv4 address a rogue DHCP server offers.
	OfferIP net.IP
	// SweepNet is the IPv4 network a sweep enumerates.
	SweepNet *net.IPNet
}

// Field names one part of a [Target] for [Target.Need] and
// [Target.Within]. [Field.Flag] renders it as the command flag that
// supplies it, so an error reads as the flags to pass. Field values are
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

// Flag renders the field as the command flag that supplies it, with the
// value form the flag takes. A host's address and MAC render as the same
// flag, since one flag carries both parts.
func (f Field) Flag() string {
	switch f {
	case FieldVictim, FieldVictimMAC:
		return "--victim <ip>,<mac>"
	case FieldGateway, FieldGatewayMAC:
		return "--gateway <ip>,<mac>"
	case FieldVictim6, FieldVictim6MAC:
		return "--victim6 <ip>,<mac>"
	case FieldServer6, FieldServer6MAC:
		return "--server6 <ip>,<mac>"
	case FieldVirtualIP, FieldRouterID, FieldOfferIP:
		return "--" + string(f) + " <ip>"
	case FieldGroup, FieldVRID, FieldOuterVLAN, FieldInnerVLAN, FieldVoiceVLAN:
		return "--" + string(f) + " <n>"
	case FieldVTPDomain:
		return "--vtp-domain <name>"
	case FieldPrefix6, FieldRoute4, FieldSweepNet:
		return "--" + string(f) + " <cidr>"
	}
	return "--" + string(f)
}

// The forms a present field must have, worded for the message that names
// the flag.
const (
	formIPv4   = "an IPv4 address"
	formIPv6   = "an IPv6 address"
	formMAC    = "a six-octet MAC"
	formVLAN   = "a VLAN id from 1 to 4094"
	formDomain = "a name of at most 32 bytes"
	formNet4   = "an IPv4 network"
	formSweep  = "an IPv4 network with at least one host address"
	formNet6   = "an IPv6 network"
)

// Need reports whether every named field is present and of the form the
// check can frame. It returns nil when they are, and otherwise one error
// with exit code 2 that lists every flag to pass, with the form a present
// field of the wrong form wants, so an operator fixes the whole command
// line in one round. The error carries [ErrCodeTargetInvalid] when any
// named field has the wrong form and [ErrCodeTargetMissing] otherwise. An
// unknown field is reported as missing.
func (t Target) Need(fields ...Field) error {
	var needs []string
	invalid := false
	for _, f := range fields {
		present, form := t.form(f)
		switch {
		case !present:
			needs = appendUnique(needs, f.Flag())
		case form != "":
			invalid = true
			needs = appendUnique(needs, f.Flag()+" wants "+form)
		}
	}
	if len(needs) == 0 {
		return nil
	}
	return targetError(invalid, needs)
}

// Within bounds a numeric field at limit for a behavior whose frame field
// is narrower than the target's: it returns an error carrying
// [ErrCodeTargetInvalid] and exit code 2 when the field is present and
// exceeds limit, and nil otherwise. Presence is [Target.Need]'s to report.
func (t Target) Within(f Field, limit uint16) error {
	v, ok := t.number(f)
	if !ok || v <= limit {
		return nil
	}
	return targetError(true, []string{"--" + string(f) + " at most " + strconv.Itoa(int(limit))})
}

func targetError(invalid bool, needs []string) error {
	code := ErrCodeTargetMissing
	if invalid {
		code = ErrCodeTargetInvalid
	}
	text := "the check needs: " + strings.Join(needs, ", ")
	return errs.New().
		Code(code).
		ExitCode(2).
		PubAttr("needs", strings.Join(needs, "; ")).
		UserMsg(text).
		Hint("pass the listed target flags in the form shown").
		Msg(text)
}

func appendUnique(list []string, s string) []string {
	for _, have := range list {
		if have == s {
			return list
		}
	}
	return append(list, s)
}

// form reports whether field f is present and, when it is, the form it
// wants and lacks, or an empty string when its form is right.
func (t Target) form(f Field) (present bool, want string) {
	switch f {
	case FieldVictim:
		return ipForm(t.Victim.IP, false)
	case FieldVictimMAC:
		return macForm(t.Victim.MAC)
	case FieldGateway:
		return ipForm(t.Gateway.IP, false)
	case FieldGatewayMAC:
		return macForm(t.Gateway.MAC)
	case FieldVictim6:
		return ipForm(t.Victim6.IP, true)
	case FieldVictim6MAC:
		return macForm(t.Victim6.MAC)
	case FieldServer6:
		return ipForm(t.Server6.IP, true)
	case FieldServer6MAC:
		return macForm(t.Server6.MAC)
	case FieldVirtualIP:
		return ipForm(t.VirtualIP, false)
	case FieldGroup:
		return t.Group != nil, ""
	case FieldVRID:
		return t.VRID != 0, ""
	case FieldOuterVLAN:
		return vlanForm(t.OuterVLAN)
	case FieldInnerVLAN:
		return vlanForm(t.InnerVLAN)
	case FieldVoiceVLAN:
		return vlanForm(t.VoiceVLAN)
	case FieldVTPDomain:
		if t.VTPDomain == "" {
			return false, ""
		}
		if len(t.VTPDomain) > 32 {
			return true, formDomain
		}
		return true, ""
	case FieldRouterID:
		return ipForm(t.RouterID, false)
	case FieldPrefix6:
		return net6Form(t.Prefix6)
	case FieldRoute4:
		return net4Form(t.Route4, false)
	case FieldOfferIP:
		return ipForm(t.OfferIP, false)
	case FieldSweepNet:
		return net4Form(t.SweepNet, true)
	}
	return false, ""
}

// number returns the value of a numeric field and whether the field is
// numeric and present.
func (t Target) number(f Field) (uint16, bool) {
	switch f {
	case FieldGroup:
		if t.Group == nil {
			return 0, false
		}
		return *t.Group, true
	case FieldVRID:
		return uint16(t.VRID), t.VRID != 0
	case FieldOuterVLAN:
		return t.OuterVLAN, t.OuterVLAN != 0
	case FieldInnerVLAN:
		return t.InnerVLAN, t.InnerVLAN != 0
	case FieldVoiceVLAN:
		return t.VoiceVLAN, t.VoiceVLAN != 0
	}
	return 0, false
}

// isIPv6 reports whether ip is a 16-byte address that is not a v4-mapped
// one, which an IPv6 header cannot carry as a source or destination of the
// link.
func isIPv6(ip net.IP) bool {
	return ip.To16() != nil && ip.To4() == nil
}

func ipForm(ip net.IP, v6 bool) (bool, string) {
	if ip == nil {
		return false, ""
	}
	if v6 {
		if !isIPv6(ip) {
			return true, formIPv6
		}
		return true, ""
	}
	if ip.To4() == nil {
		return true, formIPv4
	}
	return true, ""
}

func macForm(mac net.HardwareAddr) (bool, string) {
	if mac == nil {
		return false, ""
	}
	if len(mac) != 6 {
		return true, formMAC
	}
	return true, ""
}

func vlanForm(id uint16) (bool, string) {
	if id == 0 {
		return false, ""
	}
	if id > 4094 {
		return true, formVLAN
	}
	return true, ""
}

// net4Form checks an IPv4 network: a 4-byte address and mask, and, when
// hosts is set, a prefix short enough to leave a host address between the
// network and broadcast addresses.
func net4Form(n *net.IPNet, hosts bool) (bool, string) {
	if n == nil {
		return false, ""
	}
	if n.IP.To4() == nil || len(n.Mask) != 4 {
		if hosts {
			return true, formSweep
		}
		return true, formNet4
	}
	if ones, bits := n.Mask.Size(); hosts && bits-ones < 2 {
		return true, formSweep
	}
	return true, ""
}

func net6Form(n *net.IPNet) (bool, string) {
	if n == nil {
		return false, ""
	}
	if !isIPv6(n.IP) || len(n.Mask) != 16 {
		return true, formNet6
	}
	return true, ""
}

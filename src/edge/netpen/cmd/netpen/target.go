package main

import (
	"flag"
	"net"
	"strconv"
	"strings"

	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/edge/netpen/check"
)

// targetFlags binds the target flags of every command to one
// [check.Target]. Each flag is typed by family: an IPv4 or IPv6 host as
// "<ip>" or "<ip>,<mac>", an IPv4 address, an IPv4 or IPv6 network in CIDR
// form, a VLAN id from 1 to 4094, a VRID from 1 to 255, and a group from
// 0 up. An IPv4 value is stored in its 4-byte form. The flag refuses the
// other family at parse time, so the operator reads the error where the
// value was typed, and the same rule in [check.Target.Need] then only has
// to catch an embedder. A flag the operator leaves out leaves its field
// empty, and the behavior's own Need call reports what it lacks.
type targetFlags struct {
	check.Target
}

func (t *targetFlags) register(fs *flag.FlagSet) {
	fs.Var(hostFlag{&t.Victim, false}, "victim", "IPv4 victim host as <ip> or <ip>,<mac>")
	fs.Var(hostFlag{&t.Gateway, false}, "gateway", "IPv4 gateway host as <ip> or <ip>,<mac>")
	fs.Var(hostFlag{&t.Victim6, true}, "victim6", "IPv6 victim host as <ip> or <ip>,<mac>")
	fs.Var(hostFlag{&t.Server6, true}, "server6", "IPv6 server host as <ip> or <ip>,<mac>")
	fs.Var(ip4Flag{&t.VirtualIP}, "vip", "first-hop redundancy virtual IPv4 address")
	fs.Var(ip4Flag{&t.RouterID}, "router-id", "routing-protocol router ID, an IPv4 address")
	fs.Var(ip4Flag{&t.OfferIP}, "offer-ip", "IPv4 address the rogue DHCP server offers")
	fs.Var(groupFlag{&t.Group}, "group", "first-hop redundancy group, 0 and up")
	fs.Var(uint8Flag{&t.VRID}, "vrid", "VRRP virtual router ID, 1 to 255")
	fs.Var(vlanFlag{&t.OuterVLAN}, "outer-vlan", "outer VLAN of a double-tagged frame, 1 to 4094")
	fs.Var(vlanFlag{&t.InnerVLAN}, "inner-vlan", "inner VLAN of a double-tagged frame, 1 to 4094")
	fs.Var(vlanFlag{&t.VoiceVLAN}, "voice-vlan", "voice VLAN to place a phone in, 1 to 4094")
	fs.StringVar(&t.VTPDomain, "vtp-domain", "", "VTP management domain, at most 32 bytes")
	fs.Var(cidrFlag{&t.Prefix6, true}, "prefix6", "IPv6 prefix a router advertisement carries, in CIDR form")
	fs.Var(cidrFlag{&t.Route4, false}, "route4", "IPv4 route a routing update injects, in CIDR form")
	fs.Var(cidrFlag{&t.SweepNet, false}, "sweep-net", "IPv4 network a sweep enumerates, in CIDR form")
}

// parseIP parses s as an address of one family: the 4-byte form of an
// IPv4 address, or an IPv6 address that is not v4-mapped. It returns nil
// for the other family.
func parseIP(s string, v6 bool) net.IP {
	ip := net.ParseIP(s)
	if ip == nil {
		return nil
	}
	if v6 {
		if ip.To4() != nil {
			return nil
		}
		return ip
	}
	return ip.To4()
}

func family(v6 bool) string {
	if v6 {
		return "IPv6"
	}
	return "IPv4"
}

// hostFlag parses "<ip>" or "<ip>,<mac>" into a host of one family, with
// a six-octet MAC.
type hostFlag struct {
	host *check.Host
	v6   bool
}

func (h hostFlag) String() string {
	if h.host == nil || h.host.IP == nil {
		return ""
	}
	if h.host.MAC == nil {
		return h.host.IP.String()
	}
	return h.host.IP.String() + "," + h.host.MAC.String()
}

func (h hostFlag) Set(s string) error {
	ipText, macText, hasMAC := strings.Cut(s, ",")
	ip := parseIP(ipText, h.v6)
	if ip == nil {
		return errs.Msgf("want an %s address as <ip> or <ip>,<mac>, got %q", family(h.v6), s)
	}
	host := check.Host{IP: ip}
	if hasMAC {
		mac, err := net.ParseMAC(macText)
		if err != nil || len(mac) != 6 {
			return errs.Msgf("want a six-octet MAC after the comma, got %q", macText)
		}
		host.MAC = mac
	}
	*h.host = host
	return nil
}

// ip4Flag parses an IPv4 address into its 4-byte form.
type ip4Flag struct{ ip *net.IP }

func (f ip4Flag) String() string {
	if f.ip == nil || *f.ip == nil {
		return ""
	}
	return f.ip.String()
}

func (f ip4Flag) Set(s string) error {
	ip := parseIP(s, false)
	if ip == nil {
		return errs.Msgf("want an IPv4 address, got %q", s)
	}
	*f.ip = ip
	return nil
}

// cidrFlag parses a network in CIDR form of one family.
type cidrFlag struct {
	network **net.IPNet
	v6      bool
}

func (f cidrFlag) String() string {
	if f.network == nil || *f.network == nil {
		return ""
	}
	return (*f.network).String()
}

func (f cidrFlag) Set(s string) error {
	_, network, err := net.ParseCIDR(s)
	if err != nil || parseIP(network.IP.String(), f.v6) == nil {
		return errs.Msgf("want an %s network in CIDR form, got %q", family(f.v6), s)
	}
	*f.network = network
	return nil
}

// vlanFlag parses a VLAN id from 1 to 4094, the range an 802.1Q tag
// carries for a VLAN.
type vlanFlag struct{ value *uint16 }

func (f vlanFlag) String() string {
	if f.value == nil || *f.value == 0 {
		return ""
	}
	return strconv.Itoa(int(*f.value))
}

func (f vlanFlag) Set(s string) error {
	v, err := strconv.ParseUint(s, 10, 16)
	if err != nil || v == 0 || v > 4094 {
		return errs.Msgf("want a VLAN id from 1 to 4094, got %q", s)
	}
	*f.value = uint16(v)
	return nil
}

// groupFlag parses a decimal into the group pointer, so group 0 is a set
// group and an omitted flag leaves the pointer nil.
type groupFlag struct{ value **uint16 }

func (f groupFlag) String() string {
	if f.value == nil || *f.value == nil {
		return ""
	}
	return strconv.Itoa(int(**f.value))
}

func (f groupFlag) Set(s string) error {
	v, err := strconv.ParseUint(s, 10, 16)
	if err != nil {
		return errs.Msgf("want a number from 0 to 65535, got %q", s)
	}
	*f.value = new(uint16(v))
	return nil
}

type uint8Flag struct{ value *uint8 }

func (f uint8Flag) String() string {
	if f.value == nil || *f.value == 0 {
		return ""
	}
	return strconv.Itoa(int(*f.value))
}

func (f uint8Flag) Set(s string) error {
	v, err := strconv.ParseUint(s, 10, 8)
	if err != nil || v == 0 {
		return errs.Msgf("want a number from 1 to 255, got %q", s)
	}
	*f.value = uint8(v)
	return nil
}

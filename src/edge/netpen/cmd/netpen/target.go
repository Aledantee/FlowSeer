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
// [check.Target]. A host flag takes "<ip>" or "<ip>,<mac>", an address flag
// an IP, a network flag CIDR notation, and the number flags a decimal in
// their field's range. A flag the operator leaves out leaves its field
// empty, and the behavior's own Need call reports what it lacks.
type targetFlags struct {
	check.Target
}

func (t *targetFlags) register(fs *flag.FlagSet) {
	fs.Var(hostFlag{&t.Victim}, "victim", "victim host as <ip> or <ip>,<mac>")
	fs.Var(hostFlag{&t.Gateway}, "gateway", "gateway host as <ip> or <ip>,<mac>")
	fs.Var(hostFlag{&t.Victim6}, "victim6", "IPv6 victim host as <ip> or <ip>,<mac>")
	fs.Var(hostFlag{&t.Server6}, "server6", "IPv6 server host as <ip> or <ip>,<mac>")
	fs.Var(ipFlag{&t.VirtualIP}, "vip", "first-hop redundancy virtual IP")
	fs.Var(ipFlag{&t.RouterID}, "router-id", "routing-protocol router ID")
	fs.Var(ipFlag{&t.OfferIP}, "offer-ip", "address the rogue DHCP server offers")
	fs.Var(uintFlag{&t.Group, 16}, "group", "first-hop redundancy group")
	fs.Var(uint8Flag{&t.VRID}, "vrid", "VRRP virtual router ID")
	fs.Var(uintFlag{&t.OuterVLAN, 12}, "outer-vlan", "outer VLAN of a double-tagged frame")
	fs.Var(uintFlag{&t.InnerVLAN, 12}, "inner-vlan", "inner VLAN of a double-tagged frame")
	fs.Var(uintFlag{&t.VoiceVLAN, 12}, "voice-vlan", "voice VLAN to place a phone in")
	fs.StringVar(&t.VTPDomain, "vtp-domain", "", "VTP management domain")
	fs.Var(cidrFlag{&t.Prefix6}, "prefix6", "IPv6 prefix a router advertisement carries")
	fs.Var(cidrFlag{&t.Route4}, "route4", "IPv4 route a routing update injects")
	fs.Var(cidrFlag{&t.SweepNet}, "sweep-net", "network a sweep enumerates")
}

type hostFlag struct{ host *check.Host }

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
	ip := net.ParseIP(ipText)
	if ip == nil {
		return errs.Msgf("want <ip> or <ip>,<mac>, got %q", s)
	}
	host := check.Host{IP: ip}
	if hasMAC {
		mac, err := net.ParseMAC(macText)
		if err != nil {
			return errs.Wrapf(err, "want <ip>,<mac>, got %q", s)
		}
		host.MAC = mac
	}
	*h.host = host
	return nil
}

type ipFlag struct{ ip *net.IP }

func (f ipFlag) String() string {
	if f.ip == nil || *f.ip == nil {
		return ""
	}
	return f.ip.String()
}

func (f ipFlag) Set(s string) error {
	ip := net.ParseIP(s)
	if ip == nil {
		return errs.Msgf("want an IP address, got %q", s)
	}
	*f.ip = ip
	return nil
}

type cidrFlag struct{ network **net.IPNet }

func (f cidrFlag) String() string {
	if f.network == nil || *f.network == nil {
		return ""
	}
	return (*f.network).String()
}

func (f cidrFlag) Set(s string) error {
	_, network, err := net.ParseCIDR(s)
	if err != nil {
		return errs.Msgf("want CIDR notation, got %q", s)
	}
	*f.network = network
	return nil
}

// uintFlag parses a decimal into a uint16 field that holds at most bits
// bits, so a VLAN id stays within 12 bits.
type uintFlag struct {
	value *uint16
	bits  int
}

func (f uintFlag) String() string {
	if f.value == nil || *f.value == 0 {
		return ""
	}
	return strconv.Itoa(int(*f.value))
}

func (f uintFlag) Set(s string) error {
	v, err := strconv.ParseUint(s, 10, f.bits)
	if err != nil || v == 0 {
		return errs.Msgf("want a number from 1 to %d, got %q", 1<<f.bits-1, s)
	}
	*f.value = uint16(v)
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

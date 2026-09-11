// Package netaddr provides network hardware address value types and parsers.
package netaddr

import (
	"fmt"
	"net"

	"go.aledante.io/FlowSeer/src/common/errs"
)

// MAC represents a 48-bit IEEE 802 Media Access Control address in network byte order.
// The zero value is usable and represents the all-zero MAC address (00:00:00:00:00:00).
type MAC [6]byte

// EUI64 represents a 64-bit IEEE Extended Unique Identifier in network byte order.
// The zero value is usable and represents the all-zero identifier (00:00:00:00:00:00:00:00).
type EUI64 [8]byte

// String returns the colon-separated lower-case hexadecimal representation of m.
func (m MAC) String() string {
	return fmt.Sprintf("%02x:%02x:%02x:%02x:%02x:%02x", m[0], m[1], m[2], m[3], m[4], m[5])
}

// IsGroup reports whether m is a multicast or broadcast address according to its
// Individual/Group (I/G) bit (the least significant bit of the first octet).
func (m MAC) IsGroup() bool {
	return m[0]&0x01 != 0
}

// HardwareAddr returns a copy of m as a [net.HardwareAddr].
func (m MAC) HardwareAddr() net.HardwareAddr {
	hw := make(net.HardwareAddr, len(m))
	copy(hw, m[:])

	return hw
}

// Parse parses a colon-, hyphen-, or dot-separated hexadecimal string into a [MAC].
// It returns an error if the input cannot be parsed or does not contain exactly 6 bytes.
func Parse(s string) (MAC, error) {
	hw, err := net.ParseMAC(s)
	if err != nil {
		return MAC{}, errs.New().
			Attr("addr", s).
			Cause(err).
			Msg("parse MAC address")
	}

	if len(hw) != 6 {
		return MAC{}, errs.New().
			Attr("addr", s).
			Attr("len", len(hw)).
			Msg("MAC address must be 6 bytes")
	}

	var m MAC
	copy(m[:], hw)

	return m, nil
}

// Local returns the nth locally administered unicast MAC address (02:00:00:xx:xx:xx)
// with n encoded in the lower three big-endian octets. The Universally/Locally
// Administered (U/L) bit is set and the Individual/Group (I/G) bit is clear per
// IEEE Std 802-2014 clause 8.2.2. Supplying n greater than 0xffffff is the caller's
// error; values above 24 bits truncate to the low 24 bits.
func Local(n uint32) MAC {
	return MAC{
		0x02,
		0x00,
		0x00,
		byte(n >> 16),
		byte(n >> 8),
		byte(n),
	}
}

// FromHardwareAddr converts hw into a [MAC]. It rejects any slice whose length is not exactly six.
func FromHardwareAddr(hw net.HardwareAddr) (MAC, error) {
	if len(hw) != 6 {
		return MAC{}, errs.New().
			Attr("have", len(hw)).
			Attr("want", 6).
			Msg("hardware address must be 6 bytes")
	}

	var m MAC
	copy(m[:], hw)

	return m, nil
}

// String returns the colon-separated lower-case hexadecimal representation of e.
func (e EUI64) String() string {
	return fmt.Sprintf("%02x:%02x:%02x:%02x:%02x:%02x:%02x:%02x", e[0], e[1], e[2], e[3], e[4], e[5], e[6], e[7])
}

// IsGroup reports whether e has its Individual/Group (I/G) bit set (the least significant bit of the first octet).
func (e EUI64) IsGroup() bool {
	return e[0]&0x01 != 0
}

// HardwareAddr returns a copy of e as a [net.HardwareAddr].
func (e EUI64) HardwareAddr() net.HardwareAddr {
	hw := make(net.HardwareAddr, len(e))
	copy(hw, e[:])

	return hw
}

// ParseEUI64 parses a hexadecimal string representation of an 8-byte hardware address into an [EUI64].
func ParseEUI64(s string) (EUI64, error) {
	hw, err := net.ParseMAC(s)
	if err != nil {
		return EUI64{}, errs.New().
			Attr("addr", s).
			Cause(err).
			Msg("parse EUI-64 address")
	}

	if len(hw) != 8 {
		return EUI64{}, errs.New().
			Attr("addr", s).
			Attr("len", len(hw)).
			Msg("EUI-64 address must be 8 bytes")
	}

	var out EUI64
	copy(out[:], hw)

	return out, nil
}

// EUI64FromHardwareAddr converts hw into an [EUI64]. It rejects any slice whose length is not exactly eight.
func EUI64FromHardwareAddr(hw net.HardwareAddr) (EUI64, error) {
	if len(hw) != 8 {
		return EUI64{}, errs.New().
			Attr("have", len(hw)).
			Attr("want", 8).
			Msg("hardware address must be 8 bytes")
	}

	var out EUI64
	copy(out[:], hw)

	return out, nil
}

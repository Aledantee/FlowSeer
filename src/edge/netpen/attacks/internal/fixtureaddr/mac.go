// Package fixtureaddr parses closed address literals used by fixture generators.
package fixtureaddr

import "net"

// MustMAC parses s as a MAC address. It panics if s is invalid. Generator
// callers use closed fixture inputs, so the invariant is fixed by the generator
// rather than external input.
func MustMAC(s string) net.HardwareAddr {
	mac, err := net.ParseMAC(s)
	if err != nil {
		panic(err)
	}
	return mac
}

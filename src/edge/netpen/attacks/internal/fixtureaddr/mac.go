// Package fixtureaddr parses closed address literals used by fixture generators.
package fixtureaddr

import "net"

// MustMAC parses s as a MAC address. It panics if s is invalid. Generator
// callers pass only file-level constants, so the invariant is fixed at those
// call sites; this is the documented closed-argument answer shared with
// [regexp.MustCompile].
func MustMAC(s string) net.HardwareAddr {
	mac, err := net.ParseMAC(s)
	if err != nil {
		panic(err)
	}
	return mac
}

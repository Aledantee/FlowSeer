// link_other.go is the non-Linux stub. AF_PACKET is Linux-only, so on every
// other platform Open returns ErrUnsupportedPlatform with the netpen/leg-open
// code. The build compiles and the mock-based tests run; the real-link
// round-trip test lives behind its own build tag and linux constraint.
//
//go:build !linux

package link

func open(_ string) (Leg, error) {
	return nil, ErrUnsupportedPlatform
}

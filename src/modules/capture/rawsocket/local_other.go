// local_other.go is the non-Linux stub. AF_PACKET is Linux-only, so on
// every other platform OpenLocalInterface returns ErrUnsupportedPlatform.
// The build compiles and the mock-based tests run; the real-interface
// round-trip test lives behind its own build tag and linux constraint.
//
//go:build !linux

package rawsocket

import "golang.org/x/net/bpf"

func openLocalInterface(_ string, _ bool, _ []bpf.RawInstruction) (LocalSource, error) {
	return nil, ErrUnsupportedPlatform
}

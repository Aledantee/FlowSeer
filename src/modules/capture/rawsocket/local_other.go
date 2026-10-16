// local_other.go is the stub for platforms with no local capture: neither
// Linux (AF_PACKET) nor FreeBSD on amd64 or arm64 (BPF device), the two
// builds that define openLocalInterface themselves. There
// OpenLocalInterface returns ErrUnsupportedPlatform. The build compiles and
// the mock-based tests run; the real-interface round-trip tests live behind
// their own build tags.
//
//go:build !linux && !(freebsd && (amd64 || arm64))

package rawsocket

import "golang.org/x/net/bpf"

func openLocalInterface(_ string, _ bool, _ []bpf.RawInstruction) (Source, error) {
	return nil, ErrUnsupportedPlatform
}

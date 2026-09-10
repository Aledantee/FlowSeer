// mirror_other.go is the non-Linux stub. Raw IP sockets and IP_PKTINFO are
// Linux-specific here, so on every other platform OpenMirrorReceiver returns
// ErrUnsupportedPlatform.
//
//go:build !linux

package rawsocket

import (
	"golang.org/x/net/bpf"

	capturev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/capture/v1"
)

func openMirrorReceiver(_ []capturev1.MirrorEncapsulation, _ uint32, _ string, _ []bpf.RawInstruction) (Source, error) {
	return nil, ErrUnsupportedPlatform
}

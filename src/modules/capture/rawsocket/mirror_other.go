// Platforms without a mirror socket implementation return ErrUnsupportedPlatform.
//
//go:build !linux && !(freebsd && (amd64 || arm64))

package rawsocket

import (
	"golang.org/x/net/bpf"

	capturev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/capture/v1"
)

func openMirrorReceiver(_ []capturev1.MirrorEncapsulation, _ uint32, _ string, _ []bpf.RawInstruction) (Source, error) {
	return nil, ErrUnsupportedPlatform
}

//go:build !linux

package packetio

func openSender(_ string) (Sender, error) {
	return nil, ErrUnsupportedPlatform
}

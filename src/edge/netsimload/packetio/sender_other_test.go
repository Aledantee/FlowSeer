//go:build !linux

package packetio

import "testing"

func TestOpenSenderUnsupportedPlatform(t *testing.T) {
	sender, err := OpenSender("unused")
	if sender != nil || !IsUnsupported(err) {
		t.Fatalf("OpenSender = (%v, %v), want nil and unsupported platform", sender, err)
	}
}

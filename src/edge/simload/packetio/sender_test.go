package packetio

import "testing"

func TestUnsupportedErrorIsNotClosed(t *testing.T) {
	if !IsUnsupported(ErrUnsupportedPlatform) {
		t.Fatal("ErrUnsupportedPlatform is not classified as unsupported")
	}
	if IsUnsupported(ErrClosed) {
		t.Fatal("ErrClosed is classified as unsupported")
	}
}

package packetio

import "testing"

func TestUnsupportedErrorIsNotClosed(t *testing.T) {
	if IsUnsupported(ErrClosed) {
		t.Fatal("ErrClosed is classified as unsupported")
	}
}

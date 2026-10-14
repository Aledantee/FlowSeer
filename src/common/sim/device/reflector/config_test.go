package reflector_test

import (
	"net/netip"
	"testing"

	"go.aledante.io/FlowSeer/src/common/sim/device/reflector"
	"go.aledante.io/FlowSeer/src/common/sim/layer/phy"
)

func TestReflectorAttachmentRefusesZeroPrefix(t *testing.T) {
	cfg := reflector.Config{
		Ports:       map[string]phy.Ethernet{"p1": {}},
		Attachments: map[string]reflector.Attachment{"a": {Port: "p1", Addresses: []netip.Prefix{{}}}},
	}
	if err := cfg.Validate("reflectors.r1"); err == nil {
		t.Fatal("Validate accepted an attachment with an invalid prefix")
	}
}

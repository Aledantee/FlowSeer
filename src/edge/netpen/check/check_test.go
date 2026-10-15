package check_test

import (
	"net"
	"strings"
	"testing"

	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/edge/netpen/check"
)

func fullTarget() check.Target {
	_, prefix6, _ := net.ParseCIDR("fd00::/64")
	_, route4, _ := net.ParseCIDR("10.9.0.0/24")
	_, sweep, _ := net.ParseCIDR("172.16.0.0/24")
	mac := net.HardwareAddr{0x00, 0xaa, 0xbb, 0xcc, 0xdd, 0x01}
	return check.Target{
		Victim:    check.Host{IP: net.IPv4(172, 16, 0, 10), MAC: mac},
		Gateway:   check.Host{IP: net.IPv4(172, 16, 0, 1), MAC: mac},
		Victim6:   check.Host{IP: net.ParseIP("fd00::10"), MAC: mac},
		Server6:   check.Host{IP: net.ParseIP("fd00::1"), MAC: mac},
		VirtualIP: net.IPv4(10, 0, 0, 1),
		Group:     1,
		VRID:      51,
		OuterVLAN: 1,
		InnerVLAN: 20,
		VoiceVLAN: 100,
		VTPDomain: "lab",
		RouterID:  net.IPv4(10, 0, 0, 254),
		Prefix6:   prefix6,
		Route4:    route4,
		OfferIP:   net.IPv4(172, 16, 0, 100),
		SweepNet:  sweep,
	}
}

var everyField = []check.Field{
	check.FieldVictim, check.FieldVictimMAC, check.FieldGateway, check.FieldGatewayMAC,
	check.FieldVictim6, check.FieldVictim6MAC, check.FieldServer6, check.FieldServer6MAC,
	check.FieldVirtualIP, check.FieldGroup, check.FieldVRID,
	check.FieldOuterVLAN, check.FieldInnerVLAN, check.FieldVoiceVLAN, check.FieldVTPDomain,
	check.FieldRouterID, check.FieldPrefix6, check.FieldRoute4, check.FieldOfferIP, check.FieldSweepNet,
}

// TestTargetNeedAcceptsAFullTarget pins that every field has a presence rule
// a filled target satisfies, so a new field cannot be reported missing when
// set.
func TestTargetNeedAcceptsAFullTarget(t *testing.T) {
	if err := fullTarget().Need(everyField...); err != nil {
		t.Fatalf("Need on a full target: %v, want nil", err)
	}
}

// TestTargetNeedNamesEveryMissingField pins the one-round contract: a check
// that needs three fields of an empty target gets one coded error that
// names all three and exits 2.
func TestTargetNeedNamesEveryMissingField(t *testing.T) {
	err := check.Target{}.Need(check.FieldVictim, check.FieldGateway, check.FieldVRID)
	if err == nil {
		t.Fatal("Need on an empty target returned nil")
	}
	code, ok := errs.CodeOf(err)
	if !ok || code != check.ErrCodeTargetMissing {
		t.Errorf("code = %v (set %v), want %v", code, ok, check.ErrCodeTargetMissing)
	}
	if got := errs.ExitCode(err); got != 2 {
		t.Errorf("exit code = %d, want 2", got)
	}
	for _, want := range []string{"victim", "gateway", "vrid"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not name %q", err, want)
		}
	}
	if strings.Contains(err.Error(), "mac") {
		t.Errorf("error %q names a field that was not asked for", err)
	}
}

// TestTargetNeedChecksEachPartOfAHost pins that an IP without a MAC is
// present as the address and missing as the MAC, so a behavior that frames
// to the victim asks for both.
func TestTargetNeedChecksEachPartOfAHost(t *testing.T) {
	target := check.Target{Victim: check.Host{IP: net.IPv4(172, 16, 0, 10)}}
	if err := target.Need(check.FieldVictim); err != nil {
		t.Errorf("Need(victim) with the IP set: %v, want nil", err)
	}
	err := target.Need(check.FieldVictim, check.FieldVictimMAC)
	if err == nil || !strings.Contains(err.Error(), "victim-mac") {
		t.Errorf("Need(victim, victim-mac) with no MAC = %v, want an error naming victim-mac", err)
	}
	if err != nil && strings.Contains(err.Error(), "missing victim ") {
		t.Errorf("error %q names the present IP as missing", err)
	}
}

// TestTargetNeedUnknownFieldIsMissing pins that a field the target does not
// know is reported rather than silently accepted, since a typo in a
// behavior would otherwise pass every gate.
func TestTargetNeedUnknownFieldIsMissing(t *testing.T) {
	err := fullTarget().Need(check.Field("bogus"))
	if err == nil || !strings.Contains(err.Error(), "bogus") {
		t.Fatalf("Need(bogus) on a full target = %v, want an error naming bogus", err)
	}
}

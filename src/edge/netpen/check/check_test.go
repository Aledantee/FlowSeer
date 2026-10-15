package check_test

import (
	"net"
	"strings"
	"testing"

	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/edge/netpen/check"
	"go.aledante.io/FlowSeer/src/edge/netpen/link"
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
		Group:     new(uint16(1)),
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

func mustCIDR(t *testing.T, s string) *net.IPNet {
	t.Helper()
	_, n, err := net.ParseCIDR(s)
	if err != nil {
		t.Fatalf("ParseCIDR(%q): %v", s, err)
	}
	return n
}

// TestTargetNeedFormPerField pins, per field, that clearing it in a full
// target names exactly that field's flag with the missing code, and that a
// value of the wrong family or range names the flag and the form it wants
// with the invalid code. The expected texts are literals so a rendering
// change is a visible change.
func TestTargetNeedFormPerField(t *testing.T) {
	ip6 := net.ParseIP("2001:db8::1")
	v4mapped := net.ParseIP("::ffff:10.0.0.5")
	longMAC := make(net.HardwareAddr, 20)
	longDomain := strings.Repeat("d", 33)
	cases := []struct {
		field   check.Field
		clear   func(*check.Target)
		invalid func(*check.Target)
		want    string
	}{
		{check.FieldVictim, func(tg *check.Target) { tg.Victim.IP = nil }, func(tg *check.Target) { tg.Victim.IP = ip6 }, "--victim <ip>,<mac> wants an IPv4 address"},
		{check.FieldVictimMAC, func(tg *check.Target) { tg.Victim.MAC = nil }, func(tg *check.Target) { tg.Victim.MAC = longMAC }, "--victim <ip>,<mac> wants a six-octet MAC"},
		{check.FieldGateway, func(tg *check.Target) { tg.Gateway.IP = nil }, func(tg *check.Target) { tg.Gateway.IP = ip6 }, "--gateway <ip>,<mac> wants an IPv4 address"},
		{check.FieldGatewayMAC, func(tg *check.Target) { tg.Gateway.MAC = nil }, func(tg *check.Target) { tg.Gateway.MAC = longMAC }, "--gateway <ip>,<mac> wants a six-octet MAC"},
		{check.FieldVictim6, func(tg *check.Target) { tg.Victim6.IP = nil }, func(tg *check.Target) { tg.Victim6.IP = v4mapped }, "--victim6 <ip>,<mac> wants an IPv6 address"},
		{check.FieldVictim6MAC, func(tg *check.Target) { tg.Victim6.MAC = nil }, func(tg *check.Target) { tg.Victim6.MAC = longMAC }, "--victim6 <ip>,<mac> wants a six-octet MAC"},
		{check.FieldServer6, func(tg *check.Target) { tg.Server6.IP = nil }, func(tg *check.Target) { tg.Server6.IP = net.IPv4(10, 0, 0, 5) }, "--server6 <ip>,<mac> wants an IPv6 address"},
		{check.FieldServer6MAC, func(tg *check.Target) { tg.Server6.MAC = nil }, func(tg *check.Target) { tg.Server6.MAC = longMAC }, "--server6 <ip>,<mac> wants a six-octet MAC"},
		{check.FieldVirtualIP, func(tg *check.Target) { tg.VirtualIP = nil }, func(tg *check.Target) { tg.VirtualIP = ip6 }, "--vip <ip> wants an IPv4 address"},
		{check.FieldGroup, func(tg *check.Target) { tg.Group = nil }, nil, ""},
		{check.FieldVRID, func(tg *check.Target) { tg.VRID = 0 }, nil, ""},
		{check.FieldOuterVLAN, func(tg *check.Target) { tg.OuterVLAN = 0 }, func(tg *check.Target) { tg.OuterVLAN = 4095 }, "--outer-vlan <n> wants a VLAN id from 1 to 4094"},
		{check.FieldInnerVLAN, func(tg *check.Target) { tg.InnerVLAN = 0 }, func(tg *check.Target) { tg.InnerVLAN = 4095 }, "--inner-vlan <n> wants a VLAN id from 1 to 4094"},
		{check.FieldVoiceVLAN, func(tg *check.Target) { tg.VoiceVLAN = 0 }, func(tg *check.Target) { tg.VoiceVLAN = 65535 }, "--voice-vlan <n> wants a VLAN id from 1 to 4094"},
		{check.FieldVTPDomain, func(tg *check.Target) { tg.VTPDomain = "" }, func(tg *check.Target) { tg.VTPDomain = longDomain }, "--vtp-domain <name> wants a name of at most 32 bytes"},
		{check.FieldRouterID, func(tg *check.Target) { tg.RouterID = nil }, func(tg *check.Target) { tg.RouterID = ip6 }, "--router-id <ip> wants an IPv4 address"},
		{check.FieldPrefix6, func(tg *check.Target) { tg.Prefix6 = nil }, func(tg *check.Target) { tg.Prefix6 = mustCIDR(t, "10.0.0.0/8") }, "--prefix6 <cidr> wants an IPv6 network"},
		{check.FieldRoute4, func(tg *check.Target) { tg.Route4 = nil }, func(tg *check.Target) { tg.Route4 = mustCIDR(t, "2001:db8::/64") }, "--route4 <cidr> wants an IPv4 network"},
		{check.FieldOfferIP, func(tg *check.Target) { tg.OfferIP = nil }, func(tg *check.Target) { tg.OfferIP = ip6 }, "--offer-ip <ip> wants an IPv4 address"},
		{check.FieldSweepNet, func(tg *check.Target) { tg.SweepNet = nil }, func(tg *check.Target) { tg.SweepNet = mustCIDR(t, "10.0.0.5/32") }, "--sweep-net <cidr> wants an IPv4 network with at least one host address"},
	}
	for _, tc := range cases {
		t.Run(string(tc.field)+" cleared", func(t *testing.T) {
			target := fullTarget()
			tc.clear(&target)
			err := target.Need(everyField...)
			assertTargetError(t, err, check.ErrCodeTargetMissing, "the check needs: "+tc.field.Flag())
		})
		if tc.invalid == nil {
			continue
		}
		t.Run(string(tc.field)+" invalid", func(t *testing.T) {
			target := fullTarget()
			tc.invalid(&target)
			err := target.Need(everyField...)
			assertTargetError(t, err, check.ErrCodeTargetInvalid, "the check needs: "+tc.want)
		})
	}
}

// TestTargetNeedAcceptsTheRangeBounds pins the accepting side of each
// range rule: VLAN 4094, a 32-byte domain, and a /30 sweep network pass,
// while a /31 sweep network, which leaves no host address between its
// network and broadcast addresses, is refused.
func TestTargetNeedAcceptsTheRangeBounds(t *testing.T) {
	target := fullTarget()
	target.OuterVLAN, target.InnerVLAN, target.VoiceVLAN = 4094, 4094, 4094
	target.VTPDomain = strings.Repeat("d", 32)
	target.SweepNet = mustCIDR(t, "10.0.0.4/30")
	if err := target.Need(everyField...); err != nil {
		t.Errorf("Need at the range bounds = %v, want nil", err)
	}
	target.SweepNet = mustCIDR(t, "10.0.0.4/31")
	err := target.Need(check.FieldSweepNet)
	assertTargetError(t, err, check.ErrCodeTargetInvalid, "the check needs: --sweep-net <cidr> wants an IPv4 network with at least one host address")
}

func assertTargetError(t *testing.T, err error, wantCode errs.Code, wantText string) {
	t.Helper()
	if err == nil {
		t.Fatalf("Need = nil, want %q", wantText)
	}
	if code, ok := errs.CodeOf(err); !ok || code != wantCode {
		t.Errorf("code = %v (set %v), want %v", code, ok, wantCode)
	}
	if got := errs.ExitCode(err); got != 2 {
		t.Errorf("exit code = %d, want 2", got)
	}
	if err.Error() != wantText {
		t.Errorf("Error() = %q, want %q", err.Error(), wantText)
	}
	if got := errs.UserMessage(err); got != wantText {
		t.Errorf("UserMessage() = %q, want %q", got, wantText)
	}
	if !check.IsTargetError(err) {
		t.Errorf("IsTargetError(%v) = false, want true", err)
	}
}

// TestTargetGroupZeroIsPresentAndWithinBounds pins that group 0 passes Need,
// since HSRP group 0 is a group, and that Within refuses a group the frame
// cannot carry while accepting the bound itself.
func TestTargetGroupZeroIsPresentAndWithinBounds(t *testing.T) {
	target := fullTarget()
	target.Group = new(uint16(0))
	if err := target.Need(check.FieldGroup); err != nil {
		t.Errorf("Need(group) with group 0 = %v, want nil", err)
	}
	target.Group = new(uint16(255))
	if err := target.Within(check.FieldGroup, 255); err != nil {
		t.Errorf("Within(group, 255) with group 255 = %v, want nil", err)
	}
	target.Group = new(uint16(256))
	err := target.Within(check.FieldGroup, 255)
	assertTargetError(t, err, check.ErrCodeTargetInvalid, "the check needs: --group at most 255")
	if err := (check.Target{}).Within(check.FieldGroup, 255); err != nil {
		t.Errorf("Within(group, 255) with no group = %v, want nil", err)
	}
}

// TestTargetNeedRendersAHostAsOneFlag pins that a missing victim address
// and MAC render as the one flag that supplies both, since victim-mac is no
// flag an operator can pass.
func TestTargetNeedRendersAHostAsOneFlag(t *testing.T) {
	err := check.Target{}.Need(check.FieldVictim, check.FieldVictimMAC)
	assertTargetError(t, err, check.ErrCodeTargetMissing, "the check needs: --victim <ip>,<mac>")
}

// TestTargetNeedNamesEveryMissingField pins the one-round contract: a check
// that needs three fields of an empty target gets one coded error that
// names all three and exits 2.
func TestTargetNeedNamesEveryMissingField(t *testing.T) {
	err := check.Target{}.Need(check.FieldVictim, check.FieldGateway, check.FieldVRID)
	assertTargetError(t, err, check.ErrCodeTargetMissing, "the check needs: --victim <ip>,<mac>, --gateway <ip>,<mac>, --vrid <n>")
}

// TestTargetNeedMixesMissingAndInvalid pins that one error carries both a
// missing flag and a wrong-form one, under the invalid code, so the
// operator still fixes the command line in one round.
func TestTargetNeedMixesMissingAndInvalid(t *testing.T) {
	target := check.Target{Victim: check.Host{IP: net.ParseIP("2001:db8::1")}}
	err := target.Need(check.FieldVictim, check.FieldGateway)
	assertTargetError(t, err, check.ErrCodeTargetInvalid, "the check needs: --victim <ip>,<mac> wants an IPv4 address, --gateway <ip>,<mac>")
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
	assertTargetError(t, err, check.ErrCodeTargetMissing, "the check needs: --victim <ip>,<mac>")
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

// TestIsTargetErrorRejectsOtherErrors pins that a plain error, and an error
// with another code, is not a target error, so the runner does not record
// a craft failure as a skipped check.
func TestIsTargetErrorRejectsOtherErrors(t *testing.T) {
	if check.IsTargetError(errs.Msgf("craft failed")) {
		t.Error("IsTargetError(plain error) = true, want false")
	}
	other := errs.New().Code(link.ErrCodeLegOpen).Msg("other")
	if check.IsTargetError(other) {
		t.Error("IsTargetError(other code) = true, want false")
	}
	if check.IsTargetError(nil) {
		t.Error("IsTargetError(nil) = true, want false")
	}
}

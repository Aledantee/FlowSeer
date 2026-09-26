package conformance

import (
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"

	bfdv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/protocol/bfd/v1"
)

func TestBfdSessionStatePresence(t *testing.T) {
	withAdminDown := bfdv1.BfdSession_builder{
		State: bfdv1.BfdSessionState_BFD_SESSION_STATE_ADMIN_DOWN.Enum(),
	}.Build()
	require.True(t, withAdminDown.HasState())
	require.Equal(t, bfdv1.BfdSessionState_BFD_SESSION_STATE_ADMIN_DOWN, withAdminDown.GetState())
	require.Equal(t, int32(0), int32(withAdminDown.GetState()))

	withoutState := bfdv1.BfdSession_builder{}.Build()
	require.False(t, withoutState.HasState())
}

func validBfdSession(t *testing.T) *bfdv1.BfdSession_builder {
	return &bfdv1.BfdSession_builder{
		NetworkInstance:    proto.String("default"),
		LocalDiscriminator: proto.Uint32(1),
		RemoteAddress:      ipAddress(t, "192.0.2.1"),
		DetectMultiplier:   proto.Uint32(3),
	}
}

func TestBfdSessionRules(t *testing.T) {
	withoutNetwork := validBfdSession(t)
	withoutNetwork.NetworkInstance = nil

	withoutLocalDiscr := validBfdSession(t)
	withoutLocalDiscr.LocalDiscriminator = nil

	localDiscr0 := validBfdSession(t)
	localDiscr0.LocalDiscriminator = proto.Uint32(0)

	withoutRemoteAddr := validBfdSession(t)
	withoutRemoteAddr.RemoteAddress = nil

	multiplier0 := validBfdSession(t)
	multiplier0.DetectMultiplier = proto.Uint32(0)

	multiplier256 := validBfdSession(t)
	multiplier256.DetectMultiplier = proto.Uint32(256)

	diag9 := validBfdSession(t)
	diag9.LocalDiagnostic = bfdv1.BfdDiagnostic_BFD_DIAGNOSTIC_MIS_CONNECTIVITY_DEFECT.Enum()

	diag32 := validBfdSession(t)
	diag32.LocalDiagnostic = bfdv1.BfdDiagnostic(32).Enum()

	mismatchedFamilies := validBfdSession(t)
	mismatchedFamilies.LocalAddress = ipAddress(t, "192.0.2.2")
	mismatchedFamilies.RemoteAddress = ipAddress(t, "2001:db8::1")

	matchedFamiliesV6 := validBfdSession(t)
	matchedFamiliesV6.LocalAddress = ipAddress(t, "2001:db8::2")
	matchedFamiliesV6.RemoteAddress = ipAddress(t, "2001:db8::1")

	runFieldCases(t, []fieldCase{
		{name: "valid session", message: validBfdSession(t).Build()},
		{name: "network_instance absent", message: withoutNetwork.Build(), wantField: "network_instance", wantText: "value is required"},
		{name: "local_discriminator absent", message: withoutLocalDiscr.Build(), wantField: "local_discriminator", wantText: "value is required"},
		{name: "local_discriminator 0", message: localDiscr0.Build(), wantField: "local_discriminator", wantText: "greater than or equal to 1"},
		{name: "remote_address absent", message: withoutRemoteAddr.Build(), wantField: "remote_address", wantText: "value is required"},
		{name: "detect_multiplier 0", message: multiplier0.Build(), wantField: "detect_multiplier", wantText: "greater than or equal to 1"},
		{name: "detect_multiplier 256", message: multiplier256.Build(), wantField: "detect_multiplier", wantText: "less than or equal to 255"},
		{name: "local_diagnostic 9 passes", message: diag9.Build()},
		{name: "local_diagnostic 32 fails", message: diag32.Build(), wantRule: "bfd_session.local_diagnostic"},
		{name: "mismatched address families", message: mismatchedFamilies.Build(), wantRule: "bfd_session.address_family"},
		{name: "matched address families v6 passes", message: matchedFamiliesV6.Build()},
	})
}

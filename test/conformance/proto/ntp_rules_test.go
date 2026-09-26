package conformance

import (
	"strings"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/durationpb"

	addrv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/addr/v1"
	ntpv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/protocol/ntp/v1"
)

func TestNtpAssociationRules(t *testing.T) {
	validIP := addrv1.IpAddress_builder{
		V4: addrv1.Ipv4Address_builder{
			Octets: []byte{192, 0, 2, 1},
		}.Build(),
	}.Build()

	validAssociation := func() *ntpv1.NtpAssociation_builder {
		return &ntpv1.NtpAssociation_builder{
			NetworkInstance: proto.String("Mgmt-vrf"),
			Address:         validIP,
			Name:            proto.String("ntp.example.com"),
			Stratum:         proto.Uint32(2),
			ReferenceId:     []byte{0x7f, 0x7f, 0x01, 0x01},
			Reach:           proto.Uint32(255),
			Offset:          durationpb.New(-320 * time.Microsecond),
			Delay:           durationpb.New(1250 * time.Microsecond),
			Dispersion:      durationpb.New(500 * time.Microsecond),
			Jitter:          durationpb.New(80 * time.Microsecond),
			PollInterval:    durationpb.New(64 * time.Second),
			Selection:       ntpv1.NtpPeerSelection_NTP_PEER_SELECTION_SYSTEM_PEER.Enum(),
		}
	}

	withStratum := func(stratum uint32) *ntpv1.NtpAssociation {
		b := validAssociation()
		b.Stratum = proto.Uint32(stratum)
		return b.Build()
	}

	withoutInstance := validAssociation()
	withoutInstance.NetworkInstance = nil

	withoutAddress := validAssociation()
	withoutAddress.Address = nil

	reach256 := validAssociation()
	reach256.Reach = proto.Uint32(256)

	refID3 := validAssociation()
	refID3.ReferenceId = []byte{1, 2, 3}

	refID5 := validAssociation()
	refID5.ReferenceId = []byte{1, 2, 3, 4, 5}

	negativeJitter := validAssociation()
	negativeJitter.Jitter = durationpb.New(-1 * time.Millisecond)

	negativeDispersion := validAssociation()
	negativeDispersion.Dispersion = durationpb.New(-1 * time.Millisecond)

	zeroPoll := validAssociation()
	zeroPoll.PollInterval = durationpb.New(0)

	negativeOffset := validAssociation()
	negativeOffset.Offset = durationpb.New(-5 * time.Millisecond)

	selectionZero := validAssociation()
	selectionZero.Selection = ntpv1.NtpPeerSelection_NTP_PEER_SELECTION_UNSPECIFIED.Enum()

	selectionUndefined := validAssociation()
	selectionUndefined.Selection = ntpv1.NtpPeerSelection(9).Enum()

	longName := validAssociation()
	longName.Name = proto.String(strings.Repeat("n", 254))

	runFieldCases(t, []fieldCase{
		{
			name:    "valid IOS-XE shaped association",
			message: validAssociation().Build(),
		},
		{
			name:    "stratum 0 is valid",
			message: withStratum(0),
		},
		{
			name:    "stratum 16 is valid",
			message: withStratum(16),
		},
		{
			name:    "stratum 255 is valid",
			message: withStratum(255),
		},
		{
			name:      "stratum 256 is invalid",
			message:   withStratum(256),
			wantField: "stratum",
		},
		{
			name:      "network_instance absent",
			message:   withoutInstance.Build(),
			wantField: "network_instance",
			wantText:  "required",
		},
		{
			name:      "address absent",
			message:   withoutAddress.Build(),
			wantField: "address",
			wantText:  "required",
		},
		{
			name:      "reach exceeds 255",
			message:   reach256.Build(),
			wantField: "reach",
		},
		{
			name:      "reference_id has 3 octets",
			message:   refID3.Build(),
			wantField: "reference_id",
		},
		{
			name:      "reference_id has 5 octets",
			message:   refID5.Build(),
			wantField: "reference_id",
		},
		{
			name:      "jitter is negative",
			message:   negativeJitter.Build(),
			wantField: "jitter",
		},
		{
			name:      "dispersion is negative",
			message:   negativeDispersion.Build(),
			wantField: "dispersion",
		},
		{
			name:      "poll_interval is zero",
			message:   zeroPoll.Build(),
			wantField: "poll_interval",
		},
		{
			name:    "offset -5ms is valid",
			message: negativeOffset.Build(),
		},
		{
			name:      "selection is unspecified 0",
			message:   selectionZero.Build(),
			wantField: "selection",
		},
		{
			name:      "selection is undefined enum 9",
			message:   selectionUndefined.Build(),
			wantField: "selection",
		},
		{
			name:      "name exceeds 253 chars",
			message:   longName.Build(),
			wantField: "name",
		},
	})
}

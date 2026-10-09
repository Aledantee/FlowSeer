package assembly_test

import (
	"net/netip"
	"testing"
	"time"

	"go.aledante.io/FlowSeer/src/common/sim/assembly"

	reflectormodel "go.aledante.io/FlowSeer/src/common/sim/device/reflector"

	hostmodel "go.aledante.io/FlowSeer/src/common/sim/device/host"

	"go.aledante.io/FlowSeer/src/common/net/netaddr"
	"go.aledante.io/FlowSeer/src/common/net/vlan"
	"go.aledante.io/FlowSeer/src/common/sim/device/vswitch"
	"go.aledante.io/FlowSeer/src/common/sim/fabric"
	"go.aledante.io/FlowSeer/src/common/sim/internal/simtest"
	"go.aledante.io/FlowSeer/src/common/sim/layer/phy"
	cablemodel "go.aledante.io/FlowSeer/src/common/sim/medium/cable"
	"go.aledante.io/FlowSeer/src/common/sim/trace"
)

// TestDiffCoversEveryConfigField verifies that every exported assembly.Config field reaches
// assembly.Diff. Switches holds one switch with only a MAC set, no capability:
// assembly.Diff delegates a present switch to vswitch.Diff, and vswitch's own
// diff_coverage_test.go already walks every capability leaf; seeding a bare switch here
// keeps this gate scoped to fabric's own fields instead of re-walking vswitch.Config a
// second time.
func TestDiffCoversEveryConfigField(t *testing.T) {
	vid := vlan.ID(10)
	delay := 2 * time.Millisecond
	seed := assembly.Config{
		Start: time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC),
		Switches: map[string]vswitch.Config{
			"sw1": {MAC: netaddr.MAC{0x02, 0x00, 0x00, 0x00, 0x00, 0x01}},
		},
		Hosts: map[string]hostmodel.Config{
			"h1": {
				Address: netaddr.MAC{0x02, 0x00, 0x00, 0x00, 0x00, 0x02},
				VLAN:    &vid,
				IP: &hostmodel.IP{
					Addresses: []netip.Prefix{netip.MustParsePrefix("10.0.10.7/24")},
					Gateway:   netip.MustParseAddr("10.0.10.1"),
					Neighbors: map[netip.Addr]netaddr.MAC{
						netip.MustParseAddr("10.0.10.1"): {0x02, 0x00, 0x00, 0x00, 0x00, 0x03},
					},
				},
				Ethernet: phy.Ethernet{SupportedSpeedsBPS: []uint64{1_000_000_000}},
				Accept: hostmodel.Accept{
					Promiscuous:  true,
					AllMulticast: true,
					Multicast:    []netaddr.MAC{{0x01, 0x00, 0x5e, 0x00, 0x00, 0x01}},
				},
			},
		},
		Reflectors: map[string]reflectormodel.Config{
			"r1": {
				Address: netaddr.MAC{0x02, 0x00, 0x00, 0x00, 0x00, 0x04},
				Ports:   map[string]phy.Ethernet{"p1": {SupportedSpeedsBPS: []uint64{1_000_000_000}}},
				Attachments: map[string]reflectormodel.Attachment{
					"p1": {Port: "p1", VLAN: &vid, Addresses: []netip.Prefix{netip.MustParsePrefix("10.0.20.7/24")}},
				},
			},
		},
		Cables: []fabric.Cable{
			{
				Cable: cablemodel.Cable{LengthMeters: 3, TopSpeedBPS: 1_000_000_000, Fault: cablemodel.Fault{Kind: cablemodel.FaultLoseEveryNth, N: 5, Sequence: []uint{1}}, Medium: cablemodel.TwistedPair, Delay: &delay},
				A:     fabric.Endpoint{Node: "sw1", Port: "1/1/1"},
				B:     fabric.Endpoint{Node: "h1"},

				Evidence: []trace.EvidenceRef{"survey:rack-walk"},
			},
		},
		Uncabled: []fabric.Uncabled{
			{
				Endpoint: fabric.Endpoint{Node: "sw1", Port: "1/1/2"},
				Evidence: []trace.EvidenceRef{"survey:empty-port"},
			},
		},
		PhyAssumption: &cablemodel.PhyAssumption{
			Medium:   cablemodel.MultimodeFiber,
			Ethernet: phy.Ethernet{SupportedSpeedsBPS: []uint64{10_000_000_000}},
		},
	}

	exemptions := map[string]string{
		".Cables.*.Cable.Fault.Sequence.*": "normalizedFault (fabric/diff.go) clears Sequence whenever Kind is not " +
			"FaultLoseSequence; this fixture's Kind is FaultLoseEveryNth, under which N is the active field and " +
			"Sequence has no arm to exercise, the same one-slot-two-roles split Fault.N and Fault.Sequence share",
		".Cables.*.Evidence.*": "Evidence is provenance for why a cable's other fields hold their values, not part " +
			"of the compared configuration; assembly.Diff never reads Cable.Evidence, the same way it never reads " +
			"Uncabled.Evidence",
		".Uncabled.*.Evidence.*": "Evidence is provenance for why an uncabled endpoint is uncabled, not part of " +
			"the compared configuration; diffUncabled (fabric/diff.go) matches entries by Endpoint alone",
	}

	simtest.AssertDiffCoversConfig(t, seed, assembly.Config.Normalize, assembly.Diff, exemptions)
}

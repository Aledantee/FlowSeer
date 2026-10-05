package stp_test

import (
	"testing"
	"time"

	"go.aledante.io/FlowSeer/src/common/net/bpdu"
	"go.aledante.io/FlowSeer/src/common/net/vlan"
	"go.aledante.io/FlowSeer/src/common/sim/layer"
	"go.aledante.io/FlowSeer/src/common/sim/layer/stp"
)

// firstMSTIBridgePriorityOctet is the payload index of octet 14 of the first MSTI
// Configuration Message (IEEE 802.1Q-2003 Figure 14-2): the 3-octet LLC
// header, the 102-octet MST body, and 13 octets into the record.
const firstMSTIBridgePriorityOctet = 3 + 102 + 13

// receivedMSTI feeds an internal BPDU carrying one MSTI 1 record to a bridge
// with default priorities and returns the bridge's view of VLAN 10, which
// MSTI 1 carries.
func receivedMSTI(t *testing.T, rec bpdu.MSTIRecord, cistPortID uint16) (*stp.Layer, bpdu.BridgeID) {
	t.Helper()

	t0 := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	region := stp.MST{
		Name:      "region-1",
		Instances: map[bpdu.MSTID]stp.Instance{1: {VLANs: []vlan.ID{10}}},
	}
	cid := region.ConfigID()

	l := mustNewSTP(t, stp.Config{
		Address: mustMAC(t, "02:00:00:00:00:30"),
		Ports:   map[string]stp.Port{"1/1/1": {}},
		MST:     &region,
	}, mustPortTable(t, "1/1/1"))
	l.LinkChange(t0, "1/1/1", true, true, 1_000_000_000)

	sender := bpdu.BridgeID{Priority: 0x1000, Address: mustMAC(t, "02:00:00:00:00:01")}
	b := bpdu.BPDU{
		RootID:               sender,
		BridgeID:             sender,
		PortID:               cistPortID,
		HelloTime:            2 * time.Second,
		MaxAge:               20 * time.Second,
		ForwardDelay:         15 * time.Second,
		ConfigID:             &cid,
		RegionalRootID:       sender,
		InternalRootPathCost: 0,
		RemainingHops:        20,
		MSTIs:                []bpdu.MSTIRecord{rec},
	}
	b.SetRole(bpdu.RoleDesignated)

	l.Receive(t0.Add(time.Second), "1/1/1", b)

	return l, sender
}

func mstiRecord(bridgePriority, portPriority uint8) bpdu.MSTIRecord {
	return bpdu.MSTIRecord{
		MSTID:          1,
		Flags:          0x0c, // Designated role.
		RegionalRootID: bpdu.BridgeID{Priority: 0x1001},
		BridgePriority: bridgePriority,
		PortPriority:   portPriority,
		RemainingHops:  19,
	}
}

// TestMSTIRecordCarriesTheBridgePriorityInTheHighNibble reads the octet from
// the payload with no call to Decode: a bridge with instance priority 0x4000
// sends 0x40 in bits 5 to 8 of octet 14 (IEEE 802.1Q-2003 14.6.1 d)).
func TestMSTIRecordCarriesTheBridgePriorityInTheHighNibble(t *testing.T) {
	t.Parallel()

	t0 := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	l := mustNewSTP(t, stp.Config{
		Address: mustMAC(t, "00:11:22:33:44:01"),
		Ports:   map[string]stp.Port{"1/1/1": {}},
		MST: &stp.MST{
			Name: "region-1",
			Instances: map[bpdu.MSTID]stp.Instance{
				1: {Priority: 0x4000, PriorityPresent: true, VLANs: []vlan.ID{10}},
			},
		},
	}, mustPortTable(t, "1/1/1"))

	fx := l.LinkChange(t0, "1/1/1", true, true, 1_000_000_000)
	if len(fx.Emissions) != 1 {
		t.Fatalf("emissions = %d, want 1", len(fx.Emissions))
	}

	payload := fx.Emissions[0].Frame.Payload
	if got := payload[firstMSTIBridgePriorityOctet]; got != 0x40 {
		t.Errorf("MSTI bridge priority octet = 0x%02x, want 0x40", got)
	}
}

// TestMSTIRecordBridgePriorityIsReadFromTheHighNibble pins the receive half:
// octet 14 is the bridge priority's top byte, so 0x40 names the identifier
// 0x4000 plus the MSTID.
func TestMSTIRecordBridgePriorityIsReadFromTheHighNibble(t *testing.T) {
	t.Parallel()

	l, sender := receivedMSTI(t, mstiRecord(0x40, 0x80), 0x8001)

	got := l.VLANPortInfo(10, "1/1/1").Designated
	want := bpdu.BridgeID{Priority: 0x4001, Address: sender.Address}
	if got != want {
		t.Errorf("MSTI 1 designated bridge = %v, want %v", got, want)
	}
}

// TestMSTIRecordPortIdentifierKeepsTheCISTPortNumber pins IEEE 802.1Q-2003
// 14.2.3 and 14.6.1 e): the MSTI port identifier is the record's priority
// nibble over the low 12 bits of the CIST Port Identifier, and bits 1 to 4 of
// the record's octet are ignored.
func TestMSTIRecordPortIdentifierKeepsTheCISTPortNumber(t *testing.T) {
	t.Parallel()

	l, _ := receivedMSTI(t, mstiRecord(0x40, 0x8f), 0x8103)

	if got := l.VLANPortInfo(10, "1/1/1").DesignatedPort; got != 0x8103 {
		t.Errorf("MSTI 1 designated port = 0x%04x, want 0x8103", got)
	}
}

// TestInstancePortPriorityIsResolvedOnceInNormalize pins that an instance or
// VLAN port without a priority takes the bridge port's, and that the
// retention key, Diff, and the layer read that value alone.
func TestInstancePortPriorityIsResolvedOnceInNormalize(t *testing.T) {
	t.Parallel()

	config := func(instance stp.InstancePort) stp.Config {
		return stp.Config{
			Address: mustMAC(t, "00:11:22:33:44:01"),
			Ports:   map[string]stp.Port{"1/1/1": {Priority: 32, PriorityPresent: true}},
			MST: &stp.MST{
				Name:      "region-1",
				Instances: map[bpdu.MSTID]stp.Instance{1: {VLANs: []vlan.ID{10}, Ports: map[string]stp.InstancePort{"1/1/1": instance}}},
			},
		}
	}
	env := layer.Env{Ports: mustPortTable(t, "1/1/1")}

	t.Run("unset against an explicit 128 differs", func(t *testing.T) {
		t.Parallel()

		unset, set := config(stp.InstancePort{}), config(stp.InstancePort{Priority: 128, PriorityPresent: true})
		if stp.RetentionKey(unset, env) == stp.RetentionKey(set, env) {
			t.Error("retention key is equal for priority 32 (inherited) and 128")
		}

		var from, to string
		for _, c := range stp.Diff(unset, set) {
			if c.Subject.Kind == "mst_instance_port" && c.Field == "priority" {
				from, to = factCanonical(c.From), factCanonical(c.To)
			}
		}
		if from != "stp.port_priority=32" || to != "stp.port_priority=128" {
			t.Errorf("Diff priority change = %q to %q, want 32 to 128", from, to)
		}
	})

	t.Run("unset against an explicit copy of the bridge port's is equal", func(t *testing.T) {
		t.Parallel()

		unset, set := config(stp.InstancePort{}), config(stp.InstancePort{Priority: 32, PriorityPresent: true})
		if a, b := stp.RetentionKey(unset, env), stp.RetentionKey(set, env); a != b {
			t.Errorf("retention keys differ for the same priority:\n%s\n%s", a, b)
		}
		if changes := stp.Diff(unset, set); len(changes) != 0 {
			t.Errorf("Diff = %v, want no change", changes)
		}
	})

	t.Run("Normalize fills the instance port and marks it present", func(t *testing.T) {
		t.Parallel()

		got := config(stp.InstancePort{}).Normalize(env).MST.Instances[1].Ports["1/1/1"]
		if got.Priority != 32 || !got.PriorityPresent {
			t.Errorf("normalized instance port = %+v, want priority 32 present", got)
		}
	})

	t.Run("a VLAN tree port inherits the same way", func(t *testing.T) {
		t.Parallel()

		cfg := config(stp.InstancePort{})
		cfg.MST = nil
		cfg.PVST = &stp.PVST{Trees: map[vlan.ID]stp.Tree{10: {Ports: map[string]stp.InstancePort{"1/1/1": {}}}}}
		got := cfg.Normalize(env).PVST.Trees[10].Ports["1/1/1"]
		if got.Priority != 32 || !got.PriorityPresent {
			t.Errorf("normalized tree port = %+v, want priority 32 present", got)
		}
	})
}

// TestReceivedHelloTimeBelowOneSecondIsStoredAsOneSecond pins that a BPDU
// announcing no hello interval still elects its root, and that the layer ages
// the information three hello times of the 1 second minimum.
func TestReceivedHelloTimeBelowOneSecondIsStoredAsOneSecond(t *testing.T) {
	t.Parallel()

	t0 := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	l := mustNewSTP(t, stp.Config{
		Address: mustMAC(t, "02:00:00:00:00:30"),
		Ports:   map[string]stp.Port{"1/1/1": {}},
	}, mustPortTable(t, "1/1/1"))
	l.LinkChange(t0, "1/1/1", true, true, 1_000_000_000)

	root := bpdu.BridgeID{Priority: 0x1000, Address: mustMAC(t, "02:00:00:00:00:01")}
	b := bpdu.BPDU{
		RootID:       root,
		BridgeID:     root,
		PortID:       0x8001,
		MaxAge:       20 * time.Second,
		ForwardDelay: 15 * time.Second,
	}
	b.SetRole(bpdu.RoleDesignated)

	rx := t0.Add(time.Second)
	l.Receive(rx, "1/1/1", b)
	if got := l.PortInfo("1/1/1").Role; got != bpdu.RoleRoot {
		t.Fatalf("role after the BPDU = %v, want Root", got)
	}

	l.Advance(rx.Add(3*time.Second - time.Millisecond))
	if got := l.PortInfo("1/1/1").Role; got != bpdu.RoleRoot {
		t.Errorf("role just before 3 s = %v, want Root", got)
	}

	l.Advance(rx.Add(3 * time.Second))
	if got := l.PortInfo("1/1/1").Role; got == bpdu.RoleRoot {
		t.Errorf("role at 3 s = %v, want the information expired", got)
	}
}

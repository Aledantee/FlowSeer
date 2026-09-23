package fabric

import (
	"net/netip"
	"reflect"
	"slices"
	"testing"
	"time"

	"go.aledante.io/FlowSeer/src/common/net/arp"
	"go.aledante.io/FlowSeer/src/common/net/ethernet"
	"go.aledante.io/FlowSeer/src/common/net/ip"
	"go.aledante.io/FlowSeer/src/common/net/netaddr"
	"go.aledante.io/FlowSeer/src/common/net/vlan"
	"go.aledante.io/FlowSeer/src/common/netsim/trace"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/bridge"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/mcast"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/phy"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/port"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/routing"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/stp"
)

type fieldClassification string

const (
	fieldIncluded fieldClassification = "included"
	fieldExcluded fieldClassification = "excluded"
)

var snapshotFieldClasses = map[string]fieldClassification{
	"Clock":        fieldExcluded,
	"Queue":        fieldExcluded,
	"Queued":       fieldExcluded,
	"Links":        fieldIncluded,
	"Devices":      fieldIncluded,
	"Busy":         fieldExcluded,
	"EgressDepths": fieldExcluded,
}

var deviceFieldClasses = map[string]fieldClassification{
	"Entries":       fieldIncluded,
	"Groups":        fieldIncluded,
	"RouterPorts":   fieldIncluded,
	"Ports":         fieldIncluded,
	"Power":         fieldIncluded,
	"Counters":      fieldExcluded,
	"Roles":         fieldExcluded,
	"TreeRoles":     fieldIncluded,
	"RelayCounters": fieldExcluded,
	"Neighbors":     fieldIncluded,
}

var portInfoFieldClasses = map[string]fieldClassification{
	"MSTID":              fieldIncluded,
	"Role":               fieldIncluded,
	"State":              fieldIncluded,
	"BlockReason":        fieldIncluded,
	"Priority":           fieldIncluded,
	"PathCost":           fieldIncluded,
	"DesignatedRoot":     fieldIncluded,
	"Designated":         fieldIncluded,
	"DesignatedPort":     fieldIncluded,
	"DesignatedCost":     fieldIncluded,
	"PointToPoint":       fieldIncluded,
	"Edge":               fieldIncluded,
	"ForwardTransitions": fieldExcluded,
	"TxBPDUs":            fieldExcluded,
	"RxBPDUs":            fieldExcluded,
	"BadBPDUs":           fieldExcluded,
	"SendRSTP":           fieldIncluded,
}

var neighborEntryFieldClasses = map[string]fieldClassification{
	"VRF":       fieldIncluded,
	"Interface": fieldIncluded,
	"Addr":      fieldIncluded,
	"MAC":       fieldIncluded,
	"State":     fieldIncluded,
	"HoldDepth": fieldIncluded,
}

func checkFieldClasses(t *testing.T, typ reflect.Type, classes map[string]fieldClassification) {
	t.Helper()
	seen := make(map[string]bool, typ.NumField())
	for i := 0; i < typ.NumField(); i++ {
		f := typ.Field(i)
		seen[f.Name] = true
		class, ok := classes[f.Name]
		if !ok {
			t.Errorf("field %s.%s has no entry in classification table", typ.Name(), f.Name)
			continue
		}
		if class != fieldIncluded && class != fieldExcluded {
			t.Errorf("field %s.%s has invalid classification %q", typ.Name(), f.Name, class)
		}
	}
	for name := range classes {
		if !seen[name] {
			t.Errorf("classification table names %s.%s, which no longer exists", typ.Name(), name)
		}
	}
}

func TestFingerprintFieldClassificationWalk(t *testing.T) {
	t.Parallel()

	checkFieldClasses(t, reflect.TypeOf(Snapshot{}), snapshotFieldClasses)
	checkFieldClasses(t, reflect.TypeOf(Device{}), deviceFieldClasses)
	checkFieldClasses(t, reflect.TypeOf(stp.PortInfo{}), portInfoFieldClasses)
	checkFieldClasses(t, reflect.TypeOf(routing.NeighborEntry{}), neighborEntryFieldClasses)
}

func baseSnapshotForTest() Snapshot {
	return Snapshot{
		Clock: time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC),
		Devices: map[string]Device{
			"sw1": {
				Ports: []port.Port{
					{Name: "1/1/1", OperStatus: port.Up},
				},
				TreeRoles: map[vlan.ID]map[string]stp.PortInfo{
					10: {
						"1/1/1": {
							MSTID:          1,
							Role:           stp.RoleRoot,
							State:          stp.StateForwarding,
							BlockReason:    "",
							Priority:       128,
							PathCost:       20000,
							DesignatedRoot: stp.BridgeID{Priority: 4096, Address: netaddr.MAC{1, 2, 3, 4, 5, 6}},
							Designated:     stp.BridgeID{Priority: 4096, Address: netaddr.MAC{1, 2, 3, 4, 5, 6}},
							DesignatedPort: 1,
							DesignatedCost: 0,
							PointToPoint:   true,
							Edge:           false,
							SendRSTP:       true,
						},
					},
				},
				Entries: []bridge.Entry{
					{
						FID:      10,
						MAC:      netaddr.MAC{0, 1, 2, 3, 4, 5},
						Port:     "1/1/1",
						Origin:   bridge.Configured,
						Lifetime: bridge.Static,
					},
				},
				Groups: map[vlan.ID][]mcast.Entry{
					10: {
						{
							Group: netip.MustParseAddr("239.1.1.1"),
							Port:  "1/1/1",
							Mode:  mcast.Include,
						},
					},
				},
				RouterPorts: map[vlan.ID][]mcast.RouterPort{
					10: {
						{
							Port:     "1/1/1",
							Origin:   mcast.Configured,
							Lifetime: mcast.Static,
						},
					},
				},
				Power: vswitch.PowerResult{
					Allocation: phy.Allocation{
						Ports: map[string]phy.PortAllocation{
							"1/1/1": {
								State:         phy.PowerDelivered,
								MinMilliwatts: 1000,
								MaxMilliwatts: 5000,
								Denial:        "",
							},
						},
						Groups: map[string]phy.GroupAllocation{
							"g1": {
								BudgetMilliwatts:    30000,
								AllocatedMilliwatts: 5000,
								RemainderMilliwatts: 25000,
							},
						},
					},
				},
				Neighbors: []routing.NeighborEntry{
					{
						VRF:       "default",
						Interface: "1/1/1",
						Addr:      netip.MustParseAddr("10.0.0.1"),
						MAC:       netaddr.MAC{0, 1, 2, 3, 4, 6},
						State:     routing.NeighborReachable,
						HoldDepth: 0,
					},
				},
			},
		},
		Links: []Link{
			{
				Cable: Cable{
					A:      Endpoint{Node: "sw1", Port: "1/1/1"},
					B:      Endpoint{Node: "sw2", Port: "1/1/1"},
					Medium: TwistedPair,
				},
				A: LinkEnd{
					Endpoint: Endpoint{Node: "sw1", Port: "1/1/1"},
					Oper:     port.Up,
					Speed:    phy.Link{SpeedBPS: 1_000_000_000, DuplexA: phy.Full},
				},
				B: LinkEnd{
					Endpoint: Endpoint{Node: "sw2", Port: "1/1/1"},
					Oper:     port.Up,
					Speed:    phy.Link{SpeedBPS: 1_000_000_000, DuplexA: phy.Full},
				},
			},
		},
	}
}

func cloneSnapshot(s Snapshot) Snapshot {
	cp := s
	if s.Devices != nil {
		cp.Devices = make(map[string]Device, len(s.Devices))
		for k, v := range s.Devices {
			d := v
			d.Ports = slices.Clone(v.Ports)
			if v.TreeRoles != nil {
				d.TreeRoles = make(map[vlan.ID]map[string]stp.PortInfo, len(v.TreeRoles))
				for vid, pMap := range v.TreeRoles {
					d.TreeRoles[vid] = make(map[string]stp.PortInfo, len(pMap))
					for pName, pInfo := range pMap {
						d.TreeRoles[vid][pName] = pInfo
					}
				}
			}
			d.Entries = slices.Clone(v.Entries)
			if v.Groups != nil {
				d.Groups = make(map[vlan.ID][]mcast.Entry, len(v.Groups))
				for vid, gList := range v.Groups {
					d.Groups[vid] = slices.Clone(gList)
				}
			}
			if v.RouterPorts != nil {
				d.RouterPorts = make(map[vlan.ID][]mcast.RouterPort, len(v.RouterPorts))
				for vid, rList := range v.RouterPorts {
					d.RouterPorts[vid] = slices.Clone(rList)
				}
			}
			if v.Power.Ports != nil || v.Power.Groups != nil {
				pMap := make(map[string]phy.PortAllocation, len(v.Power.Ports))
				for pName, pa := range v.Power.Ports {
					pMap[pName] = pa
				}
				gMap := make(map[string]phy.GroupAllocation, len(v.Power.Groups))
				for gName, ga := range v.Power.Groups {
					gMap[gName] = ga
				}
				d.Power.Allocation = phy.Allocation{Ports: pMap, Groups: gMap}
			}
			d.Neighbors = slices.Clone(v.Neighbors)
			cp.Devices[k] = d
		}
	}
	if s.Links != nil {
		cp.Links = slices.Clone(s.Links)
	}
	return cp
}

func TestFingerprintInjectiveAcrossIncludedFields(t *testing.T) {
	t.Parallel()

	base := baseSnapshotForTest()
	baseFP := base.Fingerprint()

	type mutationCase struct {
		name   string
		mutate func(s *Snapshot)
	}

	cases := []mutationCase{
		{
			name: "device_name",
			mutate: func(s *Snapshot) {
				dev := s.Devices["sw1"]
				delete(s.Devices, "sw1")
				s.Devices["sw2"] = dev
			},
		},
		{
			name: "port_oper_status",
			mutate: func(s *Snapshot) {
				dev := s.Devices["sw1"]
				dev.Ports[0].OperStatus = port.Down
				s.Devices["sw1"] = dev
			},
		},
		{
			name: "port_name",
			mutate: func(s *Snapshot) {
				dev := s.Devices["sw1"]
				dev.Ports[0].Name = "1/1/2"
				s.Devices["sw1"] = dev
			},
		},
		{
			name: "tree_roles_vid",
			mutate: func(s *Snapshot) {
				dev := s.Devices["sw1"]
				tree := dev.TreeRoles[10]
				delete(dev.TreeRoles, 10)
				dev.TreeRoles[20] = tree
				s.Devices["sw1"] = dev
			},
		},
		{
			name: "tree_roles_port_name",
			mutate: func(s *Snapshot) {
				dev := s.Devices["sw1"]
				info := dev.TreeRoles[10]["1/1/1"]
				delete(dev.TreeRoles[10], "1/1/1")
				dev.TreeRoles[10]["1/1/2"] = info
				s.Devices["sw1"] = dev
			},
		},
		{
			name: "tree_roles_mstid",
			mutate: func(s *Snapshot) {
				dev := s.Devices["sw1"]
				info := dev.TreeRoles[10]["1/1/1"]
				info.MSTID = 2
				dev.TreeRoles[10]["1/1/1"] = info
				s.Devices["sw1"] = dev
			},
		},
		{
			name: "tree_roles_role",
			mutate: func(s *Snapshot) {
				dev := s.Devices["sw1"]
				info := dev.TreeRoles[10]["1/1/1"]
				info.Role = stp.RoleDesignated
				dev.TreeRoles[10]["1/1/1"] = info
				s.Devices["sw1"] = dev
			},
		},
		{
			name: "tree_roles_state",
			mutate: func(s *Snapshot) {
				dev := s.Devices["sw1"]
				info := dev.TreeRoles[10]["1/1/1"]
				info.State = stp.StateDiscarding
				dev.TreeRoles[10]["1/1/1"] = info
				s.Devices["sw1"] = dev
			},
		},
		{
			name: "tree_roles_block_reason",
			mutate: func(s *Snapshot) {
				dev := s.Devices["sw1"]
				info := dev.TreeRoles[10]["1/1/1"]
				info.BlockReason = stp.BlockReasonBPDUGuard
				dev.TreeRoles[10]["1/1/1"] = info
				s.Devices["sw1"] = dev
			},
		},
		{
			name: "tree_roles_priority",
			mutate: func(s *Snapshot) {
				dev := s.Devices["sw1"]
				info := dev.TreeRoles[10]["1/1/1"]
				info.Priority = 64
				dev.TreeRoles[10]["1/1/1"] = info
				s.Devices["sw1"] = dev
			},
		},
		{
			name: "tree_roles_path_cost",
			mutate: func(s *Snapshot) {
				dev := s.Devices["sw1"]
				info := dev.TreeRoles[10]["1/1/1"]
				info.PathCost = 40000
				dev.TreeRoles[10]["1/1/1"] = info
				s.Devices["sw1"] = dev
			},
		},
		{
			name: "tree_roles_designated_root",
			mutate: func(s *Snapshot) {
				dev := s.Devices["sw1"]
				info := dev.TreeRoles[10]["1/1/1"]
				info.DesignatedRoot.Priority = 8192
				dev.TreeRoles[10]["1/1/1"] = info
				s.Devices["sw1"] = dev
			},
		},
		{
			name: "tree_roles_designated",
			mutate: func(s *Snapshot) {
				dev := s.Devices["sw1"]
				info := dev.TreeRoles[10]["1/1/1"]
				info.Designated.Priority = 8192
				dev.TreeRoles[10]["1/1/1"] = info
				s.Devices["sw1"] = dev
			},
		},
		{
			name: "tree_roles_designated_port",
			mutate: func(s *Snapshot) {
				dev := s.Devices["sw1"]
				info := dev.TreeRoles[10]["1/1/1"]
				info.DesignatedPort = 2
				dev.TreeRoles[10]["1/1/1"] = info
				s.Devices["sw1"] = dev
			},
		},
		{
			name: "tree_roles_designated_cost",
			mutate: func(s *Snapshot) {
				dev := s.Devices["sw1"]
				info := dev.TreeRoles[10]["1/1/1"]
				info.DesignatedCost = 100
				dev.TreeRoles[10]["1/1/1"] = info
				s.Devices["sw1"] = dev
			},
		},
		{
			name: "tree_roles_point_to_point",
			mutate: func(s *Snapshot) {
				dev := s.Devices["sw1"]
				info := dev.TreeRoles[10]["1/1/1"]
				info.PointToPoint = false
				dev.TreeRoles[10]["1/1/1"] = info
				s.Devices["sw1"] = dev
			},
		},
		{
			name: "tree_roles_edge",
			mutate: func(s *Snapshot) {
				dev := s.Devices["sw1"]
				info := dev.TreeRoles[10]["1/1/1"]
				info.Edge = true
				dev.TreeRoles[10]["1/1/1"] = info
				s.Devices["sw1"] = dev
			},
		},
		{
			name: "tree_roles_send_rstp",
			mutate: func(s *Snapshot) {
				dev := s.Devices["sw1"]
				info := dev.TreeRoles[10]["1/1/1"]
				info.SendRSTP = false
				dev.TreeRoles[10]["1/1/1"] = info
				s.Devices["sw1"] = dev
			},
		},
		{
			name: "fdb_fid",
			mutate: func(s *Snapshot) {
				dev := s.Devices["sw1"]
				dev.Entries[0].FID = 20
				s.Devices["sw1"] = dev
			},
		},
		{
			name: "fdb_mac",
			mutate: func(s *Snapshot) {
				dev := s.Devices["sw1"]
				dev.Entries[0].MAC = netaddr.MAC{9, 9, 9, 9, 9, 9}
				s.Devices["sw1"] = dev
			},
		},
		{
			name: "fdb_port",
			mutate: func(s *Snapshot) {
				dev := s.Devices["sw1"]
				dev.Entries[0].Port = "1/1/2"
				s.Devices["sw1"] = dev
			},
		},
		{
			name: "fdb_origin",
			mutate: func(s *Snapshot) {
				dev := s.Devices["sw1"]
				dev.Entries[0].Origin = bridge.Observed
				s.Devices["sw1"] = dev
			},
		},
		{
			name: "fdb_lifetime",
			mutate: func(s *Snapshot) {
				dev := s.Devices["sw1"]
				dev.Entries[0].Lifetime = bridge.Aging
				s.Devices["sw1"] = dev
			},
		},
		{
			name: "mcast_group",
			mutate: func(s *Snapshot) {
				dev := s.Devices["sw1"]
				dev.Groups[10][0].Group = netip.MustParseAddr("239.2.2.2")
				s.Devices["sw1"] = dev
			},
		},
		{
			name: "mcast_port",
			mutate: func(s *Snapshot) {
				dev := s.Devices["sw1"]
				dev.Groups[10][0].Port = "1/1/2"
				s.Devices["sw1"] = dev
			},
		},
		{
			name: "mcast_mode",
			mutate: func(s *Snapshot) {
				dev := s.Devices["sw1"]
				dev.Groups[10][0].Mode = mcast.Exclude
				s.Devices["sw1"] = dev
			},
		},
		{
			name: "mcast_rport_port",
			mutate: func(s *Snapshot) {
				dev := s.Devices["sw1"]
				dev.RouterPorts[10][0].Port = "1/1/2"
				s.Devices["sw1"] = dev
			},
		},
		{
			name: "mcast_rport_origin",
			mutate: func(s *Snapshot) {
				dev := s.Devices["sw1"]
				dev.RouterPorts[10][0].Origin = mcast.Observed
				s.Devices["sw1"] = dev
			},
		},
		{
			name: "mcast_rport_lifetime",
			mutate: func(s *Snapshot) {
				dev := s.Devices["sw1"]
				dev.RouterPorts[10][0].Lifetime = mcast.Aging
				s.Devices["sw1"] = dev
			},
		},
		{
			name: "power_port_state",
			mutate: func(s *Snapshot) {
				dev := s.Devices["sw1"]
				pa := dev.Power.Ports["1/1/1"]
				pa.State = phy.PowerDenied
				dev.Power.Ports["1/1/1"] = pa
				s.Devices["sw1"] = dev
			},
		},
		{
			name: "power_port_min_mw",
			mutate: func(s *Snapshot) {
				dev := s.Devices["sw1"]
				pa := dev.Power.Ports["1/1/1"]
				pa.MinMilliwatts = 2000
				dev.Power.Ports["1/1/1"] = pa
				s.Devices["sw1"] = dev
			},
		},
		{
			name: "power_port_max_mw",
			mutate: func(s *Snapshot) {
				dev := s.Devices["sw1"]
				pa := dev.Power.Ports["1/1/1"]
				pa.MaxMilliwatts = 8000
				dev.Power.Ports["1/1/1"] = pa
				s.Devices["sw1"] = dev
			},
		},
		{
			name: "power_port_denial",
			mutate: func(s *Snapshot) {
				dev := s.Devices["sw1"]
				pa := dev.Power.Ports["1/1/1"]
				pa.Denial = trace.Reason("power-denied")
				dev.Power.Ports["1/1/1"] = pa
				s.Devices["sw1"] = dev
			},
		},
		{
			name: "power_group_budget",
			mutate: func(s *Snapshot) {
				dev := s.Devices["sw1"]
				ga := dev.Power.Groups["g1"]
				ga.BudgetMilliwatts = 50000
				dev.Power.Groups["g1"] = ga
				s.Devices["sw1"] = dev
			},
		},
		{
			name: "power_group_allocated",
			mutate: func(s *Snapshot) {
				dev := s.Devices["sw1"]
				ga := dev.Power.Groups["g1"]
				ga.AllocatedMilliwatts = 10000
				dev.Power.Groups["g1"] = ga
				s.Devices["sw1"] = dev
			},
		},
		{
			name: "power_group_remainder",
			mutate: func(s *Snapshot) {
				dev := s.Devices["sw1"]
				ga := dev.Power.Groups["g1"]
				ga.RemainderMilliwatts = 20000
				dev.Power.Groups["g1"] = ga
				s.Devices["sw1"] = dev
			},
		},
		{
			name: "link_oper",
			mutate: func(s *Snapshot) {
				s.Links[0].A.Oper = port.Down
			},
		},
		{
			name: "link_speed",
			mutate: func(s *Snapshot) {
				s.Links[0].A.Speed.SpeedBPS = 100_000_000
			},
		},
		{
			name: "link_reason",
			mutate: func(s *Snapshot) {
				s.Links[0].A.Reason = trace.Reason("cable-cut")
			},
		},
		{
			name: "link_fault",
			mutate: func(s *Snapshot) {
				s.Links[0].Fault = Fault{Kind: FaultCut}
			},
		},
		{
			name: "link_medium",
			mutate: func(s *Snapshot) {
				s.Links[0].Medium = MultimodeFiber
			},
		},
		{
			name: "neighbor_vrf",
			mutate: func(s *Snapshot) {
				dev := s.Devices["sw1"]
				dev.Neighbors[0].VRF = "red"
				s.Devices["sw1"] = dev
			},
		},
		{
			name: "neighbor_interface",
			mutate: func(s *Snapshot) {
				dev := s.Devices["sw1"]
				dev.Neighbors[0].Interface = "1/1/2"
				s.Devices["sw1"] = dev
			},
		},
		{
			name: "neighbor_addr",
			mutate: func(s *Snapshot) {
				dev := s.Devices["sw1"]
				dev.Neighbors[0].Addr = netip.MustParseAddr("10.0.0.2")
				s.Devices["sw1"] = dev
			},
		},
		{
			name: "neighbor_mac",
			mutate: func(s *Snapshot) {
				dev := s.Devices["sw1"]
				dev.Neighbors[0].MAC = netaddr.MAC{0, 1, 2, 3, 4, 7}
				s.Devices["sw1"] = dev
			},
		},
		{
			name: "neighbor_state",
			mutate: func(s *Snapshot) {
				dev := s.Devices["sw1"]
				dev.Neighbors[0].State = routing.NeighborIncomplete
				s.Devices["sw1"] = dev
			},
		},
		{
			name: "neighbor_hold_depth",
			mutate: func(s *Snapshot) {
				dev := s.Devices["sw1"]
				dev.Neighbors[0].HoldDepth = 3
				s.Devices["sw1"] = dev
			},
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			mutated := cloneSnapshot(base)
			tc.mutate(&mutated)
			mutatedFP := mutated.Fingerprint()
			if mutatedFP == baseFP {
				t.Fatalf("mutation %q produced identical fingerprint:\n%s", tc.name, baseFP)
			}
		})
	}
}

func TestFingerprintDeterministicMapOrdering(t *testing.T) {
	t.Parallel()

	// Build a snapshot with multiple devices, VLANs, ports, and links.
	createSnap := func(order []string) Snapshot {
		devices := make(map[string]Device, len(order))
		for _, name := range order {
			treeRoles := make(map[vlan.ID]map[string]stp.PortInfo)
			for _, vid := range []vlan.ID{20, 10, 30} {
				treeRoles[vid] = map[string]stp.PortInfo{
					"1/1/2": {Role: stp.RoleDesignated, State: stp.StateForwarding},
					"1/1/1": {Role: stp.RoleRoot, State: stp.StateForwarding},
				}
			}
			devices[name] = Device{
				Ports: []port.Port{
					{Name: "1/1/2", OperStatus: port.Up},
					{Name: "1/1/1", OperStatus: port.Up},
				},
				TreeRoles: treeRoles,
			}
		}
		return Snapshot{
			Devices: devices,
			Links: []Link{
				{
					Cable: Cable{
						A: Endpoint{Node: "sw2", Port: "1/1/1"},
						B: Endpoint{Node: "sw1", Port: "1/1/1"},
					},
					A: LinkEnd{Oper: port.Up},
					B: LinkEnd{Oper: port.Up},
				},
				{
					Cable: Cable{
						A: Endpoint{Node: "sw1", Port: "1/1/2"},
						B: Endpoint{Node: "sw3", Port: "1/1/2"},
					},
					A: LinkEnd{Oper: port.Up},
					B: LinkEnd{Oper: port.Up},
				},
			},
		}
	}

	snapA := createSnap([]string{"sw1", "sw2", "sw3"})
	snapB := createSnap([]string{"sw3", "sw1", "sw2"})
	snapC := createSnap([]string{"sw2", "sw3", "sw1"})

	fpA := snapA.Fingerprint()
	fpB := snapB.Fingerprint()
	fpC := snapC.Fingerprint()

	if fpA != fpB {
		t.Fatalf("fpA != fpB across device insertion order:\n%s\nvs\n%s", fpA, fpB)
	}
	if fpA != fpC {
		t.Fatalf("fpA != fpC across device insertion order:\n%s\nvs\n%s", fpA, fpC)
	}
}

func TestFingerprintTimerInsensitive(t *testing.T) {
	t.Parallel()

	base := baseSnapshotForTest()
	baseFP := base.Fingerprint()

	// 1. Advancing clock leaves fingerprint unchanged.
	advClock := cloneSnapshot(base)
	advClock.Clock = advClock.Clock.Add(10 * time.Minute)
	if got := advClock.Fingerprint(); got != baseFP {
		t.Errorf("advancing clock changed fingerprint: %s vs %s", got, baseFP)
	}

	// 2. Queueing an arrival leaves fingerprint unchanged.
	queueArr := cloneSnapshot(base)
	queueArr.Queue = []Arrival{
		{
			At:   base.Clock.Add(time.Second),
			Kind: ArrivalWake,
		},
	}
	if got := queueArr.Fingerprint(); got != baseFP {
		t.Errorf("queue arrival changed fingerprint: %s vs %s", got, baseFP)
	}

	// 3. Queued and Busy maps leave fingerprint unchanged.
	queuedBusy := cloneSnapshot(base)
	queuedBusy.Queued = map[Endpoint]int{
		{Node: "sw1", Port: "1/1/1"}: 3,
	}
	queuedBusy.Busy = map[Endpoint]time.Time{
		{Node: "sw1", Port: "1/1/1"}: base.Clock.Add(time.Second),
	}
	if got := queuedBusy.Fingerprint(); got != baseFP {
		t.Errorf("queued/busy changed fingerprint: %s vs %s", got, baseFP)
	}

	// 4. Device Counters and RelayCounters leave fingerprint unchanged.
	counters := cloneSnapshot(base)
	dev := counters.Devices["sw1"]
	dev.Counters = map[string]Counters{
		"1/1/1": {InOctets: 100, OutOctets: 200},
	}
	dev.RelayCounters = bridge.Counters{Learned: 50}
	counters.Devices["sw1"] = dev
	if got := counters.Fingerprint(); got != baseFP {
		t.Errorf("counters changed fingerprint: %s vs %s", got, baseFP)
	}

	// 5. PortInfo hello-advancing fields (TxBPDUs, RxBPDUs, ForwardTransitions, BadBPDUs)
	// leave fingerprint unchanged.
	stpCounters := cloneSnapshot(base)
	devSTP := stpCounters.Devices["sw1"]
	pInfo := devSTP.TreeRoles[10]["1/1/1"]
	pInfo.TxBPDUs = 50
	pInfo.RxBPDUs = 60
	pInfo.ForwardTransitions = 2
	pInfo.BadBPDUs = 3
	devSTP.TreeRoles[10]["1/1/1"] = pInfo
	stpCounters.Devices["sw1"] = devSTP
	if got := stpCounters.Fingerprint(); got != baseFP {
		t.Errorf("stp hello counters changed fingerprint: %s vs %s", got, baseFP)
	}
}

func TestFingerprintDetectsTopologyAndRoleChanges(t *testing.T) {
	t.Parallel()

	// 1. Port role change changes fingerprint.
	base := baseSnapshotForTest()
	mut := cloneSnapshot(base)
	dev := mut.Devices["sw1"]
	pInfo := dev.TreeRoles[10]["1/1/1"]
	pInfo.Role = stp.RoleAlternate
	dev.TreeRoles[10]["1/1/1"] = pInfo
	mut.Devices["sw1"] = dev
	if mut.Fingerprint() == base.Fingerprint() {
		t.Fatalf("port role change left fingerprint unchanged")
	}

	// 2. MSTI role change on a fabric whose CIST is unchanged changes fingerprint.
	// Setup a switch with CIST (VLAN 1) and MSTI 1 (VLAN 10).
	mstSnap1 := cloneSnapshot(base)
	mstDev1 := mstSnap1.Devices["sw1"]
	mstDev1.TreeRoles[1] = map[string]stp.PortInfo{
		"1/1/1": {MSTID: 0, Role: stp.RoleRoot, State: stp.StateForwarding},
	}
	mstDev1.TreeRoles[10] = map[string]stp.PortInfo{
		"1/1/1": {MSTID: 1, Role: stp.RoleRoot, State: stp.StateForwarding},
	}
	mstSnap1.Devices["sw1"] = mstDev1

	mstSnap2 := cloneSnapshot(mstSnap1)
	mstDev2 := mstSnap2.Devices["sw1"]
	// CIST unchanged, MSTI 1 role changed
	mstDev2.TreeRoles[10]["1/1/1"] = stp.PortInfo{
		MSTID: 1, Role: stp.RoleAlternate, State: stp.StateDiscarding,
	}
	mstSnap2.Devices["sw1"] = mstDev2

	if mstSnap1.Fingerprint() == mstSnap2.Fingerprint() {
		t.Fatalf("MSTI role change with CIST unchanged left fingerprint unchanged")
	}

	// 3. Per-VLAN root change in PVST changes fingerprint.
	pvstSnap1 := cloneSnapshot(base)
	pvstDev1 := pvstSnap1.Devices["sw1"]
	pvstDev1.TreeRoles[10] = map[string]stp.PortInfo{
		"1/1/1": {
			DesignatedRoot: stp.BridgeID{Priority: 4096, Address: netaddr.MAC{0, 0, 0, 0, 0, 1}},
		},
	}
	pvstSnap1.Devices["sw1"] = pvstDev1

	pvstSnap2 := cloneSnapshot(pvstSnap1)
	pvstDev2 := pvstSnap2.Devices["sw1"]
	pvstDev2.TreeRoles[10]["1/1/1"] = stp.PortInfo{
		DesignatedRoot: stp.BridgeID{Priority: 8192, Address: netaddr.MAC{0, 0, 0, 0, 0, 2}},
	}
	pvstSnap2.Devices["sw1"] = pvstDev2

	if pvstSnap1.Fingerprint() == pvstSnap2.Fingerprint() {
		t.Fatalf("per-VLAN root change left fingerprint unchanged")
	}
}

func TestFingerprintUnchangedAcrossHelloWake(t *testing.T) {
	t.Parallel()

	// A two-switch STP fabric run across a periodic hello wake should leave
	// the fingerprint unchanged once converged.
	t0 := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	mac1 := netaddr.MAC{0, 0, 0, 0, 1, 1}
	mac2 := netaddr.MAC{0, 0, 0, 0, 1, 2}

	ports := func() port.Table {
		tbl, _ := port.NewBuilder().
			Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
			Build()
		return tbl
	}

	cfg := Config{
		Start: t0,
		Switches: map[string]vswitch.Config{
			"sw1": {
				Ports:  ports(),
				Bridge: &bridge.Config{},
				STP: &stp.Config{
					Priority: 4096,
					Address:  mac1,
					Ports:    map[string]stp.Port{"1/1/1": {}},
				},
			},
			"sw2": {
				Ports:  ports(),
				Bridge: &bridge.Config{},
				STP: &stp.Config{
					Priority: 8192,
					Address:  mac2,
					Ports:    map[string]stp.Port{"1/1/1": {}},
				},
			},
		},
		Cables: []Cable{
			{
				A:      Endpoint{Node: "sw1", Port: "1/1/1"},
				B:      Endpoint{Node: "sw2", Port: "1/1/1"},
				Medium: TwistedPair,
			},
		},
	}

	fab, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	// Run initial steps until converged.
	fab.Run(20)
	snap1 := fab.Snapshot()
	fp1 := snap1.Fingerprint()

	// Run another hello cycle (e.g. 5 more steps).
	fab.Run(5)
	snap2 := fab.Snapshot()
	fp2 := snap2.Fingerprint()

	if fp1 != fp2 {
		t.Errorf("periodic hello changed converged fabric fingerprint:\n%s\nvs\n%s", fp1, fp2)
	}
}

func TestFingerprintIncludedFieldsAffectFingerprint(t *testing.T) {
	t.Parallel()

	base := baseSnapshotForTest()
	baseFP := base.Fingerprint()

	type fieldMutation struct {
		typ    reflect.Type
		field  string
		mutate func(s *Snapshot)
	}

	mutations := []fieldMutation{
		// Snapshot fields
		{
			typ:   reflect.TypeOf(Snapshot{}),
			field: "Devices",
			mutate: func(s *Snapshot) {
				dev := s.Devices["sw1"]
				delete(s.Devices, "sw1")
				s.Devices["sw2"] = dev
			},
		},
		{
			typ:   reflect.TypeOf(Snapshot{}),
			field: "Links",
			mutate: func(s *Snapshot) {
				s.Links[0].A.Oper = port.Down
			},
		},
		// Device fields
		{
			typ:   reflect.TypeOf(Device{}),
			field: "Ports",
			mutate: func(s *Snapshot) {
				dev := s.Devices["sw1"]
				dev.Ports[0].OperStatus = port.Down
				s.Devices["sw1"] = dev
			},
		},
		{
			typ:   reflect.TypeOf(Device{}),
			field: "TreeRoles",
			mutate: func(s *Snapshot) {
				dev := s.Devices["sw1"]
				info := dev.TreeRoles[10]["1/1/1"]
				info.Role = stp.RoleAlternate
				dev.TreeRoles[10]["1/1/1"] = info
				s.Devices["sw1"] = dev
			},
		},
		{
			typ:   reflect.TypeOf(Device{}),
			field: "Entries",
			mutate: func(s *Snapshot) {
				dev := s.Devices["sw1"]
				dev.Entries[0].Port = "1/1/2"
				s.Devices["sw1"] = dev
			},
		},
		{
			typ:   reflect.TypeOf(Device{}),
			field: "Groups",
			mutate: func(s *Snapshot) {
				dev := s.Devices["sw1"]
				dev.Groups[10][0].Mode = mcast.Exclude
				s.Devices["sw1"] = dev
			},
		},
		{
			typ:   reflect.TypeOf(Device{}),
			field: "RouterPorts",
			mutate: func(s *Snapshot) {
				dev := s.Devices["sw1"]
				dev.RouterPorts[10][0].Port = "1/1/2"
				s.Devices["sw1"] = dev
			},
		},
		{
			typ:   reflect.TypeOf(Device{}),
			field: "Power",
			mutate: func(s *Snapshot) {
				dev := s.Devices["sw1"]
				pa := dev.Power.Ports["1/1/1"]
				pa.State = phy.PowerDenied
				dev.Power.Ports["1/1/1"] = pa
				s.Devices["sw1"] = dev
			},
		},
		{
			typ:   reflect.TypeOf(Device{}),
			field: "Neighbors",
			mutate: func(s *Snapshot) {
				dev := s.Devices["sw1"]
				dev.Neighbors[0].State = routing.NeighborIncomplete
				s.Devices["sw1"] = dev
			},
		},
		// stp.PortInfo fields
		{
			typ:   reflect.TypeOf(stp.PortInfo{}),
			field: "MSTID",
			mutate: func(s *Snapshot) {
				dev := s.Devices["sw1"]
				info := dev.TreeRoles[10]["1/1/1"]
				info.MSTID = 2
				dev.TreeRoles[10]["1/1/1"] = info
				s.Devices["sw1"] = dev
			},
		},
		{
			typ:   reflect.TypeOf(stp.PortInfo{}),
			field: "Role",
			mutate: func(s *Snapshot) {
				dev := s.Devices["sw1"]
				info := dev.TreeRoles[10]["1/1/1"]
				info.Role = stp.RoleDesignated
				dev.TreeRoles[10]["1/1/1"] = info
				s.Devices["sw1"] = dev
			},
		},
		{
			typ:   reflect.TypeOf(stp.PortInfo{}),
			field: "State",
			mutate: func(s *Snapshot) {
				dev := s.Devices["sw1"]
				info := dev.TreeRoles[10]["1/1/1"]
				info.State = stp.StateDiscarding
				dev.TreeRoles[10]["1/1/1"] = info
				s.Devices["sw1"] = dev
			},
		},
		{
			typ:   reflect.TypeOf(stp.PortInfo{}),
			field: "BlockReason",
			mutate: func(s *Snapshot) {
				dev := s.Devices["sw1"]
				info := dev.TreeRoles[10]["1/1/1"]
				info.BlockReason = stp.BlockReasonBPDUGuard
				dev.TreeRoles[10]["1/1/1"] = info
				s.Devices["sw1"] = dev
			},
		},
		{
			typ:   reflect.TypeOf(stp.PortInfo{}),
			field: "Priority",
			mutate: func(s *Snapshot) {
				dev := s.Devices["sw1"]
				info := dev.TreeRoles[10]["1/1/1"]
				info.Priority = 64
				dev.TreeRoles[10]["1/1/1"] = info
				s.Devices["sw1"] = dev
			},
		},
		{
			typ:   reflect.TypeOf(stp.PortInfo{}),
			field: "PathCost",
			mutate: func(s *Snapshot) {
				dev := s.Devices["sw1"]
				info := dev.TreeRoles[10]["1/1/1"]
				info.PathCost = 40000
				dev.TreeRoles[10]["1/1/1"] = info
				s.Devices["sw1"] = dev
			},
		},
		{
			typ:   reflect.TypeOf(stp.PortInfo{}),
			field: "DesignatedRoot",
			mutate: func(s *Snapshot) {
				dev := s.Devices["sw1"]
				info := dev.TreeRoles[10]["1/1/1"]
				info.DesignatedRoot.Priority = 8192
				dev.TreeRoles[10]["1/1/1"] = info
				s.Devices["sw1"] = dev
			},
		},
		{
			typ:   reflect.TypeOf(stp.PortInfo{}),
			field: "Designated",
			mutate: func(s *Snapshot) {
				dev := s.Devices["sw1"]
				info := dev.TreeRoles[10]["1/1/1"]
				info.Designated.Priority = 8192
				dev.TreeRoles[10]["1/1/1"] = info
				s.Devices["sw1"] = dev
			},
		},
		{
			typ:   reflect.TypeOf(stp.PortInfo{}),
			field: "DesignatedPort",
			mutate: func(s *Snapshot) {
				dev := s.Devices["sw1"]
				info := dev.TreeRoles[10]["1/1/1"]
				info.DesignatedPort = 2
				dev.TreeRoles[10]["1/1/1"] = info
				s.Devices["sw1"] = dev
			},
		},
		{
			typ:   reflect.TypeOf(stp.PortInfo{}),
			field: "DesignatedCost",
			mutate: func(s *Snapshot) {
				dev := s.Devices["sw1"]
				info := dev.TreeRoles[10]["1/1/1"]
				info.DesignatedCost = 100
				dev.TreeRoles[10]["1/1/1"] = info
				s.Devices["sw1"] = dev
			},
		},
		{
			typ:   reflect.TypeOf(stp.PortInfo{}),
			field: "PointToPoint",
			mutate: func(s *Snapshot) {
				dev := s.Devices["sw1"]
				info := dev.TreeRoles[10]["1/1/1"]
				info.PointToPoint = false
				dev.TreeRoles[10]["1/1/1"] = info
				s.Devices["sw1"] = dev
			},
		},
		{
			typ:   reflect.TypeOf(stp.PortInfo{}),
			field: "Edge",
			mutate: func(s *Snapshot) {
				dev := s.Devices["sw1"]
				info := dev.TreeRoles[10]["1/1/1"]
				info.Edge = true
				dev.TreeRoles[10]["1/1/1"] = info
				s.Devices["sw1"] = dev
			},
		},
		{
			typ:   reflect.TypeOf(stp.PortInfo{}),
			field: "SendRSTP",
			mutate: func(s *Snapshot) {
				dev := s.Devices["sw1"]
				info := dev.TreeRoles[10]["1/1/1"]
				info.SendRSTP = false
				dev.TreeRoles[10]["1/1/1"] = info
				s.Devices["sw1"] = dev
			},
		},
		// routing.NeighborEntry fields
		{
			typ:   reflect.TypeOf(routing.NeighborEntry{}),
			field: "VRF",
			mutate: func(s *Snapshot) {
				dev := s.Devices["sw1"]
				dev.Neighbors[0].VRF = "red"
				s.Devices["sw1"] = dev
			},
		},
		{
			typ:   reflect.TypeOf(routing.NeighborEntry{}),
			field: "Interface",
			mutate: func(s *Snapshot) {
				dev := s.Devices["sw1"]
				dev.Neighbors[0].Interface = "1/1/2"
				s.Devices["sw1"] = dev
			},
		},
		{
			typ:   reflect.TypeOf(routing.NeighborEntry{}),
			field: "Addr",
			mutate: func(s *Snapshot) {
				dev := s.Devices["sw1"]
				dev.Neighbors[0].Addr = netip.MustParseAddr("10.0.0.2")
				s.Devices["sw1"] = dev
			},
		},
		{
			typ:   reflect.TypeOf(routing.NeighborEntry{}),
			field: "MAC",
			mutate: func(s *Snapshot) {
				dev := s.Devices["sw1"]
				dev.Neighbors[0].MAC = netaddr.MAC{0, 1, 2, 3, 4, 7}
				s.Devices["sw1"] = dev
			},
		},
		{
			typ:   reflect.TypeOf(routing.NeighborEntry{}),
			field: "State",
			mutate: func(s *Snapshot) {
				dev := s.Devices["sw1"]
				dev.Neighbors[0].State = routing.NeighborIncomplete
				s.Devices["sw1"] = dev
			},
		},
		{
			typ:   reflect.TypeOf(routing.NeighborEntry{}),
			field: "HoldDepth",
			mutate: func(s *Snapshot) {
				dev := s.Devices["sw1"]
				dev.Neighbors[0].HoldDepth = 3
				s.Devices["sw1"] = dev
			},
		},
	}

	tables := []struct {
		typ     reflect.Type
		classes map[string]fieldClassification
	}{
		{reflect.TypeOf(Snapshot{}), snapshotFieldClasses},
		{reflect.TypeOf(Device{}), deviceFieldClasses},
		{reflect.TypeOf(stp.PortInfo{}), portInfoFieldClasses},
		{reflect.TypeOf(routing.NeighborEntry{}), neighborEntryFieldClasses},
	}

	type key struct {
		typ   reflect.Type
		field string
	}
	mutMap := make(map[key]func(*Snapshot), len(mutations))
	for _, m := range mutations {
		k := key{typ: m.typ, field: m.field}
		if _, exists := mutMap[k]; exists {
			t.Fatalf("duplicate mutation for %s.%s", m.typ.Name(), m.field)
		}
		mutMap[k] = m.mutate
	}

	// 1. Assert every field marked fieldIncluded has a mutator, and mutating it changes the fingerprint.
	for _, tbl := range tables {
		for fieldName, class := range tbl.classes {
			if class != fieldIncluded {
				continue
			}
			k := key{typ: tbl.typ, field: fieldName}
			mut, ok := mutMap[k]
			if !ok {
				t.Errorf("field %s.%s is marked fieldIncluded in classification table but has no mutation check", tbl.typ.Name(), fieldName)
				continue
			}
			mutated := cloneSnapshot(base)
			mut(&mutated)
			mutFP := mutated.Fingerprint()
			if mutFP == baseFP {
				t.Errorf("field %s.%s marked fieldIncluded but perturbing it produced identical fingerprint:\n%s", tbl.typ.Name(), fieldName, baseFP)
			}
		}
	}

	// 2. Assert no mutations exist for fields that are not marked fieldIncluded.
	for _, m := range mutations {
		var found bool
		for _, tbl := range tables {
			if tbl.typ == m.typ {
				if tbl.classes[m.field] != fieldIncluded {
					t.Errorf("mutation defined for %s.%s, but field is not marked fieldIncluded", m.typ.Name(), m.field)
				}
				found = true
				break
			}
		}
		if !found {
			t.Errorf("mutation defined for unknown type %s", m.typ.Name())
		}
	}
}

func TestFingerprintNeighborResolutionState(t *testing.T) {
	t.Parallel()

	// 1. Snapshot-level verification: moving Incomplete -> Reachable changes fingerprint.
	base := baseSnapshotForTest()
	snapIncomplete := cloneSnapshot(base)
	devIncomp := snapIncomplete.Devices["sw1"]
	devIncomp.Neighbors = []routing.NeighborEntry{
		{
			VRF:       "default",
			Interface: "1/1/1",
			Addr:      netip.MustParseAddr("10.0.0.1"),
			MAC:       netaddr.MAC{},
			State:     routing.NeighborIncomplete,
			HoldDepth: 1,
		},
	}
	snapIncomplete.Devices["sw1"] = devIncomp
	fpIncomplete := snapIncomplete.Fingerprint()

	snapReachable := cloneSnapshot(snapIncomplete)
	devReach := snapReachable.Devices["sw1"]
	devReach.Neighbors = []routing.NeighborEntry{
		{
			VRF:       "default",
			Interface: "1/1/1",
			Addr:      netip.MustParseAddr("10.0.0.1"),
			MAC:       netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x55},
			State:     routing.NeighborReachable,
			HoldDepth: 0,
		},
	}
	snapReachable.Devices["sw1"] = devReach
	fpReachable := snapReachable.Fingerprint()

	if fpIncomplete == fpReachable {
		t.Fatalf("neighbor moving Incomplete -> Reachable produced identical fingerprint:\n%s", fpIncomplete)
	}

	// 2. Fabric-level verification: Incomplete -> Reachable changes fingerprint,
	// while advancing neighbor expiry alone leaves it unchanged.
	start := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	sw1MAC := netaddr.MAC{0x00, 0xaa, 0xbb, 0xcc, 0xdd, 0x01}
	sw2MAC := netaddr.MAC{0x00, 0xaa, 0xbb, 0xcc, 0xdd, 0x02}
	macH1 := netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x11}
	nextHop := netip.MustParseAddr("10.0.60.7")
	vid10 := vlan.ID(10)

	twoPorts := func() port.Table {
		tbl, _ := port.NewBuilder().
			Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
			Add(port.Port{Name: "1/1/2", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
			Build()
		return tbl
	}

	cfg := Config{
		Start: start,
		Switches: map[string]vswitch.Config{
			"sw1": {
				MAC:   sw1MAC,
				Ports: twoPorts(),
				Bridge: &bridge.Config{
					VLAN: &bridge.VLAN{
						Table:       map[vlan.ID]string{10: "vlan10"},
						Switchports: map[string]bridge.Switchport{"1/1/1": {PVID: &vid10, Tagged: []vlan.ID{10}}},
					},
				},
				Routing: &routing.Config{
					VRFs: map[string]routing.VRF{
						routing.DefaultVRF: {
							Interfaces: map[string]routing.Interface{
								"vlan10": {VLAN: 10, MAC: sw1MAC, Prefixes: []netip.Prefix{netip.MustParsePrefix("10.0.50.1/24")}},
								"rp2":    {Port: "1/1/2", MAC: sw1MAC, Prefixes: []netip.Prefix{netip.MustParsePrefix("10.0.60.1/24")}},
							},
						},
					},
				},
			},
			"sw2": {MAC: sw2MAC, Ports: twoPorts()},
		},
		Hosts: map[string]Host{"h1": {Address: macH1}},
		Cables: []Cable{
			{A: Endpoint{Node: "h1"}, B: Endpoint{Node: "sw1", Port: "1/1/1"}, LengthMeters: 5},
			{A: Endpoint{Node: "sw1", Port: "1/1/2"}, B: Endpoint{Node: "sw2", Port: "1/1/1"}, LengthMeters: 5},
		},
		PhyAssumption: &PhyAssumption{
			Medium: TwistedPair,
			Ethernet: phy.Ethernet{
				SupportedSpeedsBPS:       []uint64{10_000_000, 100_000_000, 1_000_000_000},
				AutoNegotiationSupported: phy.CapabilitySupported,
				Setting:                  &phy.Setting{AutoNegotiation: true},
			},
		},
	}

	fab, err := New(cfg)
	if err != nil {
		t.Fatalf("fabric.New: %v", err)
	}

	packet, _ := ip.Header{
		Src:      netip.MustParseAddr("10.0.50.7"),
		Dst:      nextHop,
		HopLimit: 64,
		Protocol: 17,
		V4:       &ip.V4{},
	}.Encode([]byte("payload"))
	taggedFrame := ethernet.Frame{
		Src:       macH1,
		Dst:       sw1MAC,
		EtherType: ethernet.EtherTypeIPv4,
		Tags:      []vlan.Tag{{TPID: uint16(ethernet.EtherTypeDot1Q), VID: 10}},
		Payload:   packet,
	}

	if _, err := fab.Inject(Injection{
		At:     start,
		Origin: Endpoint{Node: "sw1", Port: "1/1/1"},
		Frame:  taggedFrame,
	}); err != nil {
		t.Fatalf("Inject: %v", err)
	}
	if _, ok := fab.Step(); !ok {
		t.Fatal("fab.Step failed")
	}

	snap1 := fab.Snapshot()
	if len(snap1.Devices["sw1"].Neighbors) != 1 || snap1.Devices["sw1"].Neighbors[0].State != routing.NeighborIncomplete {
		t.Fatalf("expected 1 Incomplete neighbor on sw1, got %+v", snap1.Devices["sw1"].Neighbors)
	}
	fp1 := snap1.Fingerprint()

	replyFrame, _ := arp.Encode(arp.Message{
		HardwareType: 1,
		ProtocolType: 0x0800,
		Operation:    arp.Reply,
		SenderMAC:    sw2MAC,
		SenderAddr:   nextHop,
		TargetMAC:    sw1MAC,
		TargetAddr:   netip.MustParseAddr("10.0.60.1"),
	}, sw1MAC)

	if _, err := fab.Inject(Injection{
		At:     snap1.Clock.Add(time.Millisecond),
		Origin: Endpoint{Node: "sw1", Port: "1/1/2"},
		Frame:  replyFrame,
	}); err != nil {
		t.Fatalf("Inject ARP reply: %v", err)
	}
	if _, ok := fab.Step(); !ok {
		t.Fatal("fab.Step failed")
	}

	snap2 := fab.Snapshot()
	if len(snap2.Devices["sw1"].Neighbors) != 1 || snap2.Devices["sw1"].Neighbors[0].State != routing.NeighborReachable {
		t.Fatalf("expected 1 Reachable neighbor on sw1, got %+v", snap2.Devices["sw1"].Neighbors)
	}
	fp2 := snap2.Fingerprint()

	if fp1 == fp2 {
		t.Fatalf("resolving neighbor Incomplete -> Reachable did not change fingerprint:\n%s", fp1)
	}

	// Advance expiry alone: inject another ARP reply 10 seconds later from sw2.
	// This updates the neighbor entry's internal expiry to (tLater + ReachableTime),
	// but resolution state remains Reachable, MAC remains unchanged, hold depth remains 0.
	if _, err := fab.Inject(Injection{
		At:     snap2.Clock.Add(10 * time.Second),
		Origin: Endpoint{Node: "sw1", Port: "1/1/2"},
		Frame:  replyFrame,
	}); err != nil {
		t.Fatalf("Inject ARP reply: %v", err)
	}
	if _, ok := fab.Step(); !ok {
		t.Fatal("fab.Step failed")
	}

	snap3 := fab.Snapshot()
	fp3 := snap3.Fingerprint()

	if fp2 != fp3 {
		t.Errorf("advancing neighbor expiry alone changed fingerprint:\n%s\nvs\n%s", fp2, fp3)
	}
}

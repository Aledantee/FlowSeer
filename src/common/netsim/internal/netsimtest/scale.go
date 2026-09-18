package netsimtest

import (
	"fmt"
	"net/netip"
	"time"

	"go.aledante.io/FlowSeer/src/common/net/ethernet"
	"go.aledante.io/FlowSeer/src/common/net/netaddr"
	"go.aledante.io/FlowSeer/src/common/net/vlan"
	"go.aledante.io/FlowSeer/src/common/netsim/fabric"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/bridge"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/lag"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/port"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/routing"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/stp"
)

const (
	// RepresentativeScaleNodeCount is the declared representative switch node count (8).
	RepresentativeScaleNodeCount = 8

	// RepresentativeScalePortsPerNode is the declared physical port count per switch (32).
	RepresentativeScalePortsPerNode = 32

	// RepresentativeScaleVLANCount is the declared trunked VLAN count (16).
	RepresentativeScaleVLANCount = 16

	// RepresentativeScaleLAGMembers is the declared member count for the LAG on each node (2).
	RepresentativeScaleLAGMembers = 2

	// RepresentativeScaleRoutesPerVRF is the declared route count per VRF (64).
	RepresentativeScaleRoutesPerVRF = 64

	// RepresentativeScaleNeighborsPerVRF is the declared neighbor count per VRF (64).
	RepresentativeScaleNeighborsPerVRF = 64

	// RepresentativeScaleHostCount is the declared host count (64).
	RepresentativeScaleHostCount = 64

	// RepresentativeScaleLearnedEntries is the declared total learned forwarding entries (2048).
	RepresentativeScaleLearnedEntries = 2048

	// RepresentativeScaleQueueDepth is the declared queue depth at fork (4096).
	RepresentativeScaleQueueDepth = 4096
)

// RepresentativeFabric builds the declared representative scale topology:
// eight nodes of thirty-two ports, sixteen VLANs trunked between nodes,
// one two-member LAG per node, rapid spanning tree on every node,
// one VRF per node with sixty-four routes and sixty-four neighbors,
// sixty-four hosts, primed with two thousand and forty-eight learned forwarding
// entries and four thousand and ninety-six queued arrivals at the moment of fork.
// The scale is one step past the lab inventory measurements (five switches of
// 24-30 ports carrying 2-4 VLANs with one LAG each and a single spanning tree;
// docs/research/device-inventory/README.md:30, labsw06-ruckus-icx7150.md:125, :127)
// with each count rounded up to a deliberate power of two.
func RepresentativeFabric() *fabric.Fabric {
	return RepresentativeFabricAtQueueDepth(RepresentativeScaleQueueDepth)
}

// RepresentativeFabricAtQueueDepth builds the representative topology primed with
// the requested number of queued arrivals.
func RepresentativeFabricAtQueueDepth(queueDepth int) *fabric.Fabric {
	tStart := time.Date(2026, 9, 14, 12, 0, 0, 0, time.UTC)

	vlanTable := make(map[vlan.ID]string, RepresentativeScaleVLANCount)
	vlanList := make([]vlan.ID, RepresentativeScaleVLANCount)
	for v := range RepresentativeScaleVLANCount {
		vid := vlan.ID(100 + v)
		vlanTable[vid] = fmt.Sprintf("vlan%d", vid)
		vlanList[v] = vid
	}

	switches := make(map[string]vswitch.Config, RepresentativeScaleNodeCount)
	for s := 1; s <= RepresentativeScaleNodeCount; s++ {
		swName := fmt.Sprintf("sw%d", s)
		swMAC := netaddr.MAC{0x00, 0x5e, 0x00, 0x01, byte(s), 0x01}

		pb := port.NewBuilder()
		pb.Add(port.Port{Name: "lag1", Kind: port.Lag, AdminStatus: port.Up, OperStatus: port.Up})
		pb.Add(port.Port{Name: "1/1/1", Kind: port.Physical, LagParent: "lag1", AdminStatus: port.Up, OperStatus: port.Up})
		pb.Add(port.Port{Name: "1/1/2", Kind: port.Physical, LagParent: "lag1", AdminStatus: port.Up, OperStatus: port.Up})
		for p := 3; p <= RepresentativeScalePortsPerNode; p++ {
			pb.Add(port.Port{Name: fmt.Sprintf("1/1/%d", p), Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up})
		}
		ports, err := pb.Build()
		mustNil(err)

		switchports := make(map[string]bridge.Switchport, RepresentativeScalePortsPerNode)
		switchports["lag1"] = bridge.Switchport{
			PVID:     &vlanList[0],
			Untagged: []vlan.ID{vlanList[0]},
			Tagged:   vlanList[1:],
		}
		for p := 3; p <= RepresentativeScalePortsPerNode; p++ {
			pName := fmt.Sprintf("1/1/%d", p)
			switchports[pName] = bridge.Switchport{
				PVID:     &vlanList[0],
				Untagged: []vlan.ID{vlanList[0]},
				Tagged:   vlanList[1:],
			}
		}

		stpPorts := make(map[string]stp.Port, RepresentativeScalePortsPerNode+1)
		stpPorts["lag1"] = stp.Port{PathCost: 10000, PointToPoint: stp.PointToPointAuto}
		for p := 3; p <= RepresentativeScalePortsPerNode; p++ {
			stpPorts[fmt.Sprintf("1/1/%d", p)] = stp.Port{PathCost: 20000, PointToPoint: stp.PointToPointAuto}
		}

		routes := make([]routing.Route, RepresentativeScaleRoutesPerVRF)
		for r := range RepresentativeScaleRoutesPerVRF {
			routes[r] = routing.Route{
				Prefix:     netip.PrefixFrom(netip.AddrFrom4([4]byte{10, byte(s), byte(r), 0}), 24),
				NextHop:    netip.AddrFrom4([4]byte{10, byte(s), 100, byte(r + 1)}),
				Interface:  "vlan100",
				Preference: 1,
				Metric:     1,
			}
		}
		neighbors := make([]routing.Neighbor, RepresentativeScaleNeighborsPerVRF)
		for n := range RepresentativeScaleNeighborsPerVRF {
			neighbors[n] = routing.Neighbor{
				Interface: "vlan100",
				Addr:      netip.AddrFrom4([4]byte{10, byte(s), 100, byte(n + 1)}),
				MAC:       netaddr.MAC{0x02, byte(s), 0, 0, 0, byte(n + 1)},
			}
		}

		switches[swName] = vswitch.Config{
			Ports: ports,
			MAC:   swMAC,
			Bridge: &bridge.Config{
				AgingTime: 300 * time.Second,
				VLAN: &bridge.VLAN{
					Table:       vlanTable,
					Switchports: switchports,
				},
			},
			STP: &stp.Config{
				Priority: 32768,
				Ports:    stpPorts,
			},
			LAG: &lag.Config{
				LAGs: map[string]lag.LAG{
					"lag1": {
						Mode:    lag.ActiveBackup,
						Members: map[string]lag.Member{"1/1/1": {}, "1/1/2": {}},
					},
				},
			},
			Routing: &routing.Config{
				VRFs: map[string]routing.VRF{
					routing.DefaultVRF: {
						Interfaces: map[string]routing.Interface{
							"vlan100": {
								VLAN:     vlanList[0],
								MAC:      swMAC,
								Prefixes: []netip.Prefix{netip.MustParsePrefix(fmt.Sprintf("10.%d.0.1/16", s))},
							},
						},
						Routes:         routes,
						Neighbors:      neighbors,
						NeighborPolicy: routing.NeighborPolicy{Mode: routing.NeighborObserved},
					},
				},
			},
		}
	}

	hosts := make(map[string]fabric.Host, RepresentativeScaleHostCount)
	for h := 1; h <= RepresentativeScaleHostCount; h++ {
		hName := fmt.Sprintf("h%d", h)
		hMAC := netaddr.MAC{0x02, 0x00, 0x00, 0x00, byte(h >> 8), byte(h)}
		hIP := netip.MustParseAddr(fmt.Sprintf("10.100.0.%d", h))
		hosts[hName] = fabric.Host{
			Address: hMAC,
			VLAN:    &vlanList[0],
			IP: &fabric.HostIP{
				Addresses: []netip.Prefix{netip.PrefixFrom(hIP, 24)},
			},
		}
	}

	var cables []fabric.Cable
	// 8 hosts per switch connected to ports 1/1/3 .. 1/1/10
	for s := 1; s <= RepresentativeScaleNodeCount; s++ {
		swName := fmt.Sprintf("sw%d", s)
		for k := range 8 {
			hIndex := (s-1)*8 + k + 1
			hName := fmt.Sprintf("h%d", hIndex)
			cables = append(cables, fabric.Cable{
				A:            fabric.Endpoint{Node: hName, Port: ""},
				B:            fabric.Endpoint{Node: swName, Port: fmt.Sprintf("1/1/%d", k+3)},
				LengthMeters: 5,
				Medium:       fabric.TwistedPair,
			})
		}
	}

	// Inter-switch trunks in a ring: sw1:1/1/11 <-> sw2:1/1/12, etc.
	for s := 1; s <= RepresentativeScaleNodeCount; s++ {
		nextS := (s % RepresentativeScaleNodeCount) + 1
		cables = append(cables, fabric.Cable{
			A:            fabric.Endpoint{Node: fmt.Sprintf("sw%d", s), Port: "1/1/11"},
			B:            fabric.Endpoint{Node: fmt.Sprintf("sw%d", nextS), Port: "1/1/12"},
			LengthMeters: 10,
			Medium:       fabric.TwistedPair,
		})
	}

	// Uncabled ports: 1/1/13 .. 1/1/32 on each switch
	var uncabled []fabric.Uncabled
	for s := 1; s <= RepresentativeScaleNodeCount; s++ {
		swName := fmt.Sprintf("sw%d", s)
		for p := 13; p <= RepresentativeScalePortsPerNode; p++ {
			uncabled = append(uncabled, fabric.Uncabled{
				Endpoint: fabric.Endpoint{Node: swName, Port: fmt.Sprintf("1/1/%d", p)},
			})
		}
	}

	fabCfg := fabric.Config{
		Start:    tStart,
		Switches: switches,
		Hosts:    hosts,
		Cables:   cables,
		Uncabled: uncabled,
	}

	fab, err := fabric.New(fabCfg)
	mustNil(err)

	// Prime 2048 learned forwarding entries (256 per switch)
	entriesPerSwitch := RepresentativeScaleLearnedEntries / RepresentativeScaleNodeCount
	for s := 1; s <= RepresentativeScaleNodeCount; s++ {
		sw := fab.Switch(fmt.Sprintf("sw%d", s))
		seeds := make([]bridge.Seed, entriesPerSwitch)
		for e := range entriesPerSwitch {
			fid := vlanList[e%len(vlanList)]
			mac := netaddr.MAC{0x06, byte(s), byte(e >> 8), byte(e), 0x01, 0x02}
			seeds[e] = bridge.Seed{
				FID:      fid,
				MAC:      mac,
				Port:     "1/1/3",
				Origin:   bridge.Observed,
				Lifetime: bridge.Aging,
			}
		}
		mustNil(sw.Learn(seeds))
	}

	// Prime queued arrivals up to queueDepth
	initialQueueLen := len(fab.Snapshot().Queue)
	for i := initialQueueLen; i < queueDepth; i++ {
		frame := ethernet.Frame{
			Src:       netaddr.MAC{0x02, 0x10, byte(i >> 8), byte(i), 0x01, 0x02},
			Dst:       netaddr.MAC{0x02, 0x20, 0, 0, 0, 0x01},
			EtherType: ethernet.EtherTypeIPv4,
			Payload:   []byte("queued-arrival-payload"),
		}
		inj := fabric.Injection{
			Origin: fabric.Endpoint{Node: "sw1", Port: "1/1/3"},
			At:     tStart.Add(time.Hour + time.Duration(i)*time.Millisecond),
			Frame:  frame,
		}
		_, err := fab.Inject(inj)
		mustNil(err)
	}

	return fab
}

func mustNil(err error) {
	if err != nil {
		panic(err)
	}
}

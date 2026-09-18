package fabric

import (
	"cmp"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"

	"go.aledante.io/FlowSeer/src/common/net/netaddr"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/bridge"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/mcast"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/port"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/stp"
)

func escapeFingerprint(s string) string {
	return strings.NewReplacer(
		"%", "%25",
		":", "%3A",
		";", "%3B",
		",", "%2C",
		"=", "%3D",
		"|", "%7C",
		"/", "%2F",
		"[", "%5B",
		"]", "%5D",
		"{", "%7B",
		"}", "%7D",
	).Replace(s)
}

// Fingerprint returns a canonical string over the protocol-relevant state of
// the fabric snapshot.
func (f *Fabric) Fingerprint() string {
	return f.Snapshot().Fingerprint()
}

// Fingerprint returns a canonical string over the protocol-relevant state of
// the snapshot, deliberately excluding timers, arrival queues, and counters
// so periodic protocol wakes do not prevent convergence detection.
func (s Snapshot) Fingerprint() string {
	var b strings.Builder

	// Devices in name order.
	devNames := slices.Sorted(maps.Keys(s.Devices))
	for _, devName := range devNames {
		dev := s.Devices[devName]
		b.WriteString("dev:")
		b.WriteString(escapeFingerprint(devName))
		b.WriteString("|")

		// 1. Port operational states, in port name order.
		b.WriteString("ports:[")
		ports := slices.Clone(dev.Ports)
		slices.SortFunc(ports, func(a, b port.Port) int {
			return cmp.Compare(a.Name, b.Name)
		})
		for i, p := range ports {
			if i > 0 {
				b.WriteString(";")
			}
			b.WriteString(escapeFingerprint(p.Name))
			b.WriteString("=")
			b.WriteString(escapeFingerprint(string(p.OperStatus)))
		}
		b.WriteString("]|")

		// 2. Per-VLAN tree port information, in VLAN ID order, then port name order.
		b.WriteString("trees:[")
		if len(dev.TreeRoles) > 0 {
			vids := slices.Sorted(maps.Keys(dev.TreeRoles))
			for i, vid := range vids {
				if i > 0 {
					b.WriteString(";")
				}
				b.WriteString(strconv.FormatUint(uint64(vid), 10))
				b.WriteString("={")
				tree := dev.TreeRoles[vid]
				pNames := slices.Sorted(maps.Keys(tree))
				for j, pName := range pNames {
					if j > 0 {
						b.WriteString(",")
					}
					b.WriteString(escapeFingerprint(pName))
					b.WriteString(":")
					b.WriteString(encodeFingerprintPortInfo(tree[pName]))
				}
				b.WriteString("}")
			}
		}
		b.WriteString("]|")

		// 3. Forwarding database entries, in (FID, MAC, Port) order.
		b.WriteString("fdb:[")
		entries := slices.Clone(dev.Entries)
		slices.SortFunc(entries, func(a, b bridge.Entry) int {
			if c := cmp.Compare(a.FID, b.FID); c != 0 {
				return c
			}
			if c := bytesCompareMAC(a.MAC, b.MAC); c != 0 {
				return c
			}
			return cmp.Compare(a.Port, b.Port)
		})
		for i, e := range entries {
			if i > 0 {
				b.WriteString(";")
			}
			b.WriteString(strconv.FormatUint(uint64(e.FID), 10))
			b.WriteString(",")
			b.WriteString(escapeFingerprint(e.MAC.String()))
			b.WriteString(",")
			b.WriteString(escapeFingerprint(e.Port))
			b.WriteString(",")
			b.WriteString(escapeFingerprint(string(e.Origin)))
			b.WriteString(",")
			b.WriteString(escapeFingerprint(string(e.Lifetime)))
		}
		b.WriteString("]|")

		// 4. Multicast groups, in VLAN ID order, then (Group, Port) order.
		b.WriteString("mcast_groups:[")
		if len(dev.Groups) > 0 {
			vids := slices.Sorted(maps.Keys(dev.Groups))
			for i, vid := range vids {
				if i > 0 {
					b.WriteString(";")
				}
				b.WriteString(strconv.FormatUint(uint64(vid), 10))
				b.WriteString("={")
				grpList := slices.Clone(dev.Groups[vid])
				slices.SortFunc(grpList, func(a, b mcast.Entry) int {
					if c := a.Group.Compare(b.Group); c != 0 {
						return c
					}
					return cmp.Compare(a.Port, b.Port)
				})
				for j, grp := range grpList {
					if j > 0 {
						b.WriteString(",")
					}
					b.WriteString(escapeFingerprint(grp.Group.String()))
					b.WriteString("/")
					b.WriteString(escapeFingerprint(grp.Port))
					b.WriteString("/")
					b.WriteString(escapeFingerprint(string(grp.Mode)))
					if len(grp.Sources) > 0 {
						b.WriteString("/srcs=")
						srcs := slices.Clone(grp.Sources)
						slices.SortFunc(srcs, func(a, b mcast.SourceEntry) int {
							return a.Address.Compare(b.Address)
						})
						for k, src := range srcs {
							if k > 0 {
								b.WriteString("+")
							}
							b.WriteString(escapeFingerprint(src.Address.String()))
						}
					}
				}
				b.WriteString("}")
			}
		}
		b.WriteString("]|")

		// 5. Multicast router ports, in VLAN ID order, then Port order.
		b.WriteString("mcast_rports:[")
		if len(dev.RouterPorts) > 0 {
			vids := slices.Sorted(maps.Keys(dev.RouterPorts))
			for i, vid := range vids {
				if i > 0 {
					b.WriteString(";")
				}
				b.WriteString(strconv.FormatUint(uint64(vid), 10))
				b.WriteString("={")
				rports := slices.Clone(dev.RouterPorts[vid])
				slices.SortFunc(rports, func(a, b mcast.RouterPort) int {
					return cmp.Compare(a.Port, b.Port)
				})
				for j, rp := range rports {
					if j > 0 {
						b.WriteString(",")
					}
					b.WriteString(escapeFingerprint(rp.Port))
					b.WriteString("/")
					b.WriteString(escapeFingerprint(string(rp.Origin)))
					b.WriteString("/")
					b.WriteString(escapeFingerprint(string(rp.Lifetime)))
				}
				b.WriteString("}")
			}
		}
		b.WriteString("]|")

		// 6. Device Power.
		b.WriteString("power:[")
		if len(dev.Power.Ports) > 0 {
			b.WriteString("ports={")
			pNames := slices.Sorted(maps.Keys(dev.Power.Ports))
			for i, pName := range pNames {
				if i > 0 {
					b.WriteString(";")
				}
				pa := dev.Power.Ports[pName]
				b.WriteString(escapeFingerprint(pName))
				b.WriteString("=")
				b.WriteString(escapeFingerprint(pa.State.Canonical()))
				b.WriteString(",")
				b.WriteString(strconv.FormatUint(uint64(pa.MinMilliwatts), 10))
				b.WriteString(",")
				b.WriteString(strconv.FormatUint(uint64(pa.MaxMilliwatts), 10))
				b.WriteString(",")
				b.WriteString(escapeFingerprint(string(pa.Denial)))
			}
			b.WriteString("}")
		}
		if len(dev.Power.Groups) > 0 {
			b.WriteString("groups={")
			gNames := slices.Sorted(maps.Keys(dev.Power.Groups))
			for i, gName := range gNames {
				if i > 0 {
					b.WriteString(";")
				}
				ga := dev.Power.Groups[gName]
				b.WriteString(escapeFingerprint(gName))
				b.WriteString("=")
				b.WriteString(strconv.FormatUint(uint64(ga.BudgetMilliwatts), 10))
				b.WriteString(",")
				b.WriteString(strconv.FormatUint(uint64(ga.AllocatedMilliwatts), 10))
				b.WriteString(",")
				b.WriteString(strconv.FormatUint(uint64(ga.RemainderMilliwatts), 10))
			}
			b.WriteString("}")
		}
		b.WriteString("]|")
	}

	// Links in canonical endpoint order.
	b.WriteString("links:[")
	links := slices.Clone(s.Links)
	slices.SortFunc(links, func(a, b Link) int {
		keyA := cableEndpointsFor(a.Cable).Canonical()
		keyB := cableEndpointsFor(b.Cable).Canonical()
		return cmp.Compare(keyA, keyB)
	})
	for i, l := range links {
		if i > 0 {
			b.WriteString(";")
		}
		c := canonicalCableOrientation(l.Cable)
		endA, endB := l.A, l.B
		if compareEndpoint(l.Cable.A, c.A) != 0 {
			endA, endB = l.B, l.A
		}
		b.WriteString(cableEndpointsFor(c).Canonical())
		b.WriteString("={")
		b.WriteString("a=")
		b.WriteString(escapeFingerprint(string(endA.Oper)))
		b.WriteString(",")
		b.WriteString(escapeFingerprint(string(endA.Reason)))
		b.WriteString(",")
		b.WriteString(strconv.FormatUint(endA.Speed.SpeedBPS, 10))
		b.WriteString(",")
		b.WriteString(escapeFingerprint(string(endA.Speed.DuplexA)))
		b.WriteString(";b=")
		b.WriteString(escapeFingerprint(string(endB.Oper)))
		b.WriteString(",")
		b.WriteString(escapeFingerprint(string(endB.Reason)))
		b.WriteString(",")
		b.WriteString(strconv.FormatUint(endB.Speed.SpeedBPS, 10))
		b.WriteString(",")
		b.WriteString(escapeFingerprint(string(endB.Speed.DuplexA)))
		b.WriteString(";fault=")
		b.WriteString(escapeFingerprint(string(c.Fault.Kind)))
		b.WriteString(";medium=")
		b.WriteString(escapeFingerprint(string(c.Medium)))
		b.WriteString("}")
	}
	b.WriteString("]")

	return b.String()
}

func encodeFingerprintPortInfo(info stp.PortInfo) string {
	return fmt.Sprintf("mstid=%d,role=%s,state=%s,reason=%s,prio=%d,cost=%d,root=%s,desig=%s,desig_port=%d,desig_cost=%d,p2p=%t,edge=%t,send_rstp=%t",
		info.MSTID,
		escapeFingerprint(string(info.Role)),
		escapeFingerprint(string(info.State)),
		escapeFingerprint(string(info.BlockReason)),
		info.Priority,
		info.PathCost,
		escapeFingerprint(info.DesignatedRoot.String()),
		escapeFingerprint(info.Designated.String()),
		info.DesignatedPort,
		info.DesignatedCost,
		info.PointToPoint,
		info.Edge,
		info.SendRSTP,
	)
}

func bytesCompareMAC(a, b netaddr.MAC) int {
	for i := 0; i < 6; i++ {
		if a[i] != b[i] {
			if a[i] < b[i] {
				return -1
			}
			return 1
		}
	}
	return 0
}

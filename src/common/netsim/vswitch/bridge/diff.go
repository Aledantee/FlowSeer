package bridge

import (
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"go.aledante.io/FlowSeer/src/common/net/vlan"
	"go.aledante.io/FlowSeer/src/common/netsim/trace"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/port"
)

// VLANNameFact wraps a VLAN name string as a trace.Fact.
type VLANNameFact string

// TypeID returns the fact type identifier for VLANNameFact.
func (f VLANNameFact) TypeID() string { return "bridge.vlan_name" }

// Canonical returns the VLAN name string.
func (f VLANNameFact) Canonical() string { return string(f) }

// PVIDFact wraps a PVID as a trace.Fact.
type PVIDFact vlan.ID

// TypeID returns the fact type identifier for PVIDFact.
func (f PVIDFact) TypeID() string { return "bridge.pvid" }

// Canonical returns the decimal string of the PVID.
func (f PVIDFact) Canonical() string { return strconv.FormatUint(uint64(f), 10) }

// VID returns the underlying vlan.ID.
func (f PVIDFact) VID() vlan.ID { return vlan.ID(f) }

type vlansFact string

func (f vlansFact) TypeID() string    { return "bridge.vlans" }
func (f vlansFact) Canonical() string { return string(f) }

// VLANsFact returns an immutable snapshot of VLAN IDs in their supplied order.
func VLANsFact(ids []vlan.ID) trace.Fact {
	values := make([]string, len(ids))
	for i, id := range ids {
		values[i] = strconv.Itoa(int(id))
	}

	return vlansFact(strings.Join(values, ","))
}

// BoolFact wraps a boolean value as a trace.Fact.
type BoolFact bool

// TypeID returns the fact type identifier for BoolFact.
func (f BoolFact) TypeID() string { return "bridge.bool" }

// Canonical returns "true" or "false".
func (f BoolFact) Canonical() string { return strconv.FormatBool(bool(f)) }

// DurationFact wraps a time.Duration as a trace.Fact.
type DurationFact time.Duration

// TypeID returns the fact type identifier for DurationFact.
func (f DurationFact) TypeID() string { return "bridge.duration" }

// Canonical returns the formatted duration string.
func (f DurationFact) Canonical() string { return time.Duration(f).String() }

// IntFact wraps an integer as a trace.Fact.
type IntFact int

// TypeID returns the fact type identifier for IntFact.
func (f IntFact) TypeID() string { return "bridge.int" }

// Canonical returns the decimal string of the integer.
func (f IntFact) Canonical() string { return strconv.Itoa(int(f)) }

type stringsFact string

func (f stringsFact) TypeID() string    { return "bridge.strings" }
func (f stringsFact) Canonical() string { return string(f) }

// StringsFact returns an immutable, injective snapshot of strings in their supplied order.
func StringsFact(values []string) trace.Fact {
	var out strings.Builder
	for i, value := range values {
		if i > 0 {
			out.WriteByte(',')
		}
		out.WriteString(strconv.Quote(value))
	}

	return stringsFact(out.String())
}

type tunnelSnapshotFact string

func (f tunnelSnapshotFact) TypeID() string    { return "bridge.tunnel" }
func (f tunnelSnapshotFact) Canonical() string { return string(f) }

type switchportSnapshotFact string

func (f switchportSnapshotFact) TypeID() string    { return "bridge.switchport" }
func (f switchportSnapshotFact) Canonical() string { return string(f) }

func snapshotTunnel(t *Tunnel) tunnelSnapshotFact {
	return tunnelSnapshotFact("vid=" + strconv.Itoa(int(t.VID)) +
		";tpid=" + strconv.FormatUint(uint64(t.EffectiveTPID()), 10) +
		";customer_vids=[" + VLANsFact(t.CustomerVIDs).Canonical() + "]")
}

func snapshotSwitchport(sw Switchport) switchportSnapshotFact {
	pvid := "none"
	if sw.PVID != nil {
		pvid = strconv.Itoa(int(*sw.PVID))
	}
	tunnel := "none"
	if sw.Tunnel != nil {
		tunnel = "{" + snapshotTunnel(sw.Tunnel).Canonical() + "}"
	}

	return switchportSnapshotFact("pvid=" + pvid +
		";tagged=[" + VLANsFact(sw.Tagged).Canonical() + "]" +
		";untagged=[" + VLANsFact(sw.Untagged).Canonical() + "]" +
		";ingress_filtering=" + strconv.FormatBool(sw.IngressFiltering) +
		";admission=" + strconv.Quote(string(sw.Admission)) +
		";priority_tags=" + strconv.Quote(string(sw.PriorityTags)) +
		";tunnel=" + tunnel)
}

// Diff computes the difference between two bridge configurations, reporting changes to
// the VLAN table (additions, removals, and renames), per-port switchport settings (PVID,
// tagged and untagged sets, ingress filtering, frame admission, tunnel, and priority tags),
// aging time, maximum table entries, flood VLANs, protected ports, and BPDU forwarding.
func Diff(a, b Config) []trace.Change {
	a = a.Normalize()
	b = b.Normalize()

	var changes []trace.Change
	if (a.VLAN == nil) != (b.VLAN == nil) {
		changes = append(changes, trace.Change{
			Layer:   port.LayerVlan,
			Subject: trace.Subject{Kind: "bridge", Key: ""},
			Field:   "vlan_awareness",
			From:    BoolFact(a.VLAN != nil),
			To:      BoolFact(b.VLAN != nil),
		})
	}

	var (
		aTable map[vlan.ID]string
		bTable map[vlan.ID]string
		aPorts map[string]Switchport
		bPorts map[string]Switchport
	)
	if a.VLAN != nil {
		aTable = a.VLAN.Table
		aPorts = a.VLAN.Switchports
	}
	if b.VLAN != nil {
		bTable = b.VLAN.Table
		bPorts = b.VLAN.Switchports
	}

	var aIDs []vlan.ID
	for id := range aTable {
		aIDs = append(aIDs, id)
	}
	sort.Slice(aIDs, func(i, j int) bool { return aIDs[i] < aIDs[j] })

	for _, id := range aIDs {
		aName := aTable[id]
		bName, exists := bTable[id]
		if !exists {
			changes = append(changes, trace.Change{
				Layer: port.LayerVlan,
				Subject: trace.Subject{
					Kind: "vlan",
					Key:  strconv.Itoa(int(id)),
				},
				Field: "",
				From:  VLANNameFact(aName),
				To:    nil,
			})

			continue
		}

		if aName != bName {
			changes = append(changes, trace.Change{
				Layer: port.LayerVlan,
				Subject: trace.Subject{
					Kind: "vlan",
					Key:  strconv.Itoa(int(id)),
				},
				Field: "name",
				From:  VLANNameFact(aName),
				To:    VLANNameFact(bName),
			})
		}
	}

	var bIDs []vlan.ID
	for id := range bTable {
		if _, exists := aTable[id]; !exists {
			bIDs = append(bIDs, id)
		}
	}
	sort.Slice(bIDs, func(i, j int) bool { return bIDs[i] < bIDs[j] })

	for _, id := range bIDs {
		changes = append(changes, trace.Change{
			Layer: port.LayerVlan,
			Subject: trace.Subject{
				Kind: "vlan",
				Key:  strconv.Itoa(int(id)),
			},
			Field: "",
			From:  nil,
			To:    VLANNameFact(bTable[id]),
		})
	}

	var aPortNames []string
	for name := range aPorts {
		aPortNames = append(aPortNames, name)
	}
	slices.Sort(aPortNames)

	for _, name := range aPortNames {
		aSw := aPorts[name]
		bSw, exists := bPorts[name]
		if !exists {
			changes = append(changes, trace.Change{
				Layer: port.LayerVlan,
				Subject: trace.Subject{
					Kind: "port",
					Key:  name,
				},
				Field: "",
				From:  snapshotSwitchport(aSw),
				To:    nil,
			})

			continue
		}

		pvidChanged := false
		if (aSw.PVID == nil) != (bSw.PVID == nil) {
			pvidChanged = true
		} else if aSw.PVID != nil && *aSw.PVID != *bSw.PVID {
			pvidChanged = true
		}
		if pvidChanged {
			var fromVal, toVal trace.Fact
			if aSw.PVID != nil {
				fromVal = PVIDFact(*aSw.PVID)
			}
			if bSw.PVID != nil {
				toVal = PVIDFact(*bSw.PVID)
			}
			changes = append(changes, trace.Change{
				Layer: port.LayerVlan,
				Subject: trace.Subject{
					Kind: "port",
					Key:  name,
				},
				Field: "pvid",
				From:  fromVal,
				To:    toVal,
			})
		}

		if !slices.Equal(aSw.Tagged, bSw.Tagged) {
			changes = append(changes, trace.Change{
				Layer: port.LayerVlan,
				Subject: trace.Subject{
					Kind: "port",
					Key:  name,
				},
				Field: "tagged_vlan_ids",
				From:  VLANsFact(slices.Clone(aSw.Tagged)),
				To:    VLANsFact(slices.Clone(bSw.Tagged)),
			})
		}

		if !slices.Equal(aSw.Untagged, bSw.Untagged) {
			changes = append(changes, trace.Change{
				Layer: port.LayerVlan,
				Subject: trace.Subject{
					Kind: "port",
					Key:  name,
				},
				Field: "untagged_vlan_ids",
				From:  VLANsFact(slices.Clone(aSw.Untagged)),
				To:    VLANsFact(slices.Clone(bSw.Untagged)),
			})
		}

		if aSw.IngressFiltering != bSw.IngressFiltering {
			changes = append(changes, trace.Change{
				Layer: port.LayerVlan,
				Subject: trace.Subject{
					Kind: "port",
					Key:  name,
				},
				Field: "ingress_filtering",
				From:  BoolFact(aSw.IngressFiltering),
				To:    BoolFact(bSw.IngressFiltering),
			})
		}

		if aSw.Admission != bSw.Admission {
			changes = append(changes, trace.Change{
				Layer: port.LayerVlan,
				Subject: trace.Subject{
					Kind: "port",
					Key:  name,
				},
				Field: "frame_admission",
				From:  aSw.Admission,
				To:    bSw.Admission,
			})
		}

		var (
			tunnelChanged bool
			fromTunnel    trace.Fact
			toTunnel      trace.Fact
		)
		if (aSw.Tunnel == nil) != (bSw.Tunnel == nil) {
			tunnelChanged = true
		} else if aSw.Tunnel != nil && bSw.Tunnel != nil {
			if aSw.Tunnel.VID != bSw.Tunnel.VID ||
				aSw.Tunnel.EffectiveTPID() != bSw.Tunnel.EffectiveTPID() ||
				!slices.Equal(aSw.Tunnel.CustomerVIDs, bSw.Tunnel.CustomerVIDs) {
				tunnelChanged = true
			}
		}
		if tunnelChanged {
			if aSw.Tunnel != nil {
				fromTunnel = snapshotTunnel(aSw.Tunnel)
			}
			if bSw.Tunnel != nil {
				toTunnel = snapshotTunnel(bSw.Tunnel)
			}
			changes = append(changes, trace.Change{
				Layer: port.LayerVlan,
				Subject: trace.Subject{
					Kind: "port",
					Key:  name,
				},
				Field: "tunnel",
				From:  fromTunnel,
				To:    toTunnel,
			})
		}

		if aSw.PriorityTags != bSw.PriorityTags {
			changes = append(changes, trace.Change{
				Layer: port.LayerVlan,
				Subject: trace.Subject{
					Kind: "port",
					Key:  name,
				},
				Field: "priority_tags",
				From:  aSw.PriorityTags,
				To:    bSw.PriorityTags,
			})
		}
	}

	var bPortNames []string
	for name := range bPorts {
		if _, exists := aPorts[name]; !exists {
			bPortNames = append(bPortNames, name)
		}
	}
	slices.Sort(bPortNames)

	for _, name := range bPortNames {
		changes = append(changes, trace.Change{
			Layer: port.LayerVlan,
			Subject: trace.Subject{
				Kind: "port",
				Key:  name,
			},
			Field: "",
			From:  nil,
			To:    snapshotSwitchport(bPorts[name]),
		})
	}

	if a.AgingTime != b.AgingTime {
		changes = append(changes, trace.Change{
			Layer: port.LayerRelay,
			Subject: trace.Subject{
				Kind: "bridge",
				Key:  "",
			},
			Field: "aging_time",
			From:  DurationFact(a.AgingTime),
			To:    DurationFact(b.AgingTime),
		})
	}

	if a.MaxEntries != b.MaxEntries {
		changes = append(changes, trace.Change{
			Layer: port.LayerRelay,
			Subject: trace.Subject{
				Kind: "bridge",
				Key:  "",
			},
			Field: "max_entries",
			From:  IntFact(a.MaxEntries),
			To:    IntFact(b.MaxEntries),
		})
	}

	if !slices.Equal(a.FloodVLANs, b.FloodVLANs) {
		changes = append(changes, trace.Change{
			Layer: port.LayerRelay,
			Subject: trace.Subject{
				Kind: "bridge",
				Key:  "",
			},
			Field: "flood_vlans",
			From:  VLANsFact(slices.Clone(a.FloodVLANs)),
			To:    VLANsFact(slices.Clone(b.FloodVLANs)),
		})
	}

	if !slices.Equal(a.ProtectedPorts, b.ProtectedPorts) {
		changes = append(changes, trace.Change{
			Layer: port.LayerRelay,
			Subject: trace.Subject{
				Kind: "bridge",
				Key:  "",
			},
			Field: "protected_ports",
			From:  StringsFact(slices.Clone(a.ProtectedPorts)),
			To:    StringsFact(slices.Clone(b.ProtectedPorts)),
		})
	}

	if a.ForwardBPDU != b.ForwardBPDU {
		changes = append(changes, trace.Change{
			Layer: port.LayerRelay,
			Subject: trace.Subject{
				Kind: "bridge",
				Key:  "",
			},
			Field: "forward_bpdu",
			From:  BoolFact(a.ForwardBPDU),
			To:    BoolFact(b.ForwardBPDU),
		})
	}

	return changes
}

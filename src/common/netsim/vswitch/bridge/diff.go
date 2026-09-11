package bridge

import (
	"slices"
	"sort"
	"strconv"

	"go.aledante.io/FlowSeer/src/common/net/vlan"
	"go.aledante.io/FlowSeer/src/common/netsim/trace"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/port"
)

// Diff computes the difference between two bridge configurations, reporting changes to
// the VLAN table (additions, removals, and renames), per-port switchport settings (PVID,
// tagged and untagged sets, ingress filtering, and frame admission), aging time,
// maximum table entries, flood VLANs, protected ports, and BPDU forwarding.
func Diff(a, b Config) []trace.Change {
	var changes []trace.Change

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
				From:  aName,
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
				From:  aName,
				To:    bName,
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
			To:    bTable[id],
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
				From:  aSw,
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
			var fromVal, toVal any
			if aSw.PVID != nil {
				fromVal = *aSw.PVID
			}
			if bSw.PVID != nil {
				toVal = *bSw.PVID
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

		if !slices.Equal(slices.Sorted(slices.Values(aSw.Tagged)), slices.Sorted(slices.Values(bSw.Tagged))) {
			changes = append(changes, trace.Change{
				Layer: port.LayerVlan,
				Subject: trace.Subject{
					Kind: "port",
					Key:  name,
				},
				Field: "tagged_vlan_ids",
				From:  aSw.Tagged,
				To:    bSw.Tagged,
			})
		}

		if !slices.Equal(slices.Sorted(slices.Values(aSw.Untagged)), slices.Sorted(slices.Values(bSw.Untagged))) {
			changes = append(changes, trace.Change{
				Layer: port.LayerVlan,
				Subject: trace.Subject{
					Kind: "port",
					Key:  name,
				},
				Field: "untagged_vlan_ids",
				From:  aSw.Untagged,
				To:    bSw.Untagged,
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
				From:  aSw.IngressFiltering,
				To:    bSw.IngressFiltering,
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
			To:    bPorts[name],
		})
	}

	aAging, bAging := effectiveAgingTime(a.AgingTime), effectiveAgingTime(b.AgingTime)
	if aAging != bAging {
		changes = append(changes, trace.Change{
			Layer: port.LayerRelay,
			Subject: trace.Subject{
				Kind: "bridge",
				Key:  "",
			},
			Field: "aging_time",
			From:  aAging,
			To:    bAging,
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
			From:  a.MaxEntries,
			To:    b.MaxEntries,
		})
	}

	aFlood := slices.Clone(a.FloodVLANs)
	slices.Sort(aFlood)
	bFlood := slices.Clone(b.FloodVLANs)
	slices.Sort(bFlood)
	if !slices.Equal(aFlood, bFlood) {
		changes = append(changes, trace.Change{
			Layer: port.LayerRelay,
			Subject: trace.Subject{
				Kind: "bridge",
				Key:  "",
			},
			Field: "flood_vlans",
			From:  aFlood,
			To:    bFlood,
		})
	}

	aProt := slices.Clone(a.ProtectedPorts)
	slices.Sort(aProt)
	bProt := slices.Clone(b.ProtectedPorts)
	slices.Sort(bProt)
	if !slices.Equal(aProt, bProt) {
		changes = append(changes, trace.Change{
			Layer: port.LayerRelay,
			Subject: trace.Subject{
				Kind: "bridge",
				Key:  "",
			},
			Field: "protected_ports",
			From:  aProt,
			To:    bProt,
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
			From:  a.ForwardBPDU,
			To:    b.ForwardBPDU,
		})
	}

	return changes
}

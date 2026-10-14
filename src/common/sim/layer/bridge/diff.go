package bridge

import (
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"go.aledante.io/FlowSeer/src/common/net/vlan"
	"go.aledante.io/FlowSeer/src/common/sim/layer"
	"go.aledante.io/FlowSeer/src/common/sim/trace"
)

// vlanNameFact wraps a VLAN name string as a trace.Fact.
type vlanNameFact string

// TypeID returns the fact type identifier for vlanNameFact.
func (f vlanNameFact) TypeID() string { return "bridge.vlan_name" }

// Canonical returns the VLAN name string.
func (f vlanNameFact) Canonical() string { return string(f) }

// pvidFact wraps a PVID as a trace.Fact.
type pvidFact vlan.ID

// TypeID returns the fact type identifier for pvidFact.
func (f pvidFact) TypeID() string { return "bridge.pvid" }

// Canonical returns the decimal string of the PVID.
func (f pvidFact) Canonical() string { return strconv.FormatUint(uint64(f), 10) }

// PVIDFact returns a trace.Fact wrapping a PVID.
func PVIDFact(vid vlan.ID) trace.Fact { return pvidFact(vid) }

type vlanIDFact vlan.ID

func (f vlanIDFact) TypeID() string    { return "bridge.vlan_id" }
func (f vlanIDFact) Canonical() string { return strconv.FormatUint(uint64(f), 10) }

type portFact string

func (f portFact) TypeID() string    { return "bridge.port" }
func (f portFact) Canonical() string { return string(f) }

type tpidFact uint16

func (f tpidFact) TypeID() string    { return "bridge.tpid" }
func (f tpidFact) Canonical() string { return strconv.FormatUint(uint64(f), 10) }

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

// boolFact wraps a boolean value as a trace.Fact.
type boolFact bool

// TypeID returns the fact type identifier for boolFact.
func (f boolFact) TypeID() string { return "bridge.bool" }

// Canonical returns "true" or "false".
func (f boolFact) Canonical() string { return strconv.FormatBool(bool(f)) }

// durationFact wraps a time.Duration as a trace.Fact.
type durationFact time.Duration

// TypeID returns the fact type identifier for durationFact.
func (f durationFact) TypeID() string { return "bridge.duration" }

// Canonical returns the formatted duration string.
func (f durationFact) Canonical() string { return time.Duration(f).String() }

// intFact wraps an integer as a trace.Fact.
type intFact int

// TypeID returns the fact type identifier for intFact.
func (f intFact) TypeID() string { return "bridge.int" }

// Canonical returns the decimal string of the integer.
func (f intFact) Canonical() string { return strconv.Itoa(int(f)) }

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
	a = a.Normalize(layer.Env{})
	b = b.Normalize(layer.Env{})

	var changes []trace.Change
	if (a.VLAN == nil) != (b.VLAN == nil) {
		changes = append(changes, trace.Change{
			Layer:   LayerNameVLAN,
			Subject: trace.Subject{Kind: "bridge", Key: ""},
			Field:   "vlan.presence",
			From:    boolFact(a.VLAN != nil),
			To:      boolFact(b.VLAN != nil),
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

	changes = append(changes, diffVLANTable(aTable, bTable)...)
	changes = append(changes, diffSwitchports(aPorts, bPorts)...)
	changes = append(changes, diffRelay(a, b)...)

	return changes
}

func diffVLANTable(aTable, bTable map[vlan.ID]string) []trace.Change {
	var changes []trace.Change
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
				Layer: LayerNameVLAN,
				Subject: trace.Subject{
					Kind: "vlan",
					Key:  strconv.Itoa(int(id)),
				},
				Field: "",
				From:  vlanNameFact(aName),
				To:    nil,
			})

			continue
		}

		if aName != bName {
			changes = append(changes, trace.Change{
				Layer: LayerNameVLAN,
				Subject: trace.Subject{
					Kind: "vlan",
					Key:  strconv.Itoa(int(id)),
				},
				Field: "name",
				From:  vlanNameFact(aName),
				To:    vlanNameFact(bName),
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
			Layer: LayerNameVLAN,
			Subject: trace.Subject{
				Kind: "vlan",
				Key:  strconv.Itoa(int(id)),
			},
			Field: "",
			From:  nil,
			To:    vlanNameFact(bTable[id]),
		})
	}

	return changes
}

func diffSwitchports(aPorts, bPorts map[string]Switchport) []trace.Change {
	var changes []trace.Change
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
				Layer: LayerNameVLAN,
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

		changes = append(changes, diffSwitchport(name, aSw, bSw)...)
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
			Layer: LayerNameVLAN,
			Subject: trace.Subject{
				Kind: "port",
				Key:  name,
			},
			Field: "",
			From:  nil,
			To:    snapshotSwitchport(bPorts[name]),
		})
	}

	return changes
}

func diffSwitchport(name string, aSw, bSw Switchport) []trace.Change {
	var changes []trace.Change
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
			Layer: LayerNameVLAN,
			Subject: trace.Subject{
				Kind: "port",
				Key:  name,
			},
			Field: "pvid",
			From:  fromVal,
			To:    toVal,
		})
	}

	changes = append(changes, diffVLANMembers(aSw.Tagged, bSw.Tagged, LayerNameVLAN, trace.Subject{Kind: "port", Key: name}, "tagged_vlan_ids")...)

	changes = append(changes, diffVLANMembers(aSw.Untagged, bSw.Untagged, LayerNameVLAN, trace.Subject{Kind: "port", Key: name}, "untagged_vlan_ids")...)

	if aSw.IngressFiltering != bSw.IngressFiltering {
		changes = append(changes, trace.Change{
			Layer: LayerNameVLAN,
			Subject: trace.Subject{
				Kind: "port",
				Key:  name,
			},
			Field: "ingress_filtering",
			From:  boolFact(aSw.IngressFiltering),
			To:    boolFact(bSw.IngressFiltering),
		})
	}

	if aSw.Admission != bSw.Admission {
		changes = append(changes, trace.Change{
			Layer: LayerNameVLAN,
			Subject: trace.Subject{
				Kind: "port",
				Key:  name,
			},
			Field: "frame_admission",
			From:  aSw.Admission,
			To:    bSw.Admission,
		})
	}

	subject := trace.Subject{Kind: "port", Key: name}
	if (aSw.Tunnel == nil) != (bSw.Tunnel == nil) {
		var from, to trace.Fact
		if aSw.Tunnel != nil {
			from = snapshotTunnel(aSw.Tunnel)
		}
		if bSw.Tunnel != nil {
			to = snapshotTunnel(bSw.Tunnel)
		}
		changes = append(changes, trace.Change{
			Layer: LayerNameVLAN, Subject: subject, Field: "tunnel.presence", From: from, To: to,
		})
	} else if aSw.Tunnel != nil {
		a, b := aSw.Tunnel, bSw.Tunnel
		if a.VID != b.VID {
			changes = append(changes, trace.Change{
				Layer: LayerNameVLAN, Subject: subject, Field: "tunnel.vid", From: vlanIDFact(a.VID), To: vlanIDFact(b.VID),
			})
		}
		changes = append(changes, diffVLANMembers(a.CustomerVIDs, b.CustomerVIDs, LayerNameVLAN, subject, "tunnel.customer_vids")...)
		if from, to := a.EffectiveTPID(), b.EffectiveTPID(); from != to {
			changes = append(changes, trace.Change{
				Layer: LayerNameVLAN, Subject: subject, Field: "tunnel.tpid", From: tpidFact(from), To: tpidFact(to),
			})
		}
	}

	if aSw.PriorityTags != bSw.PriorityTags {
		changes = append(changes, trace.Change{
			Layer: LayerNameVLAN,
			Subject: trace.Subject{
				Kind: "port",
				Key:  name,
			},
			Field: "priority_tags",
			From:  aSw.PriorityTags,
			To:    bSw.PriorityTags,
		})
	}

	return changes
}

func diffRelay(a, b Config) []trace.Change {
	var changes []trace.Change
	if a.AgingTime != b.AgingTime {
		changes = append(changes, trace.Change{
			Layer: LayerName,
			Subject: trace.Subject{
				Kind: "bridge",
				Key:  "",
			},
			Field: "aging_time",
			From:  durationFact(a.AgingTime),
			To:    durationFact(b.AgingTime),
		})
	}

	if a.MaxEntries != b.MaxEntries {
		changes = append(changes, trace.Change{
			Layer: LayerName,
			Subject: trace.Subject{
				Kind: "bridge",
				Key:  "",
			},
			Field: "max_entries",
			From:  intFact(a.MaxEntries),
			To:    intFact(b.MaxEntries),
		})
	}

	changes = append(changes, diffVLANMembers(a.FloodVLANs, b.FloodVLANs, LayerName, trace.Subject{Kind: "bridge", Key: ""}, "flood_vlans")...)

	for _, name := range a.ProtectedPorts {
		if !slices.Contains(b.ProtectedPorts, name) {
			changes = append(changes, trace.Change{
				Layer: LayerName, Subject: trace.Subject{Kind: "bridge"}, Field: "protected_ports[" + strconv.Quote(name) + "]", From: portFact(name),
			})
		}
	}
	for _, name := range b.ProtectedPorts {
		if !slices.Contains(a.ProtectedPorts, name) {
			changes = append(changes, trace.Change{
				Layer: LayerName, Subject: trace.Subject{Kind: "bridge"}, Field: "protected_ports[" + strconv.Quote(name) + "]", To: portFact(name),
			})
		}
	}

	if a.ForwardBPDU != b.ForwardBPDU {
		changes = append(changes, trace.Change{
			Layer: LayerName,
			Subject: trace.Subject{
				Kind: "bridge",
				Key:  "",
			},
			Field: "forward_bpdu",
			From:  boolFact(a.ForwardBPDU),
			To:    boolFact(b.ForwardBPDU),
		})
	}

	return changes
}

func diffVLANMembers(a, b []vlan.ID, layerName trace.Layer, subject trace.Subject, field string) []trace.Change {
	var changes []trace.Change
	for _, id := range a {
		if !slices.Contains(b, id) {
			changes = append(changes, trace.Change{
				Layer: layerName, Subject: subject, Field: field + "[" + strconv.Quote(strconv.Itoa(int(id))) + "]", From: vlanIDFact(id),
			})
		}
	}
	for _, id := range b {
		if !slices.Contains(a, id) {
			changes = append(changes, trace.Change{
				Layer: layerName, Subject: subject, Field: field + "[" + strconv.Quote(strconv.Itoa(int(id))) + "]", To: vlanIDFact(id),
			})
		}
	}
	return changes
}

package stp

import (
	"crypto/hmac"
	"crypto/md5"
	"encoding/binary"
	"fmt"
	"slices"

	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/common/net/vlan"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/port"
)

// mstDigestKey is the fixed 16-byte HMAC-MD5 signature key IEEE 802.1Q
// clause 13.7 defines for the MST configuration digest. The all-zero table
// and the VID-10/VID-20 table below, keyed with this constant, are what
// prove the digest is computed the standard way:
//
//	all-zero table -> ac36177f50283cd4b83821d8ab26de62
//	VID 10 on MSTID 1, VID 20 on MSTID 2, rest zero -> 9357ebb7a8d74dd5fef4f2bab50531aa
var mstDigestKey = [16]byte{
	0x13, 0xAC, 0x06, 0xA6, 0x2E, 0x47, 0xFD, 0x51,
	0xF9, 0x5D, 0x2B, 0xA2, 0x43, 0xCD, 0x03, 0x46,
}

// MSTID identifies a Multiple Spanning Tree Instance. MSTID 0 is the Common
// and Internal Spanning Tree (CIST); 1 through 4094 are instance identifiers.
type MSTID uint16

// InstancePort holds the per-instance administrative spanning tree settings
// for one port within an MST instance.
type InstancePort struct {
	Priority        uint8
	PriorityPresent bool
	PathCost        uint32
}

// TypeID returns the fact type identifier for InstancePort.
func (p InstancePort) TypeID() string { return "stp.mst.instance_port" }

// Canonical returns the canonical string representation of the InstancePort fact.
func (p InstancePort) Canonical() string {
	return fmt.Sprintf("priority=%d,path_cost=%d",
		effectivePortPriority(p.Priority, p.PriorityPresent),
		p.PathCost)
}

// Instance holds the configuration of one Multiple Spanning Tree Instance:
// its bridge priority within the instance, the VLANs it carries, and its
// per-port settings.
type Instance struct {
	Priority uint16
	VLANs    []vlan.ID
	Ports    map[string]InstancePort
}

// TypeID returns the fact type identifier for Instance.
func (i Instance) TypeID() string { return "stp.mst.instance" }

// Canonical returns the canonical string representation of the Instance
// fact: its priority followed by its sorted VLAN list.
func (i Instance) Canonical() string {
	vlans := slices.Clone(i.VLANs)
	slices.Sort(vlans)

	return fmt.Sprintf("priority=%d,vlans=%v", i.Priority, vlans)
}

// ConfigID is the IEEE 802.1Q MST configuration identifier, the 51-octet
// value bridges exchange and compare to decide whether they belong to the
// same MST region.
type ConfigID struct {
	// Selector is always 0, "the format specified in IEEE Std 802.1Q".
	Selector uint8

	// Name is the configuration name, at most 32 octets when encoded on the
	// wire (null-padded).
	Name string

	Revision uint16

	// Digest is the HMAC-MD5 signature of the 4096-entry VID-to-MSTID table.
	Digest [16]byte
}

// MST holds the Multiple Spanning Tree region configuration of a virtual
// switch. Its presence on Config selects MSTP over RSTP.
type MST struct {
	Name      string
	Revision  uint16
	MaxHops   uint8
	Instances map[MSTID]Instance
}

// TypeID returns the fact type identifier for MST.
func (m MST) TypeID() string { return "stp.mst" }

// Canonical returns the canonical string representation of the MST fact.
func (m MST) Canonical() string {
	return fmt.Sprintf("name=%q,revision=%d,max_hops=%d", m.Name, m.Revision, effectiveMaxHops(m.MaxHops))
}

// ConfigID computes the IEEE 802.1Q MST configuration identifier for the
// region: the name and revision as configured, and the digest of the
// VID-to-MSTID table built from every instance's VLAN membership. A VID no
// instance claims maps to MSTID 0 (the CIST).
func (m MST) ConfigID() ConfigID {
	var table [4096]uint16
	for id, inst := range m.Instances {
		for _, vid := range inst.VLANs {
			if int(vid) < len(table) {
				table[vid] = uint16(id)
			}
		}
	}

	var buf [4096 * 2]byte
	for vid, mstid := range table {
		binary.BigEndian.PutUint16(buf[vid*2:], mstid)
	}

	mac := hmac.New(md5.New, mstDigestKey[:])
	mac.Write(buf[:])
	var digest [16]byte
	copy(digest[:], mac.Sum(nil))

	return ConfigID{
		Selector: 0,
		Name:     m.Name,
		Revision: m.Revision,
		Digest:   digest,
	}
}

// Clone returns a deep copy of the MST region configuration.
func (m MST) Clone() MST {
	cloned := m
	if m.Instances != nil {
		cloned.Instances = make(map[MSTID]Instance, len(m.Instances))
		for id, inst := range m.Instances {
			cloned.Instances[id] = inst.clone()
		}
	}
	return cloned
}

func (i Instance) clone() Instance {
	cloned := i
	if i.VLANs != nil {
		cloned.VLANs = slices.Clone(i.VLANs)
	}
	if i.Ports != nil {
		cloned.Ports = make(map[string]InstancePort, len(i.Ports))
		for name, p := range i.Ports {
			cloned.Ports[name] = p
		}
	}
	return cloned
}

func effectiveMaxHops(h uint8) uint8 {
	if h == 0 {
		return DefaultMaxHops
	}
	return h
}

func effectiveInstancePriority(p uint16) uint16 {
	if p == 0 {
		return DefaultBridgePriority
	}
	return p
}

// Normalize returns a normalized copy of the MST region configuration,
// filling MaxHops with 20, each instance's priority with the default bridge
// priority, and sorting each instance's VLANs.
func (m MST) Normalize() MST {
	cloned := m.Clone()
	cloned.MaxHops = effectiveMaxHops(cloned.MaxHops)
	for id, inst := range cloned.Instances {
		inst.Priority = effectiveInstancePriority(inst.Priority)
		if inst.VLANs != nil {
			slices.Sort(inst.VLANs)
		}
		for name, p := range inst.Ports {
			p.Priority = effectivePortPriority(p.Priority, p.PriorityPresent)
			p.PriorityPresent = true
			inst.Ports[name] = p
		}
		cloned.Instances[id] = inst
	}
	return cloned
}

// Validate checks the MST region configuration: every MSTID must fall in
// 1..4094, MaxHops must fall in 6..40, each instance priority must be a
// multiple of 4096, the name must be at most 32 octets, no VLAN may be
// claimed by more than one instance, and every instance port must exist in
// the port table.
func (m MST) Validate(ports port.Table) error {
	if len(m.Name) > 32 {
		return errs.New().
			Attr("field", "mst.name").
			Attr("name", m.Name).
			Msgf("MST region name %q exceeds 32 octets", m.Name)
	}

	maxHops := effectiveMaxHops(m.MaxHops)
	if maxHops < 6 || maxHops > 40 {
		return errs.New().
			Attr("field", "mst.max_hops").
			Attr("max_hops", maxHops).
			Msgf("MST max hops %d is outside 6 through 40", maxHops)
	}

	claimed := make(map[vlan.ID]MSTID, 4096)
	for _, id := range sortedMSTIDs(m.Instances) {
		if id < 1 || id > 4094 {
			return errs.New().
				Attr("field", fmt.Sprintf("mst.instances.%d", id)).
				Attr("mstid", id).
				Msgf("MST instance id %d is outside 1 through 4094", id)
		}

		inst := m.Instances[id]
		priority := effectiveInstancePriority(inst.Priority)
		if priority%4096 != 0 {
			return errs.New().
				Attr("field", fmt.Sprintf("mst.instances.%d.priority", id)).
				Attr("mstid", id).
				Attr("priority", priority).
				Msgf("MST instance %d priority %d must be a multiple of 4096", id, priority)
		}

		for _, vid := range inst.VLANs {
			if owner, ok := claimed[vid]; ok {
				return errs.New().
					Attr("field", fmt.Sprintf("mst.instances.%d.vlans", id)).
					Attr("vid", vid).
					Attr("owner", owner).
					Msgf("VLAN %d claimed by both MST instance %d and %d", vid, owner, id)
			}
			claimed[vid] = id
		}

		for _, name := range sortedKeys(inst.Ports) {
			if _, ok := ports.Port(name); !ok {
				return errs.New().
					Attr("field", fmt.Sprintf("mst.instances.%d.ports.%s", id, name)).
					Attr("mstid", id).
					Attr("port", name).
					Msgf("MST instance %d port %q absent from port table", id, name)
			}
		}
	}

	return nil
}

func sortedMSTIDs(m map[MSTID]Instance) []MSTID {
	ids := make([]MSTID, 0, len(m))
	for id := range m {
		ids = append(ids, id)
	}
	slices.Sort(ids)

	return ids
}

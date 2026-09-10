// Package vlan provides IEEE 802.1Q VLAN identifiers, priority code points, and tag values.
package vlan

const (
	// MinID is the lowest valid assigned VLAN identifier (1).
	MinID ID = 1

	// MaxID is the highest valid assigned VLAN identifier (4094).
	MaxID ID = 4094

	// MinPCP is the lowest valid Priority Code Point value (0).
	MinPCP PCP = 0

	// MaxPCP is the highest valid Priority Code Point value (7).
	MaxPCP PCP = 7
)

// ID represents an IEEE 802.1Q 12-bit VLAN identifier.
// The zero value represents an unassigned or priority-tagged identifier; [ID.Valid]
// reports false for 0 and values above 4094.
// Instances are immutable value types safe for concurrent use.
type ID uint16

// Valid reports whether id is within the standard assignable VLAN range (1 through 4094 inclusive).
// Value 0 (priority-tagged) and 4095 (reserved by standard) are not valid assigned VLAN IDs.
func (id ID) Valid() bool {
	return id >= MinID && id <= MaxID
}

// PCP represents an IEEE 802.1Q 3-bit Priority Code Point (0 through 7).
// The zero value represents default best-effort priority.
// Instances are immutable value types safe for concurrent use.
type PCP uint8

// Valid reports whether p is within the 3-bit Priority Code Point range (0 through 7 inclusive).
func (p PCP) Valid() bool {
	return p <= MaxPCP
}

// Tag represents an IEEE 802.1Q VLAN tag value on the wire.
// In a tag, VID may be 0 for priority-tagged frames where only PCP and DEI apply.
// The zero value is a usable priority-tagged tag with zero TPID, PCP, DEI, and VID.
// Instances are value types safe for concurrent use.
type Tag struct {
	TPID uint16
	PCP  PCP
	DEI  bool
	VID  ID
}

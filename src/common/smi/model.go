package smi

import (
	"slices"
	"strconv"
	"strings"
)

// MaxOIDLength is the number of sub-identifiers this package will place
// in one OID.
//
// The number is 128 because that is the cap the SNMP runtime enforces on
// every OID it builds or decodes (maxOIDComponents in src/common/snmp),
// and an OID this package resolves has to be one that runtime can carry.
// The literal is duplicated rather than imported: package smi imports
// nothing from the SNMP runtime so that a model-driven decode path there
// can depend on the parser without closing a cycle.
// TestSMIOIDLengthCapMatchesRuntimeCap in package snmp pins the two
// together, because the runtime's cap is unexported and reading it from
// here would need the import this package refuses.
const MaxOIDLength = 128

// OID is a resolved object identifier: the sub-identifiers from the root
// down to one node.
//
// The zero value is the empty OID, which is what an unresolved
// declaration carries. An OID is immutable once a [ModuleSet] holds it
// and is safe for concurrent use.
type OID struct {
	subs []uint32
}

// NewOID returns the OID naming subs. It copies subs, so the caller may
// reuse the slice.
//
// Nothing here validates the root arcs or the length. A MIB that places
// a node under a root the SNMP runtime rejects is still a MIB whose
// declarations a reader wants to see, and refusing to model it would
// lose every sibling too.
func NewOID(subs ...uint32) OID {
	if len(subs) == 0 {
		return OID{}
	}

	return OID{subs: slices.Clone(subs)}
}

// Len returns the number of sub-identifiers.
func (o OID) Len() int { return len(o.subs) }

// At returns the sub-identifier at i, or 0 when i is out of range.
func (o OID) At(i int) uint32 {
	if i < 0 || i >= len(o.subs) {
		return 0
	}

	return o.subs[i]
}

// Subs returns the sub-identifiers. The slice is the OID's own storage
// and must not be modified; copy it first if you need to.
func (o OID) Subs() []uint32 { return o.subs }

// Child returns the OID one arc below this one. It allocates, so the
// result never shares storage with the receiver.
func (o OID) Child(sub uint32) OID {
	out := make([]uint32, len(o.subs)+1)
	copy(out, o.subs)
	out[len(o.subs)] = sub

	return OID{subs: out}
}

// Parent returns the OID one arc above this one, or the empty OID when
// there is no arc to drop.
func (o OID) Parent() OID {
	if len(o.subs) == 0 {
		return OID{}
	}

	return OID{subs: o.subs[:len(o.subs)-1]}
}

// HasPrefix reports whether p names this node or one of its ancestors.
func (o OID) HasPrefix(p OID) bool {
	if len(p.subs) > len(o.subs) {
		return false
	}

	return slices.Equal(o.subs[:len(p.subs)], p.subs)
}

// Compare orders two OIDs the way a walk visits them: arc by arc, with a
// prefix before everything under it.
func (o OID) Compare(b OID) int { return slices.Compare(o.subs, b.subs) }

// String returns the dotted form, "1.3.6.1.2.1.1". The empty OID renders
// as the empty string.
func (o OID) String() string {
	if len(o.subs) == 0 {
		return ""
	}

	var sb strings.Builder
	for i, s := range o.subs {
		if i > 0 {
			sb.WriteByte('.')
		}
		sb.WriteString(strconv.FormatUint(uint64(s), 10))
	}

	return sb.String()
}

// Dialect is which SMI a module is written in. SMIv2 is the zero value
// because it is the reading a module gets when nothing in it says
// otherwise.
type Dialect uint8

// The dialects, named as the RFCs name them.
const (
	DialectV2 Dialect = iota
	DialectV1
)

// String returns the dialect's name.
func (d Dialect) String() string {
	if d == DialectV1 {
		return "SMIv1"
	}

	return "SMIv2"
}

// BaseType is the SMI type a resolved type finally stands on, with every
// textual convention and type alias between it and the declaration
// followed through.
//
// It is the wire shape, and it is deliberately separate from
// [Type.Name]: a renderer classifying an object reads both, because
// "TimeStamp" and "TimeTicks" carry the same base and mean different
// things.
type BaseType uint8

// The base types RFC 2578 §7.1 defines, plus SEQUENCE for a row type and
// the zero value for a type nothing resolved.
const (
	BaseUnknown BaseType = iota
	BaseInteger
	BaseInteger32
	BaseUnsigned32
	BaseGauge32
	BaseCounter32
	BaseCounter64
	BaseTimeTicks
	BaseOctetString
	BaseObjectIdentifier
	BaseBits
	BaseIPAddress
	BaseOpaque
	BaseNull
	BaseSequence
)

var baseTypeNames = [...]string{
	BaseUnknown:          "unknown",
	BaseInteger:          "INTEGER",
	BaseInteger32:        "Integer32",
	BaseUnsigned32:       "Unsigned32",
	BaseGauge32:          "Gauge32",
	BaseCounter32:        "Counter32",
	BaseCounter64:        "Counter64",
	BaseTimeTicks:        "TimeTicks",
	BaseOctetString:      "OCTET STRING",
	BaseObjectIdentifier: "OBJECT IDENTIFIER",
	BaseBits:             "BITS",
	BaseIPAddress:        "IpAddress",
	BaseOpaque:           "Opaque",
	BaseNull:             "NULL",
	BaseSequence:         "SEQUENCE",
}

// String returns the base type's name as the RFCs spell it.
func (b BaseType) String() string {
	if int(b) >= len(baseTypeNames) {
		return "base(" + strconv.Itoa(int(b)) + ")"
	}

	return baseTypeNames[b]
}

// Access is what an ACCESS or MAX-ACCESS clause said.
//
// The two clauses share this type because they share a vocabulary. What
// they do not share is meaning — SMIv1 states a minimum and SMIv2 a
// maximum — so [Node.Dialect] is what tells a reader which one the value
// came from.
type Access uint8

// The access levels, in the order RFC 2578 §7.3 ranks them.
const (
	AccessUnknown Access = iota
	AccessNotAccessible
	AccessAccessibleForNotify
	AccessReadOnly
	AccessReadWrite
	AccessReadCreate
	AccessWriteOnly
)

var accessNames = [...]string{
	AccessUnknown:             "unknown",
	AccessNotAccessible:       "not-accessible",
	AccessAccessibleForNotify: "accessible-for-notify",
	AccessReadOnly:            "read-only",
	AccessReadWrite:           "read-write",
	AccessReadCreate:          "read-create",
	AccessWriteOnly:           "write-only",
}

// String returns the access level as the source spells it.
func (a Access) String() string {
	if int(a) >= len(accessNames) {
		return "access(" + strconv.Itoa(int(a)) + ")"
	}

	return accessNames[a]
}

// Status is what a STATUS clause said. SMIv1's mandatory and optional
// and SMIv2's current are kept apart rather than folded together,
// because the first two describe an implementation's obligation and the
// third describes the definition's own standing.
type Status uint8

// The status values both dialects define.
const (
	StatusUnknown Status = iota
	StatusCurrent
	StatusMandatory
	StatusOptional
	StatusDeprecated
	StatusObsolete
)

var statusNames = [...]string{
	StatusUnknown:    "unknown",
	StatusCurrent:    "current",
	StatusMandatory:  "mandatory",
	StatusOptional:   "optional",
	StatusDeprecated: "deprecated",
	StatusObsolete:   "obsolete",
}

// String returns the status as the source spells it.
func (s Status) String() string {
	if int(s) >= len(statusNames) {
		return "status(" + strconv.Itoa(int(s)) + ")"
	}

	return statusNames[s]
}

// NodeKind is a node's place in the OID tree.
//
// The classification is structural rather than syntactic: a table is an
// OBJECT-TYPE whose SYNTAX is a SEQUENCE OF, its one child is the row,
// and the row's children are the columns. Reading the shape off the tree
// rather than off the row type's spelling is what lets a MIB whose
// SEQUENCE type assignment failed to parse still yield its columns.
type NodeKind uint8

// The node kinds.
const (
	// NodeUnknown is a node nothing classified, which is what an
	// unresolved declaration carries.
	NodeUnknown NodeKind = iota

	// NodeNode is a naming node with no value: an OBJECT IDENTIFIER
	// assignment, an OBJECT-IDENTITY, a MODULE-IDENTITY.
	NodeNode

	NodeScalar
	NodeTable
	NodeRow
	NodeColumn
	NodeNotification
	NodeGroup
	NodeCompliance
	NodeCapabilities
)

var nodeKindNames = [...]string{
	NodeUnknown:      "unknown",
	NodeNode:         "node",
	NodeScalar:       "scalar",
	NodeTable:        "table",
	NodeRow:          "row",
	NodeColumn:       "column",
	NodeNotification: "notification",
	NodeGroup:        "group",
	NodeCompliance:   "compliance",
	NodeCapabilities: "capabilities",
}

// String returns the kind's name.
func (k NodeKind) String() string {
	if int(k) >= len(nodeKindNames) {
		return "node(" + strconv.Itoa(int(k)) + ")"
	}

	return nodeKindNames[k]
}

// Member is one named number: an enumeration entry or one bit of a BITS
// type. Number is the number the MIB declared, never the member's
// position, because a device reporting bit 5 of a gapped BITS type means
// the member declared as 5.
type Member struct {
	Name   string
	Number int64
}

// Range is one alternative of a range or SIZE constraint. A single value
// written without ".." has Min and Max equal.
type Range struct {
	Min int64
	Max int64
}

// Type is a resolved SMI type: a textual convention, a type assignment,
// or the anonymous type an object's SYNTAX clause declares inline.
//
// Name is the textual convention's or type assignment's name, and is
// empty for an inline SYNTAX. Base is what the type finally stands on
// with every alias followed through. Both are exposed because a renderer
// classifying an object reads both: the base decides how a value is
// decoded and the name decides what it means.
//
// A Type is immutable once [Load] has returned it. Do not modify the
// slices it carries.
type Type struct {
	Name        string
	Module      string
	Base        BaseType
	Parent      string
	Members     []Member
	Ranges      []Range
	Sizes       []Range
	DisplayHint string
	Description string
	Reference   string
	Status      Status

	// Unresolved reports that some part of the type did not resolve: a
	// SYNTAX naming a type nothing defines, a chain of textual
	// conventions that closes on itself, or a member name a parse error
	// dropped or cut short. Nothing may render the type as the
	// definition its author wrote, and where a name failed to resolve
	// Base is [BaseUnknown] as well.
	Unresolved bool
}

// Enumerated reports whether the type is an integer with named numbers,
// which several rules and every renderer treat as a type of its own.
func (t *Type) Enumerated() bool {
	if t == nil {
		return false
	}
	switch t.Base {
	case BaseInteger, BaseInteger32, BaseUnsigned32:
		return len(t.Members) > 0
	default:
		return false
	}
}

// IndexPart is one column of an INDEX clause, held exactly as the source
// wrote it.
//
// Nothing resolves it, and that is settled rather than pending: index
// decoding is generic at runtime and no consumer reads index structure,
// so turning a column name into a type would buy a pass nobody reads.
type IndexPart struct {
	Name    string
	Implied bool
}

// Node is one declaration placed in the OID tree.
//
// A Node is immutable once [Load] has returned it, and the whole tree it
// belongs to is safe for concurrent readers. Do not modify its slices or
// reassign Parent or Children.
type Node struct {
	Name        string
	Module      string
	Kind        NodeKind
	Dialect     Dialect
	OID         OID
	Access      Access
	Status      Status
	Description string
	Reference   string
	Units       string

	// Type is the object's resolved SYNTAX, or nil for a node that
	// carries no value.
	Type *Type

	// Index and Augments are the INDEX and AUGMENTS clauses as written.
	Index    []IndexPart
	Augments string

	// Unresolved reports that the declaration lost something a renderer
	// reads: a required clause the parser could not find, an OID whose
	// parent nothing defines, a SYNTAX naming a type nothing defines, or
	// a name a parse error dropped or cut short. An unresolved node is
	// present so a reader can see what fell, and must never be rendered
	// as if it were whole.
	Unresolved bool

	Parent   *Node
	Children []*Node
}

// Import is one module an IMPORTS clause names and the symbols it was
// asked for.
type Import struct {
	Module  string
	Symbols []string

	// BackEdge reports that this import closes a cycle and was dropped
	// from the dependency order. Symbol lookup still follows it: the
	// author's IMPORTS list is still the best statement of where a
	// symbol comes from, and only the load order needed an acyclic graph.
	BackEdge bool
}

// Table is a conceptual table: the SEQUENCE OF node, its row, and the
// columns under the row.
//
// Columns holds every column in OID order, index columns included, each
// with the access its declaration gave it. Index is the row's INDEX
// clause as written and is not resolved into a structure.
type Table struct {
	Node     *Node
	Row      *Node
	Columns  []*Node
	Index    []IndexPart
	Augments string
}

// Module is one resolved MIB module.
//
// Nodes, Types and Tables are in the order the source declares them, so
// two loads of the same files produce the same slices. A Module is
// immutable once [Load] has returned it.
type Module struct {
	Name    string
	File    string
	Dialect Dialect

	Organization string
	ContactInfo  string
	Description  string
	LastUpdated  string
	Reference    string

	// Identity is the MODULE-IDENTITY node, or nil for a module that
	// declares none — every SMIv1 module, and a few SMIv2 ones.
	Identity *Node

	Imports []Import
	Nodes   []*Node
	Types   []*Type
	Tables  []*Table

	nodeByName map[string]*Node
	typeByName map[string]*Type
}

// Node returns the module's declaration named name.
func (m *Module) Node(name string) (*Node, bool) {
	n, ok := m.nodeByName[name]

	return n, ok
}

// Type returns the type the module declares under name.
func (m *Module) Type(name string) (*Type, bool) {
	t, ok := m.typeByName[name]

	return t, ok
}

// ModuleSet is everything one [Load] resolved: the modules, the OID tree
// they share, and every diagnostic raised reading them.
//
// It is immutable once Load returns and is safe for any number of
// concurrent readers. Nothing in it is package-level state: two Load
// calls share nothing, and a ModuleSet outlives neither.
type ModuleSet struct {
	modules []*Module
	byName  map[string]*Module
	byOID   map[string]*Node
	roots   []*Node
	types   []*Type
	order   []string
	diags   []Diagnostic
	lines   map[string]*LineTable
}

// Modules returns every resolved module, ordered by module name. The
// slice is the set's own storage and must not be modified.
func (s *ModuleSet) Modules() []*Module { return s.modules }

// Module returns the module named name.
func (s *ModuleSet) Module(name string) (*Module, bool) {
	m, ok := s.byName[name]

	return m, ok
}

// Node returns the node at oid.
func (s *ModuleSet) Node(oid OID) (*Node, bool) {
	n, ok := s.byOID[oid.String()]

	return n, ok
}

// Roots returns the topmost nodes of the OID tree, ordered by OID. A
// well-formed load has one root; a load missing the module that anchors
// a vendor subtree has several.
func (s *ModuleSet) Roots() []*Node { return s.roots }

// Type returns the type named name, searched without a module
// qualifier.
//
// The order is fixed and is part of the contract, because a renderer's
// output depends on which definition wins:
//
//  1. The SMI base types, under the spellings RFC 2578 §7.1 gives them.
//     A module that redefines Counter32 cannot change what the wire
//     carries, so the base type wins whatever a module says.
//  2. Module-declared textual conventions and type assignments, walking
//     modules in ascending module-name order and, within one module, in
//     source order.
//
// The first match wins. A base type found in step 1 is returned with an
// empty [Type.Module], since no module declared it.
func (s *ModuleSet) Type(name string) (*Type, bool) {
	if t, ok := builtinType(name); ok {
		return t, true
	}

	for _, t := range s.types {
		if t.Name == name {
			return t, true
		}
	}

	return nil, false
}

// Order returns the module names in dependency order: every module an
// importer needs comes before it, ties broken by module name, and the
// edges dropped to break an IMPORTS cycle excepted. It is what a
// consumer that has to process modules one at a time walks.
func (s *ModuleSet) Order() []string { return s.order }

// Diagnostics returns everything raised reading the set, in canonical
// order: by file, then by offset within the file, then by code. The
// order does not depend on how many workers loaded the files or on the
// order the caller supplied them in.
func (s *ModuleSet) Diagnostics() []Diagnostic { return s.diags }

// Render returns every diagnostic with its line, column and message
// worked out, in the same order [ModuleSet.Diagnostics] uses.
func (s *ModuleSet) Render() []Rendered {
	out := make([]Rendered, 0, len(s.diags))
	for _, d := range s.diags {
		out = append(out, d.Render(s.lines[d.Position().File]))
	}

	return out
}
